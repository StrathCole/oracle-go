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

// FERSource fetches fiat prices from FER.ee API (free, no API key)
// https://fer.ee/
type FERSource struct {
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

type ferResponse struct {
	Date  string             `json:"date"`
	Base  string             `json:"base"`
	Rates map[string]float64 `json:"rates"`
}

func NewFERSource(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("symbols must be an array")
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			// Skip SDR - FER doesn't support it
			if !strings.Contains(str, "SDR") {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("no valid symbols for FER")
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

	return &FERSource{
		name:     "fer",
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

func (s *FERSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing FER source", "symbols", len(s.symbols))
	return nil
}

func (s *FERSource) Start(ctx context.Context) error {
	s.logger.Info("Starting FER source")

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

func (s *FERSource) fetchPrices(ctx context.Context) error {
	// FER API uses USD as base by default
	// We fetch from=USD and get all rates
	url := "https://api.fer.ee/latest?from=USD"

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

	var data ferResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if data.Rates == nil {
		return fmt.Errorf("invalid response: no rates")
	}

	// Convert prices
	// FER gives us USD/EUR rate, we need EUR/USD
	// So we invert: EUR/USD = 1 / (USD/EUR)
	now := time.Now()
	newPrices := make(map[string]sources.Price)

	for currency, rate := range data.Rates {
		symbol := currency + "/USD"

		// Only include symbols we're interested in
		found := false
		for _, s := range s.symbols {
			if s == symbol {
				found = true
				break
			}
		}
		if !found {
			continue
		}

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
	}
	s.lastUpdate = now
	s.pricesMu.Unlock()

	// Notify subscribers
	s.notifySubscribers(newPrices, nil)

	s.logger.Debug("Updated prices from FER", "count", len(newPrices))

	return nil
}

func (s *FERSource) Stop() error {
	s.logger.Info("Stopping FER source")
	close(s.stopChan)
	return nil
}

func (s *FERSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *FERSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *FERSource) Name() string {
	return s.name
}

func (s *FERSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *FERSource) Symbols() []string {
	return s.symbols
}

func (s *FERSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *FERSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *FERSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *FERSource) notifySubscribers(prices map[string]sources.Price, err error) {
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
