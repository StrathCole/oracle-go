package sources

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// SourceType represents the type of price source
type SourceType string

const (
	SourceTypeCEX      SourceType = "cex"
	SourceTypeCosmWasm SourceType = "cosmwasm"
	SourceTypeEVM      SourceType = "evm"
	SourceTypeOracle   SourceType = "oracle"
	SourceTypeFiat     SourceType = "fiat"
	SourceTypeSDR      SourceType = "sdr"
)

// Price represents a price for a symbol at a specific time
type Price struct {
	Symbol    string          `json:"symbol"`
	Price     decimal.Decimal `json:"price"`
	Timestamp time.Time       `json:"timestamp"`
	Volume    decimal.Decimal `json:"volume,omitempty"`
	Source    string          `json:"source"`
}

// PriceUpdate represents a price update event
type PriceUpdate struct {
	Source string
	Prices map[string]Price
	Error  error
}

// Source defines the interface that all price sources must implement
type Source interface {
	// Initialize prepares the source for operation
	Initialize(ctx context.Context) error

	// Start begins fetching prices
	Start(ctx context.Context) error

	// Stop halts the source and cleans up resources
	Stop() error

	// GetPrices returns the current prices for all symbols
	GetPrices(ctx context.Context) (map[string]Price, error)

	// Subscribe allows other components to receive price updates
	Subscribe(updates chan<- PriceUpdate) error

	// Name returns the unique name of this source
	Name() string

	// Type returns the type of this source
	Type() SourceType

	// Symbols returns the list of symbols this source provides
	Symbols() []string

	// IsHealthy returns whether the source is currently healthy
	IsHealthy() bool

	// LastUpdate returns the timestamp of the last successful update
	LastUpdate() time.Time
}

// SourceFactory is a function that creates a new Source instance
type SourceFactory func(config map[string]interface{}) (Source, error)
