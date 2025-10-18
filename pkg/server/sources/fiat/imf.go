package fiat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/server/sources"
)

const imfSDRURL = "https://www.imf.org/external/np/fin/data/rms_sdrv.aspx"

// IMFSource fetches SDR/USD price from IMF website (free, no API key)
// https://www.imf.org/ - Official SDR valuations
type IMFSource struct {
	*sources.BaseSource

	timeout  time.Duration
	interval time.Duration
	client   *http.Client
}

func NewIMFSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
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
				symbolStrs = append(symbolStrs, "SDR/USD")
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("IMF source requires SDR symbol")
	}

	timeout := 10 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 5 * time.Minute // IMF updates daily, check every 5 minutes
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	// Get logger from config (passed from main.go)
	logger := sources.GetLoggerFromConfig(config)
	if logger == nil {
		return nil, fmt.Errorf("logger not provided in config")
	}

	pairs := make(map[string]string)
	for _, symbol := range symbolStrs {
		pairs[symbol] = "USD"
	}

	baseSource := sources.NewBaseSource("imf", sources.SourceTypeFiat, pairs, logger)

	s := &IMFSource{
		BaseSource: baseSource,
		timeout:    timeout,
		interval:   interval,
		client: &http.Client{
			Timeout: timeout,
		},
	}

	s.Logger().Info("Initializing IMF source", "symbols", len(s.Symbols()))
	return s, nil
}

func (s *IMFSource) Initialize(ctx context.Context) error {
	return nil
}

func (s *IMFSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting IMF source")

	if err := s.fetchWithRetries(ctx); err != nil {
		s.Logger().Warn("Initial SDR price fetch failed after retries", "error", err)
	}

	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-s.StopChan():
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.fetchWithRetries(ctx)
			}
		}
	}()

	return nil
}

func (s *IMFSource) fetchWithRetries(ctx context.Context) error {
	maxRetries := 5
	initialBackoff := time.Second
	maxBackoff := 2 * time.Minute

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-s.StopChan():
			return fmt.Errorf("source stopped during retry")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := s.fetchPrices(ctx)
		if err == nil {
			s.SetHealthy(true)
			return nil
		}

		lastErr = err
		s.Logger().Warn("Fetch attempt failed",
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

		s.Logger().Debug("Retrying after backoff", "backoff", backoff, "attempt", attempt+1)

		select {
		case <-time.After(backoff):
		case <-s.StopChan():
			return fmt.Errorf("source stopped during backoff")
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	s.SetHealthy(false)
	s.Logger().Error("Failed after all retries", "error", lastErr, "retries", maxRetries)
	return lastErr
}

func (s *IMFSource) fetchPrices(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imfSDRURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch IMF page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse SDR rate from HTML
	// Look for pattern: SDR1 = US$</td><td...>1.367670<sup>4</sup></td>
	// The IMF page shows "SDR 1 = US$ X.XXXXXX" which is the rate we need
	re := regexp.MustCompile(`SDR\s*1\s*=\s*US\$\s*</td>\s*<td[^>]*>\s*([\d.]+)`)
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		return fmt.Errorf("failed to find SDR rate in IMF page")
	}

	rateStr := string(matches[1])
	rate, err := strconv.ParseFloat(rateStr, 64)
	if err != nil {
		return fmt.Errorf("failed to parse SDR rate '%s': %w", rateStr, err)
	}

	if rate <= 0 {
		return fmt.Errorf("invalid SDR rate: %f", rate)
	}

	now := time.Now()
	s.SetPrice("SDR/USD", decimal.NewFromFloat(rate), now)

	s.Logger().Info("Parsed SDR rate from IMF", "rate", rate)
	return nil
}

func (s *IMFSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *IMFSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

func (s *IMFSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

func (s *IMFSource) Stop() error {
	s.Close()
	return nil
}
