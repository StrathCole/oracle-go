package voter

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/feeder/eventstream"
	"tc.com/oracle-prices/pkg/feeder/oracle"
	"tc.com/oracle-prices/pkg/feeder/price"
	"tc.com/oracle-prices/pkg/feeder/tx"
)

// VotingState represents the current state of the voting loop
type VotingState string

const (
	StateIdle        VotingState = "idle"
	StateFetchPrices VotingState = "fetch_prices"
	StateSubmitVote  VotingState = "submit_vote"
	StateWaitPeriod  VotingState = "wait_period"
	StateError       VotingState = "error"
)

// Voter manages the oracle voting loop
type Voter struct {
	chainID     string
	validators  []sdk.ValAddress // Validator addresses to vote for
	feeder      sdk.AccAddress   // Feeder account address
	priceClient price.Client
	grpcClient  *client.Client
	broadcaster *tx.Broadcaster
	eventStream eventstream.EventStream
	logger      zerolog.Logger

	// Voting state
	lastVotePeriod uint64
	prevotes       map[string]*oracle.Prevote // prevotes by validator address
	state          VotingState

	// Oracle parameters (updated from event stream)
	votePeriodBlocks uint64
	whitelist        []string

	// Configuration
	maxRetries    int
	retryInterval time.Duration
	gasPrice      string
	feeDenom      string
}

// Config contains voter configuration
type Config struct {
	ChainID       string
	Validators    []string // Validator addresses (bech32 encoded)
	Feeder        string   // Feeder account address (bech32 encoded)
	PriceSource   string
	MaxRetries    int
	RetryInterval time.Duration
	GasPrice      string
	FeeDenom      string
}

// NewVoter creates a new voter instance
func NewVoter(
	cfg Config,
	grpcClient *client.Client,
	broadcaster *tx.Broadcaster,
	eventStream eventstream.EventStream,
	logger zerolog.Logger,
) (*Voter, error) {
	// Initialize price client
	priceClient, err := price.NewHTTPClient(cfg.PriceSource, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create price client: %w", err)
	}

	// Parse feeder address
	feeder, err := sdk.AccAddressFromBech32(cfg.Feeder)
	if err != nil {
		return nil, fmt.Errorf("invalid feeder address: %w", err)
	}

	// Parse validator addresses
	validators := make([]sdk.ValAddress, len(cfg.Validators))
	for i, valStr := range cfg.Validators {
		val, err := sdk.ValAddressFromBech32(valStr)
		if err != nil {
			return nil, fmt.Errorf("invalid validator address %s: %w", valStr, err)
		}
		validators[i] = val
	}

	return &Voter{
		chainID:       cfg.ChainID,
		validators:    validators,
		feeder:        feeder,
		priceClient:   priceClient,
		grpcClient:    grpcClient,
		broadcaster:   broadcaster,
		eventStream:   eventStream,
		logger:        logger,
		prevotes:      make(map[string]*oracle.Prevote),
		state:         StateIdle,
		maxRetries:    cfg.MaxRetries,
		retryInterval: cfg.RetryInterval,
		gasPrice:      cfg.GasPrice,
		feeDenom:      cfg.FeeDenom,
	}, nil
}

// Start begins the voting loop
func (v *Voter) Start(ctx context.Context) error {
	v.logger.Info().
		Str("chain_id", v.chainID).
		Int("validators", len(v.validators)).
		Msg("Starting event-driven oracle voter")

	// Listen for voting period and param updates
	for {
		select {
		case <-ctx.Done():
			v.logger.Info().Msg("Voting loop stopped")
			return ctx.Err()

		case vp := <-v.eventStream.VotingPeriodStarted():
			v.logger.Info().
				Uint64("height", vp.Height).
				Uint64("period", vp.Period).
				Msg("Voting period started, performing vote cycle")

			if err := v.performVoteCycle(ctx, vp.Period); err != nil {
				v.logger.Error().
					Err(err).
					Uint64("period", vp.Period).
					Msg("Vote cycle failed")
				v.state = StateError
				// Continue to next period - don't stop on errors
			}

		case params := <-v.eventStream.ParamsUpdate():
			v.logger.Info().
				Uint64("vote_period", params.VotePeriod).
				Int("whitelist_count", len(params.Whitelist)).
				Msg("Oracle params updated")

			v.votePeriodBlocks = params.VotePeriod
			v.whitelist = params.Whitelist
		}
	}
}

