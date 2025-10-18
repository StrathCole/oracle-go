package sources_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/config"
	"tc.com/oracle-prices/pkg/server/sources"
	_ "tc.com/oracle-prices/pkg/server/sources/cex"    // Register CEX sources
	_ "tc.com/oracle-prices/pkg/server/sources/evm"    // Register EVM sources
	_ "tc.com/oracle-prices/pkg/server/sources/fiat"   // Register fiat sources
	_ "tc.com/oracle-prices/pkg/server/sources/oracle" // Register oracle sources
)

// TestRealCEXSources tests all enabled CEX sources from config
func TestRealCEXSources(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg, err := config.Load("../../../config/config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test each CEX source
	for _, sourceCfg := range cfg.Sources {
		if sourceCfg.Type != "cex" || !sourceCfg.Enabled {
			continue
		}

		t.Run(sourceCfg.Name, func(t *testing.T) {
			// Create source
			source, err := sources.Create(sourceCfg.Type, sourceCfg.Name, sourceCfg.Config)
			if err != nil {
				t.Fatalf("Failed to create source: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Initialize
			if err := source.Initialize(ctx); err != nil {
				t.Fatalf("Failed to initialize: %v", err)
			}

			// Start
			if err := source.Start(ctx); err != nil {
				t.Fatalf("Failed to start: %v", err)
			}
			defer source.Stop()

			// Wait for prices
			time.Sleep(5 * time.Second)

			// Get prices
			prices, err := source.GetPrices(ctx)
			if err != nil {
				t.Fatalf("Failed to get prices: %v", err)
			}

			if len(prices) == 0 {
				t.Error("No prices returned")
			}

			// Validate prices
			for symbol, price := range prices {
				t.Logf("%s %s: %s", sourceCfg.Name, symbol, price.Price.String())

				// Price must be positive
				if price.Price.LessThanOrEqual(decimal.Zero) {
					t.Errorf("Invalid price for %s: %s (must be > 0)", symbol, price.Price.String())
				}

				// Timestamp must be recent
				if time.Since(price.Timestamp) > 2*time.Minute {
					t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
				}

				// Source must match
				if price.Source != sourceCfg.Name {
					t.Errorf("Wrong source for %s: got %s, want %s", symbol, price.Source, sourceCfg.Name)
				}

				// Validate reasonable ranges for known pairs
				if symbol == "BTC/USD" || symbol == "BTC/USDT" {
					min := decimal.NewFromInt(10000)  // BTC > $10k
					max := decimal.NewFromInt(200000) // BTC < $200k
					if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
						t.Errorf("%s price out of expected range: %s", symbol, price.Price.String())
					}
				}

				if symbol == "LUNC/USD" || symbol == "LUNC/USDT" {
					min := decimal.NewFromFloat(0.000001)
					max := decimal.NewFromFloat(0.01)
					if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
						t.Logf("WARNING: %s price possibly out of range: %s", symbol, price.Price.String())
					}
				}
			}

			// Check health
			if !source.IsHealthy() {
				t.Error("Source should be healthy after successful fetch")
			}
		})
	}
}

// TestRealFiatSources tests all enabled fiat sources from config
func TestRealFiatSources(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg, err := config.Load("../../../config/config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	for _, sourceCfg := range cfg.Sources {
		if sourceCfg.Type != "fiat" || !sourceCfg.Enabled {
			continue
		}

		t.Run(sourceCfg.Name, func(t *testing.T) {
			source, err := sources.Create(sourceCfg.Type, sourceCfg.Name, sourceCfg.Config)
			if err != nil {
				t.Fatalf("Failed to create source: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := source.Initialize(ctx); err != nil {
				t.Fatalf("Failed to initialize: %v", err)
			}

			if err := source.Start(ctx); err != nil {
				t.Fatalf("Failed to start: %v", err)
			}
			defer source.Stop()

			time.Sleep(5 * time.Second)

			prices, err := source.GetPrices(ctx)
			if err != nil {
				t.Fatalf("Failed to get prices: %v", err)
			}

			if len(prices) == 0 {
				t.Error("No prices returned")
			}

			for symbol, price := range prices {
				t.Logf("%s %s: %s", sourceCfg.Name, symbol, price.Price.String())

				if price.Price.LessThanOrEqual(decimal.Zero) {
					t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
				}

				// Fiat rates should be reasonable
				if symbol == "EUR/USD" {
					min := decimal.NewFromFloat(0.8)
					max := decimal.NewFromFloat(1.5)
					if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
						t.Errorf("EUR/USD out of range: %s", price.Price.String())
					}
				}
			}

			if !source.IsHealthy() {
				t.Error("Source should be healthy after successful fetch")
			}
		})
	}
}

// TestRealBandProtocol tests Band Protocol oracle source
func TestRealBandProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg, err := config.Load("../../../config/config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	var bandConfig map[string]interface{}
	for _, sourceCfg := range cfg.Sources {
		if sourceCfg.Type == "oracle" && sourceCfg.Name == "band" && sourceCfg.Enabled {
			bandConfig = sourceCfg.Config
			break
		}
	}

	if bandConfig == nil {
		t.Skip("Band Protocol source not enabled in config")
	}

	source, err := sources.Create("oracle", "band", bandConfig)
	if err != nil {
		t.Fatalf("Failed to create Band Protocol source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from Band Protocol")
	}

	for symbol, price := range prices {
		t.Logf("Band Protocol %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if time.Since(price.Timestamp) > 5*time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		if price.Source != "band" {
			t.Errorf("Wrong source: got %s, want band", price.Source)
		}

		// Validate known pairs
		if symbol == "BTC/USD" {
			min := decimal.NewFromInt(10000)
			max := decimal.NewFromInt(200000)
			if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
				t.Errorf("BTC/USD out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Error("Band Protocol should be healthy after successful fetch")
	}
}

// TestRealEVMSource tests EVM source (PancakeSwap BSC)
func TestRealEVMSource(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg, err := config.Load("../../../config/config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	var evmConfig map[string]interface{}
	for _, sourceCfg := range cfg.Sources {
		if sourceCfg.Type == "evm" && sourceCfg.Name == "pancakeswap_bsc" && sourceCfg.Enabled {
			evmConfig = sourceCfg.Config
			break
		}
	}

	if evmConfig == nil {
		t.Skip("EVM source not enabled in config")
	}

	source, err := sources.Create("evm", "pancakeswap_bsc", evmConfig)
	if err != nil {
		t.Fatalf("Failed to create EVM source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from PancakeSwap BSC")
	}

	for symbol, price := range prices {
		t.Logf("PancakeSwap BSC %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if time.Since(price.Timestamp) > time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		if price.Source != "pancakeswap_bsc" {
			t.Errorf("Wrong source: got %s, want pancakeswap_bsc", price.Source)
		}

		// LUNC/USDT should be in reasonable range
		if symbol == "LUNC/USDT" {
			min := decimal.NewFromFloat(0.000001)
			max := decimal.NewFromFloat(0.01)
			if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
				t.Logf("WARNING: LUNC/USDT price possibly out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Error("EVM source should be healthy after successful fetch")
	}
}

// TestRealCosmWasmSources tests CosmWasm DEX sources on Terra Classic
func TestRealCosmWasmSources(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg, err := config.Load("../../../config/config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// CosmWasm sources need to be created manually with gRPC client
	// For now, test individual contracts directly
	testCases := []struct {
		name     string
		contract string
		symbol   string
	}{
		{
			name:     "terraport_lunc_usdc",
			contract: "terra1a29fltd5h5y8se0xanw48wkmqg7nfpmv5jsl472uun0274h8xatqd3yzfh",
			symbol:   "LUNC/USDC",
		},
		{
			name:     "terraswap_lunc_usdc",
			contract: "terra19h62lw77rluxf6yg4szcclcgk9tsalx72cv7dlzvzs8gy20g70js7c9jkc",
			symbol:   "LUNC/USDC",
		},
		{
			name:     "garuda_lunc_usdc",
			contract: "terra1vnt3tjg0v98hgp0vx8nynvklnjqzkzsqvtpzv9v56r800gdhmxwstv5y64",
			symbol:   "LUNC/USDC",
		},
	}

	// Note: This test would require gRPC client setup
	// For now, skip if gRPC endpoints not configured
	if len(cfg.Feeder.GRPCEndpoints) == 0 {
		t.Skip("No gRPC endpoints configured for CosmWasm testing")
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing %s contract: %s", tc.name, tc.contract)
			// TODO: Implement when gRPC client is available in test context
			// For now, this serves as documentation of what needs testing
			t.Skip("CosmWasm integration test requires gRPC client setup")
		})
	}
}

// TestRealBinance tests Binance WebSocket (no API key required)
func TestRealBinance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"BTC/USDT":  "BTCUSDT",
			"ETH/USDT":  "ETHUSDT",
			"LUNC/USDT": "LUNCUSDT",
		},
	}

	source, err := sources.Create("cex", "binance", config)
	if err != nil {
		t.Fatalf("Failed to create Binance source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	// Wait for WebSocket connection and initial data
	time.Sleep(8 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from Binance")
	}

	for symbol, price := range prices {
		t.Logf("Binance %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if symbol == "BTC/USDT" {
			min := decimal.NewFromInt(10000)
			max := decimal.NewFromInt(200000)
			if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
				t.Errorf("BTC/USDT out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Error("Binance should be healthy after successful fetch")
	}
}

// TestRealBitfinex tests Bitfinex REST API (no API key required)
func TestRealBitfinex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"BTC/USD": "tBTCUSD",
			"ETH/USD": "tETHUSD",
			"XRP/USD": "tXRPUSD",
		},
	}

	source, err := sources.Create("cex", "bitfinex", config)
	if err != nil {
		t.Fatalf("Failed to create Bitfinex source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from Bitfinex")
	}

	for symbol, price := range prices {
		t.Logf("Bitfinex %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}
	}

	if !source.IsHealthy() {
		t.Error("Bitfinex should be healthy after successful fetch")
	}
}

// TestRealBybit tests Bybit REST API (no API key required)
func TestRealBybit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"BTC/USDT":  "BTCUSDT",
			"ETH/USDT":  "ETHUSDT",
			"LUNC/USDT": "LUNCUSDT",
		},
	}

	source, err := sources.Create("cex", "bybit", config)
	if err != nil {
		t.Fatalf("Failed to create Bybit source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from Bybit")
	}

	for symbol, price := range prices {
		t.Logf("Bybit %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}
	}

	if !source.IsHealthy() {
		t.Error("Bybit should be healthy after successful fetch")
	}
}

// TestRealGateIO tests Gate.io REST API (no API key required)
func TestRealGateIO(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"BTC/USDT":  "BTC_USDT",
			"ETH/USDT":  "ETH_USDT",
			"LUNC/USDT": "LUNC_USDT",
		},
	}

	source, err := sources.Create("cex", "gateio", config)
	if err != nil {
		t.Fatalf("Failed to create Gate.io source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from Gate.io")
	}

	for symbol, price := range prices {
		t.Logf("Gate.io %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}
	}

	if !source.IsHealthy() {
		t.Error("Gate.io should be healthy after successful fetch")
	}
}

// TestRealOKX tests OKX REST API (no API key required)
func TestRealOKX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"BTC/USDT":  "BTC-USDT",
			"ETH/USDT":  "ETH-USDT",
			"LUNC/USDT": "LUNC-USDT",
		},
	}

	source, err := sources.Create("cex", "okx", config)
	if err != nil {
		t.Fatalf("Failed to create OKX source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from OKX")
	}

	for symbol, price := range prices {
		t.Logf("OKX %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}
	}

	if !source.IsHealthy() {
		t.Error("OKX should be healthy after successful fetch")
	}
}
