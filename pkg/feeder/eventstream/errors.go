// Package eventstream provides event stream handling for the oracle feeder.
package eventstream

import "errors"

// ErrNoRPCEndpointRequired indicates that at least one RPC endpoint is required.
var ErrNoRPCEndpointRequired = errors.New("at least one RPC endpoint required")
