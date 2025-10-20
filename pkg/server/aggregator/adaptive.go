// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/metrics"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// AdaptiveAggregator uses statistical filtering with configurable sensitivity.
// It computes median, calculates standard deviation, and filters outliers
// using the formula: |Pi - median| <= k * σ.
type AdaptiveAggregator struct {
	logger      *logging.Logger
	sensitivity float64 // k constant (e.g., 1.5-2.0)
	finalMode   string  // "median" or "average" for final aggregation
}

// Ensure AdaptiveAggregator implements Aggregator interface.
var _ Aggregator = (*AdaptiveAggregator)(nil)

// NewAdaptiveAggregator creates a new adaptive aggregator.
// sensitivity: k value for filtering (1.5 = strict, 2.0 = tolerant)
// finalMode: "median" or "average" for final aggregation of filtered prices.
func NewAdaptiveAggregator(logger *logging.Logger, sensitivity float64, finalMode string) *AdaptiveAggregator {
	// Default sensitivity if not specified
	if sensitivity <= 0 {
		sensitivity = 1.5
	}

	// Default final mode
	if finalMode != ModeMedian && finalMode != ModeAverage {
		finalMode = ModeAverage
	}

	return &AdaptiveAggregator{
		logger:      logger,
		sensitivity: sensitivity,
		finalMode:   finalMode,
	}
}

// Aggregate computes prices using adaptive threshold filtering.
func (a *AdaptiveAggregator) Aggregate(sourcePrices map[string]map[string]sources.Price, sourceWeights map[string]float64) (map[string]sources.Price, error) {
	start := time.Now()
	defer func() {
		metrics.RecordAggregation("adaptive", time.Since(start))
	}()

	if len(sourcePrices) == 0 {
		return nil, fmt.Errorf("%w", ErrNoSourcePrices)
	}

	// Collect all prices by NORMALIZED symbol with weights
	pricesBySymbol := make(map[string][]priceWithSource)
	for sourceName, prices := range sourcePrices {
		// Get weight for this source (default to 1.0 if not specified)
		weight := 1.0
		if w, ok := sourceWeights[sourceName]; ok {
			weight = w
		}

		for originalSymbol, price := range prices {
			// Normalize the symbol (e.g., LUNC/USDT -> LUNC/USD)
			normalizedSymbol := sources.NormalizeSymbol(originalSymbol)
			pricesBySymbol[normalizedSymbol] = append(pricesBySymbol[normalizedSymbol], priceWithSource{
				price:  price,
				source: sourceName,
				weight: weight,
			})
		}
	}

	// Compute adaptive filtered price for each symbol
	result := make(map[string]sources.Price)
	for symbol, prices := range pricesBySymbol {
		if len(prices) == 0 {
			continue
		}

		// Compute adaptive filtered price
		filteredPrice, err := a.computeAdaptivePrice(symbol, prices)
		if err != nil {
			a.logger.Warn("Failed to compute adaptive price", "symbol", symbol, "error", err)
			continue
		}

		// Store with normalized symbol
		filteredPrice.Symbol = symbol
		result[symbol] = filteredPrice
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("%w", ErrNoPricesComputed)
	}

	a.logger.Debug("Aggregated prices using adaptive threshold",
		"symbols", len(result),
		"sensitivity", a.sensitivity,
		"final_mode", a.finalMode)

	return result, nil
}

// computeAdaptivePrice applies adaptive threshold filtering.
// Process:
// 1. Compute median.
// 2. Compute standard deviation.
// 3. Filter by |Pi - median| <= k * σ.
// 4. Aggregate remaining values.
func (a *AdaptiveAggregator) computeAdaptivePrice(symbol string, prices []priceWithSource) (sources.Price, error) {
	if len(prices) == 0 {
		return sources.Price{}, fmt.Errorf("%w: %s", sources.ErrNoSymbolsForPrice, symbol)
	}

	// Single price - no filtering needed
	if len(prices) == 1 {
		return prices[0].price, nil
	}

	// Step 1: Compute median
	sortedPrices := make([]priceWithSource, len(prices))
	copy(sortedPrices, prices)
	sort.Slice(sortedPrices, func(i, j int) bool {
		return sortedPrices[i].price.Price.LessThan(sortedPrices[j].price.Price)
	})

	median := a.computeSimpleMedian(sortedPrices)

	// Step 2: Compute standard deviation
	stdDev := a.computeStdDev(prices, median)

	// Step 3: Filter prices by adaptive threshold
	threshold := decimal.NewFromFloat(a.sensitivity).Mul(stdDev)
	filtered := make([]priceWithSource, 0, len(prices))
	rejectedCount := 0

	for _, p := range prices {
		deviation := p.price.Price.Sub(median).Abs()

		if deviation.GreaterThan(threshold) {
			a.logger.Debug("Rejecting outlier (adaptive)",
				"symbol", symbol,
				"source", p.source,
				"price", p.price.Price.String(),
				"median", median.String(),
				"deviation", deviation.String(),
				"threshold", threshold.String(),
				"stddev", stdDev.String())

			metrics.RecordOutlierRejection(symbol)
			rejectedCount++
			continue
		}

		filtered = append(filtered, p)
	}

	// Need at least 1 price after filtering
	if len(filtered) == 0 {
		a.logger.Warn("All prices rejected by adaptive filter, using all prices",
			"symbol", symbol,
			"initial_count", len(prices),
			"stddev", stdDev.String(),
			"threshold", threshold.String())
		filtered = prices // Fall back to all prices
	}

	a.logger.Debug("Adaptive filtering complete",
		"symbol", symbol,
		"initial_count", len(prices),
		"filtered_count", len(filtered),
		"rejected_count", rejectedCount,
		"stddev", stdDev.String())

	// Step 4: Aggregate remaining values.
	var finalPrice decimal.Decimal
	if a.finalMode == ModeMedian {
		// Re-sort filtered prices for median calculation
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].price.Price.LessThan(filtered[j].price.Price)
		})
		finalPrice = a.computeWeightedMedian(filtered)
	} else {
		// Use weighted average
		finalPrice = a.computeWeightedAverage(filtered)
	}

	return sources.Price{
		Symbol:    symbol,
		Price:     finalPrice,
		Timestamp: time.Now(),
		Source:    fmt.Sprintf("adaptive_aggregator_%s", a.finalMode),
	}, nil
}