// performVoteCycle executes one complete vote cycle for the given period
func (v *Voter) performVoteCycle(ctx context.Context, period uint64) error {
	v.logger.Debug().Uint64("period", period).Msg("Starting vote cycle")

	// Check if we need to vote this period
	if period == v.lastVotePeriod {
		v.logger.Debug().Uint64("period", period).Msg("Already voted in this period")
		return nil
	}

	// Fetch prices from price server
	v.state = StateFetchPrices
	prices, err := v.fetchPrices(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}

	// Use whitelist from event stream (updated via ParamsUpdate channel)
	if len(v.whitelist) == 0 {
		return fmt.Errorf("oracle whitelist not yet loaded")
	}

	// Convert prices to oracle.Price format and filter by whitelist
	oraclePrices := v.convertToOraclePrices(prices, v.whitelist)
	if len(oraclePrices) == 0 {
		return fmt.Errorf("no whitelisted prices available")
	}

	v.logger.Debug().
		Int("total_prices", len(prices)).
		Int("whitelisted", len(oraclePrices)).
		Msg("Filtered prices by whitelist")

	// Submit vote for each validator
	v.state = StateSubmitVote
	for _, validator := range v.validators {
		if err := v.submitVoteForValidator(ctx, validator, oraclePrices, period); err != nil {
			v.logger.Error().
				Err(err).
				Str("validator", validator.String()).
				Msg("Failed to submit vote for validator")
			// Continue with other validators
			continue
		}
	}

	// Update last vote period
	v.lastVotePeriod = period
	v.state = StateWaitPeriod
	v.logger.Info().Uint64("period", period).Msg("Vote cycle completed")

	return nil
}

// submitVoteForValidator submits prevote + vote for a single validator
func (v *Voter) submitVoteForValidator(ctx context.Context, validator sdk.ValAddress, prices []oracle.Price, period uint64) error {
	valKey := validator.String()

	// Create new prevote for this period
	prevote, err := oracle.NewPrevote(prices, validator, v.feeder, v.logger)
	if err != nil {
		return fmt.Errorf("failed to create prevote: %w", err)
	}

	// Build messages for transaction
	var msgs []sdk.Msg

	// If we have a pending prevote from previous period, submit the vote now
	if oldPrevote, exists := v.prevotes[valKey]; exists {
		vote := oracle.NewVote(oldPrevote, validator, v.feeder)
		msgs = append(msgs, vote)
		v.logger.Debug().Str("validator", valKey).Msg("Including vote from previous prevote")
	}

	// Add new prevote for current period
	msgs = append(msgs, prevote.Msg)

	// Submit transaction
	if err := v.broadcastVoteTx(ctx, msgs, period); err != nil {
		return fmt.Errorf("failed to broadcast vote tx: %w", err)
	}

	// Store prevote for next period
	v.prevotes[valKey] = prevote

	v.logger.Info().
		Str("validator", valKey).
		Uint64("period", period).
		Msg("Submitted vote")

	return nil
}

