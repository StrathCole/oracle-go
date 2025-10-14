package fiat

import (
	"context"
	"testing"
	"time"

	"tc.com/oracle-prices/pkg/server/sources"
)

func TestIMFSource_Initialize(t *testing.T) {
	cfg := map[string]interface{}{
		"timeout":  15,
		"interval": 300, // 5 minutes
	}

	source, err := NewIMFSource(cfg)
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
	if len(symbols) != 1 || symbols[0] != "SDR" {
		t.Errorf("Expected symbols [SDR], got %v", symbols)
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

	source, err := NewIMFSource(cfg)
	if err != nil {
		t.Fatalf("NewIMFSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Start source
	go source.Start(context.Background())
	defer source.Stop()

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
	sdrPrice, ok := prices["SDR/USD"]
	if !ok {
		t.Error("Expected SDR/USD price")
	} else {
		if sdrPrice.Price.IsZero() {
			t.Error("SDR/USD price is zero")
		}
		// SDR should be > 1 USD (typically around 1.3-1.4)
		if sdrPrice.Price.LessThan(sdrPrice.Price.Div(sdrPrice.Price)) {
			t.Errorf("SDR/USD price seems incorrect: %s", sdrPrice.Price.String())
		}
		t.Logf("SDR/USD price: %s", sdrPrice.Price.String())
	}

	if !source.IsHealthy() {
		t.Error("Source should be healthy after successful fetch")
	}
}

func TestIMFSource_ParseSDRRate(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected float64
		hasError bool
	}{
		{
			name: "valid SDR rate - single number (current IMF format)",
			html: `<table><tr>
				<td>SDR1 = US$</td>
				<td>1.359580</td>
				</tr></table>`,
			expected: 1.359580,
			hasError: false,
		},
		{
			name: "valid SDR rate - with trailing space",
			html: `<table><tr>
				<td>SDR1 = US$</td>
				<td>1.234567 </td>
				</tr></table>`,
			expected: 1.234567,
			hasError: false,
		},
		{
			name: "valid with space in label",
			html: `<table><tr>
				<td>SDR 1 = US$</td>
				<td>1.500000</td>
				</tr></table>`,
			expected: 1.500000,
			hasError: false,
		},
		{
			name: "valid with two numbers (older IMF format)",
			html: `<table><tr>
				<td>SDR1 = US$</td>
				<td>1.32149 2</td>
				</tr></table>`,
			expected: 1.32149,
			hasError: false,
		},
		{
			name: "valid with superscript tag",
			html: `<table><tr>
				<td>SDR1 = US$</td>
				<td align="right" width="20%" nowrap="nowrap">
				1.359580
				<sup>4</sup></td>
				</tr></table>`,
			expected: 1.359580,
			hasError: false,
		},
		{
			name:     "invalid format",
			html:     `<table><tr><td>Invalid</td></tr></table>`,
			expected: 0,
			hasError: true,
		},
	}

	src, err := NewIMFSource(map[string]interface{}{})
	if err != nil {
		t.Fatalf("NewIMFSource failed: %v", err)
	}

	// Cast to *IMFSource to access internal method
	source := src.(*IMFSource)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := source.parseSDRRate(tt.html)
			if tt.hasError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if rate != tt.expected {
					t.Errorf("Expected rate %f, got %f", tt.expected, rate)
				}
			}
		})
	}
}
