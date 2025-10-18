package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
	ws "tc.com/oracle-prices/pkg/server/sources/websocket"
)

const (
	binanceBaseURL  = "https://api.binance.com"
	binanceWSURL    = "wss://stream.binance.com:9443/stream"
	binanceTimeout  = 10 * time.Second
	binancePollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// BinanceSource fetches prices from Binance (supports both REST and WebSocket)
type BinanceSource struct {
	*sources.BaseSource
	useWebSocket bool
	apiURL       string
	wsURL        string
	wsClient     *ws.Client
}

// BinancePriceTicker represents lightweight price data from /ticker/price endpoint
type BinancePriceTicker struct {
	Symbol string `json:"symbol"` // e.g., "LUNCUSDT"
	Price  string `json:"price"`  // Current price
}

// BinanceKlineMessage represents Binance kline WebSocket message
// Field types match actual Binance WebSocket format (verified 2025-10-14):
// - Timestamps are int64 (milliseconds)
// - Prices (o/c/h/l) are strings with decimal precision
// - Volumes (v/q/V/Q) are strings with decimal precision
// - Trade IDs and counts are int64
type BinanceKlineMessage struct {
	Stream string `json:"stream"` // Stream name (e.g., "btcusdt@kline_5m")
	Data   struct {
		EventType string `json:"e"` // Event type ("kline")
		EventTime int64  `json:"E"` // Event time (milliseconds)
		Symbol    string `json:"s"` // Trading pair symbol
		Kline     struct {
			StartTime           int64  `json:"t"` // Kline start time (ms)
			EndTime             int64  `json:"T"` // Kline close time (ms)
			Symbol              string `json:"s"` // Symbol
			Interval            string `json:"i"` // Interval (e.g., "5m")
			FirstTradeID        int64  `json:"f"` // First trade ID
			LastTradeID         int64  `json:"L"` // Last trade ID
			OpenPrice           string `json:"o"` // Open price (string decimal)
			ClosePrice          string `json:"c"` // Close price (current price, string decimal)
			HighPrice           string `json:"h"` // High price (string decimal)
			LowPrice            string `json:"l"` // Low price (string decimal)
			Volume              string `json:"v"` // Base asset volume (string decimal)
			NumberOfTrades      int64  `json:"n"` // Number of trades
			IsClosed            bool   `json:"x"` // Is this kline closed?
			QuoteAssetVolume    string `json:"q"` // Quote asset volume (string decimal)
			TakerBuyBaseVolume  string `json:"V"` // Taker buy base asset volume (string decimal)
			TakerBuyQuoteVolume string `json:"Q"` // Taker buy quote asset volume (string decimal)
			Ignore              string `json:"B"` // Ignore field
		} `json:"k"`
	} `json:"data"`
}

// NewBinanceSource creates a new Binance source (REST or WebSocket based on config)
func NewBinanceSource(config map[string]interface{}) (sources.Source, error) {
	logger, _ := logging.Init("info", "text", "stdout")

	// Parse pair mappings from config
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	// Check if WebSocket is enabled
	useWebSocket := true // Default to WebSocket for backward compatibility
	if useWS, ok := config["use_websocket"].(bool); ok {
		useWebSocket = useWS
	}

	apiURL := binanceBaseURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	wsURL := binanceWSURL
	if url, ok := config["websocket_url"].(string); ok && url != "" {
		wsURL = url
	}

	base := sources.NewBaseSource("binance", sources.SourceTypeCEX, pairs, logger)

	source := &BinanceSource{
		BaseSource:   base,
		useWebSocket: useWebSocket,
		apiURL:       apiURL,
		wsURL:        wsURL,
	}

	// Initialize WebSocket client if enabled
	if useWebSocket {
		source.wsClient = ws.NewClient(ws.Config{
			URL:    source.buildWebSocketURL(),
			Logger: logger.ZerologLogger(),
		})
		source.wsClient.SetHandlers(
			source.handleWSMessage,
			source.handleWSConnect,
			source.handleWSDisconnect,
		)
		logger.Info("Binance WebSocket mode enabled")
	} else {
		logger.Info("Binance REST mode enabled")
	}

	return source, nil
}

// Initialize prepares the source for operation
func (s *BinanceSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Binance source", "pairs", len(s.Symbols()))
	return nil
}

// Start begins fetching prices (REST or WebSocket based on configuration)
func (s *BinanceSource) Start(ctx context.Context) error {
	// Do initial REST fetch to have prices immediately
	s.Logger().Info("Performing initial price fetch")
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err.Error())
	}

	if s.useWebSocket {
		s.Logger().Info("Starting Binance source (WebSocket mode)")
		return s.startWebSocket(ctx)
	}

	s.Logger().Info("Starting Binance source (REST mode)")

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop halts the source and cleans up resources
func (s *BinanceSource) Stop() error {
	s.Logger().Info("Stopping Binance source")

	if s.wsClient != nil {
		s.wsClient.Close()
	}

	s.Close()
	return nil
}

