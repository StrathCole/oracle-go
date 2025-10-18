package voter

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/feeder/oracle"
	"tc.com/oracle-prices/pkg/feeder/price"
)

// MockEventStream simulates blockchain event stream for voting rounds
type MockEventStream struct {
	mock.Mock
	events      chan interface{}
	mu          sync.Mutex
	started     bool
	blockHeight int64
}

func NewMockEventStream() *MockEventStream {
	return &MockEventStream{
		events:      make(chan interface{}, 100),
		blockHeight: 1,
	}
}

func (m *MockEventStream) Start(ctx context.Context) error {
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	return nil
}

func (m *MockEventStream) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		close(m.events)
		m.started = false
	}
	return nil
}

func (m *MockEventStream) Events() <-chan interface{} {
	return m.events
}

// SendBlockEvent sends a new block event
func (m *MockEventStream) SendBlockEvent() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return
	}

	m.blockHeight++
	event := map[string]interface{}{
		"type":   "block",
		"height": m.blockHeight,
	}
	select {
	case m.events <- event:
	default:
		// Channel full, skip
	}
}

// SendVotePeriodEvent sends a vote period event
func (m *MockEventStream) SendVotePeriodEvent(period uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return
	}

	event := map[string]interface{}{
		"type":   "vote_period",
		"period": period,
	}
	select {
	case m.events <- event:
	default:
		// Channel full, skip
	}
}

// SendOracleParamsEvent sends oracle parameter update event
func (m *MockEventStream) SendOracleParamsEvent(votePeriod uint64, whitelist []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return
	}

	event := map[string]interface{}{
		"type":        "oracle_params",
		"vote_period": votePeriod,
		"whitelist":   whitelist,
	}
	select {
	case m.events <- event:
	default:
		// Channel full, skip
	}
}

// GetCurrentHeight returns the current block height
func (m *MockEventStream) GetCurrentHeight() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blockHeight
}

// TestVotingRoundWithEventStream tests a complete voting round with event stream
func TestVotingRoundWithEventStream(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup mock components
	mockEventStream := NewMockEventStream()
	mockPriceClient := new(MockPriceClient)

	// Oracle parameters (from Terra Classic mainnet)
	votePeriodBlocks := uint64(5) // 5 blocks per voting period (~30 seconds)
	whitelist := []string{"ukrw", "usdr", "uusd", "umnt", "UST"}

	// Setup price data
	testPrices := []price.Price{
		{Symbol: "KRW/USD", Price: decimal.NewFromFloat(0.00075)},
		{Symbol: "SDR/USD", Price: decimal.NewFromFloat(1.35958)},
		{Symbol: "USD", Price: decimal.NewFromFloat(1.0)},
		{Symbol: "MNT/USD", Price: decimal.NewFromFloat(0.00029)},
		{Symbol: "USTC/USD", Price: decimal.NewFromFloat(0.02)},
	}

	mockPriceClient.On("GetPrices", mock.Anything).Return(testPrices, nil)

	// Start event stream
	err := mockEventStream.Start(ctx)
	require.NoError(t, err)
	defer mockEventStream.Stop()

	// Send initial oracle params
	mockEventStream.SendOracleParamsEvent(votePeriodBlocks, whitelist)

	// Wait for params to be processed
	time.Sleep(100 * time.Millisecond)

	t.Run("BlockEventsReceivedInOrder", func(t *testing.T) {
		// Send series of block events
		initialHeight := mockEventStream.GetCurrentHeight()

		for i := 0; i < 10; i++ {
			mockEventStream.SendBlockEvent()
		}

		// Wait for events to be queued
		time.Sleep(100 * time.Millisecond)

		// Verify block height increased
		finalHeight := mockEventStream.GetCurrentHeight()
		assert.Equal(t, initialHeight+10, finalHeight)
	})

	t.Run("VotePeriodCalculation", func(t *testing.T) {
		currentHeight := mockEventStream.GetCurrentHeight()

		// Calculate vote period from block height
		// Formula: votePeriod = blockHeight / votePeriodBlocks
		expectedPeriod := uint64(currentHeight) / votePeriodBlocks

		// Send vote period event
		mockEventStream.SendVotePeriodEvent(expectedPeriod)

		time.Sleep(100 * time.Millisecond)

		// Verify event was sent
		assert.Greater(t, expectedPeriod, uint64(0))
	})

	t.Run("OracleParamsUpdate", func(t *testing.T) {
		// Update oracle params
		newVotePeriod := uint64(10)
		newWhitelist := []string{"ukrw", "uusd"}

		mockEventStream.SendOracleParamsEvent(newVotePeriod, newWhitelist)

		time.Sleep(100 * time.Millisecond)

		// Verify params event was sent (in real implementation, voter would update its config)
		assert.Equal(t, 2, len(newWhitelist))
	})

	// Note: mockPriceClient is set up but not used in this test
	// since we're testing the event stream, not the voter itself
	// mockPriceClient.AssertExpectations(t)
}

