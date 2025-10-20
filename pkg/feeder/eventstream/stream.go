// Package eventstream provides event stream handling for the oracle feeder.
package eventstream

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/StrathCole/oracle-go/pkg/feeder/client"
)

const (
	// paramsUpdateInterval is how often to poll oracle params.
	paramsUpdateInterval = 10 * time.Second
	// maxConsecutiveFailures is the number of consecutive failures before switching RPC endpoint.
	maxConsecutiveFailures = 3
)

// Stream implements EventStream using WebSocket subscription to Tendermint RPC.
type Stream struct {
	logger        zerolog.Logger
	websocket     *Websocket
	client        *client.Client
	votePeriodCh  chan VotingPeriod
	paramsCh      chan Params
	closeCh       chan struct{}
	currentParams *Params

	// Failover support
	rpcEndpoints        []string
	currentRPC          int
	consecutiveFailures int
	failoverMu          sync.Mutex // Protects failover state
}

// NewStream creates a new event stream with failover support.
func NewStream(tendermintRPC string, client *client.Client, logger zerolog.Logger) (*Stream, error) {
	return NewStreamWithFailover([]string{tendermintRPC}, client, logger)
}

// NewStreamWithFailover creates a new event stream with multiple RPC endpoints for failover.
func NewStreamWithFailover(rpcEndpoints []string, client *client.Client, logger zerolog.Logger) (*Stream, error) {
	if len(rpcEndpoints) == 0 {
		return nil, fmt.Errorf("%w", ErrNoRPCEndpointRequired)
	}

	// Create subscription message for NewBlock events
	subscribeMsg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "subscribe",
		"id":      0,
		"params": map[string]interface{}{
			"query": "tm.event='NewBlock'",
		},
	}

	primaryRPC := rpcEndpoints[0]
	ws := NewWebsocket(primaryRPC, subscribeMsg, logger)

	stream := &Stream{
		logger:              logger.With().Str("component", "eventstream").Logger(),
		websocket:           ws,
		client:              client,
		votePeriodCh:        make(chan VotingPeriod, 10),
		paramsCh:            make(chan Params, 10),
		closeCh:             make(chan struct{}),
		rpcEndpoints:        rpcEndpoints,
		currentRPC:          0,
		consecutiveFailures: 0,
	}

	if len(rpcEndpoints) > 1 {
		stream.logger.Info().
			Int("endpoints", len(rpcEndpoints)).
			Str("primary", primaryRPC).
			Msg("Event stream initialized with failover support")
	}

	return stream, nil
}

// Start begins the event stream loops.
func (s *Stream) Start(ctx context.Context) error {
	s.logger.Info().Msg("starting event stream")

	// Start WebSocket client
	if err := s.websocket.Start(ctx); err != nil {
		return fmt.Errorf("failed to start websocket: %w", err)
	}

	// Start dual event loops
	go s.votingPeriodLoop(ctx)
	go s.paramsLoop(ctx)

	return nil
}

