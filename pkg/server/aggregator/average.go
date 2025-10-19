// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"fmt"
	"time"

	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/metrics"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/shopspring/decimal"
)

// priceWithWeight tracks price and its source weight.
type priceWithWeight struct {
	price  sources.Price
	weight float64
}

// AverageAggregator aggregates prices using simple arithmetic mean.
type AverageAggregator struct {
	logger *logging.Logger
}

// Ensure AverageAggregator implements Aggregator interface.
var _ Aggregator = (*AverageAggregator)(nil)

// NewAverageAggregator creates a new average aggregator.
func NewAverageAggregator(logger *logging.Logger) *AverageAggregator {
	return &AverageAggregator{
		logger: logger,
	}
}

// Aggregate computes weighted average prices from multiple sources.
func (a *AverageAggregator) Aggregate(sourcePrices map[string]map[string]sources.Price, sourceWeights map[string]float64) (map[string]sources.Price, error) {
	start := time.Now()
	defer func() {
		metrics.RecordAggregation("average", time.Since(start))
	}()

	if len(sourcePrices) == 0 {
		return nil, fmt.Errorf("%w", ErrNoSourcePrices)
	}

	// Collect all prices by NORMALIZED symbol with weights
	pricesBySymbol := make(map[string][]priceWithWeight)

	for sourceName, prices := range sourcePrices {
		// Get weight for this source (default to 1.0 if not specified)
		weight := 1.0
		if w, ok := sourceWeights[sourceName]; ok {
			weight = w
		}

		for originalSymbol, price := range prices {
			// Normalize the symbol (e.g., LUNC/USDT -> LUNC/USD)
			normalizedSymbol := sources.NormalizeSymbol(originalSymbol)
			pricesBySymbol[normalizedSymbol] = append(pricesBySymbol[normalizedSymbol], priceWithWeight{
				price:  price,
				weight: weight,
			})
		}
	}

	// Compute weighted average for each symbol
	result := make(map[string]sources.Price)
	for symbol, prices := range pricesBySymbol {
		if len(prices) == 0 {
			continue
		}

		avg := a.computeWeightedAverage(prices)
		result[symbol] = sources.Price{
			Symbol:    symbol, // Use normalized symbol
			Price:     avg,
			Timestamp: time.Now(),
			Source:    "average_aggregator",
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("%w", ErrNoPricesComputed)
	}

	a.logger.Debug("Aggregated prices using weighted average", "symbols", len(result))
	return result, nil
}

// computeWeightedAverage calculates the weighted arithmetic mean of prices.
func (a *AverageAggregator) computeWeightedAverage(prices []priceWithWeight) decimal.Decimal {
	if len(prices) == 0 {
		return decimal.Zero
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
