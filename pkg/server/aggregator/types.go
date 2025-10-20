// Package aggregator provides price aggregation strategies.
package aggregator

import (
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// priceWithSource tracks which source provided a price and its weight.
type priceWithSource struct {
	price  sources.Price
	source string
	weight float64 // Weight for aggregation (1.0 = standard, 0.5 = half weight, etc.)
}
