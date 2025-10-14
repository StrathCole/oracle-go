package cosmwasm

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/feeder/client"
)

// TestRealTerraportDEX tests Terraport DEX integration on Terra Classic mainnet
func TestRealTerraportDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Terra Classic mainnet gRPC endpoints
	grpcEndpoints := []string{
		"grpc.terra-classic.hexxagon.io:443",
		"terra-classic-grpc.publicnode.com:443",
	}

	// Create gRPC client
	grpcClient, err := client.NewClient(grpcEndpoints, true)
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}
	defer grpcClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to gRPC
	if err := grpcClient.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect to gRPC: %v", err)
	}

	// Terraport LUNC/USDC pair configuration
	config := map[string]interface{}{
		"contract_address": "terra1a29fltd5h5y8se0xanw48wkmqg7nfpmv5jsl472uun0274h8xatqd3yzfh",
		"pair_info": map[string]interface{}{
			"symbol":     "LUNC/USDC",
			"token0":     "uluna",
			"token1":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4", // axlUSDC
			"decimals0":  6,
			"decimals1":  6,
			"is_native0": true,
			"is_native1": false,
		},
	}

	source, err := NewTerraportSource(config, grpcClient)
	if err != nil {
		t.Fatalf("Failed to create Terraport source: %v", err)
	}

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer source.Stop()

	// Wait for initial price fetch
	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from Terraport")
	}

	for symbol, price := range prices {
		t.Logf("Terraport %s: %s", symbol, price.Price.String())

		// Price must be positive
		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s (must be > 0)", symbol, price.Price.String())
		}

		// Timestamp must be recent
		if time.Since(price.Timestamp) > 2*time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		// Source must match
		if price.Source != "terraport" {
			t.Errorf("Wrong source for %s: got %s, want terraport", symbol, price.Source)
		}

		// LUNC/USDC should be in reasonable range
		if symbol == "LUNC/USDC" {
			min := decimal.NewFromFloat(0.000001)
			max := decimal.NewFromFloat(0.01)
			if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
				t.Logf("WARNING: LUNC/USDC price possibly out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Error("Terraport source should be healthy after successful fetch")
	}
}

