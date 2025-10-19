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

	"github.com/StrathCole/oracle-go/pkg/server/sources"
	ws "github.com/StrathCole/oracle-go/pkg/server/sources/websocket"
)

const (
	mexcAPIURL   = "https://api.mexc.com/api/v3/ticker/price"
	mexcWSURL    = "wss://wbs.mexc.com/ws"
	mexcPollRate = 15 * time.Second // Update every 15s (vote period is 30s)
)

// MEXCSource fetches prices from MEXC (supports both REST and WebSocket).
type MEXCSource struct {
	*sources.BaseSource

	useWebSocket   bool
	apiURL         string
	wsURL          string
	wsClient       *ws.Client
	apiKey         string        // API key for authenticated WebSocket (required for deals stream)
	wsBlocked      bool          // Whether WebSocket is currently blocked
	wsRetryTime    time.Time     // When to retry WebSocket
	wsRetryBackoff time.Duration // Current exponential backoff duration
}

// MEXCPriceTicker represents lightweight price data from /ticker/price endpoint.
type MEXCPriceTicker struct {
	Symbol string `json:"symbol"` // e.g., "LUNCUSDT"
	Price  string `json:"price"`  // Current price
}

// MEXCWSMessage represents WebSocket message from MEXC deals (trades) stream.
type MEXCWSMessage struct {
	Channel   string         `json:"c"` // Channel name (short form)
	Data      *MEXCDealsData `json:"d"` // Deals data
	Symbol    string         `json:"s"` // Trading pair
	Timestamp int64          `json:"t"` // Event time
}

// MEXCDealsData represents trade/deal data from spot@public.deals stream.
type MEXCDealsData struct {
	Deals []MEXCDeal `json:"deals"` // Array of recent trades
}

// MEXCDeal represents a single trade.
type MEXCDeal struct {
	Price     string `json:"p"` // Trade price
	Volume    string `json:"v"` // Trade volume
	Timestamp int64  `json:"t"` // Trade timestamp
	Side      int    `json:"S"` // Trade side (1=buy, 2=sell)
	TradeID   string `json:"i"` // Trade ID
}

// NewMEXCSource creates a new MEXC source (REST or WebSocket based on config).
func NewMEXCSource(config map[string]interface{}) (sources.Source, error) {
	logger := sources.GetLoggerFromConfig(config)

	// Parse pairs from config (map of "LUNC/USDT" => "LUNCUSDT")
	// MEXC uses simple concatenation without separators
	pairs, err := sources.ParsePairsFromMap(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	// Check if WebSocket is enabled
	useWebSocket := false
	if useWS, ok := config["use_websocket"].(bool); ok {
		useWebSocket = useWS
	}

	apiURL := mexcAPIURL
	if url, ok := config["api_url"].(string); ok && url != "" {
		apiURL = url
	}

	wsURL := mexcWSURL
	if url, ok := config["ws_url"].(string); ok && url != "" {
		wsURL = url
	}

	// Get API key from config (required for WebSocket)
	apiKey := ""
	if key, ok := config["api_key"].(string); ok && key != "" {
		apiKey = key
	}

	// Create base source with pair mappings
	base := sources.NewBaseSource("mexc", sources.SourceTypeCEX, pairs, logger)

	source := &MEXCSource{
		BaseSource:     base,
		useWebSocket:   useWebSocket,
		apiURL:         apiURL,
		wsURL:          wsURL,
		apiKey:         apiKey,
		wsBlocked:      false,
		wsRetryBackoff: 5 * time.Minute, // Start with 5-minute backoff
	}

	// Initialize WebSocket client if enabled
	if useWebSocket {
		if apiKey == "" {
			logger.Warn("MEXC WebSocket requires API key - falling back to REST mode")
			source.useWebSocket = false
			logger.Info("MEXC REST mode enabled")
		} else {
			// Prepare headers with API key for authentication
			headers := make(map[string][]string)
			headers["X-MEXC-APIKEY"] = []string{apiKey}
			headers["Content-Type"] = []string{"application/json"}

			source.wsClient = ws.NewClient(ws.Config{
				URL:     wsURL,
				Logger:  logger.ZerologLogger(),
				Headers: headers,
			})
			source.wsClient.SetHandlers(
				source.handleWSMessage,
				source.handleWSConnect,
				source.handleWSDisconnect,
			)
			logger.Info("MEXC WebSocket mode enabled with API key authentication")
		}
	} else {
		logger.Info("MEXC REST mode enabled")
	}

	return source, nil
}

// Initialize prepares the source for operation.
func (s *MEXCSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing MEXC source", "symbols", s.Symbols())
	return nil
}

// Start begins fetching prices (REST or WebSocket based on configuration).
func (s *MEXCSource) Start(ctx context.Context) error {
	// Do initial REST fetch to have prices immediately
	s.Logger().Info("Performing initial price fetch")
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial fetch failed", "error", err.Error())
	} else {
		s.SetHealthy(true)
	}

	if s.useWebSocket {
		s.Logger().Info("Starting MEXC source (WebSocket mode)")
		return s.startWebSocket(ctx)
	}

	s.Logger().Info("Starting MEXC source (REST mode)")

	// Start polling loop
	go s.pollLoop(ctx)

	return nil
}

