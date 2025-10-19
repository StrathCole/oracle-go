// Package eventstream provides event stream handling for the oracle feeder.
package eventstream

import "errors"

// ErrNoConnection indicates that there is no active WebSocket connection.
var ErrNoConnection = errors.New("no connection")
