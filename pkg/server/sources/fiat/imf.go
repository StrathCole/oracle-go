package fiat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

const imfSDRURL = "https://www.imf.org/external/np/fin/data/rms_sdrv.aspx"

// IMFSource fetches SDR/USD price from IMF website (free, no API key)
// https://www.imf.org/ - Official SDR valuations
type IMFSource struct {
	name          string
	symbols       []string
	timeout       time.Duration
	interval      time.Duration
	client        *http.Client
	prices        map[string]sources.Price
	pricesMu      sync.RWMutex
	lastUpdate    time.Time
	healthy       bool
	healthMu      sync.RWMutex
	subscribers   []chan<- sources.PriceUpdate
	subscribersMu sync.RWMutex
	stopChan      chan struct{}
	logger        *logging.Logger
}

func NewIMFSource(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok || len(symbols) == 0 {
		// Default to SDR only (IMF only supports SDR)
		symbols = []interface{}{"SDR"}
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			// IMF only provides SDR/USD - accept both "SDR" and "SDR/USD"
			if str == "SDR" || str == "SDR/USD" {
				symbolStrs = append(symbolStrs, "SDR")
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("IMF source only supports SDR symbol")
	}

	timeout := 10 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 5 * time.Minute // IMF updates daily, but check every 5 minutes
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	logger, err := logging.Init("info", "json", "stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return &IMFSource{
		name:     "imf",
		symbols:  symbolStrs,
		timeout:  timeout,
		interval: interval,
		client: &http.Client{
			Timeout: timeout,
		},
		prices:   make(map[string]sources.Price),
		stopChan: make(chan struct{}),
		logger:   logger,
	}, nil
}

func (s *IMFSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing IMF source for SDR/USD")
	return nil
}

func (s *IMFSource) Start(ctx context.Context) error {
	s.logger.Info("Starting IMF source")

	// Initial fetch with retry
	if err := s.retryFetch(ctx); err != nil {
		s.logger.Warn("Initial SDR price fetch failed after retries", "error", err)
	}

	// Start periodic updates
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-s.stopChan:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.retryFetch(ctx)
			}
		}
	}()

	return nil
}

func (s *IMFSource) retryFetch(ctx context.Context) error {
	maxRetries := 5
	initialBackoff := time.Second
	maxBackoff := 2 * time.Minute

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-s.stopChan:
			return fmt.Errorf("source stopped during retry")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := s.fetchSDRPrice(ctx)
		if err == nil {
			s.setHealthy(true)
			return nil
		}

		lastErr = err
		s.logger.Warn("Fetch attempt failed",
			"attempt", attempt,
			"max_retries", maxRetries,
			"error", err,
		)

		if attempt == maxRetries {
			break
		}

		backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		s.logger.Debug("Retrying after backoff", "backoff", backoff, "attempt", attempt+1)

		select {
		case <-time.After(backoff):
		case <-s.stopChan:
			return fmt.Errorf("source stopped during backoff")
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	s.setHealthy(false)
	s.logger.Error("Failed after all retries", "error", lastErr, "retries", maxRetries)
	return lastErr
}

func (s *IMFSource) fetchSDRPrice(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imfSDRURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch SDR page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.logger.Warn("Rate limit exceeded", "source", s.name)
		s.setHealthy(false)
		return fmt.Errorf("rate limit exceeded (HTTP 429)")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Parse HTML to find SDR/USD rate
	// Looking for pattern: "SDR1 = US$" followed by the rate
	rate, err := s.parseSDRRate(string(body))
	if err != nil {
		return fmt.Errorf("failed to parse SDR rate: %w", err)
	}

	now := time.Now()
	price := sources.Price{
		Symbol:    "SDR/USD",
		Price:     decimal.NewFromFloat(rate),
		Volume:    decimal.Zero,
		Timestamp: now,
		Source:    s.name,
	}

	s.pricesMu.Lock()
	s.prices["SDR/USD"] = price
	s.lastUpdate = now
	s.pricesMu.Unlock()

	// Notify subscribers
	prices := map[string]sources.Price{"SDR/USD": price}
	s.notifySubscribers(prices, nil)

	return nil
}

func (s *IMFSource) parseSDRRate(html string) (float64, error) {
	// Extract all table tags
	tableRegex := regexp.MustCompile(`<table[^>]*>([\s\S]*?)</table>`)
	tables := tableRegex.FindAllString(html, -1)

	for _, table := range tables {
		// Look for "SDR1 = US$" text
		if strings.Contains(table, "SDR1 = US$") || strings.Contains(table, "SDR 1 = US$") {
			// Extract table cells
			tdRegex := regexp.MustCompile(`<td[^>]*>([\s\S]*?)</td>`)
			cells := tdRegex.FindAllStringSubmatch(table, -1)

			for i, cell := range cells {
				cellText := strings.TrimSpace(stripHTML(cell[1]))
				if strings.Contains(cellText, "SDR1 = US$") || strings.Contains(cellText, "SDR 1 = US$") {
					// Next cell should contain the rate
					if i+1 < len(cells) {
						rateText := strings.TrimSpace(stripHTML(cells[i+1][1]))
						// Try parsing as single number first (most common format)
						rate, err := strconv.ParseFloat(rateText, 64)
						if err == nil && rate > 0 {
							s.logger.Info("Calculated SDR rate", "rate", fmt.Sprintf("%.7f", rate))
							return rate, nil
						}
						// Rate format might be "1.32149 2" - try to take the first or second number
						parts := strings.Fields(rateText)
						if len(parts) >= 1 {
							// Try first number
							rate, err := strconv.ParseFloat(parts[0], 64)
							if err == nil && rate > 0 {
								s.logger.Info("Calculated SDR rate", "rate", fmt.Sprintf("%.7f", rate))
								return rate, nil
							}
						}
						if len(parts) >= 2 {
							// Try second number
							rate, err := strconv.ParseFloat(parts[1], 64)
							if err == nil && rate > 0 {
								s.logger.Info("Calculated SDR rate", "rate", fmt.Sprintf("%.7f", rate))
								return rate, nil
							}
						}
					}
				}
			}
		}
	}

	return 0, fmt.Errorf("SDR/USD rate not found in HTML")
}

func stripHTML(s string) string {
	// Remove superscript and subscript tags with their content (footnote markers)
	re := regexp.MustCompile(`<(sup|sub)[^>]*>.*?</(sup|sub)>`)
	s = re.ReplaceAllString(s, "")
	// Remove remaining HTML tags
	re = regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, "")
}

func (s *IMFSource) Stop() error {
	s.logger.Info("Stopping IMF source")
	close(s.stopChan)
	return nil
}

func (s *IMFSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *IMFSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *IMFSource) Name() string {
	return s.name
}

func (s *IMFSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *IMFSource) Symbols() []string {
	return s.symbols
}

func (s *IMFSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *IMFSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *IMFSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *IMFSource) notifySubscribers(prices map[string]sources.Price, err error) {
	s.subscribersMu.RLock()
	defer s.subscribersMu.RUnlock()

	update := sources.PriceUpdate{
		Source: s.name,
		Prices: prices,
		Error:  err,
	}

	for _, sub := range s.subscribers {
		select {
		case sub <- update:
		default:
			s.logger.Warn("Subscriber channel full, skipping update")
		}
	}
}
