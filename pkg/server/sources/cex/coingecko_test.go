package cex

import (
	"context"
	"testing"
	"time"

	"tc.com/oracle-prices/pkg/server/sources"
)

func TestCoinGeckoSource_NewSource(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]interface{}
		wantErr   bool
		checkFunc func(*testing.T, sources.Source)
	}{
		{
			name: "valid config without API key (free API)",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USD": "terra-luna",
					"BTC/USD":  "bitcoin",
				},
				"update_interval": "60s",
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s sources.Source) {
				t.Helper()
				cg := s.(*CoinGeckoSource)
				if cg.apiKey != "" {
					t.Error("Expected no API key for free API")
				}
				if cg.minInterval != coingeckoFreeMinInterval {
					t.Errorf("Expected free API min interval %v, got %v", coingeckoFreeMinInterval, cg.minInterval)
				}
			},
		},
		{
			name: "valid config with API key (pro API)",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USD": "terra-luna",
					"BTC/USD":  "bitcoin",
				},
				"api_key":         "test_api_key_123",
				"update_interval": "30s",
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s sources.Source) {
				t.Helper()
				cg := s.(*CoinGeckoSource)
				if cg.apiKey != "test_api_key_123" {
					t.Errorf("Expected API key 'test_api_key_123', got %q", cg.apiKey)
				}
				// minInterval is set to coingeckoProMinInterval (2s) when API key is provided
				if cg.minInterval != 2*time.Second {
					t.Errorf("Expected min interval 2s (pro API), got %v", cg.minInterval)
				}
			},
		},
		{
			name: "update interval adjusted for free API rate limits",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USD": "terra-luna",
				},
				"update_interval": "5s", // Too short for free API
			},
			wantErr: false,
			checkFunc: func(t *testing.T, s sources.Source) {
				t.Helper()
				cg := s.(*CoinGeckoSource)
				// Should be adjusted to minimum
				if cg.updateInterval < coingeckoFreeMinInterval {
					t.Errorf("Expected update interval to be adjusted to at least %v, got %v",
						coingeckoFreeMinInterval, cg.updateInterval)
				}
			},
		},
		{
			name: "missing pairs config",
			config: map[string]interface{}{
				"api_key": "test_key",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := NewCoinGeckoSource(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewCoinGeckoSource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && tt.checkFunc != nil {
				tt.checkFunc(t, source)
			}
		})
	}
}

func TestCoinGeckoSource_Initialize(t *testing.T) {
	cfg := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USD": "terra-luna",
			"BTC/USD":  "bitcoin",
		},
	}

	source, err := NewCoinGeckoSource(cfg)
	if err != nil {
		t.Fatalf("NewCoinGeckoSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if source.Name() != "coingecko" {
		t.Errorf("Expected name 'coingecko', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeCEX {
		t.Errorf("Expected type SourceTypeCEX, got %v", source.Type())
	}
}

func TestCoinGeckoSource_RateLimiting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rate limiting test in short mode")
	}

	tests := []struct {
		name           string
		apiKey         string
		expectedMinGap time.Duration
	}{
		{
			name:           "free API rate limiting",
			apiKey:         "",
			expectedMinGap: coingeckoFreeMinInterval,
		},
		{
			name:           "pro API rate limiting",
			apiKey:         "test_pro_key",
			expectedMinGap: coingeckoProMinInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := map[string]interface{}{
				"pairs": map[string]interface{}{
					"BTC/USD": "bitcoin",
				},
				"update_interval": "1s", // Try to update very fast
			}
			if tt.apiKey != "" {
				cfg["api_key"] = tt.apiKey
			}

			source, err := NewCoinGeckoSource(cfg)
			if err != nil {
				t.Fatalf("NewCoinGeckoSource failed: %v", err)
			}

			cg := source.(*CoinGeckoSource)

			// Make first request
			ctx := context.Background()
			start1 := time.Now()
			err = cg.fetchPrices(ctx)
			duration1 := time.Since(start1)

			if err != nil {
				// If we get rate limited or network error, that's okay for this test
				t.Logf("First fetch failed (expected for test): %v", err)
			}

			// Make second request immediately - should be rate limited
			start2 := time.Now()
			err = cg.fetchPrices(ctx)
			duration2 := time.Since(start2)

			if err != nil {
				t.Logf("Second fetch failed (expected for test): %v", err)
			}

			// The second call should take at least minInterval time
			// (minus the time the first call took, since that counts toward the interval)
			expectedWait := tt.expectedMinGap - duration1
			if expectedWait > 0 && duration2 < expectedWait-time.Second {
				t.Errorf("Rate limiting not working: second call took %v, expected at least %v",
					duration2, expectedWait)
			}

			t.Logf("Rate limiting working: first=%v, second=%v, min_interval=%v",
				duration1, duration2, tt.expectedMinGap)
		})
	}
}

