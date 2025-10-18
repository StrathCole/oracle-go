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

// ExchangeRateSource fetches fiat prices from exchangerate.host API
// https://exchangerate.host/#/#our-services
// Requires API key (paid service)
type ExchangeRateSource struct {
	*sources.BaseSource

	apiKey   string
	timeout  time.Duration
	interval time.Duration
	client   *http.Client
}

type exchangeRateResponse struct {
	Success bool               `json:"success"`
	Rates   map[string]float64 `json:"rates"`
	Error   interface{}        `json:"error,omitempty"`
}

func NewExchangeRateSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
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
		return nil, fmt.Errorf("no valid symbols for ExchangeRate")
	}

	apiKey, ok := config["api_key"].(string)
	if !ok {
		return nil, fmt.Errorf("api_key is required for ExchangeRate")
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 30 * time.Second
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

	baseSource := sources.NewBaseSource("exchangerate", sources.SourceTypeFiat, pairs, logger)

	s := &ExchangeRateSource{
		BaseSource: baseSource,
		apiKey:     apiKey,
		timeout:    timeout,
		interval:   interval,
		client: &http.Client{
			Timeout: timeout,
		},
	}

	s.Logger().Info("Initializing ExchangeRate source", "symbols", len(s.Symbols()))
	return s, nil
}

func (s *ExchangeRateSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Starting ExchangeRate source")

	// Initial fetch with retries
	if err := s.fetchWithRetries(ctx); err != nil {
		s.Logger().Warn("Initial price fetch failed after retries", "error", err)
	}

	return nil
}

func (s *ExchangeRateSource) Start(ctx context.Context) error {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := s.fetchWithRetries(ctx); err != nil {
					s.Logger().Warn("Periodic price fetch failed", "error", err.Error())
				}
			case <-s.StopChan():
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (s *ExchangeRateSource) fetchWithRetries(ctx context.Context) error {
	maxRetries := 5
	initialBackoff := time.Second
	maxBackoff := 2 * time.Minute

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Check if we should stop
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

		// Calculate backoff with exponential growth
		backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		s.Logger().Debug("Retrying after backoff",
			"backoff", backoff,
			"attempt", attempt+1,
		)

		select {
		case <-time.After(backoff):
			// Continue to next attempt
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

func (s *ExchangeRateSource) fetchPrices(ctx context.Context) error {
	symbols := s.Symbols()

	// Convert symbols to API format
	// SDR/USD -> XDR, EUR/USD -> EUR, etc.
	apiSymbols := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		if symbol == "SDR/USD" {
			apiSymbols = append(apiSymbols, "XDR")
		} else {
			// Remove /USD suffix
			apiSymbols = append(apiSymbols, strings.Replace(symbol, "/USD", "", 1))
		}
	}

	// Build request URL
	url := fmt.Sprintf("https://api.exchangerate.host/latest?base=USD&symbols=%s&access_key=%s",
		strings.Join(apiSymbols, ","),
		s.apiKey,
	)

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

	var data exchangeRateResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !data.Success || data.Rates == nil {
		return fmt.Errorf("invalid response: success=%v, error=%v", data.Success, data.Error)
	}

	// Convert prices
	// ExchangeRate gives us USD/EUR rate, we need EUR/USD
	// So we invert: EUR/USD = 1 / (USD/EUR)
	now := time.Now()

	for symbol, rate := range data.Rates {
		// Convert back to our format
		var targetSymbol string
		if symbol == "XDR" {
			targetSymbol = "SDR/USD"
		} else {
			targetSymbol = symbol + "/USD"
		}

		// Invert the rate (they give USD/XXX, we need XXX/USD)
		price := 1.0 / rate

		s.SetPrice(targetSymbol, decimal.NewFromFloat(price), now)
	}

	s.Logger().Debug("Updated prices from ExchangeRate", "count", len(data.Rates))

	return nil
}

func (s *ExchangeRateSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *ExchangeRateSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

func (s *ExchangeRateSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

func (s *ExchangeRateSource) Stop() error {
	s.Close()
	return nil
}
