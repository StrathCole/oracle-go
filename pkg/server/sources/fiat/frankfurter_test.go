package fiat

import (
	"context"
	"testing"
	"time"

	"tc.com/oracle-prices/pkg/server/sources"
)

func TestFrankfurterSource_Initialize(t *testing.T) {
	cfg := map[string]interface{}{
		"symbols":  []interface{}{"EUR", "GBP", "JPY", "KRW"},
		"timeout":  10,
		"interval": 30,
	}

	source, err := NewFrankfurterSource(cfg)
	if err != nil {
		t.Fatalf("NewFrankfurterSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if source.Name() != "frankfurter" {
		t.Errorf("Expected name 'frankfurter', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeFiat {
		t.Errorf("Expected type SourceTypeFiat, got %v", source.Type())
	}

	symbols := source.Symbols()
	if len(symbols) != 4 {
		t.Errorf("Expected 4 symbols, got %d", len(symbols))
	}
}

func TestFrankfurterSource_FetchPrices(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := map[string]interface{}{
		"symbols":  []interface{}{"EUR", "GBP"},
		"timeout":  10,
		"interval": 30,
	}

	source, err := NewFrankfurterSource(cfg)
	if err != nil {
		t.Fatalf("NewFrankfurterSource failed: %v", err)
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
		if eurPrice.Source != "frankfurter" {
			t.Errorf("Expected source 'frankfurter', got '%s'", eurPrice.Source)
		}
		t.Logf("EUR/USD price: %s", eurPrice.Price.String())
	}

	if !source.IsHealthy() {
		t.Error("Source should be healthy after successful fetch")
	}
}

func TestFrankfurterSource_Subscribe(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := map[string]interface{}{
		"symbols":  []interface{}{"EUR"},
		"timeout":  10,
		"interval": 30,
	}

	source, err := NewFrankfurterSource(cfg)
	if err != nil {
		t.Fatalf("NewFrankfurterSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Create subscriber
	updates := make(chan sources.PriceUpdate, 10)
	err = source.Subscribe(updates)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Start source
	go source.Start(context.Background())
	defer source.Stop()

	// Wait for update
	select {
	case update := <-updates:
		if update.Error != nil {
			t.Errorf("Received error update: %v", update.Error)
		}
		if len(update.Prices) == 0 {
			t.Error("Expected prices in update")
		}
		t.Logf("Received update with %d prices", len(update.Prices))
	case <-time.After(5 * time.Second):
		t.Error("Timeout waiting for price update")
	}
}
