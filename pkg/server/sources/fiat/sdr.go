package fiat

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

// SDR basket currency amounts from IMF (effective August 1, 2022)
// Source: https://www.imf.org/external/np/fin/data/rms_sdrv.aspx
// Official IMF Executive Board Decision: https://www.imf.org/en/News/Articles/2022/05/14/pr22153-imf-board-concludes-sdr-valuation-review
//
// The SDR value is calculated as: SDR/USD = Σ(currency_amount × exchange_rate)
// where exchange_rate is the current market rate in USD per unit of currency (XXX/USD)
//
// These are the official fixed amounts decided by the IMF Executive Board.
// The amounts are reviewed every 5 years (next review: 2027)
var sdrBasketAmounts = map[string]float64{
	"USD": 0.57813,  // US Dollar
	"EUR": 0.37379,  // Euro
	"CNY": 1.0993,   // Chinese Yuan (renminbi)
	"JPY": 13.452,   // Japanese Yen
	"GBP": 0.080870, // British Pound Sterling
}

// SDRSource calculates SDR/USD rate from basket currencies
// This is a fallback when IMF HTML scraping fails
type SDRSource struct {
	name          string
	symbols       []string
	timeout       time.Duration
	interval      time.Duration
	client        *http.Client
	prices        map[string]sources.Price
	pricesMu      sync.RWMutex
	lastUpdate    time.Time
	healthy       bool
	healthMu      sync.RWMutex
	subscribers   []chan<- sources.PriceUpdate
	subscribersMu sync.RWMutex
	stopChan      chan struct{}
	logger        *logging.Logger

	// Dependencies: requires other fiat sources for basket currencies
	fiatSources []sources.Source
}

func NewSDRSource(cfg map[string]interface{}) (sources.Source, error) {
	timeout := 10
	if t, ok := cfg["timeout"].(int); ok {
		timeout = t
	}

	interval := 300 // 5 minutes default
	if i, ok := cfg["interval"].(int); ok {
		interval = i
	}

	logger, err := logging.Init("info", "json", "stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return &SDRSource{
		name:        "fiat.sdr",
		symbols:     []string{"SDR"},
		timeout:     time.Duration(timeout) * time.Second,
		interval:    time.Duration(interval) * time.Second,
		client:      &http.Client{Timeout: time.Duration(timeout) * time.Second},
		prices:      make(map[string]sources.Price),
		subscribers: make([]chan<- sources.PriceUpdate, 0),
		stopChan:    make(chan struct{}),
		logger:      logger,
		fiatSources: make([]sources.Source, 0),
	}, nil
}

func (s *SDRSource) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing SDR basket calculator")
	return nil
}

// SetFiatSources allows injection of other fiat sources for basket calculation
func (s *SDRSource) SetFiatSources(sources []sources.Source) {
	s.fiatSources = sources
}

func (s *SDRSource) Start(ctx context.Context) error {
	s.logger.Info("Starting SDR basket calculator", "interval", s.interval)
	s.setHealthy(false)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Initial fetch
	if err := s.calculateSDR(); err != nil {
		s.logger.Error("Initial SDR calculation failed", "error", err)
	} else {
		s.setHealthy(true)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.stopChan:
			return nil
		case <-ticker.C:
			if err := s.calculateSDR(); err != nil {
				s.logger.Error("Failed to calculate SDR", "error", err)
				s.setHealthy(false)
			} else {
				s.setHealthy(true)
			}
		}
	}
}