// votingPeriodLoop listens to NewBlock events and detects voting period starts.
func (s *Stream) votingPeriodLoop(ctx context.Context) {
	s.logger.Info().Msg("starting voting period loop")

	lastMessageTime := time.Now()
	watchdogTicker := time.NewTicker(10 * time.Second)
	defer watchdogTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("voting period loop stopped")
			return
		case <-s.closeCh:
			s.logger.Info().Msg("voting period loop closed")
			return
		case <-watchdogTicker.C:
			// Check if we've received messages recently
			if time.Since(lastMessageTime) > 45*time.Second {
				s.logger.Warn().
					Dur("since_last_message", time.Since(lastMessageTime)).
					Msg("no messages received recently, connection may be stale")
				s.handleConnectionFailure()
			}
		case msg := <-s.websocket.Messages():
			lastMessageTime = time.Now()

			// Parse block height
			height, err := s.parseBlockHeight(msg)
			if err != nil {
				s.logger.Error().Err(err).Msg("failed to parse block height, triggering failover")
				// Trigger immediate failover to next RPC endpoint
				s.handleParseFailure()
				continue
			}

			// Reset failure counter on successful parse
			s.failoverMu.Lock()
			s.consecutiveFailures = 0
			s.failoverMu.Unlock()

			// Get current oracle params
			if s.currentParams == nil {
				s.logger.Warn().Msg("oracle params not yet loaded, skipping voting period check")
				continue
			}

			// Check if this block starts a new voting period
			// Period starts when (height + 1) % votePeriod == 0
			if (height+1)%s.currentParams.VotePeriod == 0 {
				period := (height + 1) / s.currentParams.VotePeriod

				s.logger.Info().
					Uint64("height", height).
					Uint64("period", period).
					Msg("voting period started")

				vp := VotingPeriod{
					Height: height + 1,
					Period: period,
				}

				select {
				case s.votePeriodCh <- vp:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// paramsLoop periodically polls oracle params and sends updates.
func (s *Stream) paramsLoop(ctx context.Context) {
	s.logger.Info().Msg("starting params loop")

	// Fetch params immediately
	if err := s.updateParams(ctx); err != nil {
		s.logger.Error().Err(err).Msg("failed to fetch initial oracle params")
	}

	ticker := time.NewTicker(paramsUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("params loop stopped")
			return
		case <-s.closeCh:
			s.logger.Info().Msg("params loop closed")
			return
		case <-ticker.C:
			if err := s.updateParams(ctx); err != nil {
				s.logger.Error().Err(err).Msg("failed to update oracle params")
			}
		}
	}
}

// updateParams fetches oracle params and sends update if changed.
func (s *Stream) updateParams(ctx context.Context) error {
	params, err := s.client.GetOracleParams(ctx)
	if err != nil {
		return fmt.Errorf("failed to get oracle params: %w", err)
	}

	// Validate vote period
	if params.VotePeriod == 0 {
		return ErrInvalidVotePeriod
	}
	if params.VotePeriod > 10000 {
		s.logger.Warn().
			Uint64("vote_period", params.VotePeriod).
			Msg("Unusually large vote period detected")
	}

	// Extract denom names from DenomList
	whitelist := make([]string, len(params.Whitelist))
	for i, denom := range params.Whitelist {
		whitelist[i] = denom.Name
	}

	newParams := Params{
		VotePeriod: params.VotePeriod,
		Whitelist:  whitelist,
	}

	// Check if params changed
	if s.currentParams != nil {
		if s.currentParams.VotePeriod == newParams.VotePeriod &&
			stringSlicesEqual(s.currentParams.Whitelist, newParams.Whitelist) {
			// No change
			return nil
		}
	}

	s.logger.Info().
		Uint64("vote_period", newParams.VotePeriod).
		Int("whitelist_count", len(newParams.Whitelist)).
		Msg("oracle params updated")

	// Update current params
	s.currentParams = &newParams

	// Send update
	select {
	case s.paramsCh <- newParams:
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// parseBlockHeight extracts block height from Tendermint NewBlock event JSON.
func (s *Stream) parseBlockHeight(msg []byte) (uint64, error) {
	var event struct {
		Result struct {
			Data struct {
				Value struct {
					Block struct {
						Header struct {
							Height string `json:"height"`
						} `json:"header"`
					} `json:"block"`
				} `json:"value"`
			} `json:"data"`
		} `json:"result"`
	}

	if err := json.Unmarshal(msg, &event); err != nil {
		return 0, fmt.Errorf("failed to unmarshal event: %w", err)
	}

	// Parse height string to uint64
	var height uint64
	if _, err := fmt.Sscanf(event.Result.Data.Value.Block.Header.Height, "%d", &height); err != nil {
		return 0, fmt.Errorf("failed to parse height: %w", err)
	}

	return height, nil
}

// VotingPeriodStarted returns a channel that signals when a new voting period begins.
func (s *Stream) VotingPeriodStarted() <-chan VotingPeriod {
	return s.votePeriodCh
}

// ParamsUpdate returns a channel that signals when oracle params are updated.
func (s *Stream) ParamsUpdate() <-chan Params {
	return s.paramsCh
}

// Close shuts down the event stream.
func (s *Stream) Close() {
	s.logger.Info().Msg("closing event stream")
	close(s.closeCh)
	s.websocket.Close()
}

// handleConnectionFailure tracks failures and switches RPC endpoint if needed.
func (s *Stream) handleConnectionFailure() {
	s.failoverMu.Lock()
	defer s.failoverMu.Unlock()

	s.consecutiveFailures++

	s.logger.Warn().
		Int("consecutive_failures", s.consecutiveFailures).
		Int("max_failures", maxConsecutiveFailures).
		Msg("connection failure detected")

	// Switch to next endpoint after max consecutive failures
	if s.consecutiveFailures >= maxConsecutiveFailures && len(s.rpcEndpoints) > 1 {
		s.switchToNextEndpointLocked()
	}
}

// handleParseFailure handles persistent parse failures by triggering immediate RPC failover.
func (s *Stream) handleParseFailure() {
	s.failoverMu.Lock()
	defer s.failoverMu.Unlock()

	if len(s.rpcEndpoints) <= 1 {
		s.logger.Error().Msg("parse failure detected but no alternative RPC endpoints available")
		return
	}

	s.logger.Warn().Msg("triggering immediate RPC failover due to persistent parse failures")
	s.switchToNextEndpointLocked()
}

// switchToNextEndpointLocked switches to the next available RPC endpoint.
// Must be called with failoverMu held.
func (s *Stream) switchToNextEndpointLocked() {
	oldRPC := s.currentRPC
	oldURL := s.rpcEndpoints[oldRPC]

	// Move to next endpoint (circular)
	s.currentRPC = (s.currentRPC + 1) % len(s.rpcEndpoints)
	newURL := s.rpcEndpoints[s.currentRPC]

	s.logger.Warn().
		Str("old_endpoint", oldURL).
		Str("new_endpoint", newURL).
		Int("old_index", oldRPC).
		Int("new_index", s.currentRPC).
		Msg("switching to next RPC endpoint")

	// Update WebSocket URL
	s.websocket.UpdateURL(newURL)

	// Reset failure counter
	s.consecutiveFailures = 0
}

// stringSlicesEqual compares two string slices for equality.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
