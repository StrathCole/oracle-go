// Package websocket provides WebSocket client functionality for price sources.
package websocket

import "errors"

var (
	// ErrMaxRetriesExceeded indicates that the maximum connection retries have been exceeded.
	ErrMaxRetriesExceeded = errors.New("max connection retries exceeded")
	// ErrNotConnected indicates that the client is not connected.
	ErrNotConnected = errors.New("not connected")
	// ErrSendTimeout indicates that a send operation timed out.
	ErrSendTimeout = errors.New("send timeout")
	// ErrConnectionLost indicates that the connection was lost.
	ErrConnectionLost = errors.New("connection lost")
)
