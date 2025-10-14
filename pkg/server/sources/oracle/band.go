package oracle

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

const (
	bandProtocolAPIURL = "https://laozi1.bandchain.org/api/oracle/v1/request_prices"
	bandDefaultTimeout = 10 * time.Second
)

// BandProtocolSource fetches prices from Band Protocol oracle
// https://bandprotocol.com/
// Supports both crypto and fiat price feeds
type BandProtocolSource struct {
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

// BandPriceResult represents a single price from Band Protocol
type bandPriceResult struct {
	Symbol      string `json:"symbol"`
	Multiplier  string `json:"multiplier"`
	Px          string `json:"px"`
	RequestID   string `json:"request_id"`
	ResolveTime string `json:"resolve_time"`
}

// BandPriceResponse represents the Band Protocol API response
type bandPriceResponse struct {
	PriceResults []bandPriceResult `json:"price_results"`
}

func NewBandProtocolSource(config map[string]interface{}) (sources.Source, error) {
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

	timeout := bandDefaultTimeout
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 60 * time.Second // Band updates less frequently
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	logger, err := logging.Init("info", "json", "stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return &BandProtocolSource{
		name:     "band",
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

func (s *BandProtocolSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing Band Protocol source", "symbols", len(s.symbols))
	return nil
}

func (s *BandProtocolSource) Start(ctx context.Context) error {
	s.logger.Info("Starting Band Protocol source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.logger.Warn("Initial price fetch failed", "error", err)
		s.setHealthy(false)
	} else {
		s.setHealthy(true)
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

func (s *BandProtocolSource) fetchPrices(ctx context.Context) error {
	// Convert our symbols to Band format
	// EUR/USD -> EUR, BTC/USD -> BTC, etc.
	bandSymbols := make([]string, 0, len(s.symbols))
	for _, symbol := range s.symbols {
		parts := strings.Split(symbol, "/")
		if len(parts) == 2 {
			// Band uses simple symbols like "BTC", "EUR", etc.
			bandSymbols = append(bandSymbols, parts[0])
		}
	}

	if len(bandSymbols) == 0 {
		return fmt.Errorf("no valid symbols to fetch")
	}

	// Build request URL with multiple symbols parameters
	// Band Protocol format: ?symbols=BTC&symbols=ETH&symbols=EUR
	// Note: Band Protocol validator count requirements:
	// - ask_count: number of validators to query (max depends on active validators)
	// - min_count: minimum required responses
	// Using lower values (4/3) for more reliable results
	url := bandProtocolAPIURL + "?min_count=3&ask_count=4"
	for _, symbol := range bandSymbols {
		url += "&symbols=" + symbol
	}

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

	var data bandPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if data.PriceResults == nil {
		return fmt.Errorf("invalid response: no price results")
	}

	// Convert prices
	now := time.Now()
	newPrices := make(map[string]sources.Price)

	for _, result := range data.PriceResults {
		// Band returns prices as integers with a multiplier
		// Price = px / multiplier
		priceFloat, err := result.getPrice()
		if err != nil {
			s.logger.Warn("Failed to parse Band price", "symbol", result.Symbol, "error", err)
			continue
		}

		// Convert back to our format (e.g., BTC -> BTC/USD)
		symbol := result.Symbol + "/USD"

		newPrices[symbol] = sources.Price{
			Symbol:    symbol,
			Price:     decimal.NewFromFloat(priceFloat),
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

	s.logger.Debug("Updated prices from Band Protocol", "count", len(newPrices))

	return nil
}

func (r *bandPriceResult) getPrice() (float64, error) {
	// Parse px (price * multiplier)
	var px, multiplier float64
	var err error

	if _, err = fmt.Sscanf(r.Px, "%f", &px); err != nil {
		return 0, fmt.Errorf("failed to parse px: %w", err)
	}

	if _, err = fmt.Sscanf(r.Multiplier, "%f", &multiplier); err != nil {
		return 0, fmt.Errorf("failed to parse multiplier: %w", err)
	}

	if multiplier == 0 {
		return 0, fmt.Errorf("multiplier is zero")
	}

	return px / multiplier, nil
}

func (s *BandProtocolSource) Stop() error {
	s.logger.Info("Stopping Band Protocol source")
	close(s.stopChan)
	return nil
}

func (s *BandProtocolSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *BandProtocolSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *BandProtocolSource) Name() string {
	return s.name
}

func (s *BandProtocolSource) Type() sources.SourceType {
	return sources.SourceTypeOracle
}

func (s *BandProtocolSource) Symbols() []string {
	return s.symbols
}

func (s *BandProtocolSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *BandProtocolSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *BandProtocolSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *BandProtocolSource) notifySubscribers(prices map[string]sources.Price, err error) {
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
