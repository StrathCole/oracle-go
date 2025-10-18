package aggregator

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

// TVWAPAggregator implements Time-Volume Weighted Average Price aggregation
// Formula: TVWAP = Σ(price × volume × time_weight) / Σ(volume × time_weight)
// Time weight = e^(-age/decay_constant) to give more weight to recent trades
type TVWAPAggregator struct {
	logger        *logging.Logger
	windowSize    time.Duration // How far back to look (default: 3 minutes)
	decayConstant time.Duration // Decay constant for time weighting (default: 1 minute)

	// History tracking
	mu      sync.RWMutex
	history map[string][]PricePoint // symbol -> list of price points
}

// Ensure TVWAPAggregator implements Aggregator interface
var _ Aggregator = (*TVWAPAggregator)(nil)

// PricePoint represents a single price observation with volume and timestamp
type PricePoint struct {
	Price     decimal.Decimal
	Volume    decimal.Decimal
	Timestamp time.Time
	Source    string
}

// NewTVWAPAggregator creates a new TVWAP aggregator
func NewTVWAPAggregator(logger *logging.Logger) *TVWAPAggregator {
	return &TVWAPAggregator{
		logger:        logger,
		windowSize:    3 * time.Minute, // 3-minute rolling window
		decayConstant: 1 * time.Minute, // 1-minute decay constant
		history:       make(map[string][]PricePoint),
	}
}

// Aggregate implements the Aggregator interface using TVWAP
// Note: weights are not used in TVWAP as it relies on volume and time weighting
func (a *TVWAPAggregator) Aggregate(sourcePrices map[string]map[string]sources.Price, sourceWeights map[string]float64) (map[string]sources.Price, error) {
	start := time.Now()
	defer func() {
		metrics.RecordAggregation("tvwap", time.Since(start))
	}()

	if len(sourcePrices) == 0 {
		return nil, fmt.Errorf("no source prices provided")
	}

	now := time.Now()

	// Add new prices to history
	a.mu.Lock()
	for sourceName, prices := range sourcePrices {
		for originalSymbol, price := range prices {
			// Normalize symbol (USDT/USDC/etc -> USD)
			normalizedSymbol := sources.NormalizeSymbol(originalSymbol)

			// Add to history
			if a.history[normalizedSymbol] == nil {
				a.history[normalizedSymbol] = make([]PricePoint, 0)
			}

			a.history[normalizedSymbol] = append(a.history[normalizedSymbol], PricePoint{
				Price:     price.Price,
				Volume:    price.Volume,
				Timestamp: price.Timestamp,
				Source:    sourceName,
			})
		}
	}

	// Clean up old history (older than window size)
	for symbol := range a.history {
		a.history[symbol] = a.filterOldPoints(a.history[symbol], now)
	}
	a.mu.Unlock()

	// Compute TVWAP for each symbol
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]sources.Price)

	for symbol, points := range a.history {
		if len(points) == 0 {
			continue
		}

		tvwap, err := a.computeTVWAP(points, now)
		if err != nil {
			a.logger.Warn("Failed to compute TVWAP",
				"symbol", symbol,
				"error", err,
				"points", len(points))
			continue
		}

		// Find most recent volume for the aggregated price
		latestVolume := decimal.Zero
		latestTime := time.Time{}
		for _, p := range points {
			if p.Timestamp.After(latestTime) {
				latestTime = p.Timestamp
				latestVolume = p.Volume
			}
		}

		result[symbol] = sources.Price{
			Symbol:    symbol,
			Price:     tvwap,
			Volume:    latestVolume,
			Timestamp: now,
			Source:    "tvwap",
		}

		a.logger.Debug("Computed TVWAP",
			"symbol", symbol,
			"price", tvwap.String(),
			"points", len(points))
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no valid TVWAP prices computed")
	}

	return result, nil
}

