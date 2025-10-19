package fiat

import (
	"context"
	"fmt"
	"time"

	"tc.com/oracle-prices/pkg/server/sources"
)

// FetchWithRetriesBase is the common retry logic used by all fiat sources.
// It abstracts the identical retry pattern (exponential backoff, logging, etc.)
// that was duplicated across exchangerate.go, fixer.go, frankfurter.go, imf.go, exchangerate_free.go.
func FetchWithRetriesBase(
	ctx context.Context,
	source *sources.BaseSource,
	stopChan <-chan struct{},
	fetchFunc func(context.Context) error,
) error {
	const maxRetries = 5
	const initialBackoff = time.Second
	const maxBackoff = 2 * time.Minute

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Check if we should stop
		select {
		case <-stopChan:
			return fmt.Errorf("%w", ErrSourceStoppedRetry)
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := fetchFunc(ctx)
		if err == nil {
			source.SetHealthy(true)
			return nil
		}

		lastErr = err
		source.Logger().Warn("Fetch attempt failed",
			"attempt", attempt,
			"max_retries", maxRetries,
			"error", err,
		)

		if attempt == maxRetries {
			break
		}

		// Calculate backoff with exponential growth
		// #nosec G115 -- attempt is always positive (1 to maxRetries)
		backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		source.Logger().Debug("Retrying after backoff",
			"backoff", backoff,
			"attempt", attempt+1,
		)

		select {
		case <-time.After(backoff):
			// Continue to next attempt
		case <-stopChan:
			return fmt.Errorf("%w", ErrSourceStoppedBackoff)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	source.SetHealthy(false)
	source.Logger().Error("Failed after all retries", "error", lastErr, "retries", maxRetries)
	return lastErr
}

// ParseSymbolsFromConfig extracts and validates symbols from config.
// Used by all fiat sources to reduce duplication of config parsing logic.
func ParseSymbolsFromConfig(config map[string]interface{}, providerName string) ([]string, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w", ErrSymbolsMustBeArray)
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			if str == sdrUsdPair {
				symbolStrs = append(symbolStrs, str)
			} else if isValidSymbol(str) {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("%w for provider %s", ErrNoValidSymbolsExchange, providerName)
	}

	return symbolStrs, nil
}

// isValidSymbol checks if a symbol is in the format "XXX/USD".
func isValidSymbol(symbol string) bool {
	return len(symbol) > 4 && symbol[len(symbol)-4:] == "/USD"
}
