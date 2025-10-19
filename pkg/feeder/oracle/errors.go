// Package oracle provides oracle-specific utilities and message building.
package oracle

import "errors"

var (
	// ErrNoPricesProvided indicates that no prices were provided.
	ErrNoPricesProvided = errors.New("no prices provided")
	// ErrEmptyRates indicates that the exchange rates string is empty.
	ErrEmptyRates = errors.New("empty exchange rates string")
)
