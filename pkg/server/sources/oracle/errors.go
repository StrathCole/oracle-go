// Package oracle provides oracle aggregator price sources (e.g., Band Protocol).
package oracle

import "errors"

var (
	// ErrSymbolsMustBeArray indicates that symbols configuration must be an array.
	ErrSymbolsMustBeArray = errors.New("symbols must be an array")
	// ErrSymbolsListRequired indicates that a symbols list is required.
	ErrSymbolsListRequired = errors.New("symbols list is required")
	// ErrLoggerNotProvided indicates that a logger was not provided in config.
	ErrLoggerNotProvided = errors.New("logger not provided in config")
	// ErrNoValidSymbolsToFetch indicates that no valid symbols are available to fetch.
	ErrNoValidSymbolsToFetch = errors.New("no valid symbols to fetch")
	// ErrInvalidResponseNoPrices indicates that the response contains no price results.
	ErrInvalidResponseNoPrices = errors.New("invalid response: no price results")
	// ErrMultiplierIsZero indicates that the multiplier is zero.
	ErrMultiplierIsZero = errors.New("multiplier is zero")
)
