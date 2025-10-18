package voter

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestDeviationCalculation(t *testing.T) {
	tests := []struct {
		name      string
		voted     string
		chain     string
		wantAbove bool // true if should be above 1%
	}{
		{
			name:      "0.034% difference (your example)",
			voted:     "0.00006350764098",
			chain:     "0.000063529551730832",
			wantAbove: false, // 0.034% is below 1%
		},
		{
			name:      "exactly 1% difference",
			voted:     "0.0000101",
			chain:     "0.00001",
			wantAbove: true, // 1% should trigger warning
		},
		{
			name:      "0.5% difference",
			voted:     "0.000010050",
			chain:     "0.00001",
			wantAbove: false, // 0.5% is below 1%
		},
		{
			name:      "2% difference",
			voted:     "0.0000102",
			chain:     "0.00001",
			wantAbove: true, // 2% is above 1%
		},
		{
			name:      "10% difference",
			voted:     "0.000011",
			chain:     "0.00001",
			wantAbove: true, // 10% is way above 1%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			voted, err := sdk.NewDecFromStr(tt.voted)
			if err != nil {
				t.Fatalf("Failed to parse voted: %v", err)
			}

			chain, err := sdk.NewDecFromStr(tt.chain)
			if err != nil {
				t.Fatalf("Failed to parse chain: %v", err)
			}

			// Calculate percentage difference
			// diff = (voted - chain) / chain * 100
			diff := voted.Sub(chain).Quo(chain).MulInt64(100)
			diffAbs := diff.Abs()
			diffPercent, _ := diffAbs.Float64()

			t.Logf("Voted: %s, Chain: %s, Diff: %.6f%%", tt.voted, tt.chain, diffPercent)

			// Compare against 1.0 since diff is already in percentage (multiplied by 100)
			isAbove := diffAbs.GTE(sdk.OneDec()) // >= 1%

			if isAbove != tt.wantAbove {
				t.Errorf("Expected above 1%%: %v, got: %v (difference: %.6f%%)",
					tt.wantAbove, isAbove, diffPercent)
			}
		})
	}
}
