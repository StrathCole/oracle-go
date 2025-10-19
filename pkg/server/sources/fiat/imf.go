package fiat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/server/sources"
)

const imfSDRURL = "https://www.imf.org/external/np/fin/data/rms_sdrv.aspx"

// IMFSource fetches SDR/USD price from IMF website (free, no API key)
// https://www.imf.org/ - Official SDR valuations
type IMFSource struct {
	*sources.BaseSource

	timeout  time.Duration
	interval time.Duration
	client   *http.Client
}

// NewIMFSourceFromConfig creates a new IMFSource from config.
func NewIMFSourceFromConfig(config map[string]interface{}) (sources.Source, error) {
	symbols, ok := config["symbols"].([]interface{})
	if !ok || len(symbols) == 0 {
		// Default to SDR only (IMF only supports SDR)
		symbols = []interface{}{"SDR"}
	}

	symbolStrs := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if str, ok := s.(string); ok {
			// IMF only provides SDR/USD - accept both "SDR" and "SDR/USD"
			if str == "SDR" || str == "SDR/USD" {
				symbolStrs = append(symbolStrs, "SDR/USD")
			}
		}
	}

	if len(symbolStrs) == 0 {
		return nil, fmt.Errorf("%w", ErrIMFRequiresSDR)
	}

	timeout := 10 * time.Second
	if t, ok := config["timeout"].(int); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	interval := 5 * time.Minute // IMF updates daily, check every 5 minutes
	if i, ok := config["interval"].(int); ok {
		interval = time.Duration(i) * time.Millisecond
	}

	// Get logger from config (passed from main.go)
	logger := sources.GetLoggerFromConfig(config)
	if logger == nil {
		return nil, fmt.Errorf("%w", ErrLoggerNotProvided)
	}

	pairs := make(map[string]string)
	for _, symbol := range symbolStrs {
		pairs[symbol] = "USD"
	}

	baseSource := sources.NewBaseSource("imf", sources.SourceTypeFiat, pairs, logger)

	s := &IMFSource{
		BaseSource: baseSource,
		timeout:    timeout,
		interval:   interval,
		client: &http.Client{
			Timeout: timeout,
		},
	}

	s.Logger().Info("Initializing IMF source", "symbols", len(s.Symbols()))
	return s, nil
}

// Initialize prepares the source.
func (s *IMFSource) Initialize(_ context.Context) error {
	return nil
}

// Start starts the IMF source.
func (s *IMFSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting IMF source")

	if err := s.fetchWithRetries(ctx); err != nil {
		s.Logger().Warn("Initial SDR price fetch failed after retries", "error", err)
	}

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
				if err := s.fetchWithRetries(ctx); err != nil {
					s.Logger().Error("Failed to fetch IMF prices", "error", err)
				}
			}
		}
	}()

	return nil
}

func (s *IMFSource) fetchWithRetries(ctx context.Context) error {
	return FetchWithRetriesBase(ctx, s.BaseSource, s.StopChan(), s.fetchPrices)
}

func (s *IMFSource) fetchPrices(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", imfSDRURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch IMF page: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", sources.ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse SDR rate from HTML
	// Look for pattern: SDR1 = US$</td><td...>1.367670<sup>4</sup></td>
	// The IMF page shows "SDR 1 = US$ X.XXXXXX" which is the rate we need
	re := regexp.MustCompile(`SDR\s*1\s*=\s*US\$\s*</td>\s*<td[^>]*>\s*([\d.]+)`)
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		return fmt.Errorf("%w", ErrSDRRateNotFound)
	}

	rateStr := string(matches[1])
	rate, err := strconv.ParseFloat(rateStr, 64)
	if err != nil {
		return fmt.Errorf("failed to parse SDR rate '%s': %w", rateStr, err)
	}

	if rate <= 0 {
		return fmt.Errorf("%w: %f", ErrInvalidSDRRate, rate)
	}

	now := time.Now()
	s.SetPrice("SDR/USD", decimal.NewFromFloat(rate), now)

	s.Logger().Info("Parsed SDR rate from IMF", "rate", rate)
	return nil
}

// Type returns the source type.
func (s *IMFSource) Type() sources.SourceType {
	return sources.SourceTypeFiat
}

// GetPrices returns the current prices.
func (s *IMFSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	return s.GetAllPrices(), nil
}

// Subscribe adds a subscriber to price updates.
func (s *IMFSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// Stop stops the IMF source.
func (s *IMFSource) Stop() error {
	s.Close()
	return nil
}
