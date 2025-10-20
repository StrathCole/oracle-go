package oracle

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

const (
	bandProtocolAPIURL = "https://laozi1.bandchain.org/api/oracle/v1/request_prices"
	bandDefaultTimeout = 10 * time.Second
)

// BandProtocolSource fetches prices from Band Protocol oracle.
// https://bandprotocol.com/
// Supports both crypto and fiat price feeds.
type BandProtocolSource struct {
	*sources.BaseSource

	timeout  time.Duration
	interval time.Duration
	client   *http.Client
}

// BandPriceResult represents a single price from Band Protocol.
type bandPriceResult struct {
	Symbol      string `json:"symbol"`
	Multiplier  string `json:"multiplier"`
	Px          string `json:"px"`
	RequestID   string `json:"request_id"`
	ResolveTime string `json:"resolve_time"`
}

// BandPriceResponse represents the Band Protocol API response.
type bandPriceResponse struct {
	PriceResults []bandPriceResult `json:"price_results"`
}

// NewBandProtocolSource creates a new Band Protocol oracle source.
func NewBandProtocolSource(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w", ErrSymbolsMustBeArray)
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			symbolStrs = append(symbolStrs, str)
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("%w", ErrSymbolsListRequired)
	}

	timeout := bandDefaultTimeout
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 60 * time.Second // Band updates less frequently
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	// Get logger from config (passed from main.go)
	logger := sources.GetLoggerFromConfig(config)
	if logger == nil {
		return nil, fmt.Errorf("%w", ErrLoggerNotProvided)
	}

	// Create symbol mapping (Band uses same symbols, no transformation needed)
	pairs := make(map[string]string, len(symbolStrs))
	for _, symbol := range symbolStrs {
		pairs[symbol] = symbol
	}

	// Create BaseSource
	base := sources.NewBaseSource("band", sources.SourceTypeOracle, pairs, logger)

	return &BandProtocolSource{
		BaseSource: base,
		timeout:    timeout,
		interval:   interval,
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// Initialize prepares the source.
func (s *BandProtocolSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing Band Protocol source", "symbols", len(s.Symbols()))
	return nil
}

// Start starts the Band Protocol source.
func (s *BandProtocolSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Band Protocol source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial price fetch failed", "error", err.Error())
		s.SetHealthy(false)
	}

	// Start periodic updates
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
				if err := s.fetchPrices(ctx); err != nil {
					s.Logger().Error("Failed to fetch prices", "error", err.Error())
					s.SetHealthy(false)
				}
			}
		}
	}()

	return nil
}

func (s *BandProtocolSource) fetchPrices(ctx context.Context) error {
	// Convert our symbols to Band format
	// EUR/USD -> EUR, BTC/USD -> BTC, etc.
	bandSymbols := make([]string, 0, len(s.Symbols()))
	for _, symbol := range s.Symbols() {
		parts := strings.Split(symbol, "/")
		if len(parts) == 2 {
			// Band uses simple symbols like "BTC", "EUR", etc.
			bandSymbols = append(bandSymbols, parts[0])
		}
	}

	if len(bandSymbols) == 0 {
		return fmt.Errorf("%w", ErrNoValidSymbolsToFetch)
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	var data bandPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if data.PriceResults == nil {
		return fmt.Errorf("%w", ErrInvalidResponseNoPrices)
	}

	// Convert prices
	now := time.Now()

	for _, result := range data.PriceResults {
		// Band returns prices as integers with a multiplier
		// Price = px / multiplier
		priceFloat, err := result.getPrice()
		if err != nil {
			s.Logger().Warn("Failed to parse Band price", "symbol", result.Symbol, "error", err.Error())
			continue
		}

		// Convert back to our format (e.g., BTC -> BTC/USD)
		symbol := result.Symbol + "/USD"

		// Use BaseSource.SetPrice() which handles storage, metrics, and notification
		s.SetPrice(symbol, decimal.NewFromFloat(priceFloat), now)
	}

	s.Logger().Debug("Updated prices from Band Protocol")
	s.SetHealthy(true)

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
		return 0, fmt.Errorf("%w", ErrMultiplierIsZero)
	}

	return px / multiplier, nil
}

// Stop stops the Band Protocol source.
func (s *BandProtocolSource) Stop() error {
	s.Logger().Info("Stopping Band Protocol source")
	s.Close()
	return nil
}

// GetPrices returns the current prices.
func (s *BandProtocolSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *BandProtocolSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Type returns the source type (overrides BaseSource since it stores it differently).
func (s *BandProtocolSource) Type() sources.SourceType {
	return sources.SourceTypeOracle
}
