// Package oracle provides oracle aggregator price sources (e.g., Band Protocol).
package oracle

import (
	"tc.com/oracle-prices/pkg/server/sources"
)

func init() {
	// Register all oracle aggregator sources
	sources.Register("oracle.band", NewBandProtocolSource)
}
