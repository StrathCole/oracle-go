// Package voter provides oracle voting functionality.
package voter

import "errors"

// Voter errors.
var (
	ErrVoteHashMismatch       = errors.New("vote hash mismatch")
	ErrEmptyExchangeRates     = errors.New("exchange rates cannot be empty")
	ErrNoExchangeRatePairs    = errors.New("no exchange rate pairs found")
	ErrEmptyExchangeRatePair  = errors.New("empty exchange rate pair")
	ErrInvalidPairFormat      = errors.New("invalid pair format")
	ErrInvalidRateInPair      = errors.New("invalid rate in pair")
	ErrMissingDenomInPair     = errors.New("missing denom in pair")
	ErrWhitelistNotLoaded     = errors.New("oracle whitelist not yet loaded")
	ErrNoWhitelistedPrices    = errors.New("no whitelisted prices available")
	ErrTxFailed               = errors.New("transaction failed")
)
