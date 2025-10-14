package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	binanceWSURL          = "wss://stream.binance.com:9443"
	binancePingInterval   = 3 * time.Minute
	binanceReconnectDelay = 5 * time.Second
	binanceMaxReconnect   = 10
)

// BinanceSource fetches prices from Binance WebSocket API
type BinanceSource struct {
	*sources.BaseSource
	wsURL         string
	conn          *websocket.Conn
	connMu        sync.Mutex
	reconnectChan chan struct{}
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

// NewBinanceSource creates a new Binance WebSocket source
func NewBinanceSource(config map[string]interface{}) (sources.Source, error) {
	// Parse pair mappings from config
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	wsURL := binanceWSURL
	if url, ok := config["websocket_url"].(string); ok && url != "" {
		wsURL = url
	}

	logger, _ := logging.Init("info", "text", "stdout")
	base := sources.NewBaseSource("binance", sources.SourceTypeCEX, pairs, logger)

	return &BinanceSource{
		BaseSource:    base,
		wsURL:         wsURL,
		reconnectChan: make(chan struct{}),
	}, nil
}

// Initialize prepares the source for operation
func (s *BinanceSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Binance source", "pairs", len(s.Symbols()))
	return nil
}

// Start begins fetching prices
func (s *BinanceSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Binance source")
	go s.connectAndListen(ctx)
	return nil
}

// Stop halts the source and cleans up resources
func (s *BinanceSource) Stop() error {
	s.Logger().Info("Stopping Binance source")
	s.Close()

	s.connMu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	s.connMu.Unlock()

	return nil
}

// GetPrices returns the current prices for all symbols
func (s *BinanceSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe allows other components to receive price updates
func (s *BinanceSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// connectAndListen manages WebSocket connection with auto-reconnect
func (s *BinanceSource) connectAndListen(ctx context.Context) {
	reconnectAttempts := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.StopChan():
			return
		default:
		}

		// Connect to WebSocket
		if err := s.connect(); err != nil {
			s.Logger().Error("Failed to connect to Binance WebSocket", "error", err, "attempt", reconnectAttempts)
			s.SetHealthy(false)

			reconnectAttempts++
			if reconnectAttempts >= binanceMaxReconnect {
				s.Logger().Error("Max reconnect attempts reached, giving up")
				return
			}

			// Exponential backoff
			delay := time.Duration(reconnectAttempts) * binanceReconnectDelay
			if delay > 2*time.Minute {
				delay = 2 * time.Minute
			}

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return
			case <-s.StopChan():
				return
			}
		}

		// Reset reconnect attempts on successful connection
		reconnectAttempts = 0
		s.SetHealthy(true)

		// Listen for messages
		if err := s.listen(ctx); err != nil {
			s.Logger().Warn("WebSocket listener stopped", "error", err)
			s.SetHealthy(false)
		}

		// Close connection before reconnecting
		s.connMu.Lock()
		if s.conn != nil {
			s.conn.Close()
			s.conn = nil
		}
		s.connMu.Unlock()

		// Wait before reconnecting
		select {
		case <-time.After(binanceReconnectDelay):
		case <-ctx.Done():
			return
		case <-s.StopChan():
			return
		}
	}
}

// connect establishes WebSocket connection and subscribes to streams
func (s *BinanceSource) connect() error {
	// Build stream names for all pairs
	streams := make([]string, 0, len(s.Symbols()))
	for unifiedSymbol, sourceSymbol := range s.GetAllPairs() {
		// Binance expects lowercase symbols
		binanceSymbol := strings.ToLower(sourceSymbol)
		streams = append(streams, binanceSymbol+"@kline_5m")
		s.Logger().Debug("Subscribing to Binance stream", "unified", unifiedSymbol, "binance", binanceSymbol)
	}

	// Connect to combined stream
	url := s.wsURL + "/stream?streams=" + strings.Join(streams, "/")

	s.Logger().Info("Connecting to Binance WebSocket", "url", url)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to dial %s: %w", url, err)
	}

	s.connMu.Lock()
	s.conn = conn
	s.connMu.Unlock()

	s.Logger().Info("Connected to Binance WebSocket")

	return nil
}

// listen reads messages from WebSocket
func (s *BinanceSource) listen(ctx context.Context) error {
	// Start ping goroutine to keep connection alive
	pingTicker := time.NewTicker(binancePingInterval)
	defer pingTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.StopChan():
				return
			case <-pingTicker.C:
				s.connMu.Lock()
				if s.conn != nil {
					if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						s.Logger().Warn("Failed to send ping", "error", err)
					}
				}
				s.connMu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.StopChan():
			return nil
		default:
		}

		s.connMu.Lock()
		conn := s.conn
		s.connMu.Unlock()

		if conn == nil {
			return fmt.Errorf("connection is nil")
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("failed to read message: %w", err)
		}

		if err := s.processMessage(message); err != nil {
			s.Logger().Warn("Failed to process message", "error", err)
		}
	}
}

// processMessage parses and stores price updates
func (s *BinanceSource) processMessage(message []byte) error {
	// Binance combined stream format: {"stream":"...", "data":{...}}
	var klineMsg BinanceKlineMessage
	if err := json.Unmarshal(message, &klineMsg); err != nil {
		return fmt.Errorf("failed to unmarshal kline message: %w", err)
	}

	return s.processKline(&klineMsg)
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

	// Update price using BaseSource
	now := time.Now()
	s.SetPrice(unifiedSymbol, priceDecimal, now)
	s.SetLastUpdate(now)

	// Record metrics
	metrics.RecordSourceUpdate(s.Name(), unifiedSymbol)

	s.Logger().Debug("Updated price from Binance",
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
