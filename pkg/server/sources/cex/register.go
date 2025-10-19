// Package cex provides centralized exchange price sources.
package cex

import (
	"tc.com/oracle-prices/pkg/server/sources"
)

func init() {
	// Register all CEX sources
	sources.Register("cex.coingecko", NewCoinGeckoSource)
	sources.Register("cex.binance", NewBinanceSource)
	sources.Register("cex.bitfinex", NewBitfinexSource)
	sources.Register("cex.bybit", NewBybitSource)
	sources.Register("cex.gateio", NewGateioSource)
	sources.Register("cex.okx", NewOKXSource)
	sources.Register("cex.coinmarketcap", NewCoinMarketCapSource)
	sources.Register("cex.huobi", NewHuobiSource)
	sources.Register("cex.kraken", NewKrakenSource)
	sources.Register("cex.kucoin", NewKucoinSource)
	sources.Register("cex.mexc", NewMEXCSource)
}