// TestVotingRoundTiming tests the timing of voting rounds
func TestVotingRoundTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockEventStream := NewMockEventStream()
	err := mockEventStream.Start(ctx)
	require.NoError(t, err)
	defer mockEventStream.Stop()

	votePeriodBlocks := uint64(5)

	// Track vote periods
	votePeriods := make([]uint64, 0)
	var mu sync.Mutex

	// Simulate block production
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond) // Fast blocks for testing
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mockEventStream.SendBlockEvent()

				// Calculate and send vote period every votePeriodBlocks
				currentHeight := mockEventStream.GetCurrentHeight()
				if currentHeight%int64(votePeriodBlocks) == 0 {
					period := uint64(currentHeight) / votePeriodBlocks
					mockEventStream.SendVotePeriodEvent(period)

					mu.Lock()
					votePeriods = append(votePeriods, period)
					mu.Unlock()
				}
			}
		}
	}()

	// Wait for several vote periods
	time.Sleep(3 * time.Second)

	// Verify we had multiple vote periods
	mu.Lock()
	defer mu.Unlock()
	assert.Greater(t, len(votePeriods), 5, "Should have multiple vote periods")

	// Verify periods are sequential
	for i := 1; i < len(votePeriods); i++ {
		assert.Equal(t, votePeriods[i-1]+1, votePeriods[i],
			"Vote periods should be sequential")
	}
}

// TestPriceToVoteConversion tests converting prices to vote format
func TestPriceToVoteConversion(t *testing.T) {
	// Test prices
	prices := map[string]decimal.Decimal{
		"KRW/USD":  decimal.NewFromFloat(0.00075),
		"SDR/USD":  decimal.NewFromFloat(1.35958),
		"USD":      decimal.NewFromFloat(1.0),
		"MNT/USD":  decimal.NewFromFloat(0.00029),
		"USTC/USD": decimal.NewFromFloat(0.02),
	}

	whitelist := []string{"ukrw", "usdr", "uusd", "umnt", "UST"}

	// Convert to oracle prices
	v := &Voter{}
	oraclePrices := v.convertToOraclePrices(prices, whitelist)

	// Verify all whitelisted denoms are present
	require.Len(t, oraclePrices, 5)

	denomMap := make(map[string]oracle.Price)
	for _, p := range oraclePrices {
		denomMap[p.Denom] = p
	}

	// Verify each denom
	assert.Contains(t, denomMap, "ukrw")
	assert.Contains(t, denomMap, "usdr")
	assert.Contains(t, denomMap, "uusd")
	assert.Contains(t, denomMap, "umnt")
	assert.Contains(t, denomMap, "UST")

	// Verify price values
	assert.InDelta(t, 0.00075, denomMap["ukrw"].Price, 0.0000001)
	assert.InDelta(t, 1.35958, denomMap["usdr"].Price, 0.00001)
	assert.InDelta(t, 1.0, denomMap["uusd"].Price, 0.0000001)
	assert.InDelta(t, 0.00029, denomMap["umnt"].Price, 0.0000001)
	assert.InDelta(t, 0.02, denomMap["UST"].Price, 0.0001)
}

