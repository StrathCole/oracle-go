// Package eventstream provides event stream handling for the oracle feeder.
package eventstream

import "errors"

var (
	// ErrNoRPCEndpointRequired indicates that at least one RPC endpoint is required.
	ErrNoRPCEndpointRequired = errors.New("at least one RPC endpoint required")
)
