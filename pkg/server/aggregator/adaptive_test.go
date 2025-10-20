// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

func TestAdaptiveAggregator_SingleSource(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	sourcePrices := map[string]map[string]sources.Price{
		"binance": {
			"LUNC/USD": {
				Symbol:    "LUNC/USD",
				Price:     decimal.NewFromFloat(0.00012),
				Timestamp: time.Now(),
			},
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)

	assert.Equal(t, "LUNC/USD", result["LUNC/USD"].Symbol)
	assert.True(t, result["LUNC/USD"].Price.Equal(decimal.NewFromFloat(0.00012)))
}

func TestAdaptiveAggregator_NoOutliers(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	sourcePrices := map[string]map[string]sources.Price{
		"binance": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
		"kraken": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.000121), Timestamp: time.Now()},
		},
		"coinbase": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.000119), Timestamp: time.Now()},
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// With no outliers, should average all three prices
	expected := decimal.NewFromFloat(0.00012).
		Add(decimal.NewFromFloat(0.000121)).
		Add(decimal.NewFromFloat(0.000119)).
		Div(decimal.NewFromInt(3))

	assert.True(t, result["LUNC/USD"].Price.Sub(expected).Abs().LessThan(decimal.NewFromFloat(0.0000001)))
}

func TestAdaptiveAggregator_WithOutliers(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	sourcePrices := map[string]map[string]sources.Price{
		"binance": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
		"kraken": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.000121), Timestamp: time.Now()},
		},
		"coinbase": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.000119), Timestamp: time.Now()},
		},
		"bad_source": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.0005), Timestamp: time.Now()}, // 4x outlier
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// The outlier (0.0005) should be filtered out
	// Result should be average of the three good prices
	expected := decimal.NewFromFloat(0.00012).
		Add(decimal.NewFromFloat(0.000121)).
		Add(decimal.NewFromFloat(0.000119)).
		Div(decimal.NewFromInt(3))

	// Allow small floating point error
	diff := result["LUNC/USD"].Price.Sub(expected).Abs()
	assert.True(t, diff.LessThan(decimal.NewFromFloat(0.0000001)),
		"Expected %s, got %s, diff %s", expected.String(), result["LUNC/USD"].Price.String(), diff.String())
}

func TestAdaptiveAggregator_MedianFinalMode(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 2.0, "median")

	sourcePrices := map[string]map[string]sources.Price{
		"source1": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00010), Timestamp: time.Now()},
		},
		"source2": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
		"source3": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00011), Timestamp: time.Now()},
		},
		"source4": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00013), Timestamp: time.Now()},
		},
		"source5": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// After filtering, median should be the middle value
	// With 5 values [0.00010, 0.00011, 0.00012, 0.00012, 0.00013], median is 0.00012
	assert.True(t, result["LUNC/USD"].Price.Equal(decimal.NewFromFloat(0.00012)))
}

func TestAdaptiveAggregator_WithWeights(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	sourcePrices := map[string]map[string]sources.Price{
		"trusted_source": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
		"normal_source": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00013), Timestamp: time.Now()},
		},
	}

	weights := map[string]float64{
		"trusted_source": 2.0, // Double weight
		"normal_source":  1.0,
	}

	result, err := agg.Aggregate(sourcePrices, weights)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Weighted average: (0.00012 * 2.0 + 0.00013 * 1.0) / 3.0
	expected := decimal.NewFromFloat(0.00012).Mul(decimal.NewFromFloat(2.0)).
		Add(decimal.NewFromFloat(0.00013)).
		Div(decimal.NewFromFloat(3.0))

	diff := result["LUNC/USD"].Price.Sub(expected).Abs()
	assert.True(t, diff.LessThan(decimal.NewFromFloat(0.0000001)),
		"Expected %s, got %s", expected.String(), result["LUNC/USD"].Price.String())
}

func TestAdaptiveAggregator_SymbolNormalization(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	sourcePrices := map[string]map[string]sources.Price{
		"binance": {
			"LUNC/USDT": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
		"kraken": {
			"LUNC/USDC": {Price: decimal.NewFromFloat(0.000121), Timestamp: time.Now()},
		},
		"coinbase": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.000119), Timestamp: time.Now()},
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// All should be normalized to LUNC/USD
	assert.Contains(t, result, "LUNC/USD")
}

func TestAdaptiveAggregator_DifferentSensitivity(t *testing.T) {
	logger := logging.NewNoopLogger()

	sourcePrices := map[string]map[string]sources.Price{
		"source1": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00010), Timestamp: time.Now()},
		},
		"source2": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
		},
		"source3": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00015), Timestamp: time.Now()},
		},
	}

	// Test with strict sensitivity (1.5)
	strictAgg := NewAdaptiveAggregator(logger, 1.5, "average")
	strictResult, err := strictAgg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)

	// Test with tolerant sensitivity (2.5)
	tolerantAgg := NewAdaptiveAggregator(logger, 2.5, "average")
	tolerantResult, err := tolerantAgg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)

	// Both should produce results, but tolerant should be closer to true average
	assert.NotNil(t, strictResult)
	assert.NotNil(t, tolerantResult)
}

func TestAdaptiveAggregator_AllOutliersRejected(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 0.01, "average") // Very strict

	sourcePrices := map[string]map[string]sources.Price{
		"source1": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00010), Timestamp: time.Now()},
		},
		"source2": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00015), Timestamp: time.Now()},
		},
		"source3": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00020), Timestamp: time.Now()},
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Even with all prices rejected, should fall back to using all prices
	assert.NotNil(t, result["LUNC/USD"])
}

func TestAdaptiveAggregator_DefaultSensitivity(t *testing.T) {
	logger := logging.NewNoopLogger()

	// Test with zero sensitivity (should default to 1.5)
	agg := NewAdaptiveAggregator(logger, 0, "average")
	assert.Equal(t, 1.5, agg.sensitivity)

	// Test with negative sensitivity (should default to 1.5)
	agg2 := NewAdaptiveAggregator(logger, -1.0, "average")
	assert.Equal(t, 1.5, agg2.sensitivity)
}

func TestAdaptiveAggregator_DefaultFinalMode(t *testing.T) {
	logger := logging.NewNoopLogger()

	// Test with empty final mode (should default to "average")
	agg := NewAdaptiveAggregator(logger, 1.5, "")
	assert.Equal(t, "average", agg.finalMode)

	// Test with invalid final mode (should default to "average")
	agg2 := NewAdaptiveAggregator(logger, 1.5, "invalid")
	assert.Equal(t, "average", agg2.finalMode)
}

func TestAdaptiveAggregator_EmptyInput(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	// Empty source prices
	result, err := agg.Aggregate(map[string]map[string]sources.Price{}, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestAdaptiveAggregator_MultipleSymbols(t *testing.T) {
	logger := logging.NewNoopLogger()
	agg := NewAdaptiveAggregator(logger, 1.5, "average")

	sourcePrices := map[string]map[string]sources.Price{
		"binance": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.00012), Timestamp: time.Now()},
			"BTC/USD":  {Price: decimal.NewFromFloat(45000), Timestamp: time.Now()},
		},
		"kraken": {
			"LUNC/USD": {Price: decimal.NewFromFloat(0.000121), Timestamp: time.Now()},
			"BTC/USD":  {Price: decimal.NewFromFloat(45100), Timestamp: time.Now()},
		},
	}

	result, err := agg.Aggregate(sourcePrices, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)

	assert.Contains(t, result, "LUNC/USD")
	assert.Contains(t, result, "BTC/USD")
}
