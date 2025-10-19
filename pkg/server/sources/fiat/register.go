// Package fiat provides fiat currency and SDR price sources.
package fiat

import (
	"github.com/StrathCole/oracle-go/pkg/server/sources"
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
