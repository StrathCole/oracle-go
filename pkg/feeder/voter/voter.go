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
	dryRun        bool // If true, create votes but don't submit them
	verify        bool // If true (with DryRun), verify votes against on-chain rates

	// Verification state
	lastVotePrices map[string]sdk.Dec // Last submitted vote prices (denom -> rate)
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
	DryRun        bool // If true, create votes but don't submit them
	Verify        bool // If true (with DryRun), verify votes against on-chain rates
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
		chainID:        cfg.ChainID,
		validators:     validators,
		feeder:         feeder,
		priceClient:    priceClient,
		grpcClient:     grpcClient,
		broadcaster:    broadcaster,
		eventStream:    eventStream,
		logger:         logger,
		prevotes:       make(map[string]*oracle.Prevote),
		state:          StateIdle,
		maxRetries:     cfg.MaxRetries,
		retryInterval:  cfg.RetryInterval,
		gasPrice:       cfg.GasPrice,
		feeDenom:       cfg.FeeDenom,
		dryRun:         cfg.DryRun,
		verify:         cfg.Verify,
		lastVotePrices: make(map[string]sdk.Dec),
	}, nil
}

// Start begins the voting loop
func (v *Voter) Start(ctx context.Context) error {
	v.logger.Info().
		Str("chain_id", v.chainID).
		Int("validators", len(v.validators)).
		Bool("dry_run", v.dryRun).
		Bool("verify", v.verify).
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

			// If verification is enabled, verify the previous vote before starting new cycle
			if v.verify && v.dryRun && vp.Period > 0 {
				v.logger.Info().
					Uint64("period", vp.Period-1).
					Msg("Verifying previous period's vote against on-chain rates")

				if err := v.verifyVotesAgainstChain(ctx); err != nil {
					v.logger.Error().
						Err(err).
						Msg("Vote verification failed")
					// Continue despite verification error
				}
			}

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

	v.logger.Debug().
		Int("total_prices", len(prices)).
		Strs("whitelist", v.whitelist).
		Interface("price_symbols", func() []string {
			symbols := make([]string, 0, len(prices))
			for symbol := range prices {
				symbols = append(symbols, symbol)
			}
			return symbols
		}()).
		Msg("Price fetching complete")

	// Convert prices to oracle.Price format and filter by whitelist
	oraclePrices := v.convertToOraclePrices(prices, v.whitelist)
	if len(oraclePrices) == 0 {
		v.logger.Error().
			Int("total_prices", len(prices)).
			Strs("whitelist", v.whitelist).
			Msg("No whitelisted prices available - check symbol to denom conversion")
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
		v.logger.Info().
			Str("validator", valKey).
			Str("salt", oldPrevote.Salt).
			Str("vote_string", oldPrevote.Vote).
			Str("prevote_hash", oldPrevote.Msg.Hash).
			Msg("Including vote from previous prevote")
	}

	// Add new prevote for current period
	msgs = append(msgs, prevote.Msg)

	// Submit transaction
	if err := v.broadcastVoteTx(ctx, msgs, period); err != nil {
		// Clear any stored prevote on failure - we cannot vote for a prevote
		// that wasn't successfully submitted on-chain
		if _, hadPrevote := v.prevotes[valKey]; hadPrevote {
			delete(v.prevotes, valKey)
			v.logger.Warn().
				Str("validator", valKey).
				Msg("Cleared stored prevote after transaction failure")
		}

		// Immediately retry with fresh prevote-only transaction to minimize missed votes
		// This ensures we only miss one period instead of two
		v.logger.Info().
			Str("validator", valKey).
			Msg("Attempting immediate recovery with fresh prevote")

		freshPrevote, err := oracle.NewPrevote(prices, validator, v.feeder, v.logger)
		if err != nil {
			v.logger.Error().Err(err).Msg("Failed to create fresh prevote for recovery")
			return fmt.Errorf("failed to broadcast vote tx and recovery failed: %w", err)
		}

		// Try to submit just the fresh prevote (no vote since we have nothing to vote for)
		if err := v.broadcastVoteTx(ctx, []sdk.Msg{freshPrevote.Msg}, period); err != nil {
			v.logger.Error().
				Err(err).
				Str("validator", valKey).
				Msg("Recovery prevote also failed - will retry next period")
			return fmt.Errorf("failed to broadcast vote tx and recovery prevote: %w", err)
		}

		// Recovery successful - store the fresh prevote
		v.prevotes[valKey] = freshPrevote
		v.logger.Info().
			Str("validator", valKey).
			Str("fresh_prevote_hash", freshPrevote.Msg.Hash).
			Msg("Recovery successful - fresh prevote submitted")

		return nil
	}

	// Store prevote for next period (only if transaction succeeded)
	v.prevotes[valKey] = prevote

	// Store the current vote prices for verification (if enabled)
	if v.verify && v.dryRun {
		// Parse the vote string to extract prices for verification
		votePrices, err := oracle.ParseExchangeRates(prevote.Vote)
		if err != nil {
			v.logger.Warn().
				Err(err).
				Str("vote_string", prevote.Vote).
				Msg("Failed to parse vote for verification storage")
		} else {
			v.lastVotePrices = votePrices
			v.logger.Debug().
				Int("stored_prices", len(votePrices)).
				Msg("Stored vote prices for verification")
		}
	}

	v.logger.Info().
		Str("validator", valKey).
		Uint64("period", period).
		Str("new_prevote_hash", prevote.Msg.Hash).
		Str("new_vote_string", prevote.Vote).
		Msg("Submitted vote")

	return nil
}

