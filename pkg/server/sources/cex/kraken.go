package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	krakenAPIURL   = "https://api.kraken.com/0/public/Ticker"
	krakenPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// KrakenSource fetches prices from Kraken REST API
type KrakenSource struct {
	*sources.BaseSource

	apiURL string
}

// KrakenTickerData represents ticker data for a single pair
type KrakenTickerData struct {
	A []string `json:"a"` // Ask [price, whole lot volume, lot volume]
	B []string `json:"b"` // Bid [price, whole lot volume, lot volume]
	C []string `json:"c"` // Last trade [price, lot volume]
	V []string `json:"v"` // Volume [today, last 24 hours]
	P []string `json:"p"` // Volume weighted average price [today, last 24 hours]
	T []int    `json:"t"` // Number of trades [today, last 24 hours]
	L []string `json:"l"` // Low [today, last 24 hours]
	H []string `json:"h"` // High [today, last 24 hours]
	O string   `json:"o"` // Today's opening price
}

// KrakenResponse represents the API response
type KrakenResponse struct {
	Error  []string                    `json:"error"`
	Result map[string]KrakenTickerData `json:"result"`
}

// NewKrakenSource creates a new Kraken REST source
func NewKrakenSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "BTC/USD" => "XBTUSDT")
	// Kraken uses their own pair naming (e.g., XBTUSDT for BTC/USDT)
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	apiURL := krakenAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("kraken", sources.SourceTypeCEX, pairs, logger)

	return &KrakenSource{
		BaseSource: base,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation
func (s *KrakenSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Kraken source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices
func (s *KrakenSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Kraken source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err)
	}

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source
func (s *KrakenSource) Stop() error {
	s.Logger().Info("Kraken source stopped")
	s.Close()
	return nil
}

// GetPrices returns the current prices
func (s *KrakenSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe adds a subscriber
func (s *KrakenSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices
func (s *KrakenSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(krakenPollRate)
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

// matchKrakenSymbol attempts to match a Kraken response key to a configured unified symbol
// Kraken returns various formats depending on the pair:
//   - XXBTZUSD (prefixed format for BTC/USD)
//   - ADAUSD (simple format for some pairs)
//   - XETHZEUR (mixed format)
// We try multiple matching strategies to handle all cases
func matchKrakenSymbol(responseKey string, pairMappings map[string]string) string {
	// Strategy 1: Exact match (response key is in our config)
	if unified, ok := pairMappings[responseKey]; ok {
		return unified
	}
	
	// Strategy 2: Try all configured Kraken symbols and see if they're contained in the response key
	// This handles cases like: we send "BTCUSD", Kraken returns "XXBTZUSD"
	for unified, krakenSymbol := range pairMappings {
		// Remove common formatting from both for comparison
		cleanKraken := strings.ReplaceAll(strings.ReplaceAll(krakenSymbol, "/", ""), "-", "")
		cleanResponse := strings.ReplaceAll(strings.ReplaceAll(responseKey, "/", ""), "-", "")
		
		// Check if they match after removing formatting
		if cleanKraken == cleanResponse {
			return unified
		}
		
		// Handle Kraken's prefix additions: BTCUSD -> XXBTZUSD
		// Try with common BTC aliases
		if strings.Contains(cleanResponse, "XBT") {
			testSymbol := strings.ReplaceAll(cleanKraken, "BTC", "XBT")
			if strings.Contains(cleanResponse, testSymbol) || strings.Contains(cleanResponse, "X"+testSymbol) || strings.Contains(cleanResponse, "XX"+testSymbol[:3]) {
				return unified
			}
		}
	}
	
	// No match found
	return ""
}

// fetchPrices fetches current prices from Kraken API
func (s *KrakenSource) fetchPrices(ctx context.Context) error {
	// Build request with pair list
	pairs := s.GetAllPairs()
	krakenSymbols := make([]string, 0, len(pairs))
	for _, krakenSymbol := range pairs {
		krakenSymbols = append(krakenSymbols, krakenSymbol)
	}

	if len(krakenSymbols) == 0 {
		return fmt.Errorf("no symbols configured")
	}

	// Kraken wants comma-separated pairs
	pairParam := strings.Join(krakenSymbols, ",")
	reqURL := fmt.Sprintf("%s?pair=%s", s.apiURL, url.QueryEscape(pairParam))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
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
		return fmt.Errorf("rate limit exceeded (HTTP 429)")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var response KrakenResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response.Error) > 0 {
		return fmt.Errorf("API errors: %v", response.Error)
	}

	if len(response.Result) == 0 {
		return fmt.Errorf("no tickers in response")
	}

	now := time.Now()
	updateCount := 0

	// Debug: Log what Kraken actually returned
	responseKeys := make([]string, 0, len(response.Result))
	for k := range response.Result {
		responseKeys = append(responseKeys, k)
	}
	s.Logger().Debug("Kraken response keys", "keys", responseKeys)

	// Get our configured pair mappings for matching
	pairMappings := s.GetAllPairs()

	// Process tickers
	// Kraken returns pairs with their own format as keys
	for krakenSymbol, ticker := range response.Result {
		// Try to match the Kraken response key to our configured symbols
		unifiedSymbol := matchKrakenSymbol(krakenSymbol, pairMappings)
		
		if unifiedSymbol == "" {
			// Not a symbol we're tracking
			s.Logger().Debug("Unmatched Kraken symbol", "symbol", krakenSymbol)
			continue
		}

		// Use ask price (current sell price)
		if len(ticker.A) == 0 || ticker.A[0] == "" {
			continue
		}

		priceFloat, err := decimal.NewFromString(ticker.A[0])
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", unifiedSymbol, "price", ticker.A[0])
			continue
		}

		// Volume (24h)
		volume := decimal.Zero
		if len(ticker.V) >= 2 && ticker.V[1] != "" {
			if v, err := decimal.NewFromString(ticker.V[1]); err == nil {
				volume = v
			}
		}

		s.SetPrice(unifiedSymbol, priceFloat, now)
		updateCount++

		s.Logger().Debug("Updated Kraken price",
			"symbol", unifiedSymbol,
			"price", priceFloat.String(),
			"volume", volume.String())
	}

	if updateCount == 0 {
		return fmt.Errorf("no matching symbols found in response")
	}

	s.SetLastUpdate(now)
	s.Logger().Debug("Fetched Kraken prices", "count", updateCount)

	return nil
}
