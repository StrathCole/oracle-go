package cosmwasm

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// InitializeCosmWasmBase is a helper to reduce duplication in NewXSource constructors.
// It handles extracting logger, interval, and creating BaseSource with pairs map.
func InitializeCosmWasmBase(
	sourceName string,
	sourceType sources.SourceType,
	pairs []sources.CosmWasmPairConfig,
	defaultInterval time.Duration,
	config map[string]interface{},
) (*sources.BaseSource, time.Duration, error) {
	// Validate pairs
	if len(pairs) == 0 {
		return nil, 0, fmt.Errorf("%w", ErrNoValidPairs)
	}

	// Get or set default update interval
	updateInterval := defaultInterval
	if interval, ok := config["update_interval"].(string); ok {
		if d, err := time.ParseDuration(interval); err == nil {
			updateInterval = d
		}
	}

	// Get logger
	logger := sources.GetLoggerFromConfig(config)

	// Create simple map for BaseSource (symbol => contract_address)
	simplePairs := make(map[string]string, len(pairs))
	for _, p := range pairs {
		simplePairs[p.Symbol] = p.ContractAddress
	}

	// Create and return BaseSource
	base := sources.NewBaseSource(sourceName, sourceType, simplePairs, logger)
	return base, updateInterval, nil
}

// CalculatePoolPrice calculates price from asset amounts with decimals.
// Used by both Terraport and Terraswap (and Garuda for some logic).
func CalculatePoolPrice(amount0Str, amount1Str string, decimals0, decimals1 int) (decimal.Decimal, error) {
	// Parse amounts
	amount0, err := decimal.NewFromString(amount0Str)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to parse amount0: %w", err)
	}

	amount1, err := decimal.NewFromString(amount1Str)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to parse amount1: %w", err)
	}

	// Normalize by decimals
	decimals0D := decimal.NewFromInt(int64(decimals0))
	decimals1D := decimal.NewFromInt(int64(decimals1))

	amount0 = amount0.Div(decimal.NewFromInt(10).Pow(decimals0D))
	amount1 = amount1.Div(decimal.NewFromInt(10).Pow(decimals1D))

	// Check for zero liquidity
	if amount0.IsZero() {
		return decimal.Zero, fmt.Errorf("%w", ErrZeroLiquidity)
	}

	// Return quote/base price
	return amount1.Div(amount0), nil
}
