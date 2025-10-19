// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"fmt"

	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// Aggregator defines the interface for price aggregation strategies.
type Aggregator interface {
	// Aggregate computes aggregated prices from multiple sources
	// sourceWeights maps source names to their weights (1.0 = standard, 0.5 = half weight)
	Aggregate(sourcePrices map[string]map[string]sources.Price, sourceWeights map[string]float64) (map[string]sources.Price, error)
}

// NewAggregator creates an aggregator based on the specified mode.
func NewAggregator(mode string, logger *logging.Logger) (Aggregator, error) {
	switch mode {
	case "median":
		return NewMedianAggregator(logger), nil
	case "average":
		return NewAverageAggregator(logger), nil
	default:
		return nil, fmt.Errorf("%w: %s (supported: median, average)", ErrUnknownMode, mode)
	}
}