// computeSimpleMedian computes the simple (unweighted) median for initial filtering.
func (a *AdaptiveAggregator) computeSimpleMedian(sortedPrices []priceWithSource) decimal.Decimal {
	n := len(sortedPrices)
	if n == 0 {
		return decimal.Zero
	}

	if n == 1 {
		return sortedPrices[0].price.Price
	}

	// For even count, average the two middle values
	if n%2 == 0 {
		mid1 := sortedPrices[n/2-1].price.Price
		mid2 := sortedPrices[n/2].price.Price
		return mid1.Add(mid2).Div(decimal.NewFromInt(2))
	}

	// For odd count, return the middle value
	return sortedPrices[n/2].price.Price
}

// computeStdDev computes the standard deviation of prices.
// Formula: σ = sqrt(Σ(Pi - median)² / n).
func (a *AdaptiveAggregator) computeStdDev(prices []priceWithSource, median decimal.Decimal) decimal.Decimal {
	if len(prices) < 2 {
		return decimal.Zero
	}

	// Sum of squared deviations
	sumSquaredDev := decimal.Zero
	for _, p := range prices {
		deviation := p.price.Price.Sub(median)
		sumSquaredDev = sumSquaredDev.Add(deviation.Mul(deviation))
	}

	// Variance = sum / n
	variance := sumSquaredDev.Div(decimal.NewFromInt(int64(len(prices))))

	// Standard deviation = sqrt(variance)
	// Convert to float64 for sqrt, then back to decimal
	varianceFloat, _ := variance.Float64()
	stdDevFloat := math.Sqrt(varianceFloat)

	return decimal.NewFromFloat(stdDevFloat)
}

// computeWeightedAverage calculates the weighted arithmetic mean of prices.
func (a *AdaptiveAggregator) computeWeightedAverage(prices []priceWithSource) decimal.Decimal {
	if len(prices) == 0 {
		return decimal.Zero
	}

	if len(prices) == 1 {
		return prices[0].price.Price
	}

	weightedSum := decimal.Zero
	totalWeight := 0.0

	for _, p := range prices {
		weightedSum = weightedSum.Add(p.price.Price.Mul(decimal.NewFromFloat(p.weight)))
		totalWeight += p.weight
	}

	if totalWeight == 0 {
		return decimal.Zero
	}

	return weightedSum.Div(decimal.NewFromFloat(totalWeight))
}

// computeWeightedMedian computes the weighted median of a sorted price list.
// For weighted median: find the price where cumulative weight reaches 50% of total weight.
func (a *AdaptiveAggregator) computeWeightedMedian(prices []priceWithSource) decimal.Decimal {
	n := len(prices)
	if n == 0 {
		return decimal.Zero
	}

	if n == 1 {
		return prices[0].price.Price
	}

	// Calculate total weight
	totalWeight := 0.0
	for _, p := range prices {
		totalWeight += p.weight
	}

	// Find weighted median (price where cumulative weight reaches 50%)
	targetWeight := totalWeight / 2.0
	cumulativeWeight := 0.0

	for i, p := range prices {
		cumulativeWeight += p.weight

		// If we've reached or passed the 50% mark
		if cumulativeWeight >= targetWeight {
			// If exactly at 50% and there's a next price, average them
			if cumulativeWeight == targetWeight && i+1 < n {
				return p.price.Price.Add(prices[i+1].price.Price).Div(decimal.NewFromInt(2))
			}
			// Otherwise return this price
			return p.price.Price
		}
	}

	// Fallback (shouldn't reach here)
	return prices[n/2].price.Price
}
