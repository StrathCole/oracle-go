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

// FixerSource fetches fiat prices from Fixer.io API
// https://fixer.io/product
// Requires API key (paid service)
type FixerSource struct {
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

type fixerResponse struct {
	Success bool               `json:"success"`
	Rates   map[string]float64 `json:"rates"`
	Error   interface{}        `json:"error,omitempty"`
}

func NewFixerSource(config map[string]interface{}) (sources.Source, error) {
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
		return nil, fmt.Errorf("api_key is required for Fixer source")
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 60 * time.Second
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	logger, err := logging.Init("info", "json", "stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return &FixerSource{
		name:     "fixer",
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

func (s *FixerSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing Fixer source", "symbols", len(s.symbols))
	return nil
}

func (s *FixerSource) Start(ctx context.Context) error {
	s.logger.Info("Starting Fixer source")

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

func (s *FixerSource) fetchPrices(ctx context.Context) error {
	// Convert symbols to API format
	// Fixer base is EUR by default (free tier), so we need to fetch USD too
	apiSymbols := make([]string, 0, len(s.symbols)+1)
	apiSymbols = append(apiSymbols, "USD") // Always fetch USD for conversion

	for _, symbol := range s.symbols {
		if symbol == "SDR/USD" {
			apiSymbols = append(apiSymbols, "XDR")
		} else {
			// Remove /USD suffix
			apiSymbols = append(apiSymbols, strings.Replace(symbol, "/USD", "", 1))
		}
	}

	// Build request URL
	url := fmt.Sprintf("https://data.fixer.io/api/latest?access_key=%s&symbols=%s",
		s.apiKey,
		strings.Join(apiSymbols, ","),
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

	var data fixerResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !data.Success || data.Rates == nil {
		return fmt.Errorf("invalid response: success=%v, error=%v", data.Success, data.Error)
	}

	// Get USD rate (base is EUR, so we get EUR/USD)
	usdRate, ok := data.Rates["USD"]
	if !ok {
		return fmt.Errorf("USD rate not found in response")
	}

	// Convert all rates to USD base
	// Fixer gives EUR/XXX, we need XXX/USD
	// Formula: XXX/USD = (EUR/XXX) * (USD/EUR) = (EUR/XXX) / (EUR/USD)
	now := time.Now()
	newPrices := make(map[string]sources.Price)

	for symbol, rate := range data.Rates {
		if symbol == "USD" {
			continue // Skip USD itself
		}

		// Convert back to our format
		var targetSymbol string
		if symbol == "XDR" {
			targetSymbol = "SDR/USD"
		} else {
			targetSymbol = symbol + "/USD"
		}

		// Convert from EUR base to USD base, then invert
		// rate is EUR/XXX, usdRate is EUR/USD
		// XXX/USD = 1 / ((EUR/XXX) / (EUR/USD)) = (EUR/USD) / (EUR/XXX)
		priceInUSD := usdRate / rate
		// But we want XXX/USD (how many USD per 1 XXX), so invert
		price := 1.0 / priceInUSD

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

	s.logger.Debug("Updated prices from Fixer", "count", len(newPrices))

	return nil
}

func (s *FixerSource) Stop() error {
	s.logger.Info("Stopping Fixer source")
	close(s.stopChan)
	return nil
}

func (s *FixerSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *FixerSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *FixerSource) Name() string {
	return s.name
}

func (s *FixerSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *FixerSource) Symbols() []string {
	return s.symbols
}

func (s *FixerSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *FixerSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *FixerSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *FixerSource) notifySubscribers(prices map[string]sources.Price, err error) {
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
