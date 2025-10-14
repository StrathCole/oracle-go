package cosmwasm

import (
	"context"
	"testing"

	"tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/server/sources"
)

// mockGRPCClient is a simple mock for testing
type mockGRPCClient struct {
	queryResponse []byte
	queryError    error
}

func (m *mockGRPCClient) QuerySmartContract(ctx context.Context, contractAddress string, queryMsg []byte) ([]byte, error) {
	if m.queryError != nil {
		return nil, m.queryError
	}
	return m.queryResponse, nil
}

func (m *mockGRPCClient) CurrentEndpoint() string {
	return "localhost:9090"
}

func TestTerraportSource_NewSource(t *testing.T) {
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1xyz123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
				"decimals0":        float64(6),
				"decimals1":        float64(6),
			},
		},
	}

	source, err := NewTerraportSource(config, (*client.Client)(nil))
	if err != nil {
		t.Fatalf("NewTerraportSource failed: %v", err)
	}

	if source.Name() != "terraport" {
		t.Errorf("Expected name 'terraport', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeCosmWasm {
		t.Errorf("Expected type SourceTypeCosmWasm, got %v", source.Type())
	}

	symbols := source.Symbols()
	if len(symbols) != 1 {
		t.Errorf("Expected 1 symbol, got %d", len(symbols))
	}

	if symbols[0] != "LUNC/USDC" {
		t.Errorf("Expected LUNC/USDC, got %s", symbols[0])
	}
}

func TestTerraportSource_MultiplePairs(t *testing.T) {
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1xyz123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
			},
			map[string]interface{}{
				"symbol":           "LUNC/axlUSDC",
				"contract_address": "terra1abc456",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/axlUSDC",
			},
		},
	}

	source, err := NewTerraportSource(config, (*client.Client)(nil))
	if err != nil {
		t.Fatalf("NewTerraportSource failed: %v", err)
	}

	symbols := source.Symbols()
	if len(symbols) != 2 {
		t.Errorf("Expected 2 symbols, got %d", len(symbols))
	}
}

func TestTerraportSource_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name:   "missing pairs",
			config: map[string]interface{}{},
		},
		{
			name: "invalid pairs type",
			config: map[string]interface{}{
				"pairs": "invalid",
			},
		},
		{
			name: "empty pairs",
			config: map[string]interface{}{
				"pairs": []interface{}{},
			},
		},
		{
			name: "pair missing symbol",
			config: map[string]interface{}{
				"pairs": []interface{}{
					map[string]interface{}{
						"contract_address": "terra1xyz",
						"asset0_denom":     "uluna",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTerraportSource(tt.config, (*client.Client)(nil))
			if err == nil {
				t.Error("Expected error for invalid config, got none")
			}
		})
	}
}

func TestTerraportSource_Initialize(t *testing.T) {
	t.Skip("Skipping Initialize test - requires valid gRPC client")

	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1xyz123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
			},
		},
	}

	source, err := NewTerraportSource(config, (*client.Client)(nil))
	if err != nil {
		t.Fatalf("NewTerraportSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
}

func TestTerraswapSource_NewSource(t *testing.T) {
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1xyz123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
				"decimals0":        float64(6),
				"decimals1":        float64(6),
			},
		},
	}

	source, err := NewTerraswapSource(config, (*client.Client)(nil))
	if err != nil {
		t.Fatalf("NewTerraswapSource failed: %v", err)
	}

	if source.Name() != "terraswap" {
		t.Errorf("Expected name 'terraswap', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeCosmWasm {
		t.Errorf("Expected type SourceTypeCosmWasm, got %v", source.Type())
	}
}

func TestTerraswapSource_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name:   "missing pairs",
			config: map[string]interface{}{},
		},
		{
			name: "empty pairs",
			config: map[string]interface{}{
				"pairs": []interface{}{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTerraswapSource(tt.config, (*client.Client)(nil))
			if err == nil {
				t.Error("Expected error for invalid config, got none")
			}
		})
	}
}

func TestGarudaSource_NewSource(t *testing.T) {
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1garuda123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
				"decimals0":        float64(6),
				"decimals1":        float64(6),
			},
		},
	}

	source, err := NewGarudaSource(config, (*client.Client)(nil))
	if err != nil {
		t.Fatalf("NewGarudaSource failed: %v", err)
	}

	if source.Name() != "garuda" {
		t.Errorf("Expected name 'garuda', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeCosmWasm {
		t.Errorf("Expected type SourceTypeCosmWasm, got %v", source.Type())
	}

	symbols := source.Symbols()
	if len(symbols) != 1 {
		t.Errorf("Expected 1 symbol, got %d", len(symbols))
	}
}

func TestGarudaSource_Initialize(t *testing.T) {
	t.Skip("Skipping Initialize test - requires valid gRPC client")

	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USDC",
				"contract_address": "terra1garuda123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/B3504E092456BA618CC28AC671A71FB08C6CA0FD0BE7C8A5B5A3E2DD933CC9E4",
			},
		},
	}

	source, err := NewGarudaSource(config, (*client.Client)(nil))
	if err != nil {
		t.Fatalf("NewGarudaSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
}

func TestGarudaSource_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name:   "missing pairs",
			config: map[string]interface{}{},
		},
		{
			name: "invalid pairs type",
			config: map[string]interface{}{
				"pairs": "invalid",
			},
		},
		{
			name: "empty pairs",
			config: map[string]interface{}{
				"pairs": []interface{}{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewGarudaSource(tt.config, (*client.Client)(nil))
			if err == nil {
				t.Error("Expected error for invalid config, got none")
			}
		})
	}
}
