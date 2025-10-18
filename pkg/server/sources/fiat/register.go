package fiat

import (
	"tc.com/oracle-prices/pkg/server/sources"
)

func init() {
	// Register all fiat sources
	sources.Register("fiat.frankfurter", NewFrankfurterSourceFromConfig)
	sources.Register("fiat.imf", NewIMFSourceFromConfig)
	sources.Register("fiat.fixer", NewFixerSourceFromConfig)
	sources.Register("fiat.sdr", NewSDRSourceFromConfig)
	sources.Register("fiat.exchangerate", NewExchangeRateSourceFromConfig)
	sources.Register("fiat.exchangerate_free", NewExchangeRateFreeSourceFromConfig)
}
