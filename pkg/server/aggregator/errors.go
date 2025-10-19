// Package aggregator provides price aggregation strategies.
package aggregator

import "errors"

var (
	// ErrNoSourcePrices indicates that no source prices were provided.
	ErrNoSourcePrices = errors.New("no source prices provided")
	// ErrNoPricesComputed indicates that no prices were computed.
	ErrNoPricesComputed = errors.New("no prices computed")
	// ErrNoPricesForSymbol indicates that no prices are available for the symbol.
	ErrNoPricesForSymbol = errors.New("no prices for symbol")
	// ErrUnknownMode indicates that the aggregation mode is unknown.
	ErrUnknownMode = errors.New("unknown aggregation mode")
)
