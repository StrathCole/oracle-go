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
	garudaQueryTimeout   = 10 * time.Second
	garudaUpdateInterval = 15 * time.Second
)

// GarudaSource fetches prices from Garuda DeFi DEX pairs via gRPC smart contract queries
type GarudaSource struct {
	*sources.BaseSource
	grpcClient     *client.Client
	updateInterval time.Duration
	pairs          []GarudaPair
}

// GarudaPairConfig represents configuration for a Garuda DeFi liquidity pair
type GarudaPairConfig struct {
	Symbol          string // e.g., "LUNC/USDC"
	ContractAddress string // Garuda pair contract address
	Asset0Denom     string // First asset denom (e.g., "uluna")
	Asset1Denom     string // Second asset denom (e.g., "ibc/...")
	Decimals0       int    // Decimals for asset 0
	Decimals1       int    // Decimals for asset 1
}

// GarudaPair is an alias for GarudaPairConfig
type GarudaPair = GarudaPairConfig

// GarudaPoolResponse represents the response from querying a Garuda pair
// Garuda uses a different format than Terraport/Terraswap
type GarudaPoolResponse struct {
	Asset1 struct {
		Native string `json:"native,omitempty"`
		Token  string `json:"token,omitempty"`
	} `json:"asset1"`
	Asset2 struct {
		Native string `json:"native,omitempty"`
		Token  string `json:"token,omitempty"`
	} `json:"asset2"`
	Reserve1       string `json:"reserve1"`
	Reserve2       string `json:"reserve2"`
	TotalSupply    string `json:"total_supply"`
	LiquidityToken string `json:"liquidity_token"`
}

// NewGarudaSource creates a new Garuda DeFi source using gRPC client
func NewGarudaSource(config map[string]interface{}, grpcClient *client.Client) (sources.Source, error) {
	// Parse CosmWasm pairs configuration using helper
	pairs, err := sources.ParseCosmWasmPairs(config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pairs: %w", err)
	}

	// Convert to GarudaPair array
	garudaPairs := make([]GarudaPair, 0, len(pairs))

	// Create simple map for BaseSource (symbol => contract_address)
	simplePairs := make(map[string]string)

	for _, p := range pairs {
		garudaPair := GarudaPair{
			Symbol:          p.Symbol,
			ContractAddress: p.ContractAddress,
			Asset0Denom:     p.Asset0Denom,
			Asset1Denom:     p.Asset1Denom,
			Decimals0:       p.Decimals0,
			Decimals1:       p.Decimals1,
		}

		garudaPairs = append(garudaPairs, garudaPair)
		simplePairs[p.Symbol] = p.ContractAddress
	}

	if len(garudaPairs) == 0 {
		return nil, fmt.Errorf("no valid pairs configured")
	}

	// Get update interval
	updateInterval := garudaUpdateInterval
	if interval, ok := config["update_interval"].(string); ok {
		if d, err := time.ParseDuration(interval); err == nil {
			updateInterval = d
		}
	}

	logger, _ := logging.Init("info", "text", "stdout")

	// Create base with simple pairs map
	base := sources.NewBaseSource("garuda", sources.SourceTypeCosmWasm, simplePairs, logger)

	return &GarudaSource{
		BaseSource:     base,
		grpcClient:     grpcClient,
		updateInterval: updateInterval,
		pairs:          garudaPairs,
	}, nil
}

// Initialize prepares the source
func (s *GarudaSource) Initialize(ctx context.Context) error {
	s.Logger().Info("Initializing Garuda DeFi source",
		"pairs", len(s.pairs),
		"grpc_endpoint", s.grpcClient.CurrentEndpoint())
	return nil
}

