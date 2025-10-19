package eventstream

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

const (
	// maxReconnectBackoff is the maximum wait time between reconnection attempts.
	maxReconnectBackoff = 30 * time.Second
	// initialReconnectBackoff is the starting backoff duration.
	initialReconnectBackoff = 1 * time.Second
	// pingInterval is how often to send ping messages.
	pingInterval = 30 * time.Second
	// pongTimeout is how long to wait for pong response.
	pongTimeout = 60 * time.Second
)

// Websocket manages a connection to Tendermint RPC WebSocket endpoint.
type Websocket struct {
	url            string
	subscribeMsg   interface{}
	logger         zerolog.Logger
	conn           *websocket.Conn
	mu             sync.RWMutex
	messages       chan []byte
	closed         chan struct{}
	reconnectDelay time.Duration
}

// NewWebsocket creates a new WebSocket client.
func NewWebsocket(url string, subscribeMsg interface{}, logger zerolog.Logger) *Websocket {
	return &Websocket{
		url:            url,
		subscribeMsg:   subscribeMsg,
		logger:         logger.With().Str("component", "websocket").Logger(),
		messages:       make(chan []byte, 100),
		closed:         make(chan struct{}),
		reconnectDelay: initialReconnectBackoff,
	}
}

// Start begins the WebSocket connection loop with reconnection.
func (w *Websocket) Start(ctx context.Context) error {
	w.logger.Info().Str("url", w.url).Msg("starting websocket client")

	go w.loop(ctx)
	return nil
}

// loop maintains the WebSocket connection with automatic reconnection.
func (w *Websocket) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.logger.Info().Msg("context cancelled, stopping websocket")
			return
		case <-w.closed:
			w.logger.Info().Msg("websocket closed")
			return
		default:
			if err := w.connect(ctx); err != nil {
				w.logger.Error().Err(err).Msg("failed to connect to websocket")

				// Add jitter to prevent thundering herd problem
				// Jitter is random value between 0 and reconnectDelay/2
				jitter := time.Duration(rand.Int63n(int64(w.reconnectDelay) / 2))
				waitTime := w.reconnectDelay + jitter

				w.logger.Warn().
					Dur("backoff", waitTime).
					Msg("reconnecting after backoff")

				select {
				case <-ctx.Done():
					return
				case <-time.After(waitTime):
					// Exponential backoff with cap
					w.reconnectDelay *= 2
					if w.reconnectDelay > maxReconnectBackoff {
						w.reconnectDelay = maxReconnectBackoff
					}
					continue
				}
			}

			// Reset backoff on successful connection
			w.reconnectDelay = initialReconnectBackoff

			// Read messages until connection fails
			if err := w.readLoop(ctx); err != nil {
				w.logger.Error().Err(err).Msg("websocket read error")
			}
		}
	}
}

// connect establishes WebSocket connection and sends subscribe message.
func (w *Websocket) connect(ctx context.Context) error {
	w.logger.Info().Str("url", w.url).Msg("connecting to websocket")

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, w.url, nil)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()

	// Send subscription message
	if err := conn.WriteJSON(w.subscribeMsg); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to send subscribe message: %w", err)
	}

	w.logger.Info().Msg("websocket connected and subscribed")
	return nil
}

// readLoop reads messages from the WebSocket connection.
func (w *Websocket) readLoop(ctx context.Context) error {
	w.mu.RLock()
	conn := w.conn
	w.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("%w", ErrNoConnection)
	}

	// Set up pong handler
	conn.SetPongHandler(func(_ string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	// Set initial read deadline
	_ = conn.SetReadDeadline(time.Now().Add(pongTimeout))

	// Start ping ticker
	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	// Read messages in goroutine
	messageCh := make(chan []byte, 10)
	errorCh := make(chan error, 1)

	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				errorCh <- err
				return
			}

			// Skip subscription confirmation messages
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(msg, &response); err == nil {
				if response.Result != nil && string(response.Result) == "{}" {
					// Subscription confirmation, skip
					w.logger.Debug().Msg("subscription confirmed")
					continue
				}
			}

			messageCh <- msg
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.closed:
			return nil
		case <-pingTicker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return fmt.Errorf("ping failed: %w", err)
			}
		case err := <-errorCh:
			return err
		case msg := <-messageCh:
			select {
			case w.messages <- msg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Messages returns the channel for receiving WebSocket messages.
func (w *Websocket) Messages() <-chan []byte {
	return w.messages
}

// Close closes the WebSocket connection.
func (w *Websocket) Close() {
	close(w.closed)

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn != nil {
		_ = w.conn.Close()
		w.conn = nil
	}
}