// computeTVWAP calculates the time-volume weighted average price
// TVWAP = Σ(price × volume × weight) / Σ(volume × weight)
// where weight = e^(-age/decay_constant)
func (a *TVWAPAggregator) computeTVWAP(points []PricePoint, now time.Time) (decimal.Decimal, error) {
	if len(points) == 0 {
		return decimal.Zero, fmt.Errorf("no price points available")
	}

	numerator := decimal.Zero   // Σ(price × volume × weight)
	denominator := decimal.Zero // Σ(volume × weight)

	for _, point := range points {
		// Skip points with zero volume
		if point.Volume.IsZero() {
			continue
		}

		// Calculate time weight using exponential decay
		// weight = e^(-age/decay_constant)
		age := now.Sub(point.Timestamp)
		weight := a.calculateTimeWeight(age)

		// Convert weight to decimal
		weightDecimal := decimal.NewFromFloat(weight)

		// price × volume × weight
		contribution := point.Price.Mul(point.Volume).Mul(weightDecimal)
		numerator = numerator.Add(contribution)

		// volume × weight
		volumeWeighted := point.Volume.Mul(weightDecimal)
		denominator = denominator.Add(volumeWeighted)
	}

	if denominator.IsZero() {
		// Fallback: no volume data, use simple weighted average by time
		return a.computeSimpleWeightedAverage(points, now)
	}

	return numerator.Div(denominator), nil
}

// calculateTimeWeight returns exponential decay weight based on age
// weight = e^(-age/decay_constant)
// Recent prices get weight closer to 1, older prices decay exponentially
func (a *TVWAPAggregator) calculateTimeWeight(age time.Duration) float64 {
	// Prevent negative ages
	if age < 0 {
		age = 0
	}

	// Calculate decay factor: -age/decay_constant
	decayFactor := -float64(age) / float64(a.decayConstant)

	// e^decayFactor using math.Exp
	// For age=0: weight=1.0 (full weight)
	// For age=decay_constant: weight≈0.368
	// For age=2*decay_constant: weight≈0.135
	// For age=3*decay_constant: weight≈0.05
	return math.Exp(decayFactor)
}

// computeSimpleWeightedAverage is a fallback when no volume data is available
// Uses only time weighting: Σ(price × weight) / Σ(weight)
func (a *TVWAPAggregator) computeSimpleWeightedAverage(points []PricePoint, now time.Time) (decimal.Decimal, error) {
	if len(points) == 0 {
		return decimal.Zero, fmt.Errorf("no price points available")
	}

	numerator := decimal.Zero
	denominator := decimal.Zero

	for _, point := range points {
		age := now.Sub(point.Timestamp)
		weight := a.calculateTimeWeight(age)
		weightDecimal := decimal.NewFromFloat(weight)

		numerator = numerator.Add(point.Price.Mul(weightDecimal))
		denominator = denominator.Add(weightDecimal)
	}

	if denominator.IsZero() {
		return decimal.Zero, fmt.Errorf("zero total weight")
	}

	return numerator.Div(denominator), nil
}

// filterOldPoints removes price points older than the window size
func (a *TVWAPAggregator) filterOldPoints(points []PricePoint, now time.Time) []PricePoint {
	cutoff := now.Add(-a.windowSize)

	// Filter in-place to avoid allocations
	n := 0
	for _, point := range points {
		if point.Timestamp.After(cutoff) {
			points[n] = point
			n++
		}
	}

	return points[:n]
}

// Name returns the aggregator name
func (a *TVWAPAggregator) Name() string {
	return "tvwap"
}

// GetWindowSize returns the current window size (for testing/monitoring)
func (a *TVWAPAggregator) GetWindowSize() time.Duration {
	return a.windowSize
}

// GetHistorySize returns the number of price points in history for a symbol
func (a *TVWAPAggregator) GetHistorySize(symbol string) int {
	a.mu.RLock()
	defer a.mu.RUnlock()

	normalizedSymbol := sources.NormalizeSymbol(symbol)
	return len(a.history[normalizedSymbol])
}
