// Package sources provides price source interfaces and implementations.
package sources

import (
	"fmt"
	"strings"

	"github.com/StrathCole/oracle-go/pkg/logging"
)

// GetLoggerFromConfig extracts logger from config map or returns a default noop logger.
// Sources should use this to get the logger passed from main.go.
// If no logger is configured, returns a noop logger to prevent nil pointer dereferences.
func GetLoggerFromConfig(config map[string]interface{}) *logging.Logger {
	if loggerInterface, ok := config["logger"]; ok {
		if logger, ok := loggerInterface.(*logging.Logger); ok {
			return logger
		}
	}

	// return default noop logger if logger not found
	return logging.NewNoopLogger()
}

// ParsePairsFromMap extracts pair mappings from config where pairs is a map.
// Expected format: pairs: { "LUNC/USDT": "LUNCUSDT", "BTC/USD": "bitcoin" }.
// This is used by CEX sources that need to map unified symbols to source-specific symbols.
func ParsePairsFromMap(config map[string]interface{}) (map[string]string, error) {
	pairsRaw, ok := config["pairs"]
	if !ok {
		return nil, fmt.Errorf("%w: 'pairs' key", ErrInvalidConfig)
	}

	pairsMap, ok := pairsRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: pairs must be map[string]string", ErrInvalidConfig)
	}

	pairs := make(map[string]string, len(pairsMap))
	for unified, sourceRaw := range pairsMap {
		source, ok := sourceRaw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s is %T", ErrInvalidConfig, unified, sourceRaw)
		}
		// Validate unified symbol format
		if err := ValidateSymbolFormat(unified); err != nil {
			return nil, fmt.Errorf("unified symbol: %w", err)
		}
		pairs[unified] = source
	}

	if len(pairs) == 0 {
		return nil, fmt.Errorf("%w", ErrNoPairsConfiguredHelper)
	}

	return pairs, nil
}

// CosmWasmPairConfig represents a DEX pair configuration for CosmWasm contracts.
type CosmWasmPairConfig struct {
	Symbol          string // Unified symbol (e.g., "LUNC/USTC")
	ContractAddress string // Pair contract address
	Asset0Denom     string // First asset denom
	Asset1Denom     string // Second asset denom
	Decimals0       int    // Decimals for first asset
	Decimals1       int    // Decimals for second asset
}

// ParseCosmWasmPairs extracts CosmWasm pair configurations from config
// Expected format:
// pairs:
//   - symbol: "LUNC/USTC"
//     contract_address: "terra1..."
//     asset0_denom: "uluna"
//     asset1_denom: "uusd"
//     decimals0: 6
//     decimals1: 6.
func ParseCosmWasmPairs(config map[string]interface{}) ([]CosmWasmPairConfig, error) {
	pairsRaw, ok := config["pairs"]
	if !ok {
		return nil, fmt.Errorf("%w: pairs configuration not found", ErrInvalidConfig)
	}

	pairsList, ok := pairsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w", ErrPairsMustBeArray)
	}

	pairs := make([]CosmWasmPairConfig, 0, len(pairsList))

	for i, pairRaw := range pairsList {
		pairMap, ok := pairRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: pair at index %d is not an object", ErrInvalidConfig, i)
		}

		pair := CosmWasmPairConfig{
			Symbol:          getStringFromMap(pairMap, "symbol"),
			ContractAddress: getStringFromMap(pairMap, "contract_address"),
			Asset0Denom:     getStringFromMap(pairMap, "asset0_denom"),
			Asset1Denom:     getStringFromMap(pairMap, "asset1_denom"),
			Decimals0:       getIntFromMap(pairMap, "decimals0", 6),
			Decimals1:       getIntFromMap(pairMap, "decimals1", 6),
		}

		if pair.Symbol == "" {
			return nil, fmt.Errorf("%w: pair[%d] missing 'symbol'", ErrInvalidConfig, i)
		}
		if err := ValidateSymbolFormat(pair.Symbol); err != nil {
			return nil, fmt.Errorf("pair[%d] %s: %w", i, pair.Symbol, err)
		}
		if pair.ContractAddress == "" {
			return nil, fmt.Errorf("%w: pair[%d] %s missing 'contract_address'", ErrInvalidConfig, i, pair.Symbol)
		}

		pairs = append(pairs, pair)
	}

	if len(pairs) == 0 {
		return nil, fmt.Errorf("%w: parsed pairs", ErrNoPairsConfiguredHelper)
	}

	return pairs, nil
}

// Helper functions for extracting values from maps

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getIntFromMap(m map[string]interface{}, key string, defaultVal int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	default:
		return defaultVal
	}
}

// ValidateSymbolFormat checks if a symbol is in valid BASE/QUOTE format
// Valid formats:
//   - "LUNC/USD", "LUNC/USDT", "LUNC/USDC" (crypto pairs)
//   - "KRW/USD", "EUR/USD" (fiat pairs)
//   - "BTC/USDT" (crypto pairs)
//
// Invalid formats:
//   - "LUNC" (no quote currency)
//   - "LUNCUSDT" (no separator)
//   - "" (empty).
func ValidateSymbolFormat(symbol string) error {
	if symbol == "" {
		return fmt.Errorf("%w", ErrInvalidSymbolFormat)
	}

	parts := strings.Split(symbol, "/")
	if len(parts) != 2 {
		return fmt.Errorf("%w: %s", ErrInvalidSymbolFormat, symbol)
	}

	base := strings.TrimSpace(parts[0])
	quote := strings.TrimSpace(parts[1])

	if base == "" {
		return fmt.Errorf("%w: %s", ErrEmptyBaseCurrency, symbol)
	}
	if quote == "" {
		return fmt.Errorf("%w: %s", ErrEmptyQuoteCurrency, symbol)
	}

	return nil
}