// TestRealTerraswapDEX tests Terraswap DEX integration on Terra Classic mainnet
func TestRealTerraswapDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	grpcEndpoints := []string{
		"grpc.terra-classic.hexxagon.io:443",
		"terra-classic-grpc.publicnode.com:443",
	}

	grpcClient, err := client.NewClient(grpcEndpoints, true)
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}
	defer grpcClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := grpcClient.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect to gRPC: %v", err)
	}

	// Terraswap LUNC/USDC pair configuration
	config := map[string]interface{}{
		"contract_address": "terra19h62lw77rluxf6yg4szcclcgk9tsalx72cv7dlzvzs8gy20g70js7c9jkc",
		"pair_info": map[string]interface{}{
			"symbol":     "LUNC/USDC",
			"token0":     "uluna",
			"token1":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
			"decimals0":  6,
			"decimals1":  6,
			"is_native0": true,
			"is_native1": false,
		},
	}

	source, err := NewTerraswapSource(config, grpcClient)
	if err != nil {
		t.Fatalf("Failed to create Terraswap source: %v", err)
	}

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
		t.Error("No prices returned from Terraswap")
	}

	for symbol, price := range prices {
		t.Logf("Terraswap %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if time.Since(price.Timestamp) > 2*time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		if price.Source != "terraswap" {
			t.Errorf("Wrong source: got %s, want terraswap", price.Source)
		}

		if symbol == "LUNC/USDC" {
			min := decimal.NewFromFloat(0.000001)
			max := decimal.NewFromFloat(0.01)
			if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
				t.Logf("WARNING: LUNC/USDC price possibly out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Error("Terraswap source should be healthy after successful fetch")
	}
}

// TestRealGarudaDEX tests Garuda DeFi integration on Terra Classic mainnet
func TestRealGarudaDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	grpcEndpoints := []string{
		"grpc.terra-classic.hexxagon.io:443",
		"terra-classic-grpc.publicnode.com:443",
	}

	grpcClient, err := client.NewClient(grpcEndpoints, true)
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}
	defer grpcClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := grpcClient.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect to gRPC: %v", err)
	}

	// Garuda LUNC/USDC pair configuration
	config := map[string]interface{}{
		"contract_address": "terra1vnt3tjg0v98hgp0vx8nynvklnjqzkzsqvtpzv9v56r800gdhmxwstv5y64",
		"pair_info": map[string]interface{}{
			"symbol":     "LUNC/USDC",
			"token0":     "uluna",
			"token1":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
			"decimals0":  6,
			"decimals1":  6,
			"is_native0": true,
			"is_native1": false,
		},
	}

	source, err := NewGarudaSource(config, grpcClient)
	if err != nil {
		t.Fatalf("Failed to create Garuda source: %v", err)
	}

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
		t.Error("No prices returned from Garuda")
	}

	for symbol, price := range prices {
		t.Logf("Garuda %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if time.Since(price.Timestamp) > 2*time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		if price.Source != "garuda" {
			t.Errorf("Wrong source: got %s, want garuda", price.Source)
		}

		if symbol == "LUNC/USDC" {
			min := decimal.NewFromFloat(0.000001)
			max := decimal.NewFromFloat(0.01)
			if price.Price.LessThan(min) || price.Price.GreaterThan(max) {
				t.Logf("WARNING: LUNC/USDC price possibly out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Error("Garuda source should be healthy after successful fetch")
	}
}

// TestAllCosmWasmDEXs tests all three Terra Classic DEX sources together
func TestAllCosmWasmDEXs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	grpcEndpoints := []string{
		"grpc.terra-classic.hexxagon.io:443",
		"terra-classic-grpc.publicnode.com:443",
	}

	grpcClient, err := client.NewClient(grpcEndpoints, true)
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}
	defer grpcClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := grpcClient.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect to gRPC: %v", err)
	}

	configs := []struct {
		name     string
		address  string
		factory  func(map[string]interface{}, *client.Client) (interface{}, error)
	}{
		{
			name:    "terraport",
			address: "terra1a29fltd5h5y8se0xanw48wkmqg7nfpmv5jsl472uun0274h8xatqd3yzfh",
			factory: func(cfg map[string]interface{}, cl *client.Client) (interface{}, error) {
				return NewTerraportSource(cfg, cl)
			},
		},
		{
			name:    "terraswap",
			address: "terra19h62lw77rluxf6yg4szcclcgk9tsalx72cv7dlzvzs8gy20g70js7c9jkc",
			factory: func(cfg map[string]interface{}, cl *client.Client) (interface{}, error) {
				return NewTerraswapSource(cfg, cl)
			},
		},
		{
			name:    "garuda",
			address: "terra1vnt3tjg0v98hgp0vx8nynvklnjqzkzsqvtpzv9v56r800gdhmxwstv5y64",
			factory: func(cfg map[string]interface{}, cl *client.Client) (interface{}, error) {
				return NewGarudaSource(cfg, cl)
			},
		},
	}

	allPrices := make(map[string]decimal.Decimal)

	for _, tc := range configs {
		t.Run(tc.name, func(t *testing.T) {
			config := map[string]interface{}{
				"contract_address": tc.address,
				"pair_info": map[string]interface{}{
					"symbol":     "LUNC/USDC",
					"token0":     "uluna",
					"token1":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
					"decimals0":  6,
					"decimals1":  6,
					"is_native0": true,
					"is_native1": false,
				},
			}

			sourceInterface, err := tc.factory(config, grpcClient)
			if err != nil {
				t.Fatalf("Failed to create %s source: %v", tc.name, err)
			}

			// Type assert based on which factory was used
			var source interface {
				Initialize(context.Context) error
				Start(context.Context) error
				Stop() error
				GetPrices(context.Context) (map[string]interface{}, error)
				IsHealthy() bool
				Name() string
			}

			switch s := sourceInterface.(type) {
			case *TerraportSource:
				source = s
			case *TerraswapSource:
				source = s
			case *GarudaSource:
				source = s
			}

			if err := source.Initialize(ctx); err != nil {
				t.Fatalf("Failed to initialize %s: %v", tc.name, err)
			}

			if err := source.Start(ctx); err != nil {
				t.Fatalf("Failed to start %s: %v", tc.name, err)
			}
			defer source.Stop()

			time.Sleep(3 * time.Second)

			prices, err := source.GetPrices(ctx)
			if err != nil {
				t.Fatalf("Failed to get prices from %s: %v", tc.name, err)
			}

			if len(prices) == 0 {
				t.Errorf("No prices returned from %s", tc.name)
				return
			}

			for symbol, priceData := range prices {
				// Extract price from the interface{} (it's a Price struct)
				var price decimal.Decimal
				if p, ok := priceData.(struct {
					Symbol    string
					Price     decimal.Decimal
					Volume    decimal.Decimal
					Timestamp time.Time
					Source    string
				}); ok {
					price = p.Price
					t.Logf("%s %s: %s", tc.name, symbol, price.String())
					allPrices[tc.name] = price
				}

				if !source.IsHealthy() {
					t.Errorf("%s should be healthy", tc.name)
				}
			}
		})
	}

	// Compare prices from all three DEXs
	if len(allPrices) == 3 {
		t.Log("\nPrice Comparison:")
		for name, price := range allPrices {
			t.Logf("  %s: %s", name, price.String())
		}

		// Calculate price deviation
		var sum decimal.Decimal
		for _, price := range allPrices {
			sum = sum.Add(price)
		}
		avg := sum.Div(decimal.NewFromInt(int64(len(allPrices))))

		for name, price := range allPrices {
			deviation := price.Sub(avg).Div(avg).Mul(decimal.NewFromInt(100))
			t.Logf("  %s deviation from average: %s%%", name, deviation.StringFixed(2))

			// Warn if deviation is > 5%
			if deviation.Abs().GreaterThan(decimal.NewFromInt(5)) {
				t.Logf("  WARNING: %s price deviates >5%% from average", name)
			}
		}
	}
}
