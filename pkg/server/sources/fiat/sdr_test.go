package fiat

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/server/sources"
)

func TestSDRSource_Initialize(t *testing.T) {
	cfg := map[string]interface{}{
		"timeout":  15,
		"interval": 300,
	}

	source, err := NewSDRSourceFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewSDRSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if source.Name() != "fiat.sdr" {
		t.Errorf("Expected name 'fiat.sdr', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeFiat {
		t.Errorf("Expected type SourceTypeFiat, got %v", source.Type())
	}

	symbols := source.Symbols()
	if len(symbols) != 1 || symbols[0] != sdrUsdPair {
		t.Errorf("Expected symbols [SDR/USD], got %v", symbols)
	}
}

func TestSDRSource_CalculateSDR(t *testing.T) {
	// Test SDR calculation with known exchange rates
	// Using official IMF basket amounts (effective August 1, 2022)
	//
	// Official SDR basket amounts (from IMF Executive Board Decision):
	//   USD: 0.57813
	//   EUR: 0.37379
	//   CNY: 1.0993
	//   JPY: 13.452
	//   GBP: 0.080870
	//
	// Test exchange rates (XXX/USD, i.e., USD per 1 unit of currency):
	//   USD/USD = 1.0
	//   EUR/USD = 1.16 (1 EUR = 1.16 USD)
	//   CNY/USD = 0.14 (1 CNY = 0.14 USD)
	//   JPY/USD = 0.0066 (1 JPY = 0.0066 USD)
	//   GBP/USD = 1.33 (1 GBP = 1.33 USD)
	//
	// Expected SDR/USD calculation:
	//   = 0.57813 × 1.0 + 0.37379 × 1.16 + 1.0993 × 0.14 + 13.452 × 0.0066 + 0.080870 × 1.33
	//   = 0.578130 + 0.433596 + 0.153902 + 0.088783 + 0.107557
	//   = 1.361968

	cfg := map[string]interface{}{
		"timeout":  15,
		"interval": 300,
	}

	sdrSource, err := NewSDRSourceFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewSDRSource failed: %v", err)
	}

	// Cast to access internal methods
	source := sdrSource.(*SDRSource)

	// Create mock fiat sources with test prices
	mockSource := &mockFiatSource{
		name:    "mock",
		healthy: true,
		prices: map[string]sources.Price{
			"USD/USD": {Symbol: "USD/USD", Price: decimal.NewFromFloat(1.0)}, // Added USD/USD
			"EUR/USD": {Symbol: "EUR/USD", Price: decimal.NewFromFloat(1.16)},
			"CNY/USD": {Symbol: "CNY/USD", Price: decimal.NewFromFloat(0.14)},
			"JPY/USD": {Symbol: "JPY/USD", Price: decimal.NewFromFloat(0.0066)},
			"GBP/USD": {Symbol: "GBP/USD", Price: decimal.NewFromFloat(1.33)},
		},
	}

	source.SetFiatSources([]sources.Source{mockSource})

	// Calculate SDR
	err = source.calculateSDR(context.Background())
	if err != nil {
		t.Fatalf("calculateSDR failed: %v", err)
	}

	// Get the calculated SDR price
	prices, err := source.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("GetPrices failed: %v", err)
	}

	sdrPrice, ok := prices[sdrUsdPair]
	if !ok {
		t.Fatal("Expected SDR/USD price")
	}

	// Expected: 1.361968 (with some tolerance for floating point precision)
	expected := decimal.NewFromFloat(1.361968)
	diff := sdrPrice.Price.Sub(expected).Abs()
	tolerance := decimal.NewFromFloat(0.00001)

	if diff.GreaterThan(tolerance) {
		t.Errorf("SDR/USD price mismatch: expected %s, got %s (diff: %s)",
			expected.String(), sdrPrice.Price.String(), diff.String())
	}

	t.Logf("SDR/USD calculated correctly: %s", sdrPrice.Price.String())
}

func TestSDRSource_MissingCurrencies(t *testing.T) {
	cfg := map[string]interface{}{
		"timeout":  15,
		"interval": 300,
	}

	sdrSource, err := NewSDRSourceFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewSDRSource failed: %v", err)
	}

	source := sdrSource.(*SDRSource)

	// Create mock source with incomplete basket (missing GBP)
	mockSource := &mockFiatSource{
		name:    "mock",
		healthy: true,
		prices: map[string]sources.Price{
			"EUR/USD": {Symbol: "EUR/USD", Price: decimal.NewFromFloat(1.10)},
			"CNY/USD": {Symbol: "CNY/USD", Price: decimal.NewFromFloat(0.15)},
			"JPY/USD": {Symbol: "JPY/USD", Price: decimal.NewFromFloat(0.0075)},
			// GBP missing
		},
	}

	source.SetFiatSources([]sources.Source{mockSource})

	// Should fail due to missing currency
	err = source.calculateSDR(context.Background())
	if err == nil {
		t.Error("Expected error for missing basket currencies, got nil")
	}

	if err != nil {
		t.Logf("Correctly reported error: %v", err)
	}
}

// mockFiatSource implements sources.Source for testing.
type mockFiatSource struct {
	name    string
	healthy bool
	prices  map[string]sources.Price
}

func (m *mockFiatSource) Initialize(_ context.Context) error { return nil }
func (m *mockFiatSource) Start(_ context.Context) error      { return nil }
func (m *mockFiatSource) Stop() error                        { return nil }

func (m *mockFiatSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return m.prices, nil
}

func (m *mockFiatSource) Subscribe(_ chan<- sources.PriceUpdate) error {
	return nil
}

func (m *mockFiatSource) Name() string {
	return m.name
}

func (m *mockFiatSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (m *mockFiatSource) Symbols() []string {
	symbols := make([]string, 0, len(m.prices))
	for symbol := range m.prices {
		symbols = append(symbols, symbol)
	}
	return symbols
}

func (m *mockFiatSource) IsHealthy() bool {
	return m.healthy
}

func (m *mockFiatSource) LastUpdate() time.Time {
	return time.Now()
}