// broadcastVoteTx broadcasts a vote transaction with retry logic
func (v *Voter) broadcastVoteTx(ctx context.Context, msgs []sdk.Msg, period uint64) error {
	// Dry-run mode: log the vote details but don't actually submit
	if v.dryRun {
		v.logger.Warn().Msg("DRY RUN MODE: Transaction would be broadcast but is skipped")

		for i, msg := range msgs {
			// Use String() to get message representation
			msgStr := msg.String()
			msgType := fmt.Sprintf("%T", msg)

			// Try to identify message type from string representation
			if strings.Contains(msgType, "MsgAggregateExchangeRatePrevote") {
				v.logger.Info().
					Int("msg_index", i).
					Str("type", "prevote").
					Str("message", msgStr).
					Uint64("period", period).
					Msg("DRY RUN: Would submit prevote")
			} else if strings.Contains(msgType, "MsgAggregateExchangeRateVote") {
				v.logger.Info().
					Int("msg_index", i).
					Str("type", "vote").
					Str("message", msgStr).
					Uint64("period", period).
					Msg("DRY RUN: Would submit vote")
			} else {
				v.logger.Info().
					Int("msg_index", i).
					Str("type", msgType).
					Str("message", msgStr).
					Msg("DRY RUN: Would submit message")
			}
		}

		return nil // Pretend success in dry-run mode
	}

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

			// Wait for transaction to be included in a block and check result
			return v.verifyTxResult(ctx, txResp.TxHash)
		}

		lastErr = err
		v.logger.Warn().Err(err).Int("attempt", attempt+1).Msg("Transaction broadcast failed")
	}

	return fmt.Errorf("failed after %d attempts: %w", v.maxRetries, lastErr)
}

