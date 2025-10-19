// Package client provides gRPC client functionality with failover support.
package client

import "errors"

var (
	// ErrNoEndpointsRequired indicates that at least one gRPC endpoint is required.
	ErrNoEndpointsRequired = errors.New("at least one gRPC endpoint is required")
	// ErrAllAttemptsFailedGRPC indicates that all attempts failed across gRPC endpoints.
	ErrAllAttemptsFailedGRPC = errors.New("all attempts failed across gRPC endpoints")
)
