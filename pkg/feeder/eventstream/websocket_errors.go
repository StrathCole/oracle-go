// Package eventstream provides event stream handling for the oracle feeder.
package eventstream

import "errors"

var (
	// ErrNoConnection indicates that there is no active WebSocket connection.
	ErrNoConnection = errors.New("no connection")
)
