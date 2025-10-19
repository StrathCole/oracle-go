package voter

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/StrathCole/oracle-go/pkg/feeder/price"
)

const (
	ustDenom = "UST"
)

// MockPriceClient mocks the price client interface.
type MockPriceClient struct {
	mock.Mock
}

func (m *MockPriceClient) GetPrices(ctx context.Context) ([]price.Price, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]price.Price), args.Error(1)
}

func TestSymbolToDenom(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected string
	}{
		{
			name:     "KRW with /USD suffix",
			symbol:   "KRW/USD",
			expected: "ukrw",
		},
		{
			name:     "KRW without suffix",
			symbol:   "KRW",
			expected: "ukrw",
		},
		{
			name:     "SDR with /USD suffix",
			symbol:   "SDR/USD",
			expected: "usdr",
		},
		{
			name:     "XDR (alternative SDR symbol)",
			symbol:   "XDR",
			expected: "usdr",
		},
		{
			name:     "USD",
			symbol:   "USD",
			expected: "uusd",
		},
		{
			name:     "MNT (Mongolian Tugrik)",
			symbol:   "MNT/USD",
			expected: "umnt",
		},
		{
			name:     "USTC meta-denom",
			symbol:   "USTC/USD",
			expected: "UST",
		},
		{
			name:     "UST meta-denom",
			symbol:   "UST",
			expected: "UST",
		},
		{
			name:     "USDT suffix removal",
			symbol:   "KRW/USDT",
			expected: "ukrw",
		},
		{
			name:     "Generic symbol",
			symbol:   "EUR/USD",
			expected: "ueur",
		},
		{
			name:     "Lowercase input",
			symbol:   "krw/usd",
			expected: "ukrw",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := symbolToDenom(tt.symbol)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToOraclePrices(t *testing.T) {
	// Mock voter with minimal setup
	v := &Voter{}

	tests := []struct {
		name      string
		prices    map[string]decimal.Decimal
		whitelist []string
		expected  int // number of expected prices after filtering
	}{
		{
			name: "Filter by whitelist",
			prices: map[string]decimal.Decimal{
				"LUNC/USD": decimal.NewFromFloat(0.0001),
				"KRW/USD":  decimal.NewFromFloat(0.00075),
				"USD":      decimal.NewFromFloat(1.0),
				"SDR/USD":  decimal.NewFromFloat(1.35),
				"BTC/USD":  decimal.NewFromFloat(50000.0), // Not in whitelist
			},
			whitelist: []string{"ukrw", "uusd", "usdr"},
			expected:  3,
		},
		{
			name: "Empty whitelist",
			prices: map[string]decimal.Decimal{
				"LUNC/USD": decimal.NewFromFloat(0.0001),
				"KRW/USD":  decimal.NewFromFloat(0.00075),
			},
			whitelist: []string{},
			expected:  0,
		},
		{
			name: "USTC meta-denom",
			prices: map[string]decimal.Decimal{
				"LUNC/USD": decimal.NewFromFloat(0.0001),
				"USTC/USD": decimal.NewFromFloat(0.02),
			},
			whitelist: []string{"UST"},
			expected:  1,
		},
		{
			name:      "Empty prices",
			prices:    map[string]decimal.Decimal{},
			whitelist: []string{"ukrw", "uusd"},
			expected:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := v.convertToOraclePrices(tt.prices, tt.whitelist)
			assert.Len(t, result, tt.expected)

			// Verify denoms are in whitelist
			whitelistSet := make(map[string]bool)
			for _, denom := range tt.whitelist {
				whitelistSet[denom] = true
			}

			for _, oraclePrice := range result {
				assert.True(t, whitelistSet[oraclePrice.Denom],
					"denom %s should be in whitelist", oraclePrice.Denom)
			}
		})
	}
}

func TestConvertToOraclePricesValues(t *testing.T) {
	v := &Voter{}
	luncUSD := 0.0001

	prices := map[string]decimal.Decimal{
		"LUNC/USD": decimal.NewFromFloat(luncUSD),
		"KRW/USD":  decimal.NewFromFloat(0.00075),
		"USD":      decimal.NewFromFloat(1.0),
	}
	whitelist := []string{"ukrw", "uusd"}

	result := v.convertToOraclePrices(prices, whitelist)
	require.Len(t, result, 2)

	// Find each denom and verify price
	for _, p := range result {
		switch p.Denom {
		case "ukrw":
			// KRW/USD = 0.00075, so USD/KRW = 1/0.00075 = 1333.33
			// KRW/LUNC = 1333.33 * 0.0001 = 0.1333
			expected := (1.0 / 0.00075) * luncUSD
			assert.InDelta(t, expected, p.Price, 0.001)
		case "uusd":
			// USD/LUNC = 1.0 * luncUSD = 0.0001
			expected := 1.0 * luncUSD
			assert.InDelta(t, expected, p.Price, 0.00001)
		default:
			t.Errorf("unexpected denom: %s", p.Denom)
		}
	}
}

func TestFetchPrices(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	mockPriceClient := new(MockPriceClient)
	v := &Voter{
		priceClient: mockPriceClient,
	}

	// Setup mock expectations
	expectedPrices := []price.Price{
		{Symbol: "BTC/USD", Price: decimal.NewFromFloat(50000.0)},
		{Symbol: "ETH/USD", Price: decimal.NewFromFloat(3000.0)},
	}

	mockPriceClient.On("GetPrices", mock.Anything).Return(expectedPrices, nil)

	// Test
	ctx := context.Background()
	result, err := v.fetchPrices(ctx)

	// Verify
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.True(t, result["BTC/USD"].Equal(decimal.NewFromFloat(50000.0)))
	assert.True(t, result["ETH/USD"].Equal(decimal.NewFromFloat(3000.0)))

	mockPriceClient.AssertExpectations(t)
}

func TestFetchPricesError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	mockPriceClient := new(MockPriceClient)
	v := &Voter{
		priceClient: mockPriceClient,
	}

	// Setup mock to return error
	mockPriceClient.On("GetPrices", mock.Anything).Return(nil, assert.AnError)

	// Test
	ctx := context.Background()
	result, err := v.fetchPrices(ctx)

	// Verify
	assert.Error(t, err)
	assert.Nil(t, result)

	mockPriceClient.AssertExpectations(t)
}

