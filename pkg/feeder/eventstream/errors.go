// Package eventstream provides event stream handling for the oracle feeder.
package eventstream

import "errors"

// ErrNoRPCEndpointRequired indicates that at least one RPC endpoint is required.
var ErrNoRPCEndpointRequired = errors.New("at least one RPC endpoint required")

// ErrInvalidVotePeriod indicates that the vote period is invalid.
var ErrInvalidVotePeriod = errors.New("invalid vote period: cannot be zero")
