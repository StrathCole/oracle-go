package evm

import (
	"tc.com/oracle-prices/pkg/server/sources"
)

func init() {
	// Register all EVM sources
	sources.Register("evm.pancakeswap_bsc", NewPancakeSwapSource)
}
