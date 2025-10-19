package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/shopspring/decimal"
)

const (
	huobiBaseURL  = "https://api.huobi.pro"
	huobiTimeout  = 10 * time.Second
	huobiPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// HuobiSource fetches prices from Huobi REST API.
type HuobiSource struct {
	*sources.BaseSource

	apiURL string
}

// HuobiTicker represents a single ticker in the response.
type HuobiTicker struct {
	Symbol string      `json:"symbol"` // e.g., "btcusdt"
	Open   json.Number `json:"open"`
	High   json.Number `json:"high"`
	Low    json.Number `json:"low"`
	Close  json.Number `json:"close"`  // Last price
	Amount json.Number `json:"amount"` // Base currency volume
	Vol    json.Number `json:"vol"`    // Quote currency volume
	Count  int         `json:"count"`  // Number of trades
}

// HuobiResponse represents the API response.
type HuobiResponse struct {
	Status string        `json:"status"` // "ok" or "error"
	Ts     int64         `json:"ts"`     // Timestamp in milliseconds
	Data   []HuobiTicker `json:"data"`
}

// NewHuobiSource creates a new Huobi REST source.
func NewHuobiSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "LUNC/USDT" => "luncusdt")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := huobiBaseURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("huobi", sources.SourceTypeCEX, pairs, logger)

	return &HuobiSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation.
func (s *HuobiSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing Huobi source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices.
func (s *HuobiSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Huobi source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err.Error())
	} else {
		s.SetHealthy(true)
	}

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source.
func (s *HuobiSource) Stop() error {
	s.Logger().Info("Huobi source stopped")
	s.Close()
	return nil
}

// GetPrices returns the current prices.
func (s *HuobiSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPricesAvailable)
	}
	return prices, nil
}

// Subscribe adds a subscriber.
func (s *HuobiSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices.
func (s *HuobiSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(huobiPollRate)
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
				s.Logger().Error("Failed to fetch prices after retries", "error", err.Error())
				s.SetHealthy(false)
			} else {
				s.SetHealthy(true)
			}
		}
	}
}

// fetchPrices fetches current prices from Huobi API.
func (s *HuobiSource) fetchPrices(ctx context.Context) error {
	// Build the full endpoint URL
	url := s.apiURL + "/market/tickers"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		return fmt.Errorf("%w", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var response HuobiResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.Status != "ok" {
		return fmt.Errorf("%w: %s", sources.ErrAPIError, response.Status)
	}

	if len(response.Data) == 0 {
		return fmt.Errorf("%w", sources.ErrNoTickersInResponse)
	}

	now := time.Now()
	updateCount := 0

	// Process tickers
	for _, ticker := range response.Data {
		// Find matching unified symbol
		unifiedSymbol := s.GetUnifiedSymbol(ticker.Symbol)
		if unifiedSymbol == "" {
			// Not a symbol we're tracking
			continue
		}

		// Parse price from json.Number to preserve full precision for small values like LUNC
		price, err := decimal.NewFromString(ticker.Close.String())
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", ticker.Symbol, "price", ticker.Close.String(), "error", err)
			continue
		}

		// Check for zero or negative price
		if price.LessThanOrEqual(decimal.Zero) {
			continue
		}

		// Parse volume
		volume := decimal.Zero
		if volumeVal, err := decimal.NewFromString(ticker.Amount.String()); err == nil {
			volume = volumeVal
		}

		s.SetPrice(unifiedSymbol, price, now)
		updateCount++

		s.Logger().Debug("Updated Huobi price",
			"symbol", unifiedSymbol,
			"price", price.String(),
			"volume", volume.String())
	}

	if updateCount == 0 {
		return fmt.Errorf("%w", sources.ErrNoMatchingSymbols)
	}

	s.SetLastUpdate(now)
	s.Logger().Debug("Fetched Huobi prices", "count", updateCount)

	return nil
}
