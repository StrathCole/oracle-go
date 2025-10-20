// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"fmt"

	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

const (
	// ModeMedian uses weighted median aggregation with outlier rejection.
	ModeMedian = "median"
	// ModeAverage uses weighted average aggregation.
	ModeAverage = "average"
	// ModeAdaptive uses adaptive threshold filtering with configurable sensitivity.
	ModeAdaptive = "adaptive"
)

// Aggregator defines the interface for price aggregation strategies.
type Aggregator interface {
	// Aggregate computes aggregated prices from multiple sources
	// sourceWeights maps source names to their weights (1.0 = standard, 0.5 = half weight)
	Aggregate(sourcePrices map[string]map[string]sources.Price, sourceWeights map[string]float64) (map[string]sources.Price, error)
}

// AdaptiveConfig holds configuration for adaptive aggregator.
type AdaptiveConfig struct {
	Sensitivity float64 // k constant (1.5 = strict, 2.0 = tolerant)
	FinalMode   string  // "median" or "average" for final aggregation
}

// NewAggregator creates an aggregator based on the specified mode.
func NewAggregator(mode string, logger *logging.Logger) (Aggregator, error) {
	return NewAggregatorWithConfig(mode, logger, nil)
}

// NewAggregatorWithConfig creates an aggregator with optional configuration.
func NewAggregatorWithConfig(mode string, logger *logging.Logger, adaptiveConfig *AdaptiveConfig) (Aggregator, error) {
	switch mode {
	case ModeMedian:
		return NewMedianAggregator(logger), nil
	case ModeAverage:
		return NewAverageAggregator(logger), nil
	case ModeAdaptive:
		var sensitivity float64
		var finalMode string
		if adaptiveConfig != nil {
			sensitivity = adaptiveConfig.Sensitivity
			finalMode = adaptiveConfig.FinalMode
		}
		return NewAdaptiveAggregator(logger, sensitivity, finalMode), nil
	default:
		return nil, fmt.Errorf("%w: %s (supported: median, average, adaptive)", ErrUnknownMode, mode)
	}
}