// Start begins fetching prices
func (s *GarudaSource) Start(ctx context.Context) error {
	s.Logger().Info("Starting Garuda DeFi source")

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
				s.Logger().Info("Stopping Garuda DeFi source")
				return
			case <-s.StopChan():
				s.Logger().Info("Garuda DeFi source stopped")
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
func (s *GarudaSource) Stop() error {
	s.Close()
	return nil
}

// GetPrices returns current prices
func (s *GarudaSource) GetPrices(ctx context.Context) (map[string]sources.Price, error) {
	prices := s.GetAllPrices()
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices available")
	}
	return prices, nil
}

// Subscribe allows receiving price updates
func (s *GarudaSource) Subscribe(updates chan<- sources.PriceUpdate) error {
	s.AddSubscriber(updates)
	return nil
}

// fetchPrices queries all pairs and updates prices
func (s *GarudaSource) fetchPrices(ctx context.Context) error {
	now := time.Now()
	successCount := 0

	for _, pair := range s.pairs {
		price, err := s.fetchPairPrice(ctx, pair)
		if err != nil {
			s.Logger().Error("Failed to fetch pair price",
				"pair", pair.Symbol,
				"contract", pair.ContractAddress,
				"error", fmt.Sprintf("%v", err)) // Force string formatting of error
			continue
		}

		s.SetPrice(pair.Symbol, price, now)
		successCount++

		s.Logger().Debug("Updated price from Garuda DeFi",
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
func (s *GarudaSource) fetchPairPrice(ctx context.Context, pair GarudaPair) (decimal.Decimal, error) {
	// Build query message
	queryMsg := map[string]interface{}{
		"pool": map[string]interface{}{},
	}

	queryBytes, err := json.Marshal(queryMsg)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to marshal query: %w", err)
	}

	s.Logger().Debug("Querying Garuda contract",
		"contract", pair.ContractAddress,
		"query", string(queryBytes))

	// Query contract via gRPC
	respData, err := s.grpcClient.QuerySmartContract(ctx, pair.ContractAddress, queryBytes)
	if err != nil {
		return decimal.Zero, fmt.Errorf("gRPC query failed for contract %s: %w", pair.ContractAddress, err)
	}

	s.Logger().Debug("Got response from Garuda contract",
		"response", string(respData))

	// Parse response
	var poolResp GarudaPoolResponse
	if err := json.Unmarshal(respData, &poolResp); err != nil {
		return decimal.Zero, fmt.Errorf("failed to decode response (got: %s): %w", string(respData), err)
	}

	// Log the response to see what we actually got
	s.Logger().Debug("Garuda pool response",
		"contract", pair.ContractAddress,
		"reserve1", poolResp.Reserve1,
		"reserve2", poolResp.Reserve2)

	// Parse reserves
	if poolResp.Reserve1 == "" || poolResp.Reserve2 == "" {
		return decimal.Zero, fmt.Errorf("invalid pool response: missing reserves (response: %s)", string(respData))
	}

	amount1, err := decimal.NewFromString(poolResp.Reserve1)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to parse reserve1: %w", err)
	}

	amount2, err := decimal.NewFromString(poolResp.Reserve2)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to parse reserve2: %w", err)
	}

	// Adjust for decimals
	decimals0 := decimal.NewFromInt(int64(pair.Decimals0))
	decimals1 := decimal.NewFromInt(int64(pair.Decimals1))

	// Note: Garuda uses reserve1/reserve2, need to determine which is which based on asset denoms
	// For LUNC/USDC pair: asset2 is uluna (LUNC), asset1 is USDC
	// So reserve2 is LUNC amount, reserve1 is USDC amount
	reserve2 := amount2.Div(decimal.NewFromInt(10).Pow(decimals0)) // LUNC
	reserve1 := amount1.Div(decimal.NewFromInt(10).Pow(decimals1)) // USDC

	// Price = USDC / LUNC (quote asset per base asset)
	if reserve2.IsZero() {
		return decimal.Zero, fmt.Errorf("zero liquidity in pool")
	}

	price := reserve1.Div(reserve2)
	return price, nil
}

func init() {
	// Registration will be done by the server when it has the gRPC client
	// sources.Register("cosmwasm.garuda", NewGarudaSource)
}
