// Package evm provides EVM-based price sources (e.g., Uniswap).
package evm

import "errors"

var (
	// ErrRPCURLRequired indicates that rpc_url configuration is required.
	ErrRPCURLRequired = errors.New("rpc_url is required")
	// ErrChainIDRequired indicates that chain_id configuration is required.
	ErrChainIDRequired = errors.New("chain_id is required")
	// ErrPairsConfigRequired indicates that pairs configuration is required.
	ErrPairsConfigRequired = errors.New("pairs configuration is required")
)