// TestVotingStateTransitions tests the state machine transitions.
func TestVotingStateTransitions(t *testing.T) {
	v := &Voter{
		state: StateIdle,
	}

	// Test state getters
	assert.Equal(t, StateIdle, v.GetState())

	// Test last vote period
	v.lastVotePeriod = 100
	assert.Equal(t, uint64(100), v.GetLastVotePeriod())
}

// TestWhitelistFiltering verifies that only whitelisted denoms are included.
func TestWhitelistFiltering(t *testing.T) {
	v := &Voter{}

	// Comprehensive price map with various denoms
	prices := map[string]decimal.Decimal{
		"LUNC/USD": decimal.NewFromFloat(0.0001),
		"KRW/USD":  decimal.NewFromFloat(0.00075),
		"USD":      decimal.NewFromFloat(1.0),
		"SDR/USD":  decimal.NewFromFloat(1.35),
		"MNT/USD":  decimal.NewFromFloat(0.00029),
		"USTC/USD": decimal.NewFromFloat(0.02),
		"BTC/USD":  decimal.NewFromFloat(50000.0),
		"ETH/USD":  decimal.NewFromFloat(3000.0),
		"EUR/USD":  decimal.NewFromFloat(1.08),
	}

	// Mainnet whitelist (from copilot-instructions.md)
	mainnetWhitelist := []string{"ukrw", "usdr", "uusd", "umnt", "UST"}

	result := v.convertToOraclePrices(prices, mainnetWhitelist)

	// Should have 5 prices matching whitelist
	assert.Len(t, result, 5)

	// Verify denoms
	denoms := make(map[string]bool)
	for _, p := range result {
		denoms[p.Denom] = true
	}

	assert.True(t, denoms["ukrw"], "ukrw should be present")
	assert.True(t, denoms["usdr"], "usdr should be present")
	assert.True(t, denoms["uusd"], "uusd should be present")
	assert.True(t, denoms["umnt"], "umnt should be present")
	assert.True(t, denoms["UST"], "UST should be present")
	assert.False(t, denoms["ubtc"], "ubtc should NOT be present (not whitelisted)")
	assert.False(t, denoms["ueth"], "ueth should NOT be present (not whitelisted)")
}