// Stop stops the source.
func (s *MEXCSource) Stop() error {
	s.Logger().Info("MEXC source stopped")

	if s.wsClient != nil {
		_ = s.wsClient.Close()
	}

	s.Close()
	return nil
}

// GetPrices returns the current prices.
func (s *MEXCSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPricesAvailable)
	}
	return prices, nil
}

// Subscribe adds a subscriber.
func (s *MEXCSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// pollLoop periodically fetches prices.
func (s *MEXCSource) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(mexcPollRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.StopChan():
			return
		case <-ticker.C:
			// Use retry logic for fetching prices
			err := s.RetryWithBackoff(ctx, "fetch_prices", func() error {
				return s.fetchPrices(ctx)
			})
			if err != nil {
				s.Logger().Error("Failed to fetch prices after retries", "error", err)
				s.SetHealthy(false)
			} else {
				s.SetHealthy(true)
			}
		}
	}
}

// fetchPrices fetches current prices from MEXC API.
func (s *MEXCSource) fetchPrices(ctx context.Context) error {
	// MEXC's /ticker/price endpoint returns lightweight price data
	// Fetch all symbols at once (no query parameters)

	req, err := http.NewRequestWithContext(ctx, "GET", s.apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		return fmt.Errorf("%w (HTTP 429)", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var tickers []MEXCPriceTicker
	if err := json.Unmarshal(body, &tickers); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(tickers) == 0 {
		return fmt.Errorf("%w", sources.ErrNoTickersInResponse)
	}

	now := time.Now()
	updateCount := 0

	// Get our configured pair mappings
	pairMappings := s.GetAllPairs()

	// Build reverse lookup: MEXC symbol => unified symbol
	mexcToUnified := make(map[string]string)
	for unified, mexcSymbol := range pairMappings {
		mexcToUnified[mexcSymbol] = unified
		// Also try uppercase version
		mexcToUnified[strings.ToUpper(mexcSymbol)] = unified
	}

	// Process tickers
	for _, ticker := range tickers {
		// Try to find unified symbol for this MEXC symbol
		unifiedSymbol, ok := mexcToUnified[ticker.Symbol]
		if !ok {
			// Not a symbol we're tracking
			continue
		}

		// Parse price
		if ticker.Price == "" {
			continue
		}

		priceFloat, err := decimal.NewFromString(ticker.Price)
		if err != nil {
			s.Logger().Warn("Failed to parse price", "symbol", unifiedSymbol, "price", ticker.Price)
			continue
		}

		s.SetPrice(unifiedSymbol, priceFloat, now)
		updateCount++

		s.Logger().Debug("Updated MEXC price",
			"symbol", unifiedSymbol,
			"price", priceFloat.String())
	}

	if updateCount == 0 {
		return fmt.Errorf("%w", sources.ErrNoMatchingSymbols)
	}

	s.SetLastUpdate(now)
	s.Logger().Debug("Fetched MEXC prices", "count", updateCount)

	return nil
}

// pollLoopWhileBlocked runs a temporary polling loop while WebSocket is blocked.
func (s *MEXCSource) pollLoopWhileBlocked(ctx context.Context) {
	ticker := time.NewTicker(mexcPollRate)
	defer ticker.Stop()

	s.Logger().Info("Starting temporary REST polling while WebSocket blocked")

	for {
		select {
		case <-ctx.Done():
			s.Logger().Info("MEXC polling loop stopped (context cancelled)")
			return
		case <-ticker.C:
			// Check if it's time to retry WebSocket
			if s.wsBlocked && time.Now().After(s.wsRetryTime) {
				s.Logger().Info("Retry time reached - attempting to reconnect WebSocket",
					"backoff", s.wsRetryBackoff)

				// Reset blocked flag and try to reconnect
				s.wsBlocked = false

				if err := s.startWebSocket(ctx); err != nil {
					s.Logger().Warn("WebSocket reconnection failed, continuing with REST",
						"error", err.Error())
					// Mark as blocked again with next retry time
					s.wsBlocked = true
					s.wsRetryTime = time.Now().Add(s.wsRetryBackoff)
					continue
				}

				s.Logger().Info("WebSocket reconnected successfully - stopping REST polling")
				return
			}

			// Continue polling while blocked
			if err := s.fetchPrices(ctx); err != nil {
				s.Logger().Warn("MEXC REST fetch failed during blocked period", "error", err.Error())
			}
		}
	}
}

// WebSocket implementation

// startWebSocket starts the WebSocket connection.
func (s *MEXCSource) startWebSocket(ctx context.Context) error {
	if err := s.wsClient.ConnectWithRetry(ctx); err != nil {
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	// Subscribe to ticker streams for all configured pairs
	go s.subscribeToTickers()

	// Start PING sender (MEXC requires application-level PING every 30s)
	go s.sendPings(ctx)

	return nil
}

// sendPings sends application-level PING messages to keep connection alive.
func (s *MEXCSource) sendPings(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.Logger().Info("Stopping MEXC PING sender (context done)")
			return
		case <-s.StopChan():
			s.Logger().Info("Stopping MEXC PING sender (stop channel)")
			return
		case <-ticker.C:
			// Don't send PING if WebSocket is blocked or client is nil
			if s.wsBlocked || s.wsClient == nil {
				continue
			}

			ping := map[string]interface{}{
				"method": "PING",
			}
			if err := s.wsClient.SendJSON(ping); err != nil {
				s.Logger().Debug("Failed to send PING (connection may be closed)", "error", err.Error())
			} else {
				s.Logger().Debug("Sent PING to MEXC WebSocket")
			}
		}
	}
}

// subscribeToTickers subscribes to ticker updates for configured pairs.
func (s *MEXCSource) subscribeToTickers() {
	// MEXC WebSocket subscription format for deals (trades):
	// {"method":"SUBSCRIPTION","params":["spot@public.deals.v3.api@SYMBOL"]}
	// Note: The .api.pb (protobuf) version is blocked/restricted
	//
	// MEXC WebSocket Limits:
	// - Maximum 30 subscriptions per connection
	// - Connection valid for 24 hours max
	// - Requires PING every 30s to keep alive
	// - Server disconnects after 30s without valid subscription
	// - Server disconnects after 1 minute without data flow

	pairMappings := s.GetAllPairs()

	// Check subscription limit (MEXC supports max 30 subscriptions per connection)
	if len(pairMappings) > 30 {
		s.Logger().Warn("MEXC supports max 30 subscriptions per connection",
			"configured", len(pairMappings),
			"using_first", 30)
	}

	// Build array of all streams to subscribe to (max 30)
	streamCount := len(pairMappings)
	if streamCount > 30 {
		streamCount = 30
	}
	streams := make([]string, 0, streamCount)
	count := 0
	for _, mexcSymbol := range pairMappings {
		if count >= 30 {
			break
		}
		// Subscribe to deals (trades) stream - this is the public, non-protobuf version
		stream := fmt.Sprintf("spot@public.deals.v3.api@%s", mexcSymbol)
		streams = append(streams, stream)
		count++
	}

	// Send single subscription message with all streams
	subscription := map[string]interface{}{
		"method": "SUBSCRIPTION",
		"params": streams,
	}

	if err := s.wsClient.SendJSON(subscription); err != nil {
		s.Logger().Error("Failed to subscribe to K-line streams", "error", err, "streams", streams)
		return
	}

	s.Logger().Info("Subscribed to MEXC WebSocket K-line streams", "count", len(streams), "streams", streams)
}

// MEXCPongResponse represents PING/PONG response from MEXC.
type MEXCPongResponse struct {
	ID   int    `json:"id"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// handleWSMessage processes incoming WebSocket messages.
func (s *MEXCSource) handleWSMessage(message []byte) {
	// Log message diagnostics - check if empty
	msgLen := len(message)
	if msgLen == 0 {
		s.Logger().Warn("MEXC WebSocket received EMPTY message")
		return
	}

	// Log message details
	hexDump := fmt.Sprintf("%x", message[:minInt(20, msgLen)])
	s.Logger().Info("MEXC WebSocket message received",
		"length", msgLen,
		"first20hex", hexDump,
		"fullMessage", string(message))

	// Check for PONG response: {"id":0,"code":0,"msg":"PONG"}
	var pongResp MEXCPongResponse
	if err := json.Unmarshal(message, &pongResp); err == nil {
		if pongResp.Msg == "PONG" {
			s.Logger().Info("Received PONG from MEXC", "code", pongResp.Code)
			return
		}

		// Check for "Blocked" message
		if s.handleMEXCBlockedMessage(pongResp) {
			return
		}
	}

	var msg MEXCWSMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		s.Logger().Warn("Failed to parse WebSocket message as deals", "error", err, "raw", string(message))
		return
	}

	s.Logger().Info("Parsed MEXC message", "channel", msg.Channel, "symbol", msg.Symbol, "hasDealsData", msg.Data != nil)

	// Handle deals (trades) updates
	if strings.Contains(msg.Channel, "deals") && msg.Data != nil && len(msg.Data.Deals) > 0 {
		s.processWSDealsUpdate(msg)
	} else {
		s.Logger().Warn("Message not handled", "channel", msg.Channel, "hasDealsData", msg.Data != nil)
	}
}

// handleMEXCBlockedMessage handles MEXC WebSocket blocked messages.
func (s *MEXCSource) handleMEXCBlockedMessage(resp MEXCPongResponse) bool {
	if !strings.Contains(resp.Msg, "Blocked") && !strings.Contains(resp.Msg, "Not Subscribed successfully") {
		return false
	}

	s.Logger().Warn("MEXC WebSocket blocked - switching to REST polling with exponential backoff",
		"message", resp.Msg,
		"backoff", s.wsRetryBackoff)

	// Mark WebSocket as blocked
	s.wsBlocked = true
	s.wsRetryTime = time.Now().Add(s.wsRetryBackoff)

	// Exponential backoff: 5min -> 15min -> 45min -> 2h15min -> 6h45min (max)
	s.wsRetryBackoff *= 3
	if s.wsRetryBackoff > 6*time.Hour {
		s.wsRetryBackoff = 6 * time.Hour // Cap at 6 hours
	}

	// Close WebSocket and start polling
	if s.wsClient != nil {
		_ = s.wsClient.Close()
	}

	// Start polling loop in background
	go s.pollLoopWhileBlocked(context.Background())

	return true
}

// processWSDealsUpdate processes a deals (trades) update from WebSocket.
func (s *MEXCSource) processWSDealsUpdate(msg MEXCWSMessage) {
	pairMappings := s.GetAllPairs()

	// Find unified symbol for this MEXC symbol
	mexcToUnified := make(map[string]string)
	for unified, mexcSymbol := range pairMappings {
		mexcToUnified[mexcSymbol] = unified
		mexcToUnified[strings.ToUpper(mexcSymbol)] = unified
	}

	unifiedSymbol, ok := mexcToUnified[msg.Symbol]
	if !ok {
		s.Logger().Debug("Unknown MEXC symbol in deals", "symbol", msg.Symbol)
		return
	}

	// Use the most recent trade price (last deal in the array)
	deals := msg.Data.Deals
	if len(deals) == 0 {
		s.Logger().Warn("No deals in message", "symbol", unifiedSymbol)
		return
	}

	lastDeal := deals[len(deals)-1]
	if lastDeal.Price == "" {
		s.Logger().Warn("Missing price in deal", "symbol", unifiedSymbol)
		return
	}

	price, err := decimal.NewFromString(lastDeal.Price)
	if err != nil {
		s.Logger().Warn("Failed to parse deal price",
			"symbol", unifiedSymbol,
			"price", lastDeal.Price,
			"error", err)
		return
	}

	now := time.Now()
	s.SetPrice(unifiedSymbol, price, now)
	s.SetLastUpdate(now)
	s.SetHealthy(true)

	s.Logger().Debug("Updated MEXC price (WebSocket deals)",
		"symbol", unifiedSymbol,
		"price", price.String(),
		"numDeals", len(deals))
}

// handleWSConnect handles WebSocket connection events.
func (s *MEXCSource) handleWSConnect() {
	s.Logger().Info("MEXC WebSocket connected")
	s.SetHealthy(true)

	// Resubscribe to tickers on reconnection
	s.Logger().Info("Resubscribing to MEXC tickers after connection")
	go s.subscribeToTickers()
}

// handleWSDisconnect handles WebSocket disconnection events.
func (s *MEXCSource) handleWSDisconnect(err error) {
	if err != nil {
		s.Logger().Warn("MEXC WebSocket disconnected", "error", err.Error())
	} else {
		s.Logger().Warn("MEXC WebSocket disconnected", "error", "no error provided")
	}
	s.SetHealthy(false)
}

// minInt returns the minimum of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
