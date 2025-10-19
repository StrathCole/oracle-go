// Package sources provides price source interfaces and implementations.
package sources

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
)

const (
	// DefaultMaxRetries is the default maximum number of retries for failed requests.
	DefaultMaxRetries = 5
	// DefaultInitialBackoff is the default initial backoff duration for retries.
	DefaultInitialBackoff = 1 * time.Second
	// DefaultMaxBackoff is the default maximum backoff duration for retries.
	DefaultMaxBackoff = 2 * time.Minute
	// DefaultBackoffFactor is the default backoff multiplier factor.
	DefaultBackoffFactor = 2.0
)

// RetryConfig holds retry configuration for sources.
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     DefaultMaxRetries,
		InitialBackoff: DefaultInitialBackoff,
		MaxBackoff:     DefaultMaxBackoff,
		BackoffFactor:  DefaultBackoffFactor,
	}
}

// BaseSource provides common functionality for all price sources.
type BaseSource struct {
	name             string
	sourcetype       SourceType
	symbols          []string
	pairs            map[string]string // unified symbol -> source-specific symbol mapping
	prices           map[string]Price
	pricesMu         sync.RWMutex
	lastUpdate       time.Time
	updateMu         sync.RWMutex
	healthy          bool
	healthMu         sync.RWMutex
	subscribers      []chan<- PriceUpdate
	subscribersMu    sync.RWMutex
	stopChan         chan struct{}
	logger           *logging.Logger
	retryConfig      RetryConfig
	consecutiveFails int
	failsMu          sync.Mutex
}

// NewBaseSource creates a new base source with pair mappings
// pairs: map of unified symbol (e.g., "LUNC/USDT") -> source-specific symbol (e.g., "LUNCUSDT")
func NewBaseSource(name string, sourcetype SourceType, pairs map[string]string, logger *logging.Logger) *BaseSource {
	// Extract unified symbols from pair keys
	symbols := make([]string, 0, len(pairs))
	for unifiedSymbol := range pairs {
		symbols = append(symbols, unifiedSymbol)
	}

	return &BaseSource{
		name:             name,
		sourcetype:       sourcetype,
		symbols:          symbols,
		pairs:            pairs,
		prices:           make(map[string]Price),
		subscribers:      make([]chan<- PriceUpdate, 0),
		stopChan:         make(chan struct{}),
		logger:           logger,
		healthy:          false,
		retryConfig:      DefaultRetryConfig(),
		consecutiveFails: 0,
	}
}

// Name returns the source name.
func (b *BaseSource) Name() string {
	return b.name
}

// Type returns the source type.
func (b *BaseSource) Type() SourceType {
	return b.sourcetype
}

// Symbols returns the symbols this source provides.
func (b *BaseSource) Symbols() []string {
	return b.symbols
}

// IsHealthy returns the health status.
func (b *BaseSource) IsHealthy() bool {
	b.healthMu.RLock()
	defer b.healthMu.RUnlock()
	return b.healthy
}

// SetHealthy sets the health status.
func (b *BaseSource) SetHealthy(healthy bool) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	b.healthy = healthy

	// Record health metric for monitoring
	metrics.RecordSourceHealth(b.name, string(b.sourcetype), healthy)
}

// LastUpdate returns the time of the last successful price update.
func (b *BaseSource) LastUpdate() time.Time {
	b.updateMu.RLock()
	defer b.updateMu.RUnlock()
	return b.lastUpdate
}

// SetLastUpdate sets the last update time.
func (b *BaseSource) SetLastUpdate(t time.Time) {
	b.updateMu.Lock()
	defer b.updateMu.Unlock()
	b.lastUpdate = t
}

// GetPrice returns a single price by symbol.
func (b *BaseSource) GetPrice(symbol string) (Price, bool) {
	b.pricesMu.RLock()
	defer b.pricesMu.RUnlock()
	price, ok := b.prices[symbol]
	return price, ok
}

// SetPrice updates the price for a symbol.
// Automatically normalizes inverted symbols (e.g., "USD/LUNC" -> "LUNC/USD")
// to ensure consistent symbol format across all sources.
func (b *BaseSource) SetPrice(symbol string, price decimal.Decimal, timestamp time.Time) {
	// Normalize symbol to ensure USD/USDT/USDC is always on the right side
	normalizedSymbol, normalizedPrice := normalizeSymbolPair(symbol, price)

	// Log if normalization occurred
	if symbol != normalizedSymbol && b.logger != nil {
		b.logger.Debug("Normalized inverted symbol pair",
			"source", b.name,
			"original_symbol", symbol,
			"normalized_symbol", normalizedSymbol,
			"original_price", price.String(),
			"normalized_price", normalizedPrice.String())
	}

	b.pricesMu.Lock()
	defer b.pricesMu.Unlock()

	b.prices[normalizedSymbol] = Price{
		Symbol:    normalizedSymbol,
		Price:     normalizedPrice,
		Timestamp: timestamp,
		Source:    b.name,
	}

	// Record metrics with normalized symbol
	metrics.RecordSourceUpdate(b.name, string(b.sourcetype))

	// Notify subscribers
	b.notifySubscribers(PriceUpdate{
		Source: b.name,
		Prices: map[string]Price{
			normalizedSymbol: {
				Symbol:    normalizedSymbol,
				Price:     normalizedPrice,
				Timestamp: timestamp,
				Source:    b.name,
			},
		},
		Error: nil,
	})
}

