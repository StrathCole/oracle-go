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
	bybitAPIURL   = "https://api.bybit.com/v5/market/tickers"
	bybitPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// BybitSource fetches prices from Bybit REST API
type BybitSource struct {
	*sources.BaseSource

	apiURL string
}

// BybitResponse represents the API response
type BybitResponse struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		Category string `json:"category"`
		List     []struct {
			Symbol    string `json:"symbol"`
			LastPrice string `json:"lastPrice"`
			Volume24h string `json:"volume24h"`
		} `json:"list"`
	} `json:"result"`
}

// NewBybitSource creates a new Bybit REST source
func NewBybitSource(config map[string]interface{}) (sources.Source, error) {
	logger, _ := logging.Init("info", "text", "stdout")

	// Parse pairs from config (map of "LUNC/USDT" => "LUNCUSDT")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := bybitAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("bybit", sources.SourceTypeCEX, pairs, logger)

	return &BybitSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation
func (s *BybitSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Bybit source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices
func (s *BybitSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Bybit source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err)
	}

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source
func (s *BybitSource) Stop() error {
	s.Logger().Info("Bybit source stopped")
	return nil
}

// GetPrices returns the current prices
func (s *BybitSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe adds a subscriber
func (s *BybitSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices
func (s *BybitSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(bybitPollRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.StopChan():
			return
		case <-ticker.C:
			// Use retry logic for fetching prices
			err := s.RetryWithBackoff(ctx, "fetch_prices", func() error {
				return s.fetchPrices(ctx)
			})
			if err != nil {
				s.Logger().Error("Failed to fetch prices after retries", "error", err)
				s.SetHealthy(false)
			} else {
				s.SetHealthy(true)
			}
		}
	}
}

// fetchPrices fetches current prices from Bybit API
func (s *BybitSource) fetchPrices(ctx context.Context) error {
	// Make request (category=spot returns all spot pairs)
	url := s.apiURL + "?category=spot"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var response BybitResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.RetCode != 0 {
		return fmt.Errorf("API error: %s", response.RetMsg)
	}

	now := time.Now()
	updateCount := 0

	// Create reverse map: bybit symbol -> unified symbol
	symbolMap := make(map[string]string)
	for unifiedSymbol, bybitSymbol := range s.GetAllPairs() {
		symbolMap[bybitSymbol] = unifiedSymbol
	}

	for _, ticker := range response.Result.List {
		// Check if this is a symbol we want
		unifiedSymbol, ok := symbolMap[ticker.Symbol]
		if !ok {
			continue
		}

		priceFloat, err := strconv.ParseFloat(ticker.LastPrice, 64)
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", ticker.Symbol, "price", ticker.LastPrice, "error", err)
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
	sources.Register("cex.bybit", func(config map[string]interface{}) (sources.Source, error) {
		return NewBybitSource(config)
	})
}
