package cosmwasm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/server/sources"
)

const (
	terraportQueryTimeout   = 10 * time.Second
	terraportUpdateInterval = 30 * time.Second
)

// TerraportSource fetches prices from Terraport DEX pairs via gRPC smart contract queries
type TerraportSource struct {
	*sources.BaseSource
	grpcClient     *client.Client
	updateInterval time.Duration
	pairs          []TerraportPair
}

// TerraportPairConfig represents configuration for a Terraport liquidity pair
type TerraportPairConfig struct {
	Symbol          string // e.g., "LUNC/USDC"
	ContractAddress string // Terraport pair contract address
	Asset0Denom     string // First asset denom (e.g., "uluna")
	Asset1Denom     string // Second asset denom (e.g., "ibc/...")
	Decimals0       int    // Decimals for asset 0
	Decimals1       int    // Decimals for asset 1
}

// TerraportPair is an alias for TerraportPairConfig
type TerraportPair = TerraportPairConfig

// PoolResponse represents the response from querying a Terraport pair
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

// TerraportConfig holds configuration for creating a Terraport source
type TerraportConfig struct {
	Pairs      []TerraportPair
	GRPCClient *client.Client
	Interval   time.Duration
}

// NewTerraportSource creates a new Terraport source using gRPC client
func NewTerraportSource(config map[string]interface{}, grpcClient *client.Client) (sources.Source, error) {
	// Parse CosmWasm pairs configuration using helper
	pairs, err := sources.ParseCosmWasmPairs(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	// Convert to TerraportPair array
	terraportPairs := make([]TerraportPair, 0, len(pairs))

	// Create simple map for BaseSource (symbol => contract_address)
	simplePairs := make(map[string]string)

	for _, p := range pairs {
		terraportPair := TerraportPair{
			Symbol:          p.Symbol,
			ContractAddress: p.ContractAddress,
			Asset0Denom:     p.Asset0Denom,
			Asset1Denom:     p.Asset1Denom,
			Decimals0:       p.Decimals0,
			Decimals1:       p.Decimals1,
		}

		terraportPairs = append(terraportPairs, terraportPair)
		simplePairs[p.Symbol] = p.ContractAddress
	}

	if len(terraportPairs) == 0 {
		return nil, fmt.Errorf("no valid pairs configured")
	}

	// Get update interval
	updateInterval := terraportUpdateInterval
	if interval, ok := config["update_interval"].(string); ok {
		if d, err := time.ParseDuration(interval); err == nil {
			updateInterval = d
		}
	}

	logger, _ := logging.Init("info", "text", "stdout")

	// Create base with simple pairs map
	base := sources.NewBaseSource("terraport", sources.SourceTypeCosmWasm, simplePairs, logger)

	return &TerraportSource{
		BaseSource:     base,
		grpcClient:     grpcClient,
		updateInterval: updateInterval,
		pairs:          terraportPairs,
	}, nil
}

// Initialize prepares the source
func (s *TerraportSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Terraport source",
		"pairs", len(s.pairs),
		"grpc_endpoint", s.grpcClient.CurrentEndpoint())
	return nil
}

// Start begins fetching prices
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

// Stop halts the source
func (s *TerraportSource) Stop() error {
	s.Close()
	return nil
}

// GetPrices returns current prices
func (s *TerraportSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe allows receiving price updates
func (s *TerraportSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// fetchPrices queries all pairs and updates prices
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

	return fmt.Errorf("failed to fetch any pair prices")
}

// fetchPairPrice queries a single pair contract for reserves and calculates price using gRPC
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

	// Calculate price from reserves
	if len(poolResp.Assets) != 2 {
		return decimal.Zero, fmt.Errorf("invalid pool response: expected 2 assets, got %d", len(poolResp.Assets))
	}

	amount0, err := decimal.NewFromString(poolResp.Assets[0].Amount)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to parse amount0: %w", err)
	}

	amount1, err := decimal.NewFromString(poolResp.Assets[1].Amount)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to parse amount1: %w", err)
	}

	// Adjust for decimals
	decimals0 := decimal.NewFromInt(int64(pair.Decimals0))
	decimals1 := decimal.NewFromInt(int64(pair.Decimals1))

	amount0 = amount0.Div(decimal.NewFromInt(10).Pow(decimals0))
	amount1 = amount1.Div(decimal.NewFromInt(10).Pow(decimals1))

	// Price = amount1 / amount0 (quote asset per base asset)
	if amount0.IsZero() {
		return decimal.Zero, fmt.Errorf("zero liquidity in pool")
	}

	price := amount1.Div(amount0)
	return price, nil
}
