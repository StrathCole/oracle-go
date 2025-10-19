package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

// WebSocketServer handles WebSocket connections for real-time price streaming.
type WebSocketServer struct {
	addr     string
	logger   *logging.Logger
	upgrader websocket.Upgrader

	// Client management
	mu      sync.RWMutex
	clients map[*WebSocketClient]bool

	// Price updates channel
	updates chan map[string]sources.Price

	// Server control
	ctx    context.Context
	cancel context.CancelFunc
}

// WebSocketClient represents a connected WebSocket client.
type WebSocketClient struct {
	conn            *websocket.Conn
	send            chan []byte
	server          *WebSocketServer
	subscribedAll   bool
	subscribedPairs map[string]bool
	mu              sync.RWMutex
}

// WebSocketMessage represents a client message.
type WebSocketMessage struct {
	Type    string   `json:"type"`    // "subscribe", "unsubscribe", "ping"
	Symbols []string `json:"symbols"` // List of symbols to subscribe to
}

// PriceUpdateMessage is sent to clients.
type PriceUpdateMessage struct {
	Type      string      `json:"type"`      // "price_update"
	Timestamp string      `json:"timestamp"` // ISO 8601 timestamp
	Prices    []PriceData `json:"prices"`    // List of price updates
}

// PriceData represents a single price point.
type PriceData struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
	Volume string `json:"volume,omitempty"`
	Source string `json:"source,omitempty"`
}

// NewWebSocketServer creates a new WebSocket server.
func NewWebSocketServer(addr string, logger *logging.Logger) *WebSocketServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &WebSocketServer{
		addr:   addr,
		logger: logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(_ *http.Request) bool {
				// Allow all origins (configure CORS as needed)
				return true
			},
		},
		clients: make(map[*WebSocketClient]bool),
		updates: make(chan map[string]sources.Price, 100),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start starts the WebSocket server.
func (s *WebSocketServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)

	server := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Start broadcast goroutine
	go s.broadcastUpdates()

	s.logger.Info("Starting WebSocket server", "addr", s.addr)

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("WebSocket server error", "error", err)
		}
	}()

	// Wait for context cancellation
	<-s.ctx.Done()

	// Graceful shutdown with timeout based on parent context
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// Stop stops the WebSocket server.
func (s *WebSocketServer) Stop() {
	s.cancel()
}

// SendUpdate sends price updates to all connected clients.
func (s *WebSocketServer) SendUpdate(prices map[string]sources.Price) {
	select {
	case s.updates <- prices:
	case <-time.After(100 * time.Millisecond):
		s.logger.Warn("Update channel full, dropping price update")
	}
}

// handleWebSocket handles new WebSocket connections.
func (s *WebSocketServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("Failed to upgrade connection", "error", err)
		return
	}

	client := &WebSocketClient{
		conn:            conn,
		send:            make(chan []byte, 256),
		server:          s,
		subscribedAll:   true, // Subscribe to all by default
		subscribedPairs: make(map[string]bool),
	}

	s.registerClient(client)

	// Start client goroutines
	go client.writePump()
	go client.readPump()

	s.logger.Info("New WebSocket client connected", "remote", conn.RemoteAddr())
}

// registerClient adds a client to the server.
func (s *WebSocketServer) registerClient(client *WebSocketClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[client] = true
}

// unregisterClient removes a client from the server.
func (s *WebSocketServer) unregisterClient(client *WebSocketClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[client]; ok {
		delete(s.clients, client)
		close(client.send)
	}
}

// broadcastUpdates broadcasts price updates to all clients.
func (s *WebSocketServer) broadcastUpdates() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case prices := <-s.updates:
			s.broadcast(prices)
		}
	}
}

// broadcast sends price update to all subscribed clients.
func (s *WebSocketServer) broadcast(prices map[string]sources.Price) {
	if len(prices) == 0 {
		return
	}

	// Build price data
	priceData := make([]PriceData, 0, len(prices))
	for _, price := range prices {
		priceData = append(priceData, PriceData{
			Symbol: price.Symbol,
			Price:  price.Price.String(),
			Volume: price.Volume.String(),
			Source: price.Source,
		})
	}

	message := PriceUpdateMessage{
		Type:      "price_update",
		Timestamp: time.Now().Format(time.RFC3339),
		Prices:    priceData,
	}

	data, err := json.Marshal(message)
	if err != nil {
		s.logger.Error("Failed to marshal price update", "error", err)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for client := range s.clients {
		if client.shouldReceive(prices) {
			select {
			case client.send <- data:
			default:
				s.logger.Warn("Client send buffer full, skipping update")
			}
		}
	}
}

// writePump sends messages to the WebSocket connection.
func (c *WebSocketClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Channel closed
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.server.logger.Error("Failed to write message", "error", err)
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads messages from the WebSocket connection.
func (c *WebSocketClient) readPump() {
	defer func() {
		c.server.unregisterClient(c)
		_ = c.conn.Close()
	}()

	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.server.logger.Error("WebSocket error", "error", err)
			}
			break
		}

		c.handleMessage(message)
	}
}

// handleMessage processes client messages.
func (c *WebSocketClient) handleMessage(data []byte) {
	var msg WebSocketMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		c.server.logger.Warn("Invalid client message", "error", err)
		return
	}

	switch msg.Type {
	case "subscribe":
		c.subscribe(msg.Symbols)
	case "unsubscribe":
		c.unsubscribe(msg.Symbols)
	case "ping":
		c.sendPong()
	default:
		c.server.logger.Warn("Unknown message type", "type", msg.Type)
	}
}

// subscribe subscribes to specific symbols.
func (c *WebSocketClient) subscribe(symbols []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(symbols) == 0 || (len(symbols) == 1 && symbols[0] == "*") {
		c.subscribedAll = true
		c.subscribedPairs = make(map[string]bool)
	} else {
		c.subscribedAll = false
		for _, symbol := range symbols {
			c.subscribedPairs[symbol] = true
		}
	}

	c.server.logger.Debug("Client subscribed", "symbols", symbols)
}

// unsubscribe unsubscribes from specific symbols.
func (c *WebSocketClient) unsubscribe(symbols []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(symbols) == 0 || (len(symbols) == 1 && symbols[0] == "*") {
		c.subscribedAll = false
		c.subscribedPairs = make(map[string]bool)
	} else {
		for _, symbol := range symbols {
			delete(c.subscribedPairs, symbol)
		}
	}

	c.server.logger.Debug("Client unsubscribed", "symbols", symbols)
}

// shouldReceive checks if client should receive this price update.
func (c *WebSocketClient) shouldReceive(prices map[string]sources.Price) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.subscribedAll {
		return true
	}

	// Check if any price matches subscription
	for symbol := range prices {
		if c.subscribedPairs[symbol] {
			return true
		}
	}

	return false
}

// sendPong sends a pong response.
func (c *WebSocketClient) sendPong() {
	pong := map[string]string{"type": "pong"}
	data, _ := json.Marshal(pong)
	select {
	case c.send <- data:
	default:
	}
}
