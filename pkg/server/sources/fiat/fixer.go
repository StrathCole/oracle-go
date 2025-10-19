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
// Requires API key (paid service).
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

// NewFixerSourceFromConfig creates a new FixerSource from config.
//
//nolint:dupl
func NewFixerSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
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
		return nil, fmt.Errorf("%w", ErrNoValidSymbolsFixer)
	}

	apiKey, ok := config["api_key"].(string)
	if !ok {
		return nil, fmt.Errorf("%w", ErrAPIKeyRequiredFixer)
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
		return nil, fmt.Errorf("%w", ErrLoggerNotProvided)
	}

	pairs := make(map[string]string)
	for _, symbol := range symbolStrs {
		pairs[symbol] = baseCurrency
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

// Initialize initializes the Fixer source.
func (s *FixerSource) Initialize(_ context.Context) error {
	return nil
}

// Start starts the Fixer source.
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
				_ = s.fetchWithRetries(ctx)
			}
		}
	}()

	return nil
}

func (s *FixerSource) fetchWithRetries(ctx context.Context) error {
	return FetchWithRetriesBase(ctx, s.BaseSource, s.StopChan(), s.fetchPrices)
}

func (s *FixerSource) fetchPrices(ctx context.Context) error {
	symbols := s.Symbols()
	currencies := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		if symbol == sdrUsdPair {
			currencies = append(currencies, "XDR")
		} else {
			parts := strings.Split(symbol, "/")
			if len(parts) == 2 && parts[1] == baseCurrency {
				currencies = append(currencies, parts[0])
			}
		}
	}

	if len(currencies) == 0 {
		return fmt.Errorf("%w", ErrNoCurrenciesToFetch)
	}

	url := fmt.Sprintf("https://api.fixer.io/latest?access_key=%s&base=%s&symbols=%s",
		s.apiKey, baseCurrency, strings.Join(currencies, ","))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		s.SetHealthy(false)
		return fmt.Errorf("%w", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	var data fixerResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !data.Success {
		return fmt.Errorf("%w: %v", sources.ErrAPIError, data.Error)
	}

	now := time.Now()
	for currency, rate := range data.Rates {
		var symbol string
		if currency == "XDR" {
			symbol = sdrUsdPair
		} else {
			symbol = currency + "/" + baseCurrency
		}

		price := 1.0 / rate
		s.SetPrice(symbol, decimal.NewFromFloat(price), now)
	}

	s.Logger().Debug("Updated prices from Fixer", "count", len(data.Rates))
	return nil
}

// Type returns the source type.
func (s *FixerSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

// GetPrices returns the current prices.
func (s *FixerSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *FixerSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Stop stops the Fixer source.
func (s *FixerSource) Stop() error {
	s.Close()
	return nil
}
