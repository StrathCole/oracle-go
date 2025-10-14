package aggregator

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

// AverageAggregator aggregates prices using simple arithmetic mean
type AverageAggregator struct {
	logger *logging.Logger
}

// Ensure AverageAggregator implements Aggregator interface
var _ Aggregator = (*AverageAggregator)(nil)

// NewAverageAggregator creates a new average aggregator
func NewAverageAggregator(logger *logging.Logger) *AverageAggregator {
	return &AverageAggregator{
		logger: logger,
	}
}

// Aggregate computes average prices from multiple sources
func (a *AverageAggregator) Aggregate(sourcePrices map[string]map[string]sources.Price) (map[string]sources.Price, error) {
	start := time.Now()
	defer func() {
		metrics.RecordAggregation("average", time.Since(start))
	}()

	if len(sourcePrices) == 0 {
		return nil, fmt.Errorf("no source prices provided")
	}

	// Collect all prices by NORMALIZED symbol to handle USDT/USD/USDC aliases
	pricesBySymbol := make(map[string][]sources.Price)
	for _, prices := range sourcePrices {
		for originalSymbol, price := range prices {
			// Normalize the symbol (e.g., LUNC/USDT -> LUNC/USD)
			normalizedSymbol := sources.NormalizeSymbol(originalSymbol)
			pricesBySymbol[normalizedSymbol] = append(pricesBySymbol[normalizedSymbol], price)
		}
	}

	// Compute average for each symbol
	result := make(map[string]sources.Price)
	for symbol, prices := range pricesBySymbol {
		if len(prices) == 0 {
			continue
		}

		avg := a.computeAverage(prices)
		result[symbol] = sources.Price{
			Symbol:    symbol, // Use normalized symbol
			Price:     avg,
			Timestamp: time.Now(),
			Source:    "average_aggregator",
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no average prices computed")
	}

	a.logger.Debug("Aggregated prices using average", "symbols", len(result))
	return result, nil
}

// computeAverage calculates the arithmetic mean of prices
func (a *AverageAggregator) computeAverage(prices []sources.Price) decimal.Decimal {
	if len(prices) == 0 {
		return decimal.Zero
	}

	sum := decimal.Zero
	for _, price := range prices {
		sum = sum.Add(price.Price)
	}

	count := decimal.NewFromInt(int64(len(prices)))
	return sum.Div(count)
}
