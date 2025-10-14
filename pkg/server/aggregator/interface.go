package aggregator

import (
	"fmt"

	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

// Aggregator defines the interface for price aggregation strategies
type Aggregator interface {
	// Aggregate computes aggregated prices from multiple sources
	Aggregate(sourcePrices map[string]map[string]sources.Price) (map[string]sources.Price, error)
}

// NewAggregator creates an aggregator based on the specified mode
func NewAggregator(mode string, logger *logging.Logger) (Aggregator, error) {
	switch mode {
	case "median":
		return NewMedianAggregator(logger), nil
	case "average":
		return NewAverageAggregator(logger), nil
	case "tvwap":
		return NewTVWAPAggregator(logger), nil
	default:
		return nil, fmt.Errorf("unknown aggregation mode: %s (supported: median, average, tvwap)", mode)
	}
}