// GetAllPrices returns all prices.
func (b *BaseSource) GetAllPrices() map[string]Price {
	b.pricesMu.RLock()
	defer b.pricesMu.RUnlock()

	// Create a copy to avoid race conditions
	prices := make(map[string]Price, len(b.prices))
	for k, v := range b.prices {
		prices[k] = v
	}
	return prices
}

// AddSubscriber adds a price update subscriber.
func (b *BaseSource) AddSubscriber(ch chan<- PriceUpdate) {
	b.subscribersMu.Lock()
	defer b.subscribersMu.Unlock()
	b.subscribers = append(b.subscribers, ch)
}

// RemoveSubscriber removes a price update subscriber.
func (b *BaseSource) RemoveSubscriber(ch chan<- PriceUpdate) {
	b.subscribersMu.Lock()
	defer b.subscribersMu.Unlock()

	for i, subscriber := range b.subscribers {
		if subscriber == ch {
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			break
		}
	}
}

// notifySubscribers sends price updates to all subscribers.
func (b *BaseSource) notifySubscribers(update PriceUpdate) {
	b.subscribersMu.RLock()
	defer b.subscribersMu.RUnlock()

	for _, ch := range b.subscribers {
		select {
		case ch <- update:
		default:
			// Channel full, skip
			b.logger.Warn("Subscriber channel full, skipping update",
				"source", b.name)
		}
	}
}

// StopChan returns the stop channel.
func (b *BaseSource) StopChan() <-chan struct{} {
	return b.stopChan
}

// Close closes the stop channel.
func (b *BaseSource) Close() {
	select {
	case <-b.stopChan:
		// Already closed
	default:
		close(b.stopChan)
	}
}

// Logger returns the logger.
func (b *BaseSource) Logger() *logging.Logger {
	return b.logger
}

// GetSourceSymbol converts unified symbol to source-specific symbol.
// Returns empty string if not found.
func (b *BaseSource) GetSourceSymbol(unifiedSymbol string) string {
	return b.pairs[unifiedSymbol]
}

// GetUnifiedSymbol finds the unified symbol for a source-specific symbol.
// Returns empty string if not found.
func (b *BaseSource) GetUnifiedSymbol(sourceSymbol string) string {
	for unified, source := range b.pairs {
		if source == sourceSymbol {
			return unified
		}
	}
	return ""
}

// GetAllPairs returns a copy of the pair mappings.
func (b *BaseSource) GetAllPairs() map[string]string {
	pairs := make(map[string]string, len(b.pairs))
	for k, v := range b.pairs {
		pairs[k] = v
	}
	return pairs
}

// ParsePairsConfig extracts pair mappings from config.
// Expects config["pairs"] to be map[string]interface{} where values are strings.
func ParsePairsConfig(config map[string]interface{}) (map[string]string, error) {
	pairsRaw, ok := config["pairs"]
	if !ok {
		return make(map[string]string), nil // No pairs configured, return empty map
	}

	pairsMap, ok := pairsRaw.(map[string]interface{})
	if !ok {
		// Try to interpret as a simpler format
		return make(map[string]string), nil
	}

	pairs := make(map[string]string, len(pairsMap))
	for unified, sourceRaw := range pairsMap {
		source, ok := sourceRaw.(string)
		if !ok {
			continue // Skip non-string values
		}
		pairs[unified] = source
	}

	return pairs, nil
}

// SetRetryConfig sets custom retry configuration.
func (b *BaseSource) SetRetryConfig(config RetryConfig) {
	b.retryConfig = config
}

// GetRetryConfig returns the current retry configuration.
func (b *BaseSource) GetRetryConfig() RetryConfig {
	return b.retryConfig
}

// RecordSuccess resets the consecutive failure counter.
func (b *BaseSource) RecordSuccess() {
	b.failsMu.Lock()
	defer b.failsMu.Unlock()
	b.consecutiveFails = 0
}

// RecordFailure increments the consecutive failure counter.
func (b *BaseSource) RecordFailure() {
	b.failsMu.Lock()
	defer b.failsMu.Unlock()
	b.consecutiveFails++
}

