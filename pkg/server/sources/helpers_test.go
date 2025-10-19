package sources

import (
	"testing"
)

func TestParsePairsFromMap_Valid(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]interface{}
		expected map[string]string
	}{
		{
			name: "simple pairs",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USDT": "LUNCUSDT",
					"BTC/USDT":  "BTCUSDT",
				},
			},
			expected: map[string]string{
				"LUNC/USDT": "LUNCUSDT",
				"BTC/USDT":  "BTCUSDT",
			},
		},
		{
			name: "exchange-specific formats",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USD": "tLUNCUSD", // Bitfinex format
					"BTC/USD":  "tBTCUSD",
				},
			},
			expected: map[string]string{
				"LUNC/USD": "tLUNCUSD",
				"BTC/USD":  "tBTCUSD",
			},
		},
		{
			name: "coingecko slugs",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USD": "terra-luna",
					"USTC/USD": "terrausd",
				},
			},
			expected: map[string]string{
				"LUNC/USD": "terra-luna",
				"USTC/USD": "terrausd",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParsePairsFromMap(tt.config)
			if err != nil {
				t.Fatalf("ParsePairsFromMap failed: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d pairs, got %d", len(tt.expected), len(result))
			}

			for unifiedSymbol, sourceSymbol := range tt.expected {
				got, ok := result[unifiedSymbol]
				if !ok {
					t.Errorf("Missing pair %s", unifiedSymbol)
					continue
				}
				if got != sourceSymbol {
					t.Errorf("For %s: expected %s, got %s", unifiedSymbol, sourceSymbol, got)
				}
			}
		})
	}
}

func TestParsePairsFromMap_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]interface{}
		expectErr bool
	}{
		{
			name:      "missing pairs key",
			config:    map[string]interface{}{},
			expectErr: true,
		},
		{
			name: "pairs is not a map",
			config: map[string]interface{}{
				"pairs": "invalid",
			},
			expectErr: true,
		},
		{
			name: "pairs is array instead of map",
			config: map[string]interface{}{
				"pairs": []interface{}{"LUNC/USDT", "BTC/USDT"},
			},
			expectErr: true,
		},
		{
			name: "empty pairs map",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParsePairsFromMap(tt.config)
			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if result != nil {
					t.Errorf("Expected nil result on error, got %v", result)
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestParseCosmWasmPairs_Valid(t *testing.T) {
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USTC",
				"contract_address": "terra1xyz123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "uusd",
				"decimals0":        float64(6),
				"decimals1":        float64(6),
			},
			map[string]interface{}{
				"symbol":           "LUNC/axlUSDC",
				"contract_address": "terra1abc456",
				"asset0_denom":     "uluna",
				"asset1_denom":     "ibc/axlUSDC",
				"decimals0":        float64(6),
				"decimals1":        float64(6),
			},
		},
	}

	pairs, err := ParseCosmWasmPairs(config)
	if err != nil {
		t.Fatalf("ParseCosmWasmPairs failed: %v", err)
	}

	if len(pairs) != 2 {
		t.Fatalf("Expected 2 pairs, got %d", len(pairs))
	}

	// Check first pair
	if pairs[0].Symbol != "LUNC/USTC" {
		t.Errorf("Expected symbol LUNC/USTC, got %s", pairs[0].Symbol)
	}
	if pairs[0].ContractAddress != "terra1xyz123" {
		t.Errorf("Expected contract terra1xyz123, got %s", pairs[0].ContractAddress)
	}
	if pairs[0].Asset0Denom != "uluna" {
		t.Errorf("Expected asset0_denom uluna, got %s", pairs[0].Asset0Denom)
	}
	if pairs[0].Decimals0 != 6 {
		t.Errorf("Expected decimals0 6, got %d", pairs[0].Decimals0)
	}

	// Check second pair
	if pairs[1].Symbol != "LUNC/axlUSDC" {
		t.Errorf("Expected symbol LUNC/axlUSDC, got %s", pairs[1].Symbol)
	}
}