// verifyTxResult waits for a transaction to be included in a block and verifies it succeeded
func (v *Voter) verifyTxResult(ctx context.Context, txHash string) error {
	// GetTx now has built-in retry logic with exponential backoff (up to 10 attempts)
	// No need for manual retry loop here
	txResp, err := v.grpcClient.GetTx(ctx, txHash)
	if err != nil {
		v.logger.Error().
			Err(err).
			Str("tx_hash", txHash).
			Msg("Failed to query transaction after retries")
		return fmt.Errorf("failed to verify tx %s: %w", txHash, err)
	}

	// Check if transaction succeeded
	if txResp.Code != 0 {
		v.logger.Error().
			Str("tx_hash", txHash).
			Uint32("code", txResp.Code).
			Str("raw_log", txResp.RawLog).
			Msg("Transaction execution failed")
		return fmt.Errorf("tx %s failed with code %d: %s", txHash, txResp.Code, txResp.RawLog)
	}

	v.logger.Info().
		Str("tx_hash", txHash).
		Uint64("height", uint64(txResp.Height)).
		Uint64("gas_used", uint64(txResp.GasUsed)).
		Msg("Transaction executed successfully")
	return nil
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

// convertToOraclePrices converts price map to oracle.Price slice and filters by whitelist.
// CRITICAL: Oracle expects exchange rates in terms of LUNC, not USD!
// Format: "How many of this currency equals 1 LUNC"
//
// Price server gives us Fiat/USD rates (e.g., AUD/USD = 0.649 means 1 AUD = 0.649 USD)
// We need to convert to Fiat/LUNC (how many fiat per 1 LUNC)
//
// Formula: fiat_per_lunc = (1 / fiat_per_usd) × lunc_per_usd
// Example: If AUD/USD = 0.649 and LUNC/USD = 0.00004122
//
//	Then AUD/LUNC = (1 / 0.649) × 0.00004122 = 1.54 × 0.00004122 = 0.0000634
func (v *Voter) convertToOraclePrices(prices map[string]decimal.Decimal, whitelist []string) []oracle.Price {
	// Create set of whitelisted denoms for fast lookup
	whitelistSet := make(map[string]bool)
	for _, denom := range whitelist {
		whitelistSet[denom] = true
	}

	// Get LUNC/USD price for conversion
	// Try common variants: LUNC/USD or LUNC/USDT
	// Note: All symbols are normalized at source level (BaseSource.SetPrice)
	// and validated to be in BASE/QUOTE format during config parsing
	luncUSD, hasLuncUSD := prices["LUNC/USD"]
	if !hasLuncUSD {
		if p, ok := prices["LUNC/USDT"]; ok {
			luncUSD = p
			hasLuncUSD = true
		} else if p, ok := prices["LUNC/USDC"]; ok {
			luncUSD = p
			hasLuncUSD = true
		}
	}

	if !hasLuncUSD {
		v.logger.Error().Msg("LUNC/USD price not found - cannot convert fiat prices to LUNC terms")
		return nil
	}

	luncUSDFloat, _ := luncUSD.Float64()
	v.logger.Debug().
		Float64("lunc_usd", luncUSDFloat).
		Msg("Using LUNC/USD price for fiat conversions")

	var result []oracle.Price
	var conversions []string
	var filtered []string

	// Track which whitelisted denoms we've found
	foundDenoms := make(map[string]bool)

	for symbol, price := range prices {
		denom := symbolToDenom(symbol)

		// Skip LUNC itself - we'll handle it separately
		if symbol == "LUNC/USD" || symbol == "LUNC/USDT" || symbol == "LUNC/USDC" {
			continue
		}

		if !whitelistSet[denom] {
			filtered = append(filtered, fmt.Sprintf("%s(%s)", symbol, denom))
			continue
		}

		priceFloat, _ := price.Float64()
		if priceFloat == 0 {
			v.logger.Warn().Str("symbol", symbol).Msg("Zero price for symbol, skipping")
			continue
		}

		// Convert fiat/USD to fiat/LUNC
		// Price server gives us: Fiat/USD (e.g., AUD/USD = 0.649 means 1 AUD = 0.649 USD)
		// We need: Fiat/LUNC (how many fiat per 1 LUNC)
		// Formula: fiat_per_lunc = (1 / fiat_per_usd) * lunc_per_usd
		//        = (usd_per_fiat) * (lunc/usd)
		// Example: If AUD/USD = 0.649 and LUNC/USD = 0.00004122
		//          Then AUD/LUNC = (1 / 0.649) * 0.00004122 = 1.54 * 0.00004122 = 0.0000634
		// Note: This applies to ALL fiat currencies including SDR

		usdPerFiat := 1.0 / priceFloat           // Invert: USD/Fiat
		fiatPerLunc := usdPerFiat * luncUSDFloat // USD/Fiat × LUNC/USD = Fiat/LUNC

		result = append(result, oracle.Price{
			Denom: denom,
			Price: fiatPerLunc,
		})
		foundDenoms[denom] = true
		conversions = append(conversions, fmt.Sprintf("%s->%s: (1/%.6f) * %.8f = %.10f",
			symbol, denom, priceFloat, luncUSDFloat, fiatPerLunc))
	}

	// Special handling: Add uusd (USD/LUNC rate)
	if whitelistSet["uusd"] && !foundDenoms["uusd"] {
		result = append(result, oracle.Price{
			Denom: "uusd",
			Price: luncUSDFloat,
		})
		foundDenoms["uusd"] = true
		conversions = append(conversions, fmt.Sprintf("LUNC/USD->uusd: %.8f", luncUSDFloat))
		v.logger.Debug().
			Float64("uusd", luncUSDFloat).
			Msg("Added uusd (LUNC/USD price)")
	}

	// Special handling: Ensure UST (USTC/USD) is included if in whitelist
	// This should already be handled by symbolToDenom("USTC/USD") -> "UST"
	// but we log it for visibility
	if whitelistSet["UST"] && foundDenoms["UST"] {
		v.logger.Debug().Msg("UST (USTC/USD price) included in vote")
	}

	// Add abstain votes (0.0) for missing whitelisted denoms
	// This allows validators to participate even when some prices are unavailable
	// Following old TypeScript feeder behavior: missing denoms get price "0.000000"
	var abstainDenoms []string
	for _, denom := range whitelist {
		if !foundDenoms[denom] && denom != "UST" { // Skip UST meta-denom check
			result = append(result, oracle.Price{
				Denom: denom,
				Price: 0.0, // Abstain vote
			})
			abstainDenoms = append(abstainDenoms, denom)
		}
	}

	if len(abstainDenoms) > 0 {
		v.logger.Info().
			Strs("denoms", abstainDenoms).
			Msg("Added abstain votes (0.0) for missing whitelisted denoms")
	} else if whitelistSet["UST"] && !foundDenoms["UST"] {
		v.logger.Warn().Msg("UST in whitelist but USTC/USD price not found")
	}

	v.logger.Debug().
		Strs("filtered_out", filtered).
		Int("matched", len(result)).
		Msg("Price to denom conversion complete")

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

	// Remove "/USD", "/USDT", "/USDC" suffix if present (case-insensitive)
	upper = strings.TrimSuffix(upper, "/USD")
	upper = strings.TrimSuffix(upper, "/USDT")
	upper = strings.TrimSuffix(upper, "/USDC")

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

// verifyVotesAgainstChain compares the last submitted vote with on-chain exchange rates
// and logs any deviations > 1%
func (v *Voter) verifyVotesAgainstChain(ctx context.Context) error {
	v.logger.Debug().
		Bool("verify", v.verify).
		Bool("dry_run", v.dryRun).
		Int("stored_prices", len(v.lastVotePrices)).
		Msg("verifyVotesAgainstChain called")

	if !v.verify || !v.dryRun {
		v.logger.Debug().
			Bool("verify", v.verify).
			Bool("dry_run", v.dryRun).
			Msg("Skipping verification (not enabled or not in dry-run mode)")
		return nil // Only verify in dry-run mode with --verify flag
	}

	if len(v.lastVotePrices) == 0 {
		v.logger.Info().Msg("No previous vote to verify (this is normal for the first period)")
		return nil
	}

	v.logger.Info().Msg("Starting vote verification against on-chain rates...")

	// Query on-chain exchange rates
	exchangeRates, err := v.grpcClient.GetExchangeRates(ctx)
	if err != nil {
		return fmt.Errorf("failed to query on-chain exchange rates: %w", err)
	}

	v.logger.Info().
		Int("vote_denoms", len(v.lastVotePrices)).
		Int("chain_denoms", len(exchangeRates)).
		Msg("Starting vote verification against on-chain rates")

	// Compare each price
	for denom, votedRate := range v.lastVotePrices {
		// Find on-chain rate for this denom
		var chainRate sdk.Dec
		found := false
		for _, coin := range exchangeRates {
			if coin.Denom == denom {
				chainRate = coin.Amount
				found = true
				break
			}
		}

		if !found {
			v.logger.Warn().
				Str("denom", denom).
				Msg("Denom not found in on-chain rates (may not have enough votes)")
			continue
		}

		// Calculate percentage difference
		// diff = (voted - chain) / chain * 100
		diff := votedRate.Sub(chainRate).Quo(chainRate).MulInt64(100)
		diffAbs := diff.Abs()
		diffPercent, _ := diffAbs.Float64()

		// Compare against 1.0 since diff is already in percentage (multiplied by 100)
		if diffAbs.GTE(sdk.OneDec()) { // >= 1%
			v.logger.Warn().Msgf("⚠️  %s: DEVIATION %.4f%% (voted=%s, chain=%s)",
				denom, diffPercent, votedRate.String(), chainRate.String())
		} else {
			v.logger.Info().Msgf("✓ %s: %.4f%% diff", denom, diffPercent)
		}
	}

	v.logger.Info().Msg("Vote verification complete")
	return nil
}
