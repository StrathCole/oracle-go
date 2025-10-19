package fiat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/shopspring/decimal"
)

const (
	baseCurrency = "USD"
	xdrDenom     = "XDR"
	sdrUsdPair   = "SDR/USD"
)

// ExchangeRateSource fetches fiat prices from exchangerate.host API
// https://exchangerate.host/#/#our-services
// Requires API key (paid service).
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

// NewExchangeRateSourceFromConfig creates a new ExchangeRateSource from config.
//
//nolint:dupl
func NewExchangeRateSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w", ErrSymbolsMustBeArray)
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			if strings.HasSuffix(str, "/"+baseCurrency) || str == sdrUsdPair {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("%w", ErrNoValidSymbolsExchange)
	}

	apiKey, ok := config["api_key"].(string)
	if !ok {
		return nil, fmt.Errorf("%w", ErrAPIKeyRequiredExchange)
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
		return nil, fmt.Errorf("%w", ErrLoggerNotProvided)
	}

	pairs := make(map[string]string)
	for _, symbol := range symbolStrs {
		pairs[symbol] = baseCurrency
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

// Initialize initializes the ExchangeRate source.
func (s *ExchangeRateSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Starting ExchangeRate source")

	// Initial fetch with retries
	if err := s.fetchWithRetries(ctx); err != nil {
		s.Logger().Warn("Initial price fetch failed after retries", "error", err)
	}

	return nil
}

// Start starts the ExchangeRate source.
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
	return FetchWithRetriesBase(ctx, s.BaseSource, s.StopChan(), s.fetchPrices)
}

func (s *ExchangeRateSource) fetchPrices(ctx context.Context) error {
	symbols := s.Symbols()

	// Convert symbols to API format
	// SDR/USD -> XDR, EUR/USD -> EUR, etc.
	apiSymbols := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		if symbol == sdrUsdPair {
			apiSymbols = append(apiSymbols, xdrDenom)
		} else {
			// Remove /USD suffix
			apiSymbols = append(apiSymbols, strings.Replace(symbol, "/"+baseCurrency, "", 1))
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		s.SetHealthy(false)
		return fmt.Errorf("%w", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	var data exchangeRateResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !data.Success || data.Rates == nil {
		return fmt.Errorf("%w: success=%v, error=%v", ErrInvalidResponse, data.Success, data.Error)
	}

	// Convert prices
	// ExchangeRate gives us USD/EUR rate, we need EUR/USD
	// So we invert: EUR/USD = 1 / (USD/EUR)
	now := time.Now()

	for symbol, rate := range data.Rates {
		// Convert back to our format
		var targetSymbol string
		if symbol == xdrDenom {
			targetSymbol = sdrUsdPair
		} else {
			targetSymbol = symbol + "/" + baseCurrency
		}

		// Invert the rate (they give USD/XXX, we need XXX/USD)
		price := 1.0 / rate

		s.SetPrice(targetSymbol, decimal.NewFromFloat(price), now)
	}

	s.Logger().Debug("Updated prices from ExchangeRate", "count", len(data.Rates))

	return nil
}

// Type returns the source type.
func (s *ExchangeRateSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

// GetPrices returns the current prices.
func (s *ExchangeRateSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *ExchangeRateSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Stop stops the ExchangeRate source.
func (s *ExchangeRateSource) Stop() error {
	s.Close()
	return nil
}
