// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"fmt"
	"sort"
	"time"

	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/metrics"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/shopspring/decimal"
)

const (
	// OutlierThreshold is the percentage deviation from median to consider an outlier.
	OutlierThreshold = 0.10 // 10%
)

// MedianAggregator aggregates prices using median and rejects outliers.
type MedianAggregator struct {
	logger *logging.Logger
}

// Ensure MedianAggregator implements Aggregator interface.
var _ Aggregator = (*MedianAggregator)(nil)

// NewMedianAggregator creates a new median aggregator.
func NewMedianAggregator(logger *logging.Logger) *MedianAggregator {
	return &MedianAggregator{
		logger: logger,
	}
}

// Aggregate computes median prices from multiple sources with outlier detection.
func (a *MedianAggregator) Aggregate(sourcePrices map[string]map[string]sources.Price, sourceWeights map[string]float64) (map[string]sources.Price, error) {
	start := time.Now()
	defer func() {
		metrics.RecordAggregation("median", time.Since(start))
	}()

	if len(sourcePrices) == 0 {
		return nil, fmt.Errorf("%w", ErrNoSourcePrices)
	}

	// Collect all prices by NORMALIZED symbol to handle USDT/USD/USDC aliases
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

	// Compute median for each symbol
	result := make(map[string]sources.Price)
	for symbol, prices := range pricesBySymbol {
		if len(prices) == 0 {
			continue
		}

		// Compute median with outlier rejection
		medianPrice, err := a.computeMedianWithOutlierRejection(symbol, prices)
		if err != nil {
			a.logger.Warn("Failed to compute median", "symbol", symbol, "error", err)
			continue
		}

		// Store with normalized symbol
		medianPrice.Symbol = symbol
		result[symbol] = medianPrice
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("%w", ErrNoPricesComputed)
	}

	a.logger.Debug("Aggregated prices", "symbols", len(result))
	return result, nil
}

// computeMedianWithOutlierRejection computes median with 10% outlier rejection.
func (a *MedianAggregator) computeMedianWithOutlierRejection(symbol string, prices []priceWithSource) (sources.Price, error) {
	if len(prices) == 0 {
		return sources.Price{}, fmt.Errorf("%w: %s", sources.ErrNoSymbolsForPrice, symbol)
	}

	// Single price - no outlier detection needed
	if len(prices) == 1 {
		return prices[0].price, nil
	}

	// Sort prices by value
	sort.Slice(prices, func(i, j int) bool {
		return prices[i].price.Price.LessThan(prices[j].price.Price)
	})

	// Compute initial median
	initialMedian := a.median(prices)

	// Filter outliers (prices deviating >10% from median)
	filtered := make([]priceWithSource, 0, len(prices))
	for _, p := range prices {
		deviation := p.price.Price.Sub(initialMedian).Abs()
		deviationPct := deviation.Div(initialMedian)

		if deviationPct.GreaterThan(decimal.NewFromFloat(OutlierThreshold)) {
			a.logger.Debug("Rejecting outlier",
				"symbol", symbol,
				"source", p.source,
				"price", p.price.Price.String(),
				"median", initialMedian.String(),
				"deviation_pct", deviationPct.Mul(decimal.NewFromInt(100)).String())

			metrics.RecordOutlierRejection(symbol)
			continue
		}

		filtered = append(filtered, p)
	}

	// Need at least 1 price after filtering
	if len(filtered) == 0 {
		a.logger.Warn("All prices rejected as outliers, using initial median",
			"symbol", symbol,
			"initial_count", len(prices))
		filtered = prices // Fall back to all prices
	}

	// Compute final median from filtered prices
	finalMedian := a.median(filtered)

	return sources.Price{
		Symbol:    symbol,
		Price:     finalMedian,
		Timestamp: time.Now(),
		Source:    "median_aggregator",
	}, nil
}

// median computes the weighted median of a sorted price list.
// For weighted median: find the price where cumulative weight reaches 50% of total weight.
func (a *MedianAggregator) median(prices []priceWithSource) decimal.Decimal {
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

// priceWithSource tracks which source provided a price and its weight.
type priceWithSource struct {
	price  sources.Price
	source string
	weight float64 // Weight for aggregation (1.0 = standard, 0.5 = half weight, etc.)
}
