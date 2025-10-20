package fiat

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

func TestIMFSource_Initialize(t *testing.T) {
	cfg := map[string]interface{}{
		"timeout":  15,
		"interval": 300, // 5 minutes
	}

	source, err := NewIMFSourceFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewIMFSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if source.Name() != "imf" {
		t.Errorf("Expected name 'imf', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeFiat {
		t.Errorf("Expected type SourceTypeFiat, got %v", source.Type())
	}

	symbols := source.Symbols()
	if len(symbols) != 1 || symbols[0] != sdrUsdPair {
		t.Errorf("Expected symbols [SDR/USD], got %v", symbols)
	}
}

func TestIMFSource_FetchPrices(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := map[string]interface{}{
		"timeout":  15000,  // 15 seconds
		"interval": 300000, // 5 minutes
	}

	source, err := NewIMFSourceFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewIMFSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Start source
	if err := source.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = source.Stop() }()

	// Wait for first update (may take longer due to HTML parsing)
	time.Sleep(5 * time.Second)

	prices, err := source.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("GetPrices failed: %v", err)
	}

	if len(prices) == 0 {
		t.Error("Expected prices, got empty map")
	}

	// Check if SDR/USD exists
	sdrPrice, ok := prices[sdrUsdPair]
	if !ok {
		t.Error("Expected SDR/USD price")
	} else {
		if sdrPrice.Price.IsZero() {
			t.Error("SDR/USD price is zero")
		}
		// IMF reports "SDR 1 = US$ X.XX" format, which gives us the direct USD value per SDR
		// SDR should be around $1.30-$1.40 typically
		if sdrPrice.Price.LessThan(decimal.NewFromFloat(1.0)) || sdrPrice.Price.GreaterThan(decimal.NewFromFloat(2.0)) {
			t.Errorf("SDR/USD price seems out of expected range (1.0-2.0): %s", sdrPrice.Price.String())
		}
		t.Logf("SDR/USD price: %s", sdrPrice.Price.String())
	}

	if !source.IsHealthy() {
		t.Error("Source should be healthy after successful fetch")
	}
}

// TestIMFSource_ParseSDRRate removed - parseSDRRate is now private implementation detail
// HTML parsing is tested through integration tests via fetchPrices()
