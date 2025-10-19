package evm

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/StrathCole/oracle-go/pkg/metrics"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/shopspring/decimal"
)

// PancakeSwapSource implements price fetching from PancakeSwap V2 pairs on BSC.
type PancakeSwapSource struct {
	*sources.BaseSource
	client       *ethclient.Client
	rpcURL       string
	chainID      uint64
	pairs        []PairConfig
	prices       map[string]sources.Price
	mu           sync.RWMutex
	updateTicker *time.Ticker
	stopChan     chan struct{}
	pairABI      abi.ABI
}

// PairConfig holds configuration for a trading pair.
type PairConfig struct {
	Symbol      string
	PairAddress common.Address
	Token0Addr  common.Address
	Token1Addr  common.Address
	Decimals0   int
	Decimals1   int
}

// Uniswap V2 Pair ABI (only getReserves function).
const pairABIJSON = `[{
	"constant": true,
	"inputs": [],
	"name": "getReserves",
	"outputs": [
		{"internalType": "uint112", "name": "reserve0", "type": "uint112"},
		{"internalType": "uint112", "name": "reserve1", "type": "uint112"},
		{"internalType": "uint32", "name": "blockTimestampLast", "type": "uint32"}
	],
	"payable": false,
	"stateMutability": "view",
	"type": "function"
}]`

// NewPancakeSwapSource creates a new PancakeSwap price source.
func NewPancakeSwapSource(config map[string]interface{}) (sources.Source, error) {
	// Parse RPC URL
	rpcURL, ok := config["rpc_url"].(string)
	if !ok || rpcURL == "" {
		return nil, fmt.Errorf("%w", ErrRPCURLRequired)
	}

	// Parse chain ID
	chainID, ok := config["chain_id"].(int)
	if !ok {
		return nil, fmt.Errorf("%w", ErrChainIDRequired)
	}

	// Parse pairs
	pairsRaw, ok := config["pairs"].([]interface{})
	if !ok || len(pairsRaw) == 0 {
		return nil, fmt.Errorf("%w", ErrPairsConfigRequired)
	}

	pairs := make([]PairConfig, 0, len(pairsRaw))
	for _, pairRaw := range pairsRaw {
		pairMap, ok := pairRaw.(map[string]interface{})
		if !ok {
			continue
		}

		symbol, _ := pairMap["symbol"].(string)
		pairAddr, _ := pairMap["pair_address"].(string)
		token0Addr, _ := pairMap["token0_address"].(string)
		token1Addr, _ := pairMap["token1_address"].(string)

		// Handle decimals (can be float64 from YAML)
		decimals0 := 18
		if d, ok := pairMap["decimals0"].(float64); ok {
			decimals0 = int(d)
		} else if d, ok := pairMap["decimals0"].(int); ok {
			decimals0 = d
		}

		decimals1 := 18
		if d, ok := pairMap["decimals1"].(float64); ok {
			decimals1 = int(d)
		} else if d, ok := pairMap["decimals1"].(int); ok {
			decimals1 = d
		}

		if symbol == "" || pairAddr == "" {
			continue
		}

		pairs = append(pairs, PairConfig{
			Symbol:      symbol,
			PairAddress: common.HexToAddress(pairAddr),
			Token0Addr:  common.HexToAddress(token0Addr),
			Token1Addr:  common.HexToAddress(token1Addr),
			Decimals0:   decimals0,
			Decimals1:   decimals1,
		})
	}

	if len(pairs) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPairsConfigured)
	}

	// Parse ABI
	pairABI, err := abi.JSON(strings.NewReader(pairABIJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to parse pair ABI: %w", err)
	}

	// Create base source with pair mappings
	pairMappings := make(map[string]string)
	for _, pair := range pairs {
		pairMappings[pair.Symbol] = pair.Symbol // EVM doesn't transform symbols
	}

	logger := sources.GetLoggerFromConfig(config)
	base := sources.NewBaseSource("pancakeswap_bsc", sources.SourceTypeEVM, pairMappings, logger)

	// Validate chainID is non-negative
	if chainID < 0 {
		return nil, fmt.Errorf("%w: chain_id must be non-negative", ErrChainIDRequired)
	}

	source := &PancakeSwapSource{
		BaseSource: base,
		rpcURL:     rpcURL,
		chainID:    uint64(chainID),
		pairs:      pairs,
		prices:     make(map[string]sources.Price),
		stopChan:   make(chan struct{}),
		pairABI:    pairABI,
	}

	return source, nil
}

// Initialize connects to the EVM RPC endpoint.
func (s *PancakeSwapSource) Initialize(ctx context.Context) error {
	client, err := ethclient.DialContext(ctx, s.rpcURL)
	if err != nil {
		return fmt.Errorf("failed to connect to RPC: %w", err)
	}

	s.client = client
	s.SetHealthy(true)
	return nil
}

