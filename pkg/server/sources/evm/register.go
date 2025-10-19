// Package evm provides EVM-based price sources (e.g., Uniswap).
package evm

import (
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

func init() {
	// Register all EVM sources
	sources.Register("evm.pancakeswap_bsc", NewPancakeSwapSource)
}