// GetConsecutiveFailures returns the number of consecutive failures.
func (b *BaseSource) GetConsecutiveFailures() int {
	b.failsMu.Lock()
	defer b.failsMu.Unlock()
	return b.consecutiveFails
}

// CalculateBackoff calculates the backoff duration for the current attempt.
// Uses exponential backoff with jitter.
func (b *BaseSource) CalculateBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// Calculate exponential backoff: initialBackoff * (factor ^ attempt)
	backoff := float64(b.retryConfig.InitialBackoff) * math.Pow(b.retryConfig.BackoffFactor, float64(attempt-1))

	// Cap at max backoff
	if backoff > float64(b.retryConfig.MaxBackoff) {
		backoff = float64(b.retryConfig.MaxBackoff)
	}

	duration := time.Duration(backoff)

	// Add jitter (±10%) to prevent thundering herd
	jitter := time.Duration(float64(duration) * 0.1 * (2.0*float64(time.Now().UnixNano()%100)/100.0 - 1.0))
	duration += jitter

	return duration
}

// RetryWithBackoff executes a function with exponential backoff retry logic.
// Returns the error from the last attempt if all retries fail.
func (b *BaseSource) RetryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	var lastErr error

	for attempt := 1; attempt <= b.retryConfig.MaxRetries; attempt++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during %s: %w", operation, ctx.Err())
		case <-b.stopChan:
			return fmt.Errorf("%w: %s", ErrSourceStopped, operation)
		default:
		}

		// Execute the operation
		err := fn()
		if err == nil {
			// Success
			if attempt > 1 {
				b.logger.Info("Operation succeeded after retry",
					"operation", operation,
					"attempt", attempt,
					"source", b.name)
			}
			b.RecordSuccess()
			return nil
		}

		lastErr = err
		b.RecordFailure()

		// Last attempt, don't sleep
		if attempt == b.retryConfig.MaxRetries {
			b.logger.Error("Operation failed after all retries",
				"operation", operation,
				"attempts", attempt,
				"source", b.name,
				"error", err)
			break
		}

		// Calculate backoff and sleep
		backoff := b.CalculateBackoff(attempt)
		b.logger.Warn("Operation failed, retrying with backoff",
			"operation", operation,
			"attempt", attempt,
			"max_attempts", b.retryConfig.MaxRetries,
			"backoff", backoff,
			"source", b.name,
			"error", err)

		select {
		case <-time.After(backoff):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
		case <-b.stopChan:
			return fmt.Errorf("%w: backoff", ErrSourceStopped)
		}
	}

	return fmt.Errorf("operation %s failed after %d attempts: %w", operation, b.retryConfig.MaxRetries, lastErr)
}

// normalizeSymbolPair ensures the symbol is in "ASSET/USD" or "ASSET/USDT" format.
// If the symbol has USD/USDT/USDC as the BASE (e.g., "USD/KRW", "USDT/LUNC", "USDC/LUNC"),
// it swaps the pair and inverts the price to get the correct "ASSET/USD" format.
//
// Examples:
// - "LUNC/USD", price=0.00004122 -> "LUNC/USD", 0.00004122 (no change)
// - "USDC/LUNC", price=24000 -> "LUNC/USDC", 0.0000417 (inverted)
// - "USD/KRW", price=1300 -> "KRW/USD", 0.00077 (inverted)
// - "USDT/BTC", price=0.00002 -> "BTC/USDT", 50000 (inverted)
// - "EUR/USD", price=1.08 -> "EUR/USD", 1.08 (no change - EUR is the asset).
func normalizeSymbolPair(symbol string, price decimal.Decimal) (string, decimal.Decimal) {
	// Convert to uppercase for processing
	upper := strings.ToUpper(symbol)

	// Split symbol into parts
	parts := strings.Split(upper, "/")
	if len(parts) != 2 {
		// No slash, assume it's already in correct format
		return symbol, price
	}

	base := parts[0]
	quote := parts[1]

	// Check if base is USD/USDT/USDC (the quote currency we want on the right side)
	// If so, the symbol is inverted and needs to be swapped
	isInverted := (base == "USD" || base == "USDT" || base == "USDC")

	if isInverted {
		// Swap base and quote: "USD/KRW" becomes "KRW/USD"
		normalizedSymbol := quote + "/" + base

		// Invert price (avoid division by zero)
		if price.IsZero() {
			return normalizedSymbol, price
		}
		normalizedPrice := decimal.NewFromInt(1).Div(price)

		return normalizedSymbol, normalizedPrice
	}

	// Already in correct format (ASSET/USD or ASSET/USDT)
	return symbol, price
}
