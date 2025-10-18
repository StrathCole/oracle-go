package websocket

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// Client represents a WebSocket client with reconnection support
type Client struct {
	url           string
	conn          *websocket.Conn
	connMu        sync.RWMutex
	reconnectWait time.Duration
	maxRetries    int
	pingInterval  time.Duration
	pongWait      time.Duration
	writeWait     time.Duration
	logger        zerolog.Logger
	headers       http.Header // Custom headers for WebSocket handshake

	// Channels
	send chan []byte
	done chan struct{}

	// Handlers
	onMessage    func([]byte)
	onConnect    func()
	onDisconnect func(error)

	// State
	connected bool
	stateMu   sync.RWMutex
	closed    bool      // Tracks if Close() has been called
	closeMu   sync.Mutex // Protects closed flag
}

// Config holds WebSocket client configuration
type Config struct {
	URL           string
	ReconnectWait time.Duration
	MaxRetries    int
	PingInterval  time.Duration
	PongWait      time.Duration
	WriteWait     time.Duration
	Logger        zerolog.Logger
	Headers       http.Header // Custom headers for WebSocket handshake
}

// NewClient creates a new WebSocket client
func NewClient(cfg Config) *Client {
	if cfg.ReconnectWait == 0 {
		cfg.ReconnectWait = 5 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = -1 // Infinite retries
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.PongWait == 0 {
		cfg.PongWait = 60 * time.Second
	}
	if cfg.WriteWait == 0 {
		cfg.WriteWait = 10 * time.Second
	}

	return &Client{
		url:           cfg.URL,
		reconnectWait: cfg.ReconnectWait,
		maxRetries:    cfg.MaxRetries,
		pingInterval:  cfg.PingInterval,
		pongWait:      cfg.PongWait,
		writeWait:     cfg.WriteWait,
		logger:        cfg.Logger,
		headers:       cfg.Headers,
		send:          make(chan []byte, 256),
		done:          make(chan struct{}),
	}
}

// SetHandlers sets the event handlers
func (c *Client) SetHandlers(onMessage func([]byte), onConnect func(), onDisconnect func(error)) {
	c.onMessage = onMessage
	c.onConnect = onConnect
	c.onDisconnect = onDisconnect
}

// Connect establishes the WebSocket connection
func (c *Client) Connect(ctx context.Context) error {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	// Use custom headers if provided (for authentication, etc.)
	conn, _, err := dialer.DialContext(ctx, c.url, c.headers)
	if err != nil {
		return err
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	c.setConnected(true)

	if c.onConnect != nil {
		c.onConnect()
	}

	c.logger.Info().Str("url", c.url).Msg("WebSocket connected")

	// Start message handlers
	go c.readPump()
	go c.writePump()
	go c.pingPump()

	return nil
}

// ConnectWithRetry connects with automatic retry
func (c *Client) ConnectWithRetry(ctx context.Context) error {
	retries := 0
	for {
		err := c.Connect(ctx)
		if err == nil {
			return nil
		}

		retries++
		if c.maxRetries > 0 && retries >= c.maxRetries {
			return errors.New("max connection retries exceeded")
		}

		c.logger.Warn().
			Err(err).
			Int("retry", retries).
			Dur("wait", c.reconnectWait).
			Msg("WebSocket connection failed, retrying...")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.reconnectWait):
			// Exponential backoff (up to 60s)
			c.reconnectWait = c.reconnectWait * 2
			if c.reconnectWait > 60*time.Second {
				c.reconnectWait = 60 * time.Second
			}
		}
	}
}

// Send sends a message to the WebSocket
func (c *Client) Send(data []byte) error {
	if !c.IsConnected() {
		return errors.New("not connected")
	}

	select {
	case c.send <- data:
		return nil
	case <-time.After(c.writeWait):
		return errors.New("send timeout")
	}
}

// SendJSON sends a JSON message
// Note: Uses full mutex lock to prevent concurrent writes to the WebSocket connection
func (c *Client) SendJSON(v interface{}) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn == nil {
		return errors.New("not connected")
	}

	c.conn.SetWriteDeadline(time.Now().Add(c.writeWait))
	return c.conn.WriteJSON(v)
}

// Close closes the WebSocket connection
func (c *Client) Close() error {
	// Prevent double-close
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()

	c.setConnected(false)
	close(c.done)

	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		err := c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		c.conn = nil
		return err
	}

	return nil
}

// IsConnected returns the connection status
func (c *Client) IsConnected() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.connected
}

func (c *Client) setConnected(connected bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.connected = connected
}

// readPump reads messages from the WebSocket
func (c *Client) readPump() {
	defer func() {
		c.reconnect()
	}()

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return
	}

	conn.SetReadDeadline(time.Now().Add(c.pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(c.pongWait))
		return nil
	})

	for {
		select {
		case <-c.done:
			return
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Error().Err(err).Msg("WebSocket read error")
			}
			return
		}

		if c.onMessage != nil {
			c.onMessage(message)
		}
	}
}

// writePump writes messages to the WebSocket
func (c *Client) writePump() {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case message := <-c.send:
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				continue
			}

			conn.SetWriteDeadline(time.Now().Add(c.writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.logger.Error().Err(err).Msg("WebSocket write error")
				return
			}
		}
	}
}

// pingPump sends periodic ping messages
func (c *Client) pingPump() {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			// Use full lock to prevent concurrent writes
			c.connMu.Lock()
			conn := c.conn
			if conn == nil {
				c.connMu.Unlock()
				continue
			}

			conn.SetWriteDeadline(time.Now().Add(c.writeWait))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			c.connMu.Unlock()
			
			if err != nil {
				c.logger.Warn().Err(err).Msg("WebSocket ping failed")
				return
			}
		}
	}
}

// reconnect attempts to reconnect after disconnection
func (c *Client) reconnect() {
	// Check if we're shutting down
	select {
	case <-c.done:
		c.logger.Info().Msg("WebSocket client shutting down, skipping reconnection")
		return
	default:
	}

	c.setConnected(false)

	if c.onDisconnect != nil {
		c.onDisconnect(errors.New("connection lost"))
	}

	c.logger.Warn().Msg("WebSocket disconnected, attempting to reconnect...")

	// Attempt reconnection
	ctx := context.Background()
	if err := c.ConnectWithRetry(ctx); err != nil {
		c.logger.Error().Err(err).Msg("WebSocket reconnection failed")
	}
}