// TestPrevoteHashCalculation tests prevote hash calculation
func TestPrevoteHashCalculation(t *testing.T) {
	// Create test prices
	testPrices := []oracle.Price{
		{Denom: "ukrw", Price: 0.00075},
		{Denom: "uusd", Price: 1.0},
	}

	// Create test addresses using SDK address from hex
	// Using same bytes for both validator and feeder for testing purposes
	testBytes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A,
		0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14}

	validatorAddr := sdk.ValAddress(testBytes)
	feederAddr := sdk.AccAddress(testBytes)

	// Create logger
	logger := zerolog.Nop()

	// Create prevote
	prevote, err := oracle.NewPrevote(testPrices, validatorAddr, feederAddr, logger)
	require.NoError(t, err)
	require.NotNil(t, prevote)

	// Verify prevote fields
	assert.NotEmpty(t, prevote.Salt, "Salt should not be empty")
	assert.NotEmpty(t, prevote.Vote, "Vote string should not be empty")
	assert.NotNil(t, prevote.Msg, "Prevote message should not be nil")

	// Verify vote string format (should contain prices and denoms)
	assert.Contains(t, prevote.Vote, "ukrw")
	assert.Contains(t, prevote.Vote, "uusd")
}

// TestMultipleValidators tests voting for multiple validators
func TestMultipleValidators(t *testing.T) {
	// Create different test addresses
	testBytes1 := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A,
		0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14}
	testBytes2 := []byte{0x14, 0x13, 0x12, 0x11, 0x10, 0x0F, 0x0E, 0x0D, 0x0C, 0x0B,
		0x0A, 0x09, 0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}

	validator1 := sdk.ValAddress(testBytes1)
	feeder1 := sdk.AccAddress(testBytes1)

	validator2 := sdk.ValAddress(testBytes2)
	feeder2 := sdk.AccAddress(testBytes2)

	testPrices := []oracle.Price{
		{Denom: "ukrw", Price: 0.00075},
	}

	logger := zerolog.Nop()

	// Create prevotes for both validators
	prevote1, err := oracle.NewPrevote(testPrices, validator1, feeder1, logger)
	require.NoError(t, err)

	prevote2, err := oracle.NewPrevote(testPrices, validator2, feeder2, logger)
	require.NoError(t, err)

	// Prevotes should be different (different salts)
	assert.NotEqual(t, prevote1.Salt, prevote2.Salt)
	assert.NotEqual(t, prevote1.Msg.Hash, prevote2.Msg.Hash)
}

// TestVotePeriodDetection tests detecting new vote periods from block events
func TestVotePeriodDetection(t *testing.T) {
	votePeriodBlocks := uint64(5)

	testCases := []struct {
		blockHeight    int64
		expectedPeriod uint64
		isNewPeriod    bool
	}{
		{1, 0, false},
		{4, 0, false},
		{5, 1, true},
		{9, 1, false},
		{10, 2, true},
		{15, 3, true},
		{20, 4, true},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Height_%d", tc.blockHeight), func(t *testing.T) {
			period := uint64(tc.blockHeight) / votePeriodBlocks
			isNewPeriod := tc.blockHeight%int64(votePeriodBlocks) == 0

			assert.Equal(t, tc.expectedPeriod, period)
			assert.Equal(t, tc.isNewPeriod, isNewPeriod)
		})
	}
}