// TestCaseInsensitiveSymbolMapping verifies symbol mapping handles different cases.
func TestCaseInsensitiveSymbolMapping(t *testing.T) {
	tests := []struct {
		symbol   string
		expected string
	}{
		{"KRW/USD", "ukrw"},
		{"krw/usd", "ukrw"},
		{"Krw/Usd", "ukrw"},
		{"KRW", "ukrw"},
		{"USTC", "UST"},
		{"ustc", "UST"},
		{"Ustc", "UST"},
	}

	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			result := symbolToDenom(tt.symbol)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMetaDenomHandling tests special handling of UST meta-denom.
func TestMetaDenomHandling(t *testing.T) {
	v := &Voter{}

	prices := map[string]decimal.Decimal{
		"LUNC/USD": decimal.NewFromFloat(0.0001),
		"USTC/USD": decimal.NewFromFloat(0.02),
		"USD":      decimal.NewFromFloat(1.0),
	}

	whitelist := []string{"UST", "uusd"}

	result := v.convertToOraclePrices(prices, whitelist)
	require.Len(t, result, 2)

	// Find UST and verify it doesn't have 'u' prefix
	var ustFound bool
	for _, p := range result {
		if p.Denom == ustDenom {
			ustFound = true
			// UST should NOT start with 'u'
			assert.NotEqual(t, "uUST", p.Denom)
			assert.NotEqual(t, "uust", p.Denom)
			break
		}
	}

	assert.True(t, ustFound, "UST meta-denom should be present")
}

// TestPriceConversion verifies decimal to float64 conversion and LUNC price scaling.
func TestPriceConversion(t *testing.T) {
	v := &Voter{}
	luncUSD := 0.0001

	tests := []struct {
		name     string
		input    float64
		expected float64 // expected output after LUNC conversion
		delta    float64
	}{
		{
			name:     "Small price (KRW)",
			input:    0.00075,
			expected: (1.0 / 0.00075) * luncUSD, // (1 / input) * luncUSD
			delta:    0.01,
		},
		{
			name:     "Large price (BTC)",
			input:    50000.12345,
			expected: (1.0 / 50000.12345) * luncUSD,
			delta:    0.0000001,
		},
		{
			name:     "SDR price",
			input:    1.35958,
			expected: (1.0 / 1.35958) * luncUSD,
			delta:    0.00001,
		},
		{
			name:     "Exact 1.0",
			input:    1.0,
			expected: 1.0 * luncUSD, // 1.0 * 0.0001 = 0.0001
			delta:    0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prices := map[string]decimal.Decimal{
				"LUNC/USD": decimal.NewFromFloat(luncUSD),
				"TEST/USD": decimal.NewFromFloat(tt.input),
			}
			whitelist := []string{"utest"}

			result := v.convertToOraclePrices(prices, whitelist)
			require.Len(t, result, 1)
			assert.InDelta(t, tt.expected, result[0].Price, tt.delta)
		})
	}
}

// TestEmptyInputs verifies behavior with empty inputs.
func TestEmptyInputs(t *testing.T) {
	v := &Voter{}

	t.Run("Empty prices map", func(t *testing.T) {
		prices := map[string]decimal.Decimal{}
		whitelist := []string{"ukrw", "uusd"}

		result := v.convertToOraclePrices(prices, whitelist)
		assert.Empty(t, result)
	})

	t.Run("Empty whitelist", func(t *testing.T) {
		prices := map[string]decimal.Decimal{
			"KRW/USD": decimal.NewFromFloat(0.00075),
		}
		whitelist := []string{}

		result := v.convertToOraclePrices(prices, whitelist)
		assert.Empty(t, result)
	})

	t.Run("Both empty", func(t *testing.T) {
		prices := map[string]decimal.Decimal{}
		whitelist := []string{}

		result := v.convertToOraclePrices(prices, whitelist)
		assert.Empty(t, result)
	})
}

// TestSymbolSuffixVariations tests different symbol suffix formats.
func TestSymbolSuffixVariations(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected string
	}{
		{"With /USD", "KRW/USD", "ukrw"},
		{"With /USDT", "KRW/USDT", "ukrw"},
		{"Without suffix", "KRW", "ukrw"},
		{"Multiple slashes (keep only remove suffix)", "TEST/KRW/USD", "utest/krw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := symbolToDenom(tt.symbol)
			assert.Equal(t, tt.expected, result)
		})
	}
}
