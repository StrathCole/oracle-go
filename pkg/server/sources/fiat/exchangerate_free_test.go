package fiat

import (
	"context"
	"testing"
	"time"
)

func TestExchangeRateFreeSource_Initialize(t *testing.T) {
	config := map[string]interface{}{
		"symbols":  []interface{}{"EUR/USD", "GBP/USD", "JPY/USD", "KRW/USD"},
		"timeout":  5000,
		"interval": 3600000, // 1 hour
	}

	source, err := NewExchangeRateFreeSourceFromConfig(config)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	if source.Name() != "exchangerate_free" {
		t.Errorf("Expected name 'exchangerate_free', got %s", source.Name())
	}

	symbols := source.Symbols()
	if len(symbols) != 4 {
		t.Errorf("Expected 4 symbols, got %d", len(symbols))
	}
}

func TestExchangeRateFreeSource_FetchPrices(t *testing.T) {
	config := map[string]interface{}{
		"symbols":  []interface{}{"EUR/USD", "GBP/USD"},
		"timeout":  10000,
		"interval": 3600000,
	}

	source, err := NewExchangeRateFreeSourceFromConfig(config)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	ctx := context.Background()
	if err := source.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}

	freeSource := source.(*ExchangeRateFreeSource)

	// Test fetching prices
	if err := freeSource.fetchPrices(ctx); err != nil {
		t.Fatalf("Failed to fetch prices: %v", err)
	}

	// Verify we got prices
	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	if len(prices) == 0 {
		t.Fatal("No prices returned")
	}

	// Verify EUR/USD price is reasonable (should be around 0.8-1.2)
	if eurPrice, ok := prices["EUR/USD"]; ok {
		rate, _ := eurPrice.Price.Float64()
		t.Logf("EUR/USD price: %f", rate)
		if rate < 0.5 || rate > 2.0 {
			t.Errorf("EUR/USD price %f seems unreasonable", rate)
		}
	} else {
		t.Error("EUR/USD price not found")
	}

	// Verify next update time was set
	if freeSource.nextUpdateTime.IsZero() {
		t.Error("Next update time was not set from API response")
	} else {
		t.Logf("Next update time: %s", freeSource.nextUpdateTime.Format(time.RFC3339))
		t.Logf("Time until next update: %s", time.Until(freeSource.nextUpdateTime))

		// Next update should be in the future
		if freeSource.nextUpdateTime.Before(time.Now()) {
			t.Error("Next update time is in the past")
		}

		// Next update should be within reasonable timeframe (24-48 hours for free tier)
		timeUntil := time.Until(freeSource.nextUpdateTime)
		if timeUntil > 48*time.Hour {
			t.Errorf("Next update time seems too far in future: %s", timeUntil)
		}
	}
}

func TestExchangeRateFreeSource_SDRMapping(t *testing.T) {
	config := map[string]interface{}{
		"symbols":  []interface{}{"SDR/USD"},
		"timeout":  10000,
		"interval": 3600000,
	}

	source, err := NewExchangeRateFreeSourceFromConfig(config)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	ctx := context.Background()
	freeSource := source.(*ExchangeRateFreeSource)

	// Test fetching SDR price (API uses XDR code)
	if err := freeSource.fetchPrices(ctx); err != nil {
		t.Fatalf("Failed to fetch prices: %v", err)
	}

	prices, err := source.GetPrices(ctx)
	if err != nil {
		t.Fatalf("Failed to get prices: %v", err)
	}

	// Verify SDR/USD price
	if sdrPrice, ok := prices["SDR/USD"]; ok {
		rate, _ := sdrPrice.Price.Float64()
		t.Logf("SDR/USD price: %f", rate)
		// SDR should be around 1.3-1.4 USD
		if rate < 1.0 || rate > 2.0 {
			t.Errorf("SDR/USD price %f seems unreasonable (expected ~1.3-1.4)", rate)
		}
	} else {
		t.Error("SDR/USD price not found (XDR mapping may have failed)")
	}
}

func TestExchangeRateFreeSource_RateLimitRespect(t *testing.T) {
	config := map[string]interface{}{
		"symbols":  []interface{}{"EUR/USD"},
		"timeout":  5000,
		"interval": 1800000, // 30 minutes (should be enforced to 60 min minimum)
	}

	source, err := NewExchangeRateFreeSourceFromConfig(config)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	freeSource := source.(*ExchangeRateFreeSource)

	// Verify minimum interval is enforced (should be 60 minutes)
	expectedMin := 60 * time.Minute
	if freeSource.minInterval < expectedMin {
		t.Errorf("Minimum interval %s is less than expected %s (rate limit protection)",
			freeSource.minInterval, expectedMin)
	}

	t.Logf("Minimum interval correctly set to: %s", freeSource.minInterval)
}
