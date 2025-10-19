// Package voter provides oracle voting functionality.
package voter

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

// ExchangeRatePrevote represents a prevote message (hash commitment).
type ExchangeRatePrevote struct {
	Hash      string
	Denom     string
	Feeder    string
	Validator string
}

// ExchangeRateVote represents a vote message (reveal).
type ExchangeRateVote struct {
	ExchangeRates string // Comma-separated "rate:denom" pairs
	Salt          string
	Denom         string
	Feeder        string
	Validator     string
}

// VoteMessage combines prevote and vote for a single submission.
type VoteMessage struct {
	Prevote *ExchangeRatePrevote
	Vote    *ExchangeRateVote
}

// PriceVote represents a single price vote for aggregation.
type PriceVote struct {
	Denom string
	Price decimal.Decimal
}

// GenerateSalt creates a cryptographically secure random salt for vote hashing.
func GenerateSalt() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const saltLength = 64

	// Use crypto/rand for cryptographically secure random numbers
	randomBytes := make([]byte, saltLength)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to hex encoding of random bytes if charset selection fails
		return hex.EncodeToString(randomBytes)[:saltLength]
	}

	salt := make([]byte, saltLength)
	for i := range salt {
		salt[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return string(salt)
}

// BuildExchangeRatesString formats prices into "rate:denom" string.
// Example: "123.456ulunc,0.987ukrw,1.0usdr".
func BuildExchangeRatesString(prices map[string]decimal.Decimal) string {
	// Sort denoms for consistent ordering
	denoms := make([]string, 0, len(prices))
	for denom := range prices {
		denoms = append(denoms, denom)
	}
	sort.Strings(denoms)

	// Build rate:denom pairs
	pairs := make([]string, 0, len(prices))
	for _, denom := range denoms {
		price := prices[denom]
		// Format: price with max 18 decimals + denom
		pairs = append(pairs, price.String()+denom)
	}

	return strings.Join(pairs, ",")
}

// BuildPrevote creates a prevote message with hash commitment.
func BuildPrevote(salt, exchangeRates, validator, feeder string) *ExchangeRatePrevote {
	// Hash format: SHA256(salt + exchange_rates + validator)
	// This matches Terra Classic oracle module expected format
	preimage := salt + ":" + exchangeRates + ":" + validator
	hash := sha256.Sum256([]byte(preimage))
	hashHex := hex.EncodeToString(hash[:])

	return &ExchangeRatePrevote{
		Hash:      hashHex,
		Denom:     "uusd", // Always uusd for Terra Classic
		Feeder:    feeder,
		Validator: validator,
	}
}

// BuildVote creates a vote message (reveal of prevote).
func BuildVote(salt, exchangeRates, validator, feeder string) *ExchangeRateVote {
	return &ExchangeRateVote{
		ExchangeRates: exchangeRates,
		Salt:          salt,
		Denom:         "uusd", // Always uusd for Terra Classic
		Feeder:        feeder,
		Validator:     validator,
	}
}

// VerifyVoteMatchesPrevote checks if vote reveals the prevote correctly.
func VerifyVoteMatchesPrevote(prevote *ExchangeRatePrevote, vote *ExchangeRateVote) error {
	// Recompute hash from vote
	preimage := vote.Salt + ":" + vote.ExchangeRates + ":" + vote.Validator
	hash := sha256.Sum256([]byte(preimage))
	hashHex := hex.EncodeToString(hash[:])

	if hashHex != prevote.Hash {
		return fmt.Errorf("%w: expected %s, got %s", ErrVoteHashMismatch, prevote.Hash, hashHex)
	}

	return nil
}

// FilterPricesByWhitelist removes non-whitelisted denoms.
func FilterPricesByWhitelist(prices map[string]decimal.Decimal, whitelist []string) map[string]decimal.Decimal {
	filtered := make(map[string]decimal.Decimal)
	whitelistMap := make(map[string]bool)
	for _, denom := range whitelist {
		whitelistMap[denom] = true
	}

	for denom, price := range prices {
		if whitelistMap[denom] {
			filtered[denom] = price
		}
	}

	return filtered
}

// ValidateExchangeRates checks if exchange rates string is valid.
func ValidateExchangeRates(exchangeRates string) error {
	if exchangeRates == "" {
		return fmt.Errorf("%w", ErrEmptyExchangeRates)
	}

	pairs := strings.Split(exchangeRates, ",")
	if len(pairs) == 0 {
		return fmt.Errorf("%w", ErrNoExchangeRatePairs)
	}

	for _, pair := range pairs {
		// Each pair should be "number<denom>"
		// Example: "0.000123ulunc" or "1000.0ukrw"
		if pair == "" {
			return fmt.Errorf("%w", ErrEmptyExchangeRatePair)
		}

		// Find where number ends and denom starts (first letter after numbers/decimal)
		denomStart := 0
		for i, c := range pair {
			if (c < '0' || c > '9') && c != '.' && c != '-' {
				denomStart = i
				break
			}
		}

		if denomStart == 0 {
			return fmt.Errorf("%w: %s", ErrInvalidPairFormat, pair)
		}

		rateStr := pair[:denomStart]
		denom := pair[denomStart:]

		// Validate rate is a number
		if _, err := decimal.NewFromString(rateStr); err != nil {
			return fmt.Errorf("%w: pair=%s, %w", ErrInvalidRateInPair, pair, err)
		}

		// Validate denom is not empty
		if denom == "" {
			return fmt.Errorf("%w: %s", ErrMissingDenomInPair, pair)
		}
	}

	return nil
}
