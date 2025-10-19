// Package price provides price client functionality.
package price

import "errors"

var (
	// ErrPriceServerHTTPError indicates that the price server returned an HTTP error.
	ErrPriceServerHTTPError = errors.New("price server returned HTTP error")
)
