package fiat

import (
	"tc.com/oracle-prices/pkg/server/sources"
)

func init() {
	// Register all fiat sources
	sources.Register("fiat.frankfurter", NewFrankfurterSource)
	sources.Register("fiat.fer", NewFERSource)
	sources.Register("fiat.imf", NewIMFSource)
	sources.Register("fiat.sdr", NewSDRSource)
	sources.Register("fiat.exchangerate", NewExchangeRateSource)
	sources.Register("fiat.fixer", NewFixerSource)
}
