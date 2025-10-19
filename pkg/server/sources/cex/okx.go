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
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	okxAPIURL   = "https://www.okx.com/api/v5/market/tickers"
	okxPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// OKXSource fetches prices from OKX (OKEX) REST API.
type OKXSource struct {
	*sources.BaseSource

	apiURL string
}

// OKXTicker represents a ticker in the API response.
type OKXTicker struct {
	InstID    string `json:"instId"`    // Instrument ID (e.g., "BTC-USDT")
	Last      string `json:"last"`      // Last traded price
	LastSz    string `json:"lastSz"`    // Last traded size
	AskPx     string `json:"askPx"`     // Best ask price
	BidPx     string `json:"bidPx"`     // Best bid price
	Vol24h    string `json:"vol24h"`    // 24h trading volume
	VolCcy24h string `json:"volCcy24h"` // 24h trading volume in quote currency
	Ts        string `json:"ts"`        // Ticker data generation time
}

// OKXResponse represents the API response.
type OKXResponse struct {
	Code string      `json:"code"` // Error code, "0" means success
	Msg  string      `json:"msg"`  // Error message
	Data []OKXTicker `json:"data"` // Ticker data
}

// NewOKXSource creates a new OKX REST source.
func NewOKXSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "LUNC/USDT" => "LUNC-USDT")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := okxAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("okx", sources.SourceTypeCEX, pairs, logger)

	return &OKXSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation.
func (s *OKXSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing OKX source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices.
func (s *OKXSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting OKX source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err)
	} else {
		s.SetHealthy(true)
	}

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source.
func (s *OKXSource) Stop() error {
	s.Logger().Info("OKX source stopped")
	return nil
}

// GetPrices returns the current prices.
func (s *OKXSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPricesAvailable)
	}
	return prices, nil
}

// Subscribe adds a subscriber.
func (s *OKXSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices.
func (s *OKXSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(okxPollRate)
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

// fetchPrices fetches current prices from OKX API.
func (s *OKXSource) fetchPrices(ctx context.Context) error {
	// Query with inst type (SPOT)
	url := s.apiURL + "?instType=SPOT"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		s.SetHealthy(false)
		return fmt.Errorf("%w (HTTP 429)", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var response OKXResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.Code != "0" {
		return fmt.Errorf("%w: %s - %s", sources.ErrAPIError, response.Code, response.Msg)
	}

	now := time.Now()
	updateCount := 0

	// Create reverse map: okx symbol -> unified symbol
	symbolMap := make(map[string]string)
	for unifiedSymbol, okxSymbol := range s.GetAllPairs() {
		symbolMap[okxSymbol] = unifiedSymbol
	}

	for _, ticker := range response.Data {
		// Check if this is a symbol we want
		unifiedSymbol, ok := symbolMap[ticker.InstID]
		if !ok {
			continue
		}

		priceFloat, err := strconv.ParseFloat(ticker.Last, 64)
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", ticker.InstID, "price", ticker.Last, "error", err)
			continue
		}

		// Use BaseSource SetPrice
		s.SetPrice(unifiedSymbol, decimal.NewFromFloat(priceFloat), now)
		metrics.RecordSourceUpdate(s.Name(), unifiedSymbol)
		updateCount++
	}

	if updateCount > 0 {
		s.SetHealthy(true)
		s.Logger().Debug("Updated prices", "count", updateCount)
	}

	return nil
}
