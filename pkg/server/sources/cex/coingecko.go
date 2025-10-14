package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	coingeckoBaseURL = "https://api.coingecko.com/api/v3"
	coingeckoTimeout = 10 * time.Second
)

// CoinGeckoSource fetches prices from CoinGecko REST API
type CoinGeckoSource struct {
	*sources.BaseSource

	apiKey         string
	updateInterval time.Duration
	client         *http.Client
}

// NewCoinGeckoSource creates a new CoinGecko source
func NewCoinGeckoSource(config map[string]interface{}) (sources.Source, error) {
	logger, _ := logging.Init("info", "text", "stdout")

	// Parse pairs from config (map of "LUNC/USD" => "terra-luna")
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	updateInterval := 60 * time.Second
	if interval, ok := config["update_interval"].(string); ok {
		if d, err := time.ParseDuration(interval); err == nil {
			updateInterval = d
		}
	}

	apiKey := ""
	if key, ok := config["api_key"].(string); ok {
		apiKey = key
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("coingecko", sources.SourceTypeCEX, pairs, logger)

	return &CoinGeckoSource{
		BaseSource:     base,
		apiKey:         apiKey,
		updateInterval: updateInterval,
		client: &http.Client{
			Timeout: coingeckoTimeout,
		},
	}, nil
}

// Initialize prepares the source for operation
func (s *CoinGeckoSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing CoinGecko source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices
func (s *CoinGeckoSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting CoinGecko source")

	// Fetch initial prices
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Failed to fetch initial prices", "error", err)
	}

	// Start update loop
	go s.updateLoop(ctx)

	return nil
}

// Stop halts the source and cleans up resources
func (s *CoinGeckoSource) Stop() error {
	s.Logger().Info("Stopping CoinGecko source")
	// StopChan is closed by BaseSource, we just log
	return nil
}

// GetPrices returns the current prices for all symbols
func (s *CoinGeckoSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe allows other components to receive price updates
func (s *CoinGeckoSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// updateLoop periodically fetches prices
func (s *CoinGeckoSource) updateLoop(ctx context.Context) {
	ticker := time.NewTicker(s.updateInterval)
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

// fetchPrices fetches prices from CoinGecko API
func (s *CoinGeckoSource) fetchPrices(ctx context.Context) error {
	// Build list of unique CoinGecko IDs from pair mappings
	idSet := make(map[string]bool)
	idToSymbol := make(map[string]string) // Map CoinGecko ID back to unified symbol

	for unifiedSymbol, coinGeckoID := range s.GetAllPairs() {
		idSet[coinGeckoID] = true
		idToSymbol[coinGeckoID] = unifiedSymbol
		s.Logger().Debug("CoinGecko pair mapping", "unified", unifiedSymbol, "coingecko_id", coinGeckoID)
	}

	if len(idSet) == 0 {
		return fmt.Errorf("no valid symbols to fetch")
	}

	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	// Build API URL
	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=usd",
		coingeckoBaseURL,
		strings.Join(ids, ","))

	if s.apiKey != "" {
		url += "&x_cg_pro_api_key=" + s.apiKey
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse response
	var data map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Update prices using BaseSource methods
	now := time.Now()
	updateCount := 0

	for coinGeckoID, priceData := range data {
		usdPrice, ok := priceData["usd"]
		if !ok {
			continue
		}

		// Get the unified symbol for this CoinGecko ID
		unifiedSymbol, ok := idToSymbol[coinGeckoID]
		if !ok {
			continue
		}

		// Use BaseSource SetPrice
		s.SetPrice(unifiedSymbol, decimal.NewFromFloat(usdPrice), now)
		metrics.RecordSourceUpdate(s.Name(), unifiedSymbol)
		updateCount++
	}

	if updateCount == 0 {
		return fmt.Errorf("no prices extracted from response")
	}

	s.SetHealthy(true)
	metrics.RecordSourceHealth(s.Name(), string(s.Type()), true)

	s.Logger().Debug("Fetched prices from CoinGecko", "count", updateCount)

	return nil
}

// Register the source in init
func init() {
	sources.Register("cex.coingecko", func(config map[string]interface{}) (sources.Source, error) {
		return NewCoinGeckoSource(config)
	})
}