// Start begins fetching prices at regular intervals.
func (s *PancakeSwapSource) Start(ctx context.Context) error {
	if s.client == nil {
		return fmt.Errorf("%w", sources.ErrClientNotInitialized)
	}

	// Fetch initial prices
	if err := s.fetchPrices(ctx); err != nil {
		return fmt.Errorf("failed to fetch initial prices: %w", err)
	}

	// Start update ticker (15 seconds)
	s.updateTicker = time.NewTicker(15 * time.Second)

	go func(ctx context.Context) {
		for {
			select {
			case <-s.updateTicker.C:
				fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				if err := s.fetchPrices(fetchCtx); err != nil {
					s.SetHealthy(false)
				} else {
					s.SetHealthy(true)
				}
				cancel()
			case <-s.stopChan:
				return
			}
		}
	}(ctx)

	return nil
}

// Stop halts the price fetching.
func (s *PancakeSwapSource) Stop() error {
	if s.updateTicker != nil {
		s.updateTicker.Stop()
	}
	close(s.stopChan)

	if s.client != nil {
		s.client.Close()
	}

	return nil
}

// GetPrices returns the current prices.
func (s *PancakeSwapSource) GetPrices(_ context.Context) (map[string]sources.Price, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.prices) == 0 {
		return nil, fmt.Errorf("%w", sources.ErrNoPricesAvailable)
	}

	// Return a copy
	result := make(map[string]sources.Price, len(s.prices))
	for k, v := range s.prices {
		result[k] = v
	}

	return result, nil
}

// Subscribe is not implemented for EVM sources.
func (s *PancakeSwapSource) Subscribe(_ chan<- sources.PriceUpdate) error {
	return fmt.Errorf("%w", sources.ErrSubscribeNotImplemented)
}

// fetchPrices queries all pair contracts for reserves and calculates prices.
func (s *PancakeSwapSource) fetchPrices(ctx context.Context) error {
	newPrices := make(map[string]sources.Price)

	for _, pair := range s.pairs {
		reserves, err := s.getReserves(ctx, pair.PairAddress)
		if err != nil {
			// Log error but continue with other pairs
			continue
		}

		price := s.calculatePrice(reserves.Reserve0, reserves.Reserve1, pair.Decimals0, pair.Decimals1)

		newPrices[pair.Symbol] = sources.Price{
			Symbol:    pair.Symbol,
			Price:     price,
			Timestamp: time.Now(),
			Source:    s.Name(),
		}
	}

	if len(newPrices) == 0 {
		return fmt.Errorf("%w", sources.ErrNoPricesFetched)
	}

	s.mu.Lock()
	s.prices = newPrices
	s.SetLastUpdate(time.Now())
	s.mu.Unlock()

	// Record metrics for all updated prices
	for symbol := range newPrices {
		metrics.RecordSourceUpdate(s.Name(), symbol)
	}

	return nil
}

// Reserves holds the pair reserves.
type Reserves struct {
	Reserve0           *big.Int
	Reserve1           *big.Int
	BlockTimestampLast uint32
}

// getReserves calls the getReserves() function on a Uniswap V2 pair contract.
func (s *PancakeSwapSource) getReserves(ctx context.Context, pairAddr common.Address) (*Reserves, error) {
	// Pack the getReserves function call
	data, err := s.pairABI.Pack("getReserves")
	if err != nil {
		return nil, fmt.Errorf("failed to pack getReserves call: %w", err)
	}

	// Call the contract
	result, err := s.client.CallContract(ctx, ethereum.CallMsg{
		To:   &pairAddr,
		Data: data,
	}, nil) // nil = latest block
	if err != nil {
		return nil, fmt.Errorf("failed to call getReserves: %w", err)
	}

	// Unpack the result
	var reserves struct {
		Reserve0           *big.Int
		Reserve1           *big.Int
		BlockTimestampLast uint32
	}

	err = s.pairABI.UnpackIntoInterface(&reserves, "getReserves", result)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack getReserves result: %w", err)
	}

	return &Reserves{
		Reserve0:           reserves.Reserve0,
		Reserve1:           reserves.Reserve1,
		BlockTimestampLast: reserves.BlockTimestampLast,
	}, nil
}

// calculatePrice calculates the spot price from reserves.
// Price = (reserve1 / 10^decimals1) / (reserve0 / 10^decimals0).
func (s *PancakeSwapSource) calculatePrice(reserve0, reserve1 *big.Int, decimals0, decimals1 int) decimal.Decimal {
	if reserve0.Sign() == 0 || reserve1.Sign() == 0 {
		return decimal.Zero
	}

	// Bounds check decimals
	if decimals0 < 0 || decimals0 > 255 {
		decimals0 = 0
	}
	if decimals1 < 0 || decimals1 > 255 {
		decimals1 = 0
	}

	// Create decimal scaling factors
	// #nosec G115 -- decimals validated above to be 0-255
	scale0 := decimal.NewFromBigInt(big.NewInt(10), int32(decimals0)) // #nosec G115
	// #nosec G115 -- decimals validated above to be 0-255
	scale1 := decimal.NewFromBigInt(big.NewInt(10), int32(decimals1))

	// Convert reserves to decimals
	amount0 := decimal.NewFromBigInt(reserve0, 0).Div(scale0)
	amount1 := decimal.NewFromBigInt(reserve1, 0).Div(scale1)

	// Spot price = amount1 / amount0
	return amount1.Div(amount0)
}
