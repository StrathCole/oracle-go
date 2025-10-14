package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

// Price represents a single price from the price server
type Price struct {
	Symbol    string          `json:"symbol"`
	Price     decimal.Decimal `json:"price"`
	Volume    decimal.Decimal `json:"volume"`
	Timestamp time.Time       `json:"timestamp"`
	Source    string          `json:"source"`
}

// Client interface for fetching prices
type Client interface {
	GetPrices(ctx context.Context) ([]Price, error)
}

// HTTPClient implements Client using HTTP requests
type HTTPClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPClient creates a new HTTP price client
func NewHTTPClient(baseURL string, timeout time.Duration) (Client, error) {
	return &HTTPClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// GetPrices fetches prices from the price server
func (c *HTTPClient) GetPrices(ctx context.Context) ([]Price, error) {
	url := c.baseURL + "/v1/prices"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("price server returned %d: %s", resp.StatusCode, string(body))
	}

	var prices []Price
	if err := json.NewDecoder(resp.Body).Decode(&prices); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return prices, nil
}