// GetPrices returns the latest prices
func (s *BinanceSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe allows other components to receive price updates
func (s *BinanceSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// REST mode implementation

// pollLoop continuously fetches prices at the configured interval
func (s *BinanceSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(binancePollRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.StopChan():
			return
		case <-ticker.C:
			if err := s.fetchPrices(ctx); err != nil {
				s.Logger().Error("Failed to fetch prices", "error", err)
				s.SetHealthy(false)
			} else {
				s.SetHealthy(true)
			}
		}
	}
}

// fetchPrices retrieves current prices from REST API
func (s *BinanceSource) fetchPrices(ctx context.Context) error {
	// Build the full endpoint URL
	url := s.apiURL + "/api/v3/ticker/price"
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var tickers []BinancePriceTicker
	if err := json.Unmarshal(body, &tickers); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Process each ticker
	for _, ticker := range tickers {
		// Normalize symbol to lowercase for matching
		sourceSymbol := strings.ToUpper(ticker.Symbol)

		// Find unified symbol
		unifiedSymbol := s.GetUnifiedSymbol(sourceSymbol)
		if unifiedSymbol == "" {
			continue // Not a symbol we're tracking
		}

		// Parse price
		priceDecimal, err := decimal.NewFromString(ticker.Price)
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", sourceSymbol, "price", ticker.Price, "error", err)
			continue
		}

		// Update price (automatically records metrics)
		now := time.Now()
		s.SetPrice(unifiedSymbol, priceDecimal, now)
		s.SetLastUpdate(now)
	}

	return nil
}

// WebSocket mode implementation

// startWebSocket initializes and starts WebSocket connection
func (s *BinanceSource) startWebSocket(ctx context.Context) error {
	if err := s.wsClient.ConnectWithRetry(ctx); err != nil {
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	// Subscribe to price streams
	if err := s.subscribeToTickers(); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	return nil
}

// buildWebSocketURL creates the WebSocket URL with all symbol streams
func (s *BinanceSource) buildWebSocketURL() string {
	// Build stream names for all pairs
	streams := make([]string, 0, len(s.Symbols()))
	for _, sourceSymbol := range s.GetAllPairs() {
		// Binance expects lowercase symbols for streams
		binanceSymbol := strings.ToLower(sourceSymbol)
		streams = append(streams, binanceSymbol+"@kline_5m")
	}

	// Binance combined stream format
	return s.wsURL + "/stream?streams=" + strings.Join(streams, "/")
}

// subscribeToTickers subscribes to all configured ticker streams
// For Binance, subscription is done via URL parameters, so this is a no-op
func (s *BinanceSource) subscribeToTickers() error {
	s.Logger().Info("Binance streams subscribed via URL parameters")
	return nil
}

// handleWSMessage processes incoming WebSocket messages
func (s *BinanceSource) handleWSMessage(message []byte) {
	// Binance combined stream format: {"stream":"...", "data":{...}}
	var klineMsg BinanceKlineMessage
	if err := json.Unmarshal(message, &klineMsg); err != nil {
		s.Logger().Warn("Failed to unmarshal kline message", "error", err)
		return
	}

	// Process kline data
	if err := s.processKline(&klineMsg); err != nil {
		s.Logger().Warn("Failed to process kline", "error", err)
	}
}

// handleWSConnect is called when WebSocket connection is established
func (s *BinanceSource) handleWSConnect() {
	s.Logger().Info("Binance WebSocket connected")
	s.SetHealthy(true)

	// Resubscribe to tickers (though for Binance this is a no-op since subscription is via URL)
	if err := s.subscribeToTickers(); err != nil {
		s.Logger().Error("Failed to resubscribe to tickers", "error", err)
	}
}

// handleWSDisconnect is called when WebSocket connection is lost
func (s *BinanceSource) handleWSDisconnect(err error) {
	s.Logger().Warn("Binance WebSocket disconnected", "error", err)
	s.SetHealthy(false)
}

// processKline updates price for a symbol from kline data
func (s *BinanceSource) processKline(msg *BinanceKlineMessage) error {
	// Find unified symbol for this Binance symbol
	binanceSymbol := strings.ToLower(msg.Data.Symbol)

	// Get unified symbol from the source-specific symbol
	unifiedSymbol := s.GetUnifiedSymbol(binanceSymbol)
	if unifiedSymbol == "" {
		// Try uppercase version
		unifiedSymbol = s.GetUnifiedSymbol(strings.ToUpper(msg.Data.Symbol))
		if unifiedSymbol == "" {
			return nil // Not a symbol we're tracking
		}
	}

	// Use close price as current price
	priceDecimal, err := decimal.NewFromString(msg.Data.Kline.ClosePrice)
	if err != nil {
		return fmt.Errorf("failed to parse price %s: %w", msg.Data.Kline.ClosePrice, err)
	}

	// Parse volume (optional)
	volumeDecimal := decimal.Zero
	if msg.Data.Kline.Volume != "" {
		if v, err := decimal.NewFromString(msg.Data.Kline.Volume); err == nil {
			volumeDecimal = v
		}
	}

	// Update price using BaseSource (automatically records metrics)
	now := time.Now()
	s.SetPrice(unifiedSymbol, priceDecimal, now)
	s.SetLastUpdate(now)

	s.Logger().Debug("Updated price from Binance kline",
		"symbol", unifiedSymbol,
		"price", priceDecimal.String(),
		"volume", volumeDecimal.String())

	return nil
}

// Register the source in init
func init() {
	sources.Register("cex.binance", func(config map[string]interface{}) (sources.Source, error) {
		return NewBinanceSource(config)
	})
}
