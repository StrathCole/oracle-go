package fiat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

// ExchangeRateSource fetches fiat prices from exchangerate.host API
// https://exchangerate.host/#/#our-services
// Requires API key (paid service)
type ExchangeRateSource struct {
	name          string
	symbols       []string
	apiKey        string
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

type exchangeRateResponse struct {
	Success bool               `json:"success"`
	Rates   map[string]float64 `json:"rates"`
	Error   interface{}        `json:"error,omitempty"`
}

func NewExchangeRateSource(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("symbols must be an array")
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			symbolStrs = append(symbolStrs, str)
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("symbols list is required")
	}

	// Get API key from config or environment
	apiKey := ""
	if key, ok := config["api_key"].(string); ok && key != "" {
		apiKey = key
	} else if keyEnv, ok := config["api_key_env"].(string); ok && keyEnv != "" {
		// In production, would read from os.Getenv(keyEnv)
		return nil, fmt.Errorf("api_key_env not yet implemented, please provide api_key directly")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("api_key is required for ExchangeRate source")
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 30 * time.Second
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	logger, err := logging.Init("info", "json", "stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return &ExchangeRateSource{
		name:     "exchangerate",
		symbols:  symbolStrs,
		apiKey:   apiKey,
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

func (s *ExchangeRateSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing ExchangeRate source", "symbols", len(s.symbols))
	return nil
}

func (s *ExchangeRateSource) Start(ctx context.Context) error {
	s.logger.Info("Starting ExchangeRate source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.logger.Warn("Initial price fetch failed", "error", err)
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
				if err := s.fetchPrices(ctx); err != nil {
					s.logger.Error("Failed to fetch prices", "error", err)
					s.setHealthy(false)
				} else {
					s.setHealthy(true)
				}
			}
		}
	}()

	return nil
}

func (s *ExchangeRateSource) fetchPrices(ctx context.Context) error {
	// Convert symbols to API format
	// SDR/USD -> XDR, EUR/USD -> EUR, etc.
	apiSymbols := make([]string, 0, len(s.symbols))
	for _, symbol := range s.symbols {
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
	newPrices := make(map[string]sources.Price)

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

		newPrices[targetSymbol] = sources.Price{
			Symbol:    targetSymbol,
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
	}
	s.lastUpdate = now
	s.pricesMu.Unlock()

	// Notify subscribers
	s.notifySubscribers(newPrices, nil)

	s.logger.Debug("Updated prices from ExchangeRate", "count", len(newPrices))

	return nil
}

func (s *ExchangeRateSource) Stop() error {
	s.logger.Info("Stopping ExchangeRate source")
	close(s.stopChan)
	return nil
}

func (s *ExchangeRateSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *ExchangeRateSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *ExchangeRateSource) Name() string {
	return s.name
}

func (s *ExchangeRateSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *ExchangeRateSource) Symbols() []string {
	return s.symbols
}

func (s *ExchangeRateSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *ExchangeRateSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *ExchangeRateSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *ExchangeRateSource) notifySubscribers(prices map[string]sources.Price, err error) {
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
