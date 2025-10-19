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

const (
	luncUsdcPair = "LUNC/USDC"
)

// TestRealTerraportDEX tests the Terraport source against the real Terra Classic mainnet.
func TestRealTerraportDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           luncUsdcPair,
				"contract_address": "terra1a29fltd5h5y8se0xanw48wkmqg7nfpmv5jsl472uun0274h8xatqd3yzfh",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/0BB9D8513E8E8E9AE6A9D211D9136E6DA42288DDE6CFAA453A150A4566054DC5",
				"decimals0":        6,
				"decimals1":        6,
			},
		},
	}
	minPrice := decimal.NewFromFloat(0.00001)
	maxPrice := decimal.NewFromFloat(0.001)
	testCosmWasmDEXSource(t, "terraport", config, NewTerraportSource, minPrice, maxPrice)
}

// TestRealTerraswapDEX tests Terraswap DEX integration on Terra Classic mainnet.
func TestRealTerraswapDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           luncUsdcPair,
				"contract_address": "terra19h62lw77rluxf6yg4szcclcgk9tsalx72cv7dlzvzs8gy20g70js7c9jkc",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/0BB9D8513E8E8E9AE6A9D211D9136E6DA42288DDE6CFAA453A150A4566054DC5",
				"decimals0":        6,
				"decimals1":        6,
			},
		},
	}
	minPrice := decimal.NewFromFloat(0.00001)
	maxPrice := decimal.NewFromFloat(0.001)
	testCosmWasmDEXSource(t, "terraswap", config, NewTerraswapSource, minPrice, maxPrice)
}

// TestRealGarudaDEX tests the Garuda source against the real Terra Classic mainnet.
func TestRealGarudaDEX(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           luncUsdcPair,
				"contract_address": "terra1vnt3tjg0v98hgp0vx8nynvklnjqzkzsqvtpzv9v56r800gdhmxwstv5y64",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/0BB9D8513E8E8E9AE6A9D211D9136E6DA42288DDE6CFAA453A150A4566054DC5",
				"decimals0":        6,
				"decimals1":        6,
			},
		},
	}
	minPrice := decimal.NewFromFloat(0.00001)
	maxPrice := decimal.NewFromFloat(0.001)
	testCosmWasmDEXSource(t, "garuda", config, NewGarudaSource, minPrice, maxPrice)
}

// nolint:gocognit // TestAllCosmWasmDEXs comprehensively tests all enabled DEX sources with nested loops and validations
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
	grpcClient, err := client.NewClient(client.Config{
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
						"asset1_denom":     "ibc/0BB9D8513E8E8E9AE6A9D211D9136E6DA42288DDE6CFAA453A150A4566054DC5",
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
			defer func() { _ = source.Stop() }()

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

// setupCosmWasmTestClient sets up a gRPC client for CosmWasm integration tests.
func setupCosmWasmTestClient(t *testing.T) *client.Client {
	t.Helper()
	// Configure SDK
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount("terra", "terrapub")
	sdkConfig.SetBech32PrefixForValidator("terravaloper", "terravaloperpub")
	sdkConfig.SetBech32PrefixForConsensusNode("terravalcons", "terravalconspub")
	sdkConfig.SetCoinType(330)
	sdkConfig.SetPurpose(44)

	// Terra Classic mainnet gRPC endpoints
	grpcEndpoints := []client.EndpointConfig{
		{Address: "terra-classic-grpc.publicnode.com:443", TLS: true},
	}

	logger := zerolog.Nop()
	grpcClient, err := client.NewClient(client.Config{
		Endpoints:         grpcEndpoints,
		ChainID:           "columbus-5",
		InterfaceRegistry: codectypes.NewInterfaceRegistry(),
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("Failed to create gRPC client: %v", err)
	}

	return grpcClient
}

// testCosmWasmDEXSource is a helper to reduce duplication in CosmWasm DEX integration tests.
func testCosmWasmDEXSource(
	t *testing.T,
	sourceName string,
	config map[string]interface{},
	sourceCreator func(map[string]interface{}, *client.Client) (sources.Source, error),
	minPrice, maxPrice decimal.Decimal,
) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	grpcClient := setupCosmWasmTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	source, err := sourceCreator(config, grpcClient)
	if err != nil {
		t.Fatalf("Failed to create %s source: %v", sourceName, err)
	}

	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start %s: %v", sourceName, err)
	}
	defer func() { _ = source.Stop() }()

	time.Sleep(2 * time.Second)

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Error("No prices returned from " + sourceName)
	}

	for symbol, price := range prices {
		t.Logf("%s %s: %s", sourceName, symbol, price.Price.String())

		if price.Price.LessThanOrEqual(decimal.Zero) {
			t.Errorf("Invalid price for %s: %s", symbol, price.Price.String())
		}

		if time.Since(price.Timestamp) > 2*time.Minute {
			t.Errorf("Stale price for %s: %s", symbol, price.Timestamp)
		}

		if price.Source != sourceName {
			t.Errorf("Wrong source: got %s, want %s", price.Source, sourceName)
		}

		if symbol == luncUsdcPair {
			if price.Price.LessThan(minPrice) || price.Price.GreaterThan(maxPrice) {
				t.Logf("WARNING: LUNC/USDC price possibly out of range: %s", price.Price.String())
			}
		}
	}

	if !source.IsHealthy() {
		t.Errorf("%s source should be healthy after successful fetch", sourceName)
	}
}