func TestParseCosmWasmPairs_DefaultDecimals(t *testing.T) {
	config := map[string]interface{}{
		"pairs": []interface{}{
			map[string]interface{}{
				"symbol":           "LUNC/USTC",
				"contract_address": "terra1xyz123",
				"asset0_denom":     "uluna",
				"asset1_denom":     "uusd",
				// decimals not specified - should default to 6
			},
		},
	}

	pairs, err := ParseCosmWasmPairs(config)
	if err != nil {
		t.Fatalf("ParseCosmWasmPairs failed: %v", err)
	}

	if len(pairs) != 1 {
		t.Fatalf("Expected 1 pair, got %d", len(pairs))
	}

	if pairs[0].Decimals0 != 6 {
		t.Errorf("Expected default decimals0 6, got %d", pairs[0].Decimals0)
	}
	if pairs[0].Decimals1 != 6 {
		t.Errorf("Expected default decimals1 6, got %d", pairs[0].Decimals1)
	}
}

func TestParseCosmWasmPairs_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]interface{}
		expectErr bool
	}{
		{
			name:      "missing pairs key",
			config:    map[string]interface{}{},
			expectErr: true,
		},
		{
			name: "pairs is not an array",
			config: map[string]interface{}{
				"pairs": "invalid",
			},
			expectErr: true,
		},
		{
			name: "pairs is map instead of array",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{
					"LUNC/USTC": "terra1xyz",
				},
			},
			expectErr: true,
		},
		{
			name: "empty pairs array",
			config: map[string]interface{}{
				"pairs": []interface{}{},
			},
			expectErr: true,
		},
		{
			name: "pair missing symbol",
			config: map[string]interface{}{
				"pairs": []interface{}{
					map[string]interface{}{
						"contract_address": "terra1xyz123",
						"asset0_denom":     "uluna",
						"asset1_denom":     "uusd",
					},
				},
			},
			expectErr: true,
		},
		{
			name: "pair missing contract_address",
			config: map[string]interface{}{
				"pairs": []interface{}{
					map[string]interface{}{
						"symbol":       "LUNC/USTC",
						"asset0_denom": "uluna",
						"asset1_denom": "uusd",
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseCosmWasmPairs(tt.config)
			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if len(result) > 0 {
					t.Errorf("Expected empty result on error, got %v", result)
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSymbolFormat(t *testing.T) {
	tests := []struct {
		name    string
		symbol  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid crypto pair with USD",
			symbol:  "LUNC/USD",
			wantErr: false,
		},
		{
			name:    "valid crypto pair with USDT",
			symbol:  "LUNC/USDT",
			wantErr: false,
		},
		{
			name:    "valid crypto pair with USDC",
			symbol:  "BTC/USDC",
			wantErr: false,
		},
		{
			name:    "valid fiat pair",
			symbol:  "EUR/USD",
			wantErr: false,
		},
		{
			name:    "valid crypto-crypto pair",
			symbol:  "LUNC/USTC",
			wantErr: false,
		},
		{
			name:    "invalid: no slash separator",
			symbol:  "LUNCUSDT",
			wantErr: true,
			errMsg:  "must be in BASE/QUOTE format",
		},
		{
			name:    "invalid: no quote currency",
			symbol:  "LUNC",
			wantErr: true,
			errMsg:  "must be in BASE/QUOTE format",
		},
		{
			name:    "invalid: empty string",
			symbol:  "",
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name:    "invalid: empty base",
			symbol:  "/USD",
			wantErr: true,
			errMsg:  "BASE currency cannot be empty",
		},
		{
			name:    "invalid: empty quote",
			symbol:  "LUNC/",
			wantErr: true,
			errMsg:  "QUOTE currency cannot be empty",
		},
		{
			name:    "valid: with whitespace (trimmed)",
			symbol:  " LUNC / USD ",
			wantErr: false,
		},
		{
			name:    "invalid: too many slashes",
			symbol:  "LUNC/USD/USDT",
			wantErr: true,
			errMsg:  "must be in BASE/QUOTE format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSymbolFormat(tt.symbol)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateSymbolFormat() expected error but got nil")
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateSymbolFormat() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("ValidateSymbolFormat() unexpected error = %v", err)
			}
		})
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
