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
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	coinmarketcapAPIURL   = "https://pro-api.coinmarketcap.com/v2/cryptocurrency/quotes/latest"
	coinmarketcapPollRate = 15 * time.Second // 15 seconds (API rate limits)
)

// CoinMarketCapSource fetches prices from CoinMarketCap REST API
type CoinMarketCapSource struct {
	*sources.BaseSource

	apiKey string
	apiURL string
}

// CoinMarketCapQuote represents a quote in USD
type CoinMarketCapQuote struct {
	Price            float64 `json:"price"`
	Volume24h        float64 `json:"volume_24h"`
	PercentChange24h float64 `json:"percent_change_24h"`
	MarketCap        float64 `json:"market_cap"`
}

// CoinMarketCapData represents cryptocurrency data
type CoinMarketCapData struct {
	Slug  string                        `json:"slug"`
	Quote map[string]CoinMarketCapQuote `json:"quote"`
}

// CoinMarketCapResponse represents the API response
type CoinMarketCapResponse struct {
	Status struct {
		Timestamp    string `json:"timestamp"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	} `json:"status"`
	Data map[string]CoinMarketCapData `json:"data"`
}

// NewCoinMarketCapSource creates a new CoinMarketCap REST source
func NewCoinMarketCapSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "LUNC/USD" => "terra-luna-classic")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	// API key is required for CoinMarketCap
	apiKey, ok := config["api_key"].(string)
	if !ok || apiKey == "" {
		return nil, fmt.Errorf("api_key is required for CoinMarketCap")
	}

	apiURL := coinmarketcapAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("coinmarketcap", sources.SourceTypeCEX, pairs, logger)

	return &CoinMarketCapSource{
		BaseSource: base,
		apiKey:     apiKey,
		apiURL:     apiURL,
	}, nil
}

// Initialize prepares the source for operation
func (s *CoinMarketCapSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing CoinMarketCap source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices
func (s *CoinMarketCapSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting CoinMarketCap source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err)
	}

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source
func (s *CoinMarketCapSource) Stop() error {
	s.Logger().Info("CoinMarketCap source stopped")
	return nil
}

// GetPrices returns the current prices
func (s *CoinMarketCapSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe adds a subscriber
func (s *CoinMarketCapSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices
func (s *CoinMarketCapSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(coinmarketcapPollRate)
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

// fetchPrices fetches current prices from CoinMarketCap API
func (s *CoinMarketCapSource) fetchPrices(ctx context.Context) error {
	// Convert pairs to slugs using pair mappings
	slugs := make([]string, 0, len(s.GetAllPairs()))
	slugToSymbol := make(map[string]string) // slug -> unified symbol

	for unifiedSymbol, cmcSlug := range s.GetAllPairs() {
		slugs = append(slugs, cmcSlug)
		slugToSymbol[cmcSlug] = unifiedSymbol
		s.Logger().Debug("CoinMarketCap pair mapping", "unified", unifiedSymbol, "slug", cmcSlug)
	}

	if len(slugs) == 0 {
		return fmt.Errorf("no valid symbols to fetch")
	}

	// Build request URL with slug parameter
	params := url.Values{}
	params.Add("slug", strings.Join(slugs, ","))
	params.Add("convert", "USD")

	requestURL := fmt.Sprintf("%s?%s", s.apiURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add API key header
	req.Header.Set("X-CMC_PRO_API_KEY", s.apiKey)
	req.Header.Set("Accept", "application/json")

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

	var response CoinMarketCapResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.Status.ErrorCode != 0 {
		return fmt.Errorf("API error: %s", response.Status.ErrorMessage)
	}

	now := time.Now()

	for _, data := range response.Data {
		// Find the unified symbol for this slug using reverse mapping
		unifiedSymbol, ok := slugToSymbol[data.Slug]
		if !ok {
			continue
		}

		// Get USD quote
		usdQuote, ok := data.Quote["USD"]
		if !ok {
			s.Logger().Warn("No USD quote for symbol", "slug", data.Slug)
			continue
		}

		price := decimal.NewFromFloat(usdQuote.Price)

		// Update price using BaseSource
		s.SetPrice(unifiedSymbol, price, now)
		metrics.RecordSourceUpdate(s.Name(), unifiedSymbol)
	}

	if len(s.GetAllPrices()) > 0 {
		s.SetHealthy(true)
		s.Logger().Debug("Updated prices", "count", len(s.GetAllPrices()))
	}

	return nil
}

func init() {
	sources.Register("cex.coinmarketcap", func(config map[string]interface{}) (sources.Source, error) {
		return NewCoinMarketCapSource(config)
	})
}