func TestCoinGeckoSource_FetchPrices_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := map[string]interface{}{
		"pairs": map[string]interface{}{
			"BTC/USD":  "bitcoin",
			"ETH/USD":  "ethereum",
			"LUNC/USD": "terra-luna",
		},
		// Use a long interval to avoid hitting rate limits during test
		"update_interval": "60s",
	}

	source, err := NewCoinGeckoSource(cfg)
	if err != nil {
		t.Fatalf("NewCoinGeckoSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Start source
	ctx := context.Background()
	go func() { _ = source.Start(ctx) }()
	defer func() { _ = source.Stop() }()

	// Wait for initial fetch
	time.Sleep(20 * time.Second) // Account for free API rate limiting

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("GetPrices failed: %v", err)
	}

	if len(prices) == 0 {
		t.Error("Expected prices, got empty map")
	}

	// Check if we got BTC/USD price
	btcPrice, ok := prices["BTC/USD"]
	if !ok {
		t.Error("Expected BTC/USD price")
	} else {
		if btcPrice.Price.IsZero() {
			t.Error("BTC/USD price is zero")
		}
		if btcPrice.Source != "coingecko" {
			t.Errorf("Expected source 'coingecko', got '%s'", btcPrice.Source)
		}
		t.Logf("BTC/USD price: %s", btcPrice.Price.String())
	}

	if !source.IsHealthy() {
		t.Error("Source should be healthy after successful fetch")
	}
}

func TestCoinGeckoSource_GetSourceSymbol(t *testing.T) {
	cfg := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USD": "terra-luna",
			"BTC/USD":  "bitcoin",
			"ETH/USD":  "ethereum",
		},
	}

	src, err := NewCoinGeckoSource(cfg)
	if err != nil {
		t.Fatalf("NewCoinGeckoSource failed: %v", err)
	}

	source := src.(*CoinGeckoSource)

	tests := []struct {
		unified  string
		expected string
	}{
		{"LUNC/USD", "terra-luna"},
		{"BTC/USD", "bitcoin"},
		{"ETH/USD", "ethereum"},
		{"UNKNOWN/USD", ""}, // Should return empty for unknown symbol
	}

	for _, tt := range tests {
		t.Run(tt.unified, func(t *testing.T) {
			result := source.GetSourceSymbol(tt.unified)
			if result != tt.expected {
				t.Errorf("GetSourceSymbol(%s) = %s, want %s", tt.unified, result, tt.expected)
			}
		})
	}
}

func TestCoinGeckoSource_GetUnifiedSymbol(t *testing.T) {
	cfg := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USD": "terra-luna",
			"BTC/USD":  "bitcoin",
			"ETH/USD":  "ethereum",
		},
	}

	src, err := NewCoinGeckoSource(cfg)
	if err != nil {
		t.Fatalf("NewCoinGeckoSource failed: %v", err)
	}

	source := src.(*CoinGeckoSource)

	tests := []struct {
		sourceSymbol string
		expected     string
	}{
		{"terra-luna", "LUNC/USD"},
		{"bitcoin", "BTC/USD"},
		{"ethereum", "ETH/USD"},
		{"unknown-coin", ""}, // Should return empty for unknown symbol
	}

	for _, tt := range tests {
		t.Run(tt.sourceSymbol, func(t *testing.T) {
			result := source.GetUnifiedSymbol(tt.sourceSymbol)
			if result != tt.expected {
				t.Errorf("GetUnifiedSymbol(%s) = %s, want %s", tt.sourceSymbol, result, tt.expected)
			}
		})
	}
}
