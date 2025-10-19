// Package tx provides transaction building and broadcasting functionality.
package tx

import "errors"

var (
	// ErrTransactionRejected indicates that the transaction was rejected.
	ErrTransactionRejected = errors.New("transaction rejected")
	// ErrInvalidParameter indicates that an invalid parameter was provided.
	ErrInvalidParameter = errors.New("invalid parameter")
)