func (s *SDRSource) calculateSDR() error {
	s.logger.Debug("Calculating SDR from basket currencies")

	// Collect current prices for basket currencies
	basketPrices := make(map[string]decimal.Decimal)

	// If no fiat sources provided, we can't calculate
	if len(s.fiatSources) == 0 {
		return fmt.Errorf("no fiat sources available for SDR calculation")
	}

	// Gather prices from all fiat sources
	allPrices := make(map[string]sources.Price)
	for _, source := range s.fiatSources {
		if !source.IsHealthy() {
			continue
		}

		prices, err := source.GetPrices(context.Background())
		if err != nil {
			s.logger.Warn("Failed to get prices from source", "source", source.Name(), "error", err)
			continue
		}

		for symbol, price := range prices {
			allPrices[symbol] = price
		}
	}

	// Extract basket currency prices (XXX/USD format)
	for currency := range sdrBasketAmounts {
		if currency == "USD" {
			// USD/USD = 1.0
			basketPrices[currency] = decimal.NewFromInt(1)
			continue
		}

		symbol := currency + "/USD"
		if price, ok := allPrices[symbol]; ok {
			basketPrices[currency] = price.Price
			s.logger.Debug("Found basket currency price", "currency", currency, "price", price.Price.String())
		}
	}

	// Check if we have all basket currencies
	if len(basketPrices) != 5 {
		missing := []string{}
		for currency := range sdrBasketAmounts {
			if _, ok := basketPrices[currency]; !ok {
				missing = append(missing, currency)
			}
		}
		return fmt.Errorf("missing basket currencies: %v (have %d/5)", missing, len(basketPrices))
	}

	// Calculate SDR value in USD using basket amounts
	// SDR valuation formula (from IMF):
	//   SDR/USD = Σ(currency_amount_i × exchange_rate_i)
	// where:
	//   - currency_amount_i is the fixed amount of each currency in the basket
	//   - exchange_rate_i is the current XXX/USD rate (how many USD per 1 unit of currency XXX)
	//
	// Example: If EUR amount = 0.38671 and EUR/USD = 1.16, then EUR contributes 0.44858 USD
	//
	// The currency amounts are reviewed every 5 years by the IMF (current: effective August 1, 2022)
	sdrValue := decimal.Zero
	for currency, amount := range sdrBasketAmounts {
		currencyPrice := basketPrices[currency]
		amountDecimal := decimal.NewFromFloat(amount)
		contribution := amountDecimal.Mul(currencyPrice)
		sdrValue = sdrValue.Add(contribution)

		s.logger.Debug("SDR basket contribution",
			"currency", currency,
			"amount", amount,
			"price", currencyPrice.String(),
			"contribution", contribution.String())
	}

	now := time.Now()
	newPrices := map[string]sources.Price{
		"SDR/USD": {
			Symbol:    "SDR/USD",
			Price:     sdrValue,
			Volume:    decimal.Zero,
			Timestamp: now,
			Source:    s.name,
		},
	}

	// Update stored prices
	s.pricesMu.Lock()
	s.prices["SDR/USD"] = newPrices["SDR/USD"]
	s.lastUpdate = now
	s.pricesMu.Unlock()

	// Notify subscribers
	s.notifySubscribers(newPrices, nil)

	s.logger.Info("Calculated SDR rate", "rate", sdrValue.String())

	return nil
}

func (s *SDRSource) Stop() error {
	s.logger.Info("Stopping SDR basket calculator")
	close(s.stopChan)
	return nil
}

func (s *SDRSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()

	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

func (s *SDRSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	s.subscribers = append(s.subscribers, updates)
	return nil
}

func (s *SDRSource) Name() string {
	return s.name
}

func (s *SDRSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

func (s *SDRSource) Symbols() []string {
	return s.symbols
}

func (s *SDRSource) IsHealthy() bool {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.healthy
}

func (s *SDRSource) LastUpdate() time.Time {
	s.pricesMu.RLock()
	defer s.pricesMu.RUnlock()
	return s.lastUpdate
}

func (s *SDRSource) setHealthy(healthy bool) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.healthy = healthy
}

func (s *SDRSource) notifySubscribers(prices map[string]sources.Price, err error) {
	s.subscribersMu.RLock()
	defer s.subscribersMu.RUnlock()

	update := sources.PriceUpdate{
		Source: s.name,
		Prices: prices,
		Error:  err,
	}

	for _, sub := range s.subscribers {
		select {
		case sub <- update:
		default:
			s.logger.Warn("Subscriber channel full, skipping update")
		}
	}
}
