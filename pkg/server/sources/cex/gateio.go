package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	gateioAPIURL   = "https://api.gateio.ws/api/v4/spot/tickers"
	gateioPollRate = 60 * time.Second
)

// GateioSource fetches prices from Gate.io REST API
type GateioSource struct {
	*sources.BaseSource

	apiURL string
}

// GateioTicker represents a ticker response from Gate.io
type GateioTicker struct {
	CurrencyPair     string `json:"currency_pair"`
	Last             string `json:"last"`
	LowestAsk        string `json:"lowest_ask"`
	HighestBid       string `json:"highest_bid"`
	ChangePercentage string `json:"change_percentage"`
	BaseVolume       string `json:"base_volume"`
	QuoteVolume      string `json:"quote_volume"`
	High24h          string `json:"high_24h"`
	Low24h           string `json:"low_24h"`
}

// NewGateioSource creates a new Gate.io REST source
func NewGateioSource(config map[string]interface{}) (sources.Source, error) {
	logger, _ := logging.Init("info", "text", "stdout")

	// Parse pairs from config (map of "LUNC/USDT" => "LUNC_USDT")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := gateioAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("gateio", sources.SourceTypeCEX, pairs, logger)

	return &GateioSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation
func (s *GateioSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Gate.io source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices
func (s *GateioSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Gate.io source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err)
	}

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source
func (s *GateioSource) Stop() error {
	s.Logger().Info("Gate.io source stopped")
	return nil
}

// GetPrices returns the current prices
func (s *GateioSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe adds a subscriber
func (s *GateioSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices
func (s *GateioSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(gateioPollRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.StopChan():
			return
		case <-ticker.C:
			if err := s.fetchPrices(ctx); err != nil {
				s.Logger().Error("Failed to fetch prices", "error", err)
				s.SetHealthy(false)
			}
		}
	}
}

// fetchPrices fetches current prices from Gate.io API
func (s *GateioSource) fetchPrices(ctx context.Context) error {
	// Make request (returns all tickers)
	req, err := http.NewRequestWithContext(ctx, "GET", s.apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var tickers []GateioTicker
	if err := json.Unmarshal(body, &tickers); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	now := time.Now()
	updateCount := 0

	// Create reverse map: gateio symbol -> unified symbol
	symbolMap := make(map[string]string)
	for unifiedSymbol, gateioSymbol := range s.GetAllPairs() {
		symbolMap[gateioSymbol] = unifiedSymbol
	}

	for _, ticker := range tickers {
		// Check if this is a symbol we want
		unifiedSymbol, ok := symbolMap[ticker.CurrencyPair]
		if !ok {
			continue
		}

		priceFloat, err := strconv.ParseFloat(ticker.Last, 64)
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", ticker.CurrencyPair, "price", ticker.Last, "error", err)
			continue
		}

		// Use BaseSource SetPrice
		s.SetPrice(unifiedSymbol, decimal.NewFromFloat(priceFloat), now)
		metrics.RecordSourceUpdate(s.Name(), unifiedSymbol)
		updateCount++
	}

	if updateCount > 0 {
		s.SetHealthy(true)
		metrics.RecordSourceHealth(s.Name(), string(s.Type()), true)
		s.Logger().Debug("Updated prices", "count", updateCount)
	}

	return nil
}

func init() {
	sources.Register("cex.gateio", func(config map[string]interface{}) (sources.Source, error) {
		return NewGateioSource(config)
	})
}
