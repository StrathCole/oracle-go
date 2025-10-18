package fiat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

// FrankfurterSource fetches fiat prices from Frankfurter API (free, no API key)
// https://www.frankfurter.app/docs/
type FrankfurterSource struct {
	*sources.BaseSource
	
	timeout  time.Duration
	interval time.Duration
	client   *http.Client
}

type frankfurterResponse struct {
	Amount float64            `json:"amount"`
	Base   string             `json:"base"`
	Date   string             `json:"date"`
	Rates  map[string]float64 `json:"rates"`
}

func NewFrankfurterSource(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("symbols must be an array")
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			// Skip SDR - Frankfurter doesn't support it
			if !strings.Contains(str, "SDR") {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("no valid symbols for Frankfurter")
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 15 * time.Second // Update every 15s (vote period is 30s)
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	// Create logger
	logger, err := logging.Init("info", "json", "stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return &FrankfurterSource{
		name:     "frankfurter",
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

func (s *FrankfurterSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing Frankfurter source", "symbols", len(s.symbols))
	return nil
}

func (s *FrankfurterSource) Start(ctx context.Context) error {
	s.logger.Info("Starting Frankfurter source")

	// Initial fetch with retry
	if err := s.retryFetch(ctx); err != nil {
		s.logger.Warn("Initial price fetch failed after retries", "error", err)
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

func (s *FrankfurterSource) retryFetch(ctx context.Context) error {
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

		err := s.fetchPrices(ctx)
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

func (s *FrankfurterSource) fetchPrices(ctx context.Context) error {
	// Convert symbols to API format
	// EUR/USD -> EUR, JPY/USD -> JPY, etc.
	currencies := make([]string, 0, len(s.symbols))
	for _, symbol := range s.symbols {
		parts := strings.Split(symbol, "/")
		if len(parts) == 2 && parts[1] == "USD" {
			currencies = append(currencies, parts[0])
		}
	}

	if len(currencies) == 0 {
		return fmt.Errorf("no valid currencies to fetch")
	}

	// Build request URL with USD as base
	url := fmt.Sprintf("https://api.frankfurter.app/latest?from=USD&to=%s",
		strings.Join(currencies, ","))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
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

	var data frankfurterResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert prices
	// Frankfurter gives us USD/EUR rate, we need EUR/USD
	// So we invert: EUR/USD = 1 / (USD/EUR)
	now := time.Now()
	newPrices := make(map[string]sources.Price)

	for currency, rate := range data.Rates {
		symbol := currency + "/USD"

		// Invert the rate (they give USD/XXX, we need XXX/USD)
		price := 1.0 / rate

		newPrices[symbol] = sources.Price{
			Symbol:    symbol,
			Price:     decimal.NewFromFloat(price),
			Volume:    decimal.Zero,
			Timestamp: now,
			Source:    s.name,
		}
	}

	// Update stored prices
	s.pricesMu.Lock()
	for symbol, price := range newPrices {
		s.prices[symbol] = price
		// Record metric for this price update
		metrics.RecordSourceUpdate(s.name, symbol)
	}
	s.lastUpdate = now
	s.pricesMu.Unlock()

	// Notify subscribers
	s.notifySubscribers(newPrices, nil)

	s.logger.Debug("Updated prices from Frankfurter", "count", len(newPrices))

	return nil
}

func (s *FrankfurterSource) Stop() error {
	s.logger.Info("Stopping Frankfurter source")
	close(s.stopChan)
	return nil
}

func (s *FrankfurterSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *FrankfurterSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *FrankfurterSource) Name() string {
	return s.name
}

func (s *FrankfurterSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *FrankfurterSource) Symbols() []string {
	return s.symbols
}

func (s *FrankfurterSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *FrankfurterSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *FrankfurterSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *FrankfurterSource) notifySubscribers(prices map[string]sources.Price, err error) {
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
