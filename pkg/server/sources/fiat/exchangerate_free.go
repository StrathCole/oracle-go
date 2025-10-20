package fiat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// ExchangeRateFreeSource fetches fiat prices from ExchangeRate-API free tier (no API key required)
// https://www.exchangerate-api.com/docs/free
// Free tier: open.er-api.com - No API key, 1500 requests/month
// Free tier updates once per day, so we cache the next update time from API response.
type ExchangeRateFreeSource struct {
	*sources.BaseSource

	timeout        time.Duration
	minInterval    time.Duration // Minimum interval to prevent rate limiting
	nextUpdateTime time.Time     // Next update time from API response
	client         *http.Client
}

type exchangeRateFreeResponse struct {
	Result             string             `json:"result"`
	Provider           string             `json:"provider"`
	Documentation      string             `json:"documentation"`
	TermsOfUse         string             `json:"terms_of_use"`
	TimeLastUpdateUnix int64              `json:"time_last_update_unix"`
	TimeLastUpdateUTC  string             `json:"time_last_update_utc"`
	TimeNextUpdateUnix int64              `json:"time_next_update_unix"`
	TimeNextUpdateUTC  string             `json:"time_next_update_utc"`
	TimeEOLUnix        int64              `json:"time_eol_unix"`
	BaseCode           string             `json:"base_code"`
	Rates              map[string]float64 `json:"rates"`
}

// NewExchangeRateFreeSourceFromConfig creates a new ExchangeRateFreeSource from config.
func NewExchangeRateFreeSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
	symbolsIface, ok := config["symbols"]
	if !ok {
		return nil, fmt.Errorf("%w", ErrMissingSymbolsInConfig)
	}

	symbolList, ok := symbolsIface.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w", ErrInvalidSymbolsType)
	}

	symbolStrs := make([]string, 0, len(symbolList))
	for _, s := range symbolList {
		if str, ok := s.(string); ok {
			if strings.HasSuffix(str, "/USD") {
				symbolStrs = append(symbolStrs, str)
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("%w", ErrNoValidSymbolsAPI)
	}

	timeout := 5 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	// Minimum interval to prevent rate limiting
	// Free tier: 1500 req/month ≈ 50 req/day ≈ 1 req per 30 minutes
	// Set conservative minimum of 1 hour to avoid rate limits
	minInterval := 60 * time.Minute
	if i, ok := config["interval"].(int); ok {
		configInterval := time.Duration(i) * time.Millisecond
		if configInterval > minInterval {
			minInterval = configInterval
		}
	}

	logger := sources.GetLoggerFromConfig(config)

	pairs := make(map[string]string)
	for _, symbol := range symbolStrs {
		pairs[symbol] = baseCurrency
	}

	baseSource := sources.NewBaseSource("exchangerate_free", sources.SourceTypeFiat, pairs, logger)

	s := &ExchangeRateFreeSource{
		BaseSource:  baseSource,
		timeout:     timeout,
		minInterval: minInterval,
		client: &http.Client{
			Timeout: timeout,
		},
	}

	s.Logger().Info("Initializing ExchangeRate-API Free source", "symbols", len(s.Symbols()))
	return s, nil
}

// Initialize prepares the source.
func (s *ExchangeRateFreeSource) Initialize(_ context.Context) error {
	return nil
}

// Start starts the ExchangeRate-API Free source.
func (s *ExchangeRateFreeSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting ExchangeRate-API Free source")

	// Initial fetch to get the first nextUpdateTime
	if err := s.fetchWithRetries(ctx); err != nil {
		s.Logger().Warn("Initial price fetch failed after retries", "error", err)
	}

	go func() {
		for {
			select {
			case <-s.StopChan():
				return
			case <-ctx.Done():
				return
			default:
			}

			// Calculate time until next update
			var sleepDuration time.Duration
			if !s.nextUpdateTime.IsZero() {
				// Use API-provided next update time + 5 second grace period
				timeUntilUpdate := time.Until(s.nextUpdateTime)
				if timeUntilUpdate > 0 {
					sleepDuration = timeUntilUpdate + (5 * time.Second)
					s.Logger().Debug("Sleeping until next scheduled update",
						"next_update", s.nextUpdateTime.Format(time.RFC3339),
						"sleep_duration", sleepDuration,
					)
				} else {
					// Next update time has passed, fetch immediately
					sleepDuration = 0
				}
			} else {
				// No next update time available, use minimum interval
				sleepDuration = s.minInterval
				s.Logger().Debug("No next update time, using minimum interval", "interval", sleepDuration)
			}

			// Enforce minimum interval to prevent rate limiting
			if sleepDuration < s.minInterval {
				s.Logger().Debug("Enforcing minimum interval",
					"calculated", sleepDuration,
					"minimum", s.minInterval,
				)
				sleepDuration = s.minInterval
			}

			// Sleep until next update
			timer := time.NewTimer(sleepDuration)
			select {
			case <-s.StopChan():
				timer.Stop()
				return
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				// Time to fetch new prices
				_ = s.fetchWithRetries(ctx)
			}
		}
	}()

	return nil
}

func (s *ExchangeRateFreeSource) fetchWithRetries(ctx context.Context) error {
	return FetchWithRetriesBase(ctx, s.BaseSource, s.StopChan(), s.fetchPrices)
}

func (s *ExchangeRateFreeSource) fetchPrices(ctx context.Context) error {
	// ExchangeRate-API free tier endpoint
	url := "https://open.er-api.com/v6/latest/USD"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		s.Logger().Warn("Rate limit exceeded", "source", s.Name())
		s.SetHealthy(false)
		return fmt.Errorf("%w", sources.ErrRateLimitExceeded)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	var data exchangeRateFreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if data.Result != "success" {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, data.Result)
	}

	// Store next update time from API response
	if data.TimeNextUpdateUnix > 0 {
		s.nextUpdateTime = time.Unix(data.TimeNextUpdateUnix, 0)
		s.Logger().Debug("Next update time from API",
			"next_update", s.nextUpdateTime.Format(time.RFC3339),
			"time_until", time.Until(s.nextUpdateTime),
		)
	}

	now := time.Now()
	symbols := s.Symbols()
	updatedCount := 0

	for _, symbol := range symbols {
		parts := strings.Split(symbol, "/")
		if len(parts) != 2 || parts[1] != baseCurrency {
			continue
		}

		currency := parts[0]
		// ExchangeRate-API uses XDR for SDR
		if currency == "SDR" {
			currency = "XDR"
		}

		if rate, ok := data.Rates[currency]; ok {
			// Invert the rate: API gives USD per currency, we want currency per USD
			price := 1.0 / rate
			s.SetPrice(symbol, decimal.NewFromFloat(price), now)
			updatedCount++
		}
	}

	if updatedCount == 0 {
		return fmt.Errorf("%w", ErrNoPricesUpdated)
	}

	s.Logger().Debug("Updated prices from ExchangeRate-API Free",
		"count", updatedCount,
		"total_rates", len(data.Rates),
		"next_update", s.nextUpdateTime.Format(time.RFC3339),
		"time_until_next", time.Until(s.nextUpdateTime),
	)
	return nil
}

// Type returns the source type.
func (s *ExchangeRateFreeSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

// GetPrices returns the current prices.
func (s *ExchangeRateFreeSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *ExchangeRateFreeSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Stop stops the ExchangeRate-API Free source.
func (s *ExchangeRateFreeSource) Stop() error {
	s.Close()
	return nil
}