// broadcastVoteTx broadcasts a vote transaction with retry logic
func (v *Voter) broadcastVoteTx(ctx context.Context, msgs []sdk.Msg, period uint64) error {
	var lastErr error

	// Estimate gas
	gasLimit := tx.EstimateGas(len(msgs))

	// Calculate fee
	fee, err := tx.CalculateFee(gasLimit, v.gasPrice, v.feeDenom)
	if err != nil {
		// Fallback to default fee
		fee = tx.DefaultFee(len(msgs))
		v.logger.Warn().Err(err).Msg("Failed to calculate fee, using default")
	}

	for attempt := 0; attempt < v.maxRetries; attempt++ {
		if attempt > 0 {
			v.logger.Debug().
				Int("attempt", attempt+1).
				Int("max", v.maxRetries).
				Msg("Retrying vote submission")
			time.Sleep(v.retryInterval)
		}

		// Broadcast transaction
		txResp, err := v.broadcaster.BroadcastTx(ctx, tx.BroadcastTxRequest{
			Msgs:      msgs,
			Feeder:    v.feeder,
			FeeAmount: fee,
			GasLimit:  gasLimit,
			Memo:      fmt.Sprintf("Oracle vote period %d", period),
		})

		if err == nil {
			v.logger.Info().
				Str("tx_hash", txResp.TxHash).
				Uint64("height", uint64(txResp.Height)).
				Msg("Vote transaction broadcast successful")
			return nil
		}

		lastErr = err
		v.logger.Warn().Err(err).Int("attempt", attempt+1).Msg("Transaction broadcast failed")
	}

	return fmt.Errorf("failed after %d attempts: %w", v.maxRetries, lastErr)
}

// fetchPrices retrieves current prices from price server
func (v *Voter) fetchPrices(ctx context.Context) (map[string]decimal.Decimal, error) {
	prices, err := v.priceClient.GetPrices(ctx)
	if err != nil {
		return nil, err
	}

	// Convert to map[symbol]price
	priceMap := make(map[string]decimal.Decimal)
	for _, p := range prices {
		priceMap[p.Symbol] = p.Price
	}

	v.logger.Debug().Int("count", len(priceMap)).Msg("Fetched prices")

	return priceMap, nil
}

// convertToOraclePrices converts price map to oracle.Price slice and filters by whitelist
func (v *Voter) convertToOraclePrices(prices map[string]decimal.Decimal, whitelist []string) []oracle.Price {
	// Create set of whitelisted denoms for fast lookup
	whitelistSet := make(map[string]bool)
	for _, denom := range whitelist {
		whitelistSet[denom] = true
	}

	var result []oracle.Price
	for symbol, price := range prices {
		// Try to extract denom from symbol (e.g., "BTC/USD" -> "BTC" -> "ubtc")
		// For now, use simple conversion
		denom := symbolToDenom(symbol)

		if whitelistSet[denom] {
			priceFloat, _ := price.Float64()
			result = append(result, oracle.Price{
				Denom: denom,
				Price: priceFloat,
			})
		}
	}

	return result
}

// symbolToDenom converts a price symbol to oracle denom format
// Terra Classic oracle denoms:
// - ukrw (Korean Won)
// - usdr (Special Drawing Rights)
// - uusd (US Dollar)
// - umnt (Mongolian Tugrik)
// - UST (Meta-denom for USTC/USD, no 'u' prefix)
//
// Symbol format examples:
// - "KRW/USD" or "KRW" -> "ukrw"
// - "SDR/USD" or "SDR" -> "usdr"
// - "USD" -> "uusd" (special case, represents the price of 1 USD in LUNC)
// - "MNT/USD" or "MNT" -> "umnt"
// - "USTC/USD" or "USTC" -> "UST" (meta-denom, no prefix)
func symbolToDenom(symbol string) string {
	// Convert to uppercase for processing
	upper := strings.ToUpper(symbol)

	// Remove "/USD" or "/USDT" suffix if present (case-insensitive)
	upper = strings.TrimSuffix(upper, "/USD")
	upper = strings.TrimSuffix(upper, "/USDT")

	// Handle special cases
	switch upper {
	case "SDR", "XDR":
		return "usdr" // SDR (Special Drawing Rights)
	case "USTC", "UST":
		return "UST" // USTC meta-denom (no 'u' prefix)
	case "USD":
		return "uusd" // US Dollar
	case "KRW":
		return "ukrw" // Korean Won
	case "MNT":
		return "umnt" // Mongolian Tugrik
	}

	// For other symbols, convert to lowercase and add 'u' prefix
	// This handles potential future additions to the oracle whitelist
	return "u" + strings.ToLower(upper)
}

// GetState returns the current voting state
func (v *Voter) GetState() VotingState {
	return v.state
}

// GetLastVotePeriod returns the last period we voted in
func (v *Voter) GetLastVotePeriod() uint64 {
	return v.lastVotePeriod
}
