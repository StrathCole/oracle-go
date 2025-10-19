package cex

import (
	"context"
	"testing"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

func TestBinanceSource_NewSource(t *testing.T) {
	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USDT": "LUNCUSDT",
			"BTC/USDT":  "BTCUSDT",
		},
		"websocket_url": "wss://stream.binance.com:9443",
	}

	source, err := NewBinanceSource(config)
	if err != nil {
		t.Fatalf("NewBinanceSource failed: %v", err)
	}

	if source.Name() != "binance" {
		t.Errorf("Expected name 'binance', got '%s'", source.Name())
	}

	if source.Type() != sources.SourceTypeCEX {
		t.Errorf("Expected type SourceTypeCEX, got %v", source.Type())
	}

	symbols := source.Symbols()
	if len(symbols) != 2 {
		t.Errorf("Expected 2 symbols, got %d", len(symbols))
	}

	// Check symbols contain LUNC/USDT and BTC/USDT
	symbolMap := make(map[string]bool)
	for _, s := range symbols {
		symbolMap[s] = true
	}

	if !symbolMap["LUNC/USDT"] {
		t.Error("Expected LUNC/USDT in symbols")
	}
	if !symbolMap["BTC/USDT"] {
		t.Error("Expected BTC/USDT in symbols")
	}
}

func TestBinanceSource_Initialize(t *testing.T) {
	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USDT": "LUNCUSDT",
		},
		"websocket_url": "wss://stream.binance.com:9443",
	}

	source, err := NewBinanceSource(config)
	if err != nil {
		t.Fatalf("NewBinanceSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
}

func TestBinanceSource_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name:   "missing pairs",
			config: map[string]interface{}{},
		},
		{
			name: "invalid pairs type",
			config: map[string]interface{}{
				"pairs": "invalid",
			},
		},
		{
			name: "empty pairs",
			config: map[string]interface{}{
				"pairs": map[string]interface{}{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBinanceSource(tt.config)
			if err == nil {
				t.Error("Expected error for invalid config, got none")
			}
		})
	}
}

func TestBinanceSource_GetSourceSymbol(t *testing.T) {
	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USDT": "LUNCUSDT",
			"BTC/USDT":  "BTCUSDT",
		},
	}

	source, err := NewBinanceSource(config)
	if err != nil {
		t.Fatalf("NewBinanceSource failed: %v", err)
	}

	binanceSource := source.(*BinanceSource)

	// Test GetSourceSymbol
	sourceSymbol := binanceSource.GetSourceSymbol("LUNC/USDT")
	if sourceSymbol == "" {
		t.Error("Expected to find source symbol for LUNC/USDT")
	}
	if sourceSymbol != "LUNCUSDT" {
		t.Errorf("Expected LUNCUSDT, got %s", sourceSymbol)
	}

	// Test GetUnifiedSymbol
	unifiedSymbol := binanceSource.GetUnifiedSymbol("BTCUSDT")
	if unifiedSymbol == "" {
		t.Error("Expected to find unified symbol for BTCUSDT")
	}
	if unifiedSymbol != "BTC/USDT" {
		t.Errorf("Expected BTC/USDT, got %s", unifiedSymbol)
	}

	// Test non-existent symbol
	result := binanceSource.GetSourceSymbol("INVALID/PAIR")
	if result != "" {
		t.Error("Should return empty string for non-existent pair")
	}
}

// Integration test - requires network connection.
func TestBinanceSource_FetchPrices_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := map[string]interface{}{
		"pairs": map[string]interface{}{
			"LUNC/USDT": "LUNCUSDT",
			"BTC/USDT":  "BTCUSDT",
		},
	}

	source, err := NewBinanceSource(config)
	if err != nil {
		t.Fatalf("NewBinanceSource failed: %v", err)
	}

	err = source.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Note: Actual WebSocket testing would require running Start()
	// and waiting for messages, which is complex for unit tests
	// This test mainly validates initialization
	t.Log("Binance source initialized successfully")
}
