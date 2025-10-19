// Package oracle provides oracle aggregator price sources (e.g., Band Protocol).
package oracle

import (
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

func init() {
	// Register all oracle aggregator sources
	sources.Register("oracle.band", NewBandProtocolSource)
}
