package fiat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
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

// NewFrankfurterSourceFromConfig creates a new FrankfurterSource from config.
func NewFrankfurterSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
	symbolsIface, ok := config["symbols"]
	if !ok {
		return nil, fmt.Errorf("%w", ErrMissingSymbolsInConfig)
	}

	symbolList, ok := symbolsIface.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w", ErrInvalidSymbolsType)
	}

	symbolStrs := make([]string, 0, len(symbolList))
	for _, s := range symbolList {
		if str, ok := s.(string); ok {
			if strings.HasSuffix(str, "/USD") || str == "SDR/USD" {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("%w", ErrNoValidSymbolsFrankfurt)
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 15 * time.Second
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

	baseSource := sources.NewBaseSource("frankfurter", sources.SourceTypeFiat, pairs, logger)

	s := &FrankfurterSource{
		BaseSource: baseSource,
		timeout:    timeout,
		interval:   interval,
		client: &http.Client{
			Timeout: timeout,
		},
	}

	s.Logger().Info("Initializing Frankfurter source", "symbols", len(s.Symbols()))
	return s, nil
}

// Initialize prepares the source.
func (s *FrankfurterSource) Initialize(_ context.Context) error {
	return nil
}

// Start starts the Frankfurter source.
func (s *FrankfurterSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Frankfurter source")

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

func (s *FrankfurterSource) fetchWithRetries(ctx context.Context) error {
	return FetchWithRetriesBase(ctx, s.BaseSource, s.StopChan(), s.fetchPrices)
}

func (s *FrankfurterSource) fetchPrices(ctx context.Context) error {
	symbols := s.Symbols()
	currencies := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		parts := strings.Split(symbol, "/")
		if len(parts) == 2 && parts[1] == "USD" {
			currencies = append(currencies, parts[0])
		}
	}

	if len(currencies) == 0 {
		return fmt.Errorf("%w", ErrNoCurrenciesToFetch)
	}

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

	var data frankfurterResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	now := time.Now()
	for currency, rate := range data.Rates {
		symbol := currency + "/USD"
		price := 1.0 / rate
		s.SetPrice(symbol, decimal.NewFromFloat(price), now)
	}

	s.Logger().Debug("Updated prices from Frankfurter", "count", len(data.Rates))
	return nil
}

// Type returns the source type.
func (s *FrankfurterSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

// GetPrices returns the current prices.
func (s *FrankfurterSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *FrankfurterSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Stop stops the Frankfurter source.
func (s *FrankfurterSource) Stop() error {
	s.Close()
	return nil
}
