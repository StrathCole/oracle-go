package sources

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
)

// BaseSource provides common functionality for all price sources
type BaseSource struct {
	name          string
	sourcetype    SourceType
	symbols       []string
	pairs         map[string]string // unified symbol -> source-specific symbol mapping
	prices        map[string]Price
	pricesMu      sync.RWMutex
	lastUpdate    time.Time
	updateMu      sync.RWMutex
	healthy       bool
	healthMu      sync.RWMutex
	subscribers   []chan<- PriceUpdate
	subscribersMu sync.RWMutex
	stopChan      chan struct{}
	logger        *logging.Logger
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
		name:        name,
		sourcetype:  sourcetype,
		symbols:     symbols,
		pairs:       pairs,
		prices:      make(map[string]Price),
		subscribers: make([]chan<- PriceUpdate, 0),
		stopChan:    make(chan struct{}),
		logger:      logger,
		healthy:     false,
	}
}

// Name returns the source name
func (b *BaseSource) Name() string {
	return b.name
}

// Type returns the source type
func (b *BaseSource) Type() SourceType {
	return b.sourcetype
}

// Symbols returns the symbols this source provides
func (b *BaseSource) Symbols() []string {
	return b.symbols
}

// IsHealthy returns the health status
func (b *BaseSource) IsHealthy() bool {
	b.healthMu.RLock()
	defer b.healthMu.RUnlock()
	return b.healthy
}

// SetHealthy sets the health status
func (b *BaseSource) SetHealthy(healthy bool) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	b.healthy = healthy
}

// LastUpdate returns the time of the last successful price update
func (b *BaseSource) LastUpdate() time.Time {
	b.updateMu.RLock()
	defer b.updateMu.RUnlock()
	return b.lastUpdate
}

// SetLastUpdate sets the last update time
func (b *BaseSource) SetLastUpdate(t time.Time) {
	b.updateMu.Lock()
	defer b.updateMu.Unlock()
	b.lastUpdate = t
}

// GetPrice returns a single price by symbol
func (b *BaseSource) GetPrice(symbol string) (Price, bool) {
	b.pricesMu.RLock()
	defer b.pricesMu.RUnlock()
	price, ok := b.prices[symbol]
	return price, ok
}

// SetPrice sets a price for a symbol and notifies subscribers
func (b *BaseSource) SetPrice(symbol string, price decimal.Decimal, timestamp time.Time) {
	b.pricesMu.Lock()
	p := Price{
		Symbol:    symbol,
		Price:     price,
		Timestamp: timestamp,
		Source:    b.name,
	}
	b.prices[symbol] = p
	b.pricesMu.Unlock()

	// Notify subscribers
	pricesMap := map[string]Price{
		symbol: p,
	}
	b.notifySubscribers(PriceUpdate{
		Source: b.name,
		Prices: pricesMap,
	})
}

// GetAllPrices returns all prices
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

// AddSubscriber adds a price update subscriber
func (b *BaseSource) AddSubscriber(ch chan<- PriceUpdate) {
	b.subscribersMu.Lock()
	defer b.subscribersMu.Unlock()
	b.subscribers = append(b.subscribers, ch)
}

// RemoveSubscriber removes a price update subscriber
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

// notifySubscribers sends price updates to all subscribers
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

// StopChan returns the stop channel
func (b *BaseSource) StopChan() <-chan struct{} {
	return b.stopChan
}

// Close closes the stop channel
func (b *BaseSource) Close() {
	select {
	case <-b.stopChan:
		// Already closed
	default:
		close(b.stopChan)
	}
}

// Logger returns the logger
func (b *BaseSource) Logger() *logging.Logger {
	return b.logger
}

// GetSourceSymbol converts unified symbol to source-specific symbol
// Returns empty string if not found
func (b *BaseSource) GetSourceSymbol(unifiedSymbol string) string {
	return b.pairs[unifiedSymbol]
}

// GetUnifiedSymbol finds the unified symbol for a source-specific symbol
// Returns empty string if not found
func (b *BaseSource) GetUnifiedSymbol(sourceSymbol string) string {
	for unified, source := range b.pairs {
		if source == sourceSymbol {
			return unified
		}
	}
	return ""
}

// GetAllPairs returns a copy of the pair mappings
func (b *BaseSource) GetAllPairs() map[string]string {
	pairs := make(map[string]string, len(b.pairs))
	for k, v := range b.pairs {
		pairs[k] = v
	}
	return pairs
}

// ParsePairsConfig extracts pair mappings from config
// Expects config["pairs"] to be map[string]interface{} where values are strings
func ParsePairsConfig(config map[string]interface{}) (map[string]string, error) {
	pairsRaw, ok := config["pairs"]
	if !ok {
		return nil, nil // No pairs configured, return empty map
	}
	
	pairsMap, ok := pairsRaw.(map[string]interface{})
	if !ok {
		// Try to interpret as a simpler format
		return nil, nil
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
