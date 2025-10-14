package fiat

import (
	"context"
	"testing"
	"time"

	"tc.com/oracle-prices/pkg/server/sources"
)

func TestFERSource_Initialize(t *testing.T) {
	cfg := map[string]interface{}{
		"symbols":  []interface{}{"EUR", "GBP", "JPY"},
		"timeout":  10,
		"interval": 30,
	}

	source, err := NewFERSource(cfg)
	if err != nil {
		t.Fatalf("NewFERSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if source.Name() != "fer" {
		t.Errorf("Expected name 'fer', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeFiat {
		t.Errorf("Expected type SourceTypeFiat, got %v", source.Type())
	}
}

func TestFERSource_FetchPrices(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := map[string]interface{}{
		"symbols":  []interface{}{"EUR", "GBP"},
		"timeout":  10,
		"interval": 30,
	}

	source, err := NewFERSource(cfg)
	if err != nil {
		t.Fatalf("NewFERSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Start source
	go source.Start(context.Background())
	defer source.Stop()

	// Wait for first update
	time.Sleep(3 * time.Second)

	prices, err := source.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("GetPrices failed: %v", err)
	}

	if len(prices) == 0 {
		t.Error("Expected prices, got empty map")
	}

	// Check if EUR/USD exists
	eurPrice, ok := prices["EUR/USD"]
	if !ok {
		t.Error("Expected EUR/USD price")
	} else {
		if eurPrice.Price.IsZero() {
			t.Error("EUR/USD price is zero")
		}
		t.Logf("EUR/USD price: %s", eurPrice.Price.String())
	}

	if !source.IsHealthy() {
		t.Error("Source should be healthy after successful fetch")
	}
}
