package eventstream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"tc.com/oracle-prices/pkg/feeder/client"
)

const (
	// paramsUpdateInterval is how often to poll oracle params
	paramsUpdateInterval = 10 * time.Second
)

// Stream implements EventStream using WebSocket subscription to Tendermint RPC
type Stream struct {
	logger        zerolog.Logger
	websocket     *Websocket
	client        *client.Client
	votePeriodCh  chan VotingPeriod
	paramsCh      chan Params
	closeCh       chan struct{}
	currentParams *Params
	
	// Failover support
	rpcEndpoints  []string
	currentRPC    int
}

// NewStream creates a new event stream with failover support
func NewStream(tendermintRPC string, client *client.Client, logger zerolog.Logger) (*Stream, error) {
	return NewStreamWithFailover([]string{tendermintRPC}, client, logger)
}

// NewStreamWithFailover creates a new event stream with multiple RPC endpoints for failover
func NewStreamWithFailover(rpcEndpoints []string, client *client.Client, logger zerolog.Logger) (*Stream, error) {
	if len(rpcEndpoints) == 0 {
		return nil, fmt.Errorf("at least one RPC endpoint required")
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
		logger:       logger.With().Str("component", "eventstream").Logger(),
		websocket:    ws,
		client:       client,
		votePeriodCh: make(chan VotingPeriod, 10),
		paramsCh:     make(chan Params, 10),
		closeCh:      make(chan struct{}),
		rpcEndpoints: rpcEndpoints,
		currentRPC:   0,
	}
	
	if len(rpcEndpoints) > 1 {
		stream.logger.Info().
			Int("endpoints", len(rpcEndpoints)).
			Str("primary", primaryRPC).
			Msg("Event stream initialized with failover support")
	}
	
	return stream, nil
}

// Start begins the event stream loops
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

// votingPeriodLoop listens to NewBlock events and detects voting period starts
func (s *Stream) votingPeriodLoop(ctx context.Context) {
	s.logger.Info().Msg("starting voting period loop")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("voting period loop stopped")
			return
		case <-s.closeCh:
			s.logger.Info().Msg("voting period loop closed")
			return
		case msg := <-s.websocket.Messages():
			height, err := s.parseBlockHeight(msg)
			if err != nil {
				s.logger.Error().Err(err).Msg("failed to parse block height")
				continue
			}

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

// paramsLoop periodically polls oracle params and sends updates
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

// updateParams fetches oracle params and sends update if changed
func (s *Stream) updateParams(ctx context.Context) error {
	params, err := s.client.GetOracleParams(ctx)
	if err != nil {
		return fmt.Errorf("failed to get oracle params: %w", err)
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

// parseBlockHeight extracts block height from Tendermint NewBlock event JSON
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

// VotingPeriodStarted returns a channel that signals when a new voting period begins
func (s *Stream) VotingPeriodStarted() <-chan VotingPeriod {
	return s.votePeriodCh
}

// ParamsUpdate returns a channel that signals when oracle params are updated
func (s *Stream) ParamsUpdate() <-chan Params {
	return s.paramsCh
}

// Close shuts down the event stream
func (s *Stream) Close() {
	s.logger.Info().Msg("closing event stream")
	close(s.closeCh)
	s.websocket.Close()
}

// stringSlicesEqual compares two string slices for equality
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
