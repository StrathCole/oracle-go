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
	kucoinAPIURL   = "https://api.kucoin.com/api/v1/market/allTickers"
	kucoinPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// KucoinSource fetches prices from Kucoin REST API.
type KucoinSource struct {
	*sources.BaseSource

	apiURL string
}

// KucoinTicker represents a single ticker in the response.
type KucoinTicker struct {
	Symbol      string `json:"symbol"`      // e.g., "BTC-USDT"
	Buy         string `json:"buy"`         // Best bid price
	Sell        string `json:"sell"`        // Best ask price
	Last        string `json:"last"`        // Last traded price
	Vol         string `json:"vol"`         // 24h volume (base currency)
	VolValue    string `json:"volValue"`    // 24h volume (quote currency)
	High        string `json:"high"`        // 24h high
	Low         string `json:"low"`         // 24h low
	ChangePrice string `json:"changePrice"` // 24h change
	ChangeRate  string `json:"changeRate"`  // 24h change rate
}

// KucoinResponse represents the API response.
type KucoinResponse struct {
	Code string `json:"code"` // "200000" for success
	Data struct {
		Time   int64          `json:"time"`
		Ticker []KucoinTicker `json:"ticker"`
	} `json:"data"`
}

// NewKucoinSource creates a new Kucoin REST source.
func NewKucoinSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "LUNC/USDT" => "LUNC-USDT")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := kucoinAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("kucoin", sources.SourceTypeCEX, pairs, logger)

	return &KucoinSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation.
func (s *KucoinSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing Kucoin source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices.
func (s *KucoinSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Kucoin source")

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
func (s *KucoinSource) Stop() error {
	s.Logger().Info("Kucoin source stopped")
	s.Close()
	return nil
}

// GetPrices returns the current prices.
func (s *KucoinSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPricesAvailable)
	}
	return prices, nil
}

// Subscribe adds a subscriber.
func (s *KucoinSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices.
func (s *KucoinSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(kucoinPollRate)
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

// fetchPrices fetches current prices from Kucoin API.
func (s *KucoinSource) fetchPrices(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.apiURL, nil)
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
		return fmt.Errorf("%w (HTTP 429)", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var response KucoinResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.Code != "200000" {
		return fmt.Errorf("%w: %s", sources.ErrAPIError, response.Code)
	}

	if len(response.Data.Ticker) == 0 {
		return fmt.Errorf("%w", sources.ErrNoTickersInResponse)
	}

	now := time.Now()
	updateCount := 0

	// Process tickers
	for _, ticker := range response.Data.Ticker {
		// Find matching unified symbol
		unifiedSymbol := ""
		for unified, kucoinSymbol := range s.GetAllPairs() {
			if kucoinSymbol == ticker.Symbol {
				unifiedSymbol = unified
				break
			}
		}

		if unifiedSymbol == "" {
			// Not a symbol we're tracking
			continue
		}

		// Use last traded price
		if ticker.Last == "" {
			continue
		}

		price, err := decimal.NewFromString(ticker.Last)
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", unifiedSymbol, "price", ticker.Last)
			continue
		}

		// Volume (base currency)
		volume := decimal.Zero
		if ticker.Vol != "" {
			if v, err := decimal.NewFromString(ticker.Vol); err == nil {
				volume = v
			}
		}

		s.SetPrice(unifiedSymbol, price, now)
		updateCount++

		s.Logger().Debug("Updated Kucoin price",
			"symbol", unifiedSymbol,
			"price", price.String(),
			"volume", volume.String())
	}

	if updateCount == 0 {
		return fmt.Errorf("%w", sources.ErrNoMatchingSymbols)
	}

	s.SetLastUpdate(now)
	s.Logger().Debug("Fetched Kucoin prices", "count", updateCount)

	return nil
}
