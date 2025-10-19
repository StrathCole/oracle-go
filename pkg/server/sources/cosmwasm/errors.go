// Package cosmwasm provides CosmWasm-based price sources.
package cosmwasm

import "errors"

var (
	// ErrNoValidPairs indicates that no valid pairs are configured.
	ErrNoValidPairs = errors.New("no valid pairs configured")
	// ErrNoPricesAvailable indicates that no prices are available.
	ErrNoPricesAvailable = errors.New("no prices available")
	// ErrNoPoolPrices indicates that pool prices could not be fetched.
	ErrNoPoolPrices = errors.New("failed to fetch any pair prices")
	// ErrInvalidPoolResponse indicates an invalid pool response.
	ErrInvalidPoolResponse = errors.New("invalid pool response")
	// ErrZeroLiquidity indicates zero liquidity in a pool.
	ErrZeroLiquidity = errors.New("zero liquidity in pool")
	// ErrGRPCNotInitialized indicates that the gRPC client is not initialized.
	ErrGRPCNotInitialized = errors.New("gRPC client not initialized for CosmWasm sources")
)
