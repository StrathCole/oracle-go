// Package oracle provides oracle-specific utilities and message building.
package oracle

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	oracletypes "github.com/classic-terra/core/v3/x/oracle/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog"
)

// MaxSaltNumber is the maximum salt number we can use for randomness.
// Salt length is limited to 4 characters by the oracle module.
var MaxSaltNumber = big.NewInt(9999)

// Price represents a price for a specific denomination.
type Price struct {
	Denom string  // Denomination (e.g., "LUNC", "USD", "KRW")
	Price float64 // Price value
}

// Prevote holds the prevote message along with salt and vote string for later reveal.
type Prevote struct {
	Msg  *oracletypes.MsgAggregateExchangeRatePrevote // Prevote message (hash commitment)
	Salt string                                       // Random salt (1-4 digits)
	Vote string                                       // Vote string to be revealed later (format: "price1denom1,price2denom2,...")
}

// NewPrevote creates a new prevote from prices.
// It formats prices as exchange rate tuples, generates a random salt,
// computes the hash commitment, and returns the prevote message.
//
// The vote string format is: "price1denom1,price2denom2,..."
// For example: "0.000041ulunc,111234.9uusd,3966.11ukrw"
//
// The hash is computed as: SHA256("{salt}:{vote}:{validator}").
func NewPrevote(prices []Price, validator sdk.ValAddress, feeder sdk.AccAddress, logger zerolog.Logger) (*Prevote, error) {
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", ErrNoPricesProvided)
	}

	// Sort prices by denom to ensure deterministic order
	// This is critical for hash verification - the order must be consistent
	sortedPrices := make([]Price, len(prices))
	copy(sortedPrices, prices)
	sort.Slice(sortedPrices, func(i, j int) bool {
		return sortedPrices[i].Denom < sortedPrices[j].Denom
	})

	// Build exchange rate tuples string
	voteParts := make([]string, len(sortedPrices))
	for i, price := range sortedPrices {
		// Format: convert price to Dec-compatible string
		priceStr := float64ToDec(price.Price)

		// Ensure denom starts with 'u' (micro prefix)
		denom := price.Denom
		if !strings.HasPrefix(denom, "u") && denom != "UST" {
			denom = "u" + strings.ToLower(denom)
		}

		voteParts[i] = fmt.Sprintf("%s%s", priceStr, denom)
	}
	voteStr := strings.Join(voteParts, ",")

	// Generate random salt (1-4 digits)
	saltBig, err := rand.Int(rand.Reader, MaxSaltNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}
	salt := saltBig.String()

	// Compute hash: SHA256("{salt}:{vote}:{validator}")
	hash := oracletypes.GetAggregateVoteHash(salt, voteStr, validator)

	logger.Info().
		Str("validator", validator.String()).
		Str("feeder", feeder.String()).
		Msg("Created prevote")

	logger.Debug().
		Str("salt", salt).
		Str("vote", voteStr).
		Str("hash", hash.String()).
		Str("validator", validator.String()).
		Str("feeder", feeder.String()).
		Int("num_prices", len(sortedPrices)).
		Msg("Created prevote")

	return &Prevote{
		Msg:  oracletypes.NewMsgAggregateExchangeRatePrevote(hash, feeder, validator),
		Salt: salt,
		Vote: voteStr,
	}, nil
}

// NewVote creates a vote message from a prevote.
// This reveals the salt and exchange rates that were previously committed to via hash.
func NewVote(prevote *Prevote, validator sdk.ValAddress, feeder sdk.AccAddress) *oracletypes.MsgAggregateExchangeRateVote {
	return oracletypes.NewMsgAggregateExchangeRateVote(
		prevote.Salt,
		prevote.Vote,
		feeder,
		validator,
	)
}

// VerifyPrevoteHash verifies that the prevote hash matches the on-chain prevote.
// This should be called before submitting a vote to ensure the prevote was accepted.
func VerifyPrevoteHash(prevote *Prevote, validator sdk.ValAddress, chainHash string) bool {
	localHash := oracletypes.GetAggregateVoteHash(prevote.Salt, prevote.Vote, validator).String()
	return localHash == chainHash
}

// float64ToDec converts a float64 price to a Cosmos SDK Dec-compatible string.
// It formats the float with up to 18 decimal places (Dec precision limit).
// Trailing zeros are removed for compact representation.
func float64ToDec(price float64) string {
	// Format with maximum precision (-1 means smallest possible representation)
	formattedPrice := strconv.FormatFloat(price, 'f', -1, 64)

	parts := strings.Split(formattedPrice, ".")
	intPart := parts[0]
	decPart := ""

	if len(parts) > 1 {
		decPart = parts[1]
	}

	// SDK Dec has maximum 18 decimal places
	if len(decPart) > 18 {
		decPart = decPart[:18]
	}

	// Remove trailing zeros
	decPart = strings.TrimRight(decPart, "0")

	if decPart == "" {
		return intPart
	}

	return intPart + "." + decPart
}

// ParseExchangeRates parses a comma-separated exchange rate string.
// Format: "price1denom1,price2denom2,..."
// Example: "0.000041ulunc,111234.9uusd"
//
// Returns a map of denom -> price.
func ParseExchangeRates(exchangeRates string) (map[string]sdk.Dec, error) {
	if exchangeRates == "" {
		return nil, fmt.Errorf("%w", ErrEmptyRates)
	}

	tuples, err := oracletypes.ParseExchangeRateTuples(exchangeRates)
	if err != nil {
		return nil, fmt.Errorf("failed to parse exchange rate tuples: %w", err)
	}

	result := make(map[string]sdk.Dec, len(tuples))
	for _, tuple := range tuples {
		result[tuple.Denom] = tuple.ExchangeRate
	}

	return result, nil
}

// FormatPricesForOracle converts Price slice to oracle-compatible format.
// It handles denomination prefixing and filters out invalid denoms.
func FormatPricesForOracle(prices []Price, whitelist []string) []Price {
	whitelistMap := make(map[string]bool)
	for _, denom := range whitelist {
		whitelistMap[denom] = true
	}

	var result []Price
	for _, price := range prices {
		// Add 'u' prefix if not present and not meta-denom
		denom := price.Denom
		if !strings.HasPrefix(denom, "u") && denom != "UST" {
			denom = "u" + strings.ToLower(denom)
		}

		// Only include whitelisted denoms
		if whitelistMap[denom] {
			result = append(result, Price{
				Denom: denom,
				Price: price.Price,
			})
		}
	}

	return result
}

// BuildCombinedMessages builds a combined vote + prevote message array.
// This is the standard pattern for oracle voting:
// 1. Reveal previous vote (if exists)
// 2. Submit new prevote (hash commitment)
//
// Returns messages in the order: [vote, prevote] or just [prevote] if no previous vote.
func BuildCombinedMessages(
	oldPrevote *Prevote,
	newPrevote *Prevote,
	validator sdk.ValAddress,
	feeder sdk.AccAddress,
) []sdk.Msg {
	msgs := make([]sdk.Msg, 0, 2)

	// Add vote message first (reveal old prevote)
	if oldPrevote != nil {
		voteMsg := NewVote(oldPrevote, validator, feeder)
		msgs = append(msgs, voteMsg)
	}

	// Add new prevote message (hash commitment)
	msgs = append(msgs, newPrevote.Msg)

	return msgs
}
