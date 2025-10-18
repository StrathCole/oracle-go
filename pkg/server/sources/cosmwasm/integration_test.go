package cosmwasm

import (
	"context"
	"testing"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/server/sources"
)

// TestRealTerraportDEX tests the Terraport source against the real Terra Classic mainnet
func TestRealTerraportDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Configure SDK (required before any address operations)
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount("terra", "terrapub")
	sdkConfig.SetBech32PrefixForValidator("terravaloper", "terravaloperpub")
	sdkConfig.SetBech32PrefixForConsensusNode("terravalcons", "terravalconspub")
	sdkConfig.SetCoinType(330)
	sdkConfig.SetPurpose(44)
	// Note: Can't seal in test (may already be sealed)

	// Terra Classic mainnet gRPC endpoints
	grpcEndpoints := []client.EndpointConfig{
		{Address: "grpc.terra-classic.hexxagon.io:443", TLS: true},
		{Address: "terra-classic-grpc.publicnode.com:443", TLS: true},
	}

	// Create gRPC client with correct ClientConfig
	logger := zerolog.Nop() // Silent logger for tests
	grpcClient, err := client.NewClient(client.ClientConfig{
		Endpoints:         grpcEndpoints,
		ChainID:           "columbus-5",
		InterfaceRegistry: codectypes.NewInterfaceRegistry(),
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}
	// Client connections are established in NewClient, no Connect() method

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Terraport LUNC/USDC pair configuration
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1a29fltd5h5y8se0xanw48wkmqg7nfpmv5jsl472uun0274h8xatqd3yzfh",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
				"decimals0":        6,
				"decimals1":        6,
			},
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

	// Wait a bit for initial fetch
	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}
	// Validate prices
	if len(prices) == 0 {
		t.Error("No prices returned from Terraport")
	}

	for symbol, price := range prices {
		t.Logf("Terraport %s: %s", symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if time.Since(price.Timestamp) > 2*time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		if price.Source != "terraport" {
			t.Errorf("Wrong source: got %s, want terraport", price.Source)
		}

		if symbol == "LUNC/USDC" {
			min := decimal.NewFromFloat(0.00001)
			max := decimal.NewFromFloat(0.001)
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

	// Configure SDK
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount("terra", "terrapub")
	sdkConfig.SetBech32PrefixForValidator("terravaloper", "terravaloperpub")
	sdkConfig.SetBech32PrefixForConsensusNode("terravalcons", "terravalconspub")
	sdkConfig.SetCoinType(330)
	sdkConfig.SetPurpose(44)

	grpcEndpoints := []client.EndpointConfig{
		{Address: "grpc.terra-classic.hexxagon.io:443", TLS: true},
		{Address: "terra-classic-grpc.publicnode.com:443", TLS: true},
	}

	logger := zerolog.Nop()
	grpcClient, err := client.NewClient(client.ClientConfig{
		Endpoints:         grpcEndpoints,
		ChainID:           "columbus-5",
		InterfaceRegistry: codectypes.NewInterfaceRegistry(),
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Terraswap LUNC/USDC pair configuration
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra19h62lw77rluxf6yg4szcclcgk9tsalx72cv7dlzvzs8gy20g70js7c9jkc",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
				"decimals0":        6,
				"decimals1":        6,
			},
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

	// Configure SDK
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount("terra", "terrapub")
	sdkConfig.SetBech32PrefixForValidator("terravaloper", "terravaloperpub")
	sdkConfig.SetBech32PrefixForConsensusNode("terravalcons", "terravalconspub")
	sdkConfig.SetCoinType(330)
	sdkConfig.SetPurpose(44)

	grpcEndpoints := []client.EndpointConfig{
		{Address: "grpc.terra-classic.hexxagon.io:443", TLS: true},
		{Address: "terra-classic-grpc.publicnode.com:443", TLS: true},
	}

	logger := zerolog.Nop()
	grpcClient, err := client.NewClient(client.ClientConfig{
		Endpoints:         grpcEndpoints,
		ChainID:           "columbus-5",
		InterfaceRegistry: codectypes.NewInterfaceRegistry(),
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Garuda LUNC/USDC pair configuration
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1vnt3tjg0v98hgp0vx8nynvklnjqzkzsqvtpzv9v56r800gdhmxwstv5y64",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
				"decimals0":        6,
				"decimals1":        6,
			},
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

	// Configure SDK
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount("terra", "terrapub")
	sdkConfig.SetBech32PrefixForValidator("terravaloper", "terravaloperpub")
	sdkConfig.SetBech32PrefixForConsensusNode("terravalcons", "terravalconspub")
	sdkConfig.SetCoinType(330)
	sdkConfig.SetPurpose(44)

	grpcEndpoints := []client.EndpointConfig{
		{Address: "grpc.terra-classic.hexxagon.io:443", TLS: true},
		{Address: "terra-classic-grpc.publicnode.com:443", TLS: true},
	}

	logger := zerolog.Nop()
	grpcClient, err := client.NewClient(client.ClientConfig{
		Endpoints:         grpcEndpoints,
		ChainID:           "columbus-5",
		InterfaceRegistry: codectypes.NewInterfaceRegistry(),
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	configs := []struct {
		name    string
		address string
		factory func(map[string]interface{}, *client.Client) (interface{}, error)
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
				"pairs": []interface{}{
					map[string]interface{}{
						"symbol":           "LUNC/USDC",
						"contract_address": tc.address,
						"asset0_denom":     "uluna",
						"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
						"decimals0":        6,
						"decimals1":        6,
					},
				},
			}

			sourceInterface, err := tc.factory(config, grpcClient)
			if err != nil {
				t.Fatalf("Failed to create %s source: %v", tc.name, err)
			}

			// Use sources.Source interface directly
			var source sources.Source

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

			for symbol, price := range prices {
				t.Logf("%s %s: %s", tc.name, symbol, price.Price.String())
				allPrices[tc.name] = price.Price

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