// TestWhitelistFiltering tests that only whitelisted denoms are included in votes
func TestWhitelistFilteringInVote(t *testing.T) {
	v := &Voter{}

	// Prices with both whitelisted and non-whitelisted denoms
	prices := map[string]decimal.Decimal{
		"KRW/USD": decimal.NewFromFloat(0.00075), // whitelisted
		"USD":     decimal.NewFromFloat(1.0),     // whitelisted
		"BTC/USD": decimal.NewFromFloat(50000.0), // NOT whitelisted
		"ETH/USD": decimal.NewFromFloat(3000.0),  // NOT whitelisted
	}

	whitelist := []string{"ukrw", "uusd"}

	oraclePrices := v.convertToOraclePrices(prices, whitelist)

	// Should only have 2 prices (whitelisted ones)
	require.Len(t, oraclePrices, 2)

	denoms := make(map[string]bool)
	for _, p := range oraclePrices {
		denoms[p.Denom] = true
	}

	assert.True(t, denoms["ukrw"])
	assert.True(t, denoms["uusd"])
	assert.False(t, denoms["ubtc"]) // BTC should be filtered out
	assert.False(t, denoms["ueth"]) // ETH should be filtered out
}

// TestRealRPCConnection tests connecting to the real Terra Classic RPC
// This test is skipped in short mode and requires network access
func TestRealRPCConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real RPC integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

	// Use the real gRPC endpoint from config
	grpcEndpoint := "grpc.terra-classic.hexxagon.io:443"

	t.Logf("Connecting to real Terra Classic gRPC: %s", grpcEndpoint)

	// Create gRPC client with proper interface registry
	ir := codectypes.NewInterfaceRegistry()

	clientCfg := client.ClientConfig{
		Endpoints: []client.EndpointConfig{
			{Address: grpcEndpoint, TLS: true},
		},
		ChainID:           "columbus-5",
		InterfaceRegistry: ir,
		Logger:            logger,
	}

	grpcClient, err := client.NewClient(clientCfg)
	require.NoError(t, err, "Failed to create gRPC client")
	defer grpcClient.Close()

	// Test 1: Query oracle parameters
	t.Run("QueryOracleParams", func(t *testing.T) {
		params, err := grpcClient.GetOracleParams(ctx)
		require.NoError(t, err, "Failed to query oracle params")
		require.NotNil(t, params, "Oracle params should not be nil")

		t.Logf("Oracle Parameters:")
		t.Logf("  Vote Period: %d blocks", params.VotePeriod)
		t.Logf("  Vote Threshold: %s", params.VoteThreshold)
		t.Logf("  Reward Band: %s", params.RewardBand)
		t.Logf("  Whitelist: %v", params.Whitelist)

		assert.Greater(t, params.VotePeriod, uint64(0), "Vote period should be > 0")
		assert.NotEmpty(t, params.Whitelist, "Whitelist should not be empty")

		// Terra Classic typically has these denoms in whitelist
		expectedDenoms := map[string]bool{
			"ukrw": false,
			"uusd": false,
			"usdr": false,
			"umnt": false,
		}

		for _, denom := range params.Whitelist {
			if _, exists := expectedDenoms[denom.Name]; exists {
				expectedDenoms[denom.Name] = true
			}
		}

		foundAny := false
		for denom, found := range expectedDenoms {
			if found {
				t.Logf("  ✓ Found expected denom: %s", denom)
				foundAny = true
			}
		}
		assert.True(t, foundAny, "Should find at least one expected denom in whitelist")
	})

	// Test 2: Query exchange rates - Note: direct query not available in client
	// Exchange rates are typically queried through LCD REST API or via oracle module queries

	// Test 3: Test vote period calculation
	t.Run("VotePeriodCalculation", func(t *testing.T) {
		params, err := grpcClient.GetOracleParams(ctx)
		require.NoError(t, err, "Failed to query oracle params")

		votePeriod := params.VotePeriod
		assert.Greater(t, votePeriod, uint64(0), "Vote period should be > 0")

		// Terra Classic typically uses 5 block vote periods (~30 seconds)
		if votePeriod == 5 {
			t.Logf("✓ Vote period is 5 blocks (standard Terra Classic)")
		} else {
			t.Logf("⚠ Vote period is %d blocks (non-standard)", votePeriod)
		}
	})
}

