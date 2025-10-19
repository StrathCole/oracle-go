package cosmwasm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/StrathCole/oracle-go/pkg/feeder/client"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/shopspring/decimal"
)

const (
	terraportQueryTimeout   = 10 * time.Second
	terraportUpdateInterval = 15 * time.Second
)

// TerraportSource fetches prices from Terraport DEX pairs via gRPC smart contract queries.
type TerraportSource struct {
	*sources.BaseSource
	grpcClient     *client.Client
	updateInterval time.Duration
	pairs          []TerraportPair
}

// TerraportPairConfig represents configuration for a Terraport liquidity pair.
type TerraportPairConfig struct {
	Symbol          string // e.g., "LUNC/USDC"
	ContractAddress string // Terraport pair contract address
	Asset0Denom     string // First asset denom (e.g., "uluna")
	Asset1Denom     string // Second asset denom (e.g., "ibc/...")
	Decimals0       int    // Decimals for asset 0
	Decimals1       int    // Decimals for asset 1
}

// TerraportPair is an alias for TerraportPairConfig.
type TerraportPair = TerraportPairConfig

// PoolResponse represents the response from querying a Terraport pair.
type PoolResponse struct {
	Assets []struct {
		Info struct {
			NativeToken *struct {
				Denom string `json:"denom"`
			} `json:"native_token,omitempty"`
			Token *struct {
				ContractAddr string `json:"contract_addr"`
			} `json:"token,omitempty"`
		} `json:"info"`
		Amount string `json:"amount"`
	} `json:"assets"`
}

// TerraportConfig holds configuration for creating a Terraport source.
type TerraportConfig struct {
	Pairs      []TerraportPair
	GRPCClient *client.Client
	Interval   time.Duration
}

// NewTerraportSource creates a new Terraport source using gRPC client.
func NewTerraportSource(config map[string]interface{}, grpcClient *client.Client) (sources.Source, error) {
	// Parse CosmWasm pairs configuration using helper
	pairs, err := sources.ParseCosmWasmPairs(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	// Initialize base source using helper
	base, updateInterval, err := InitializeCosmWasmBase(
		"terraport",
		sources.SourceTypeCosmWasm,
		pairs,
		terraportUpdateInterval,
		config,
	)
	if err != nil {
		return nil, err
	}

	// Convert to TerraportPair array
	terraportPairs := make([]TerraportPair, 0, len(pairs))
	for _, p := range pairs {
		terraportPairs = append(terraportPairs, TerraportPair{
			Symbol:          p.Symbol,
			ContractAddress: p.ContractAddress,
			Asset0Denom:     p.Asset0Denom,
			Asset1Denom:     p.Asset1Denom,
			Decimals0:       p.Decimals0,
			Decimals1:       p.Decimals1,
		})
	}

	return &TerraportSource{
		BaseSource:     base,
		grpcClient:     grpcClient,
		updateInterval: updateInterval,
		pairs:          terraportPairs,
	}, nil
}

// Initialize prepares the source.
func (s *TerraportSource) Initialize(_ context.Context) error {
	s.Logger().Info("Initializing Terraport source",
		"pairs", len(s.pairs),
		"grpc_endpoint", s.grpcClient.CurrentEndpoint())
	return nil
}

// Start begins fetching prices.
func (s *TerraportSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Terraport source")

	// Initial fetch
	if err := s.fetchPrices(ctx); err != nil {
		s.Logger().Warn("Initial price fetch failed", "error", err)
	}

	// Start ticker for periodic updates
	ticker := time.NewTicker(s.updateInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.Logger().Info("Stopping Terraport source")
				return
			case <-s.StopChan():
				s.Logger().Info("Terraport source stopped")
				return
			case <-ticker.C:
				if err := s.fetchPrices(ctx); err != nil {
					s.Logger().Error("Failed to fetch prices", "error", err)
					s.SetHealthy(false)
				}
			}
		}
	}()

	return nil
}

// Stop halts the source.
func (s *TerraportSource) Stop() error {
	s.Close()
	return nil
}

// GetPrices returns current prices.
func (s *TerraportSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("%w", ErrNoPricesAvailable)
	}
	return prices, nil
}

// Subscribe allows receiving price updates.
func (s *TerraportSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// fetchPrices queries all pairs and updates prices.
func (s *TerraportSource) fetchPrices(ctx context.Context) error {
	now := time.Now()
	successCount := 0

	for _, pair := range s.pairs {
		price, err := s.fetchPairPrice(ctx, pair)
		if err != nil {
			s.Logger().Error("Failed to fetch pair price",
				"pair", pair.Symbol,
				"contract", pair.ContractAddress,
				"error", err)
			continue
		}

		s.SetPrice(pair.Symbol, price, now)
		successCount++

		s.Logger().Debug("Updated price from Terraport",
			"pair", pair.Symbol,
			"price", price.String())
	}

	if successCount > 0 {
		s.SetHealthy(true)
		s.SetLastUpdate(now)
		return nil
	}

	return fmt.Errorf("%w", ErrNoPoolPrices)
}

// fetchPairPrice queries a single pair contract for reserves and calculates price using gRPC.
//
//nolint:dupl
func (s *TerraportSource) fetchPairPrice(ctx context.Context, pair TerraportPair) (decimal.Decimal, error) {
	// Build query message
	queryMsg := map[string]interface{}{
		"pool": map[string]interface{}{},
	}

	queryBytes, err := json.Marshal(queryMsg)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Query contract via gRPC
	respData, err := s.grpcClient.QuerySmartContract(ctx, pair.ContractAddress, queryBytes)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to query contract: %w", err)
	}

	// Parse response
	var poolResp PoolResponse
	if err := json.Unmarshal(respData, &poolResp); err != nil {
		return decimal.Zero, fmt.Errorf("failed to decode response: %w", err)
	}

	// Validate asset count
	if len(poolResp.Assets) != 2 {
		return decimal.Zero, fmt.Errorf("%w: expected 2 assets, got %d", ErrInvalidPoolResponse, len(poolResp.Assets))
	}

	// Calculate price using helper
	return CalculatePoolPrice(poolResp.Assets[0].Amount, poolResp.Assets[1].Amount, pair.Decimals0, pair.Decimals1)
}
