package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/StrathCole/oracle-go/pkg/metrics"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/shopspring/decimal"
)

const (
	bitfinexAPIURL   = "https://api-pub.bitfinex.com/v2/tickers"
	bitfinexPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// BitfinexSource fetches prices from Bitfinex REST API.
type BitfinexSource struct {
	*sources.BaseSource

	apiURL string
}

// NewBitfinexSource creates a new Bitfinex REST source.
func NewBitfinexSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "LUNC/USD" => "tLUNCUSD")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := bitfinexAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("bitfinex", sources.SourceTypeCEX, pairs, logger)

	return &BitfinexSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation.
func (s *BitfinexSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing Bitfinex source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices.
func (s *BitfinexSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Bitfinex source")

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
func (s *BitfinexSource) Stop() error {
	s.Logger().Info("Bitfinex source stopped")
	return nil
}

// GetPrices returns the current prices.
func (s *BitfinexSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPricesAvailable)
	}
	return prices, nil
}

// Subscribe is not implemented for REST sources.
func (s *BitfinexSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices.
func (s *BitfinexSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(bitfinexPollRate)
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

// fetchPrices fetches current prices from Bitfinex API.
func (s *BitfinexSource) fetchPrices(ctx context.Context) error {
	// Build symbols parameter from pair mappings
	symbolsParam := make([]string, 0, len(s.GetAllPairs()))
	symbolMap := make(map[string]string) // bitfinex symbol -> unified symbol

	for unifiedSymbol, bitfinexSymbol := range s.GetAllPairs() {
		symbolsParam = append(symbolsParam, bitfinexSymbol)
		symbolMap[bitfinexSymbol] = unifiedSymbol
		s.Logger().Debug("Bitfinex pair mapping", "unified", unifiedSymbol, "bitfinex", bitfinexSymbol)
	}

	symbolsQuery := strings.Join(symbolsParam, ",")

	// Make request
	url := fmt.Sprintf("%s?symbols=%s", s.apiURL, symbolsQuery)
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

	// Parse response: array of ticker arrays
	// Each ticker is: [SYMBOL, BID, BID_SIZE, ASK, ASK_SIZE, DAILY_CHANGE, DAILY_CHANGE_RELATIVE, LAST_PRICE, VOLUME, HIGH, LOW]
	var tickers [][]interface{}
	if err := json.Unmarshal(body, &tickers); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	now := time.Now()
	updateCount := 0

	for _, ticker := range tickers {
		if len(ticker) < 8 {
			s.Logger().Warn("Invalid ticker format", "ticker", ticker)
			continue
		}

		// Index 0: symbol (string)
		// Index 7: last price (float64)
		bitfinexSymbol, ok := ticker[0].(string)
		if !ok {
			continue
		}

		priceFloat, ok := ticker[7].(float64)
		if !ok {
			continue
		}

		// Get unified symbol from Bitfinex symbol
		unifiedSymbol, ok := symbolMap[bitfinexSymbol]
		if !ok {
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
