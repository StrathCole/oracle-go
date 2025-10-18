package fiat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/server/sources"
)

// FixerSource fetches fiat prices from Fixer.io API
// https://fixer.io/product
// Requires API key (paid service)
type FixerSource struct {
	*sources.BaseSource

	apiKey   string
	timeout  time.Duration
	interval time.Duration
	client   *http.Client
}

type fixerResponse struct {
	Success bool               `json:"success"`
	Rates   map[string]float64 `json:"rates"`
	Error   interface{}        `json:"error,omitempty"`
}

func NewFixerSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("symbols must be an array")
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			if strings.HasSuffix(str, "/USD") || str == "SDR/USD" {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("no valid symbols for Fixer")
	}

	apiKey, ok := config["api_key"].(string)
	if !ok {
		return nil, fmt.Errorf("api_key is required for Fixer")
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 60 * time.Second
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

	baseSource := sources.NewBaseSource("fixer", sources.SourceTypeFiat, pairs, logger)

	s := &FixerSource{
		BaseSource: baseSource,
		apiKey:     apiKey,
		timeout:    timeout,
		interval:   interval,
		client: &http.Client{
			Timeout: timeout,
		},
	}

	s.Logger().Info("Initializing Fixer source", "symbols", len(s.Symbols()))
	return s, nil
}

func (s *FixerSource) Initialize(ctx context.Context) error {
	return nil
}

func (s *FixerSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Fixer source")

	if err := s.fetchWithRetries(ctx); err != nil {
		s.Logger().Warn("Initial price fetch failed after retries", "error", err)
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

func (s *FixerSource) fetchWithRetries(ctx context.Context) error {
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

func (s *FixerSource) fetchPrices(ctx context.Context) error {
	symbols := s.Symbols()
	currencies := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		if symbol == "SDR/USD" {
			currencies = append(currencies, "XDR")
		} else {
			parts := strings.Split(symbol, "/")
			if len(parts) == 2 && parts[1] == "USD" {
				currencies = append(currencies, parts[0])
			}
		}
	}

	if len(currencies) == 0 {
		return fmt.Errorf("no valid currencies to fetch")
	}

	url := fmt.Sprintf("https://api.fixer.io/latest?access_key=%s&base=USD&symbols=%s",
		s.apiKey, strings.Join(currencies, ","))

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
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		s.SetHealthy(false)
		return fmt.Errorf("rate limit exceeded (HTTP 429)")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var data fixerResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !data.Success {
		return fmt.Errorf("API error: %v", data.Error)
	}

	now := time.Now()
	for currency, rate := range data.Rates {
		var symbol string
		if currency == "XDR" {
			symbol = "SDR/USD"
		} else {
			symbol = currency + "/USD"
		}

		price := 1.0 / rate
		s.SetPrice(symbol, decimal.NewFromFloat(price), now)
	}

	s.Logger().Debug("Updated prices from Fixer", "count", len(data.Rates))
	return nil
}

func (s *FixerSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *FixerSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

func (s *FixerSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

func (s *FixerSource) Stop() error {
	s.Close()
	return nil
}
