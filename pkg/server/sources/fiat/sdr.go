package fiat

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// SDR basket currency amounts from IMF (effective August 1, 2022)
// Source: https://www.imf.org/external/np/fin/data/rms_sdrv.aspx
// Official IMF Executive Board Decision: https://www.imf.org/en/News/Articles/2022/05/14/pr22153-imf-board-concludes-sdr-valuation-review
//
// The SDR value is calculated as: SDR/USD = Σ(currency_amount × exchange_rate)
// where exchange_rate is the current market rate in USD per unit of currency (XXX/USD)
//
// These are the official fixed amounts decided by the IMF Executive Board.
// The amounts are reviewed every 5 years (next review: 2027).
var sdrBasketAmounts = map[string]float64{
	"USD": 0.57813,  // US Dollar
	"EUR": 0.37379,  // Euro
	"CNY": 1.0993,   // Chinese Yuan (renminbi)
	"JPY": 13.452,   // Japanese Yen
	"GBP": 0.080870, // British Pound Sterling
}

// SDRSource calculates SDR/USD rate from basket currencies.
// This is a fallback when IMF HTML scraping fails.
type SDRSource struct {
	*sources.BaseSource

	timeout  time.Duration
	interval time.Duration

	// Dependencies: requires other fiat sources for basket currencies
	fiatSources []sources.Source
}

// NewSDRSourceFromConfig creates a new SDRSource from config.
func NewSDRSourceFromConfig(cfg map[string]interface{}) (sources.Source, error) {
	timeout := 10
	if t, ok := cfg["timeout"].(int); ok {
		timeout = t
	}

	interval := 300 // 5 minutes default
	if i, ok := cfg["interval"].(int); ok {
		interval = i
	}

	logger := sources.GetLoggerFromConfig(cfg)

	pairs := map[string]string{
		"SDR/USD": "USD",
	}

	baseSource := sources.NewBaseSource("fiat.sdr", sources.SourceTypeFiat, pairs, logger)

	s := &SDRSource{
		BaseSource:  baseSource,
		timeout:     time.Duration(timeout) * time.Second,
		interval:    time.Duration(interval) * time.Second,
		fiatSources: make([]sources.Source, 0),
	}

	s.Logger().Info("Initializing SDR source (basket calculator)")
	return s, nil
}

// Initialize prepares the source.
func (s *SDRSource) Initialize(_ context.Context) error {
	return nil
}

// Start starts the SDR source.
func (s *SDRSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting SDR source")

	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-s.StopChan():
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.calculateSDR(ctx); err != nil {
					s.Logger().Warn("SDR calculation failed", "error", err.Error())
				}
			}
		}
	}()

	return nil
}

// SetFiatSources sets the fiat sources to use for basket calculation.
func (s *SDRSource) SetFiatSources(sources []sources.Source) {
	s.fiatSources = sources
}

func (s *SDRSource) calculateSDR(ctx context.Context) error {
	if len(s.fiatSources) == 0 {
		return fmt.Errorf("%w", ErrNoFiatSourcesAvailable)
	}

	// Collect exchange rates from fiat sources
	rates := make(map[string]decimal.Decimal)

	for _, source := range s.fiatSources {
		prices, err := source.GetPrices(ctx)
		if err != nil {
			continue
		}

		for symbol, price := range prices {
			// Extract currency code (e.g., "EUR/USD" -> "EUR")
			if len(symbol) >= 3 {
				currency := symbol[:3]
				if _, exists := sdrBasketAmounts[currency]; exists {
					rates[currency] = price.Price
				}
			}
		}
	}

	// Check if we have all required basket currencies
	missing := make([]string, 0)
	for currency := range sdrBasketAmounts {
		if _, exists := rates[currency]; !exists {
			missing = append(missing, currency)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrMissingBasketCurr, missing)
	}

	// Calculate SDR value: SDR/USD = Σ(currency_amount × exchange_rate)
	sdrValue := decimal.Zero

	for currency, amount := range sdrBasketAmounts {
		rate := rates[currency]
		contribution := rate.Mul(decimal.NewFromFloat(amount))
		sdrValue = sdrValue.Add(contribution)
	}

	if sdrValue.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("%w: %s", ErrInvalidSDRValue, sdrValue.String())
	}

	now := time.Now()
	s.SetPrice("SDR/USD", sdrValue, now)

	s.Logger().Info("Calculated SDR rate",
		"rate", sdrValue.String(),
		"currencies", len(rates),
	)

	s.SetHealthy(true)
	return nil
}

// Type returns the source type.
func (s *SDRSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

// GetPrices returns the current prices.
func (s *SDRSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *SDRSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Stop stops the SDR source.
func (s *SDRSource) Stop() error {
	s.Close()
	return nil
}