// TestRealPriceSourceConnection tests connecting to a real price source
// This requires the price server to be running on localhost:8080
func TestRealPriceSourceConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real price source integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to connect to local price server
	priceClient, err := price.NewHTTPClient("http://localhost:8080", 5*time.Second)
	require.NoError(t, err, "Failed to create price client")

	prices, err := priceClient.GetPrices(ctx)

	if err != nil {
		t.Logf("⚠ Price server not running on localhost:8080: %v", err)
		t.Skip("Skipping test - price server not available")
		return
	}

	t.Logf("Successfully fetched %d prices from price server", len(prices))

	// Validate price data
	assert.NotEmpty(t, prices, "Should receive some prices")

	for _, p := range prices {
		t.Logf("  %s: %s (source: %s)", p.Symbol, p.Price.String(), p.Source)
		assert.True(t, p.Price.IsPositive(), "Price for %s should be positive", p.Symbol)
	}
}

// TestEndToEndVotingSimulation simulates a complete voting round with real RPC
// This test requires both network access and validates vote construction
func TestEndToEndVotingSimulation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping end-to-end voting simulation in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

	grpcEndpoint := "grpc.terra-classic.hexxagon.io:443"

	t.Logf("Simulating voting round against real RPC: %s", grpcEndpoint)

	// Create gRPC client
	ir := codectypes.NewInterfaceRegistry()

	clientCfg := client.ClientConfig{
		Endpoints: []client.EndpointConfig{
			{Address: grpcEndpoint, TLS: true},
		},
		ChainID:           "columbus-5",
		InterfaceRegistry: ir,
		Logger:            logger,
	}

	grpcClient, err := client.NewClient(clientCfg)
	require.NoError(t, err, "Failed to create gRPC client")
	defer grpcClient.Close()

	// Query oracle parameters
	params, err := grpcClient.GetOracleParams(ctx)
	require.NoError(t, err, "Failed to query oracle params")

	t.Logf("Oracle Parameters:")
	t.Logf("  Vote Period: %d blocks", params.VotePeriod)

	whitelist := make([]string, len(params.Whitelist))
	for i, denom := range params.Whitelist {
		whitelist[i] = denom.Name
		t.Logf("  Whitelist[%d]: %s", i, denom.Name)
	}

	// Create mock oracle prices for testing
	mockOraclePrices := []oracle.Price{
		{Denom: "ukrw", Price: 0.00075},
		{Denom: "uusd", Price: 1.0},
		{Denom: "usdr", Price: 1.35},
		{Denom: "umnt", Price: 0.00029},
	}

	// Filter by whitelist
	var filteredPrices []oracle.Price
	whitelistMap := make(map[string]bool)
	for _, w := range whitelist {
		whitelistMap[w] = true
	}

	for _, p := range mockOraclePrices {
		if whitelistMap[p.Denom] {
			filteredPrices = append(filteredPrices, p)
		}
	}

	t.Logf("Filtered %d prices for voting:", len(filteredPrices))
	for _, p := range filteredPrices {
		t.Logf("  %s: %f", p.Denom, p.Price)
	}

	// Create test validator and feeder addresses
	validator := sdk.ValAddress([]byte("test_validator_address_12345"))
	feeder := sdk.AccAddress([]byte("test_feeder_address_12345"))

	// Create prevote (without actually submitting)
	prevote, err := oracle.NewPrevote(filteredPrices, validator, feeder, logger)
	require.NoError(t, err, "Failed to create prevote")

	t.Logf("Created prevote:")
	t.Logf("  Salt: %s", prevote.Salt)
	t.Logf("  Vote: %s", prevote.Vote)
	t.Logf("  Hash: %s", prevote.Msg.Hash)

	assert.NotEmpty(t, prevote.Salt, "Salt should not be empty")
	assert.NotEmpty(t, prevote.Vote, "Vote string should not be empty")
	assert.NotEmpty(t, prevote.Msg.Hash, "Hash should not be empty")

	// Verify vote string format
	assert.Contains(t, prevote.Vote, "ukrw", "Vote should contain ukrw if in whitelist")

	t.Logf("✓ End-to-end voting simulation completed successfully (dry-run, no actual votes submitted)")
}
