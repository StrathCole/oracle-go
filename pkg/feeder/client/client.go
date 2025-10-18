package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	oracletypes "github.com/classic-terra/core/v3/x/oracle/types"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txservice "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Oracle defines the interface for oracle-specific queries.
type Oracle interface {
	AggregatePrevote(context.Context, *oracletypes.QueryAggregatePrevoteRequest, ...grpc.CallOption) (*oracletypes.QueryAggregatePrevoteResponse, error)
	Params(context.Context, *oracletypes.QueryParamsRequest, ...grpc.CallOption) (*oracletypes.QueryParamsResponse, error)
}

// Auth defines the interface for authentication queries.
type Auth interface {
	Account(context.Context, *authtypes.QueryAccountRequest, ...grpc.CallOption) (*authtypes.QueryAccountResponse, error)
}

// Wasm defines the interface for CosmWasm contract queries.
type Wasm interface {
	SmartContractState(context.Context, *wasmtypes.QuerySmartContractStateRequest, ...grpc.CallOption) (*wasmtypes.QuerySmartContractStateResponse, error)
}

// TxService defines the interface for transaction broadcasting and queries.
type TxService interface {
	BroadcastTx(context.Context, *txservice.BroadcastTxRequest, ...grpc.CallOption) (*txservice.BroadcastTxResponse, error)
	GetTx(context.Context, *txservice.GetTxRequest, ...grpc.CallOption) (*txservice.GetTxResponse, error)
}

// Client wraps gRPC connections and provides oracle, auth, and tx service clients.
type Client struct {
	logger    zerolog.Logger
	endpoints []string
	current   int
	mu        sync.RWMutex

	// gRPC connections
	conns []*grpc.ClientConn

	// Service clients (created from current connection)
	oracleClient Oracle
	authClient   Auth
	wasmClient   Wasm
	txClient     TxService

	// Codec for unpacking Any types
	ir codectypes.InterfaceRegistry
}

// ClientConfig holds configuration for creating a new Client.
type ClientConfig struct {
	Endpoints         []EndpointConfig             // gRPC endpoints (with failover and per-endpoint TLS)
	ChainID           string                       // Chain ID (for context)
	InterfaceRegistry codectypes.InterfaceRegistry // For unpacking Any types
	Logger            zerolog.Logger               // Logger
}

// EndpointConfig represents a single gRPC endpoint with its TLS setting
type EndpointConfig struct {
	Address string
	TLS     bool
}

// NewClient creates a new gRPC client with failover support across multiple endpoints.
// It establishes connections to all endpoints and creates service clients from the first endpoint.
func NewClient(cfg ClientConfig) (*Client, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("at least one gRPC endpoint is required")
	}

	conns := make([]*grpc.ClientConn, len(cfg.Endpoints))
	endpoints := make([]string, len(cfg.Endpoints))
	
	for i, epCfg := range cfg.Endpoints {
		endpoints[i] = epCfg.Address
		
		var transportCreds grpc.DialOption
		if epCfg.TLS {
			transportCreds = grpc.WithTransportCredentials(
				credentials.NewTLS(&tls.Config{
					InsecureSkipVerify: false,
				}),
			)
		} else {
			transportCreds = grpc.WithTransportCredentials(insecure.NewCredentials())
		}
		
		conn, err := grpc.Dial(epCfg.Address, transportCreds)
		if err != nil {
			// Close any successful connections before returning
			for j := 0; j < i; j++ {
				conns[j].Close()
			}
			return nil, fmt.Errorf("failed to connect to %s: %w", epCfg.Address, err)
		}
		conns[i] = conn
		cfg.Logger.Info().Str("endpoint", epCfg.Address).Bool("tls", epCfg.TLS).Msg("Connected to gRPC endpoint")
	}

	c := &Client{
		logger:    cfg.Logger,
		endpoints: endpoints,
		current:   0,
		conns:     conns,
		ir:        cfg.InterfaceRegistry,
	}

	// Create service clients from first connection
	c.oracleClient = oracletypes.NewQueryClient(conns[0])
	c.authClient = authtypes.NewQueryClient(conns[0])
	c.wasmClient = wasmtypes.NewQueryClient(conns[0])
	c.txClient = txservice.NewServiceClient(conns[0])

	return c, nil
}

// OracleClient returns the oracle query client.
func (c *Client) OracleClient() Oracle {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.oracleClient
}

// AuthClient returns the auth query client.
func (c *Client) AuthClient() Auth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authClient
}

// WasmClient returns the wasm query client.
func (c *Client) WasmClient() Wasm {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.wasmClient
}

// TxClient returns the transaction service client.
func (c *Client) TxClient() TxService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.txClient
}

// InterfaceRegistry returns the codec interface registry for unpacking Any types.
func (c *Client) InterfaceRegistry() codectypes.InterfaceRegistry {
	return c.ir
}

// Failover rotates to the next endpoint and recreates service clients.
// This is automatically called by WithFailover wrapper on RPC errors.
func (c *Client) Failover() {
	c.mu.Lock()
	defer c.mu.Unlock()

	oldIndex := c.current
	c.current = (c.current + 1) % len(c.endpoints)

	c.logger.Warn().
		Str("from", c.endpoints[oldIndex]).
		Str("to", c.endpoints[c.current]).
		Msg("Failing over to next gRPC endpoint")

	// Recreate service clients from new connection
	c.oracleClient = oracletypes.NewQueryClient(c.conns[c.current])
	c.authClient = authtypes.NewQueryClient(c.conns[c.current])
	c.wasmClient = wasmtypes.NewQueryClient(c.conns[c.current])
	c.txClient = txservice.NewServiceClient(c.conns[c.current])
}

// CurrentEndpoint returns the currently active endpoint.
func (c *Client) CurrentEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.endpoints[c.current]
}

// Close closes all gRPC connections.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	for i, conn := range c.conns {
		if err := conn.Close(); err != nil {
			c.logger.Error().Err(err).Str("endpoint", c.endpoints[i]).Msg("Failed to close gRPC connection")
			lastErr = err
		}
	}
	return lastErr
}

// WithFailover wraps an RPC call with automatic failover on error.
// It attempts the call on all endpoints before giving up.
//
// Example:
//
//	resp, err := WithFailover(c, func() (interface{}, error) {
//		return c.OracleClient().Params(ctx, &oracletypes.QueryParamsRequest{})
//	})
//	if err != nil {
//		return nil, err
//	}
//	params := resp.(*oracletypes.QueryParamsResponse)
func WithFailover[T any](c *Client, call func() (T, error)) (T, error) {
	return WithFailoverRetry(c, call, 0) // Default: try all endpoints once
}

// WithFailoverRetry wraps an RPC call with automatic failover and configurable retries.
// 
// Parameters:
//   - call: The RPC function to execute
//   - maxAttempts: Maximum number of attempts (0 = number of endpoints, no retries)
//
// Behavior:
//   - For transient errors (NotFound, connection issues), retries on same endpoint with exponential backoff
//   - For persistent errors, rotates to next endpoint
//   - Uses exponential backoff: 500ms, 1s, 2s, 4s, 8s (capped at 8s)
//
// Example:
//
//	// Retry up to 10 times for tx confirmation (with backoff, no premature rotation)
//	resp, err := WithFailoverRetry(c, func() (*txservice.GetTxResponse, error) {
//		return c.TxClient().GetTx(ctx, &txservice.GetTxRequest{Hash: txHash})
//	}, 10)
func WithFailoverRetry[T any](c *Client, call func() (T, error), maxAttempts int) (T, error) {
	var zero T
	
	// Default maxAttempts to number of endpoints (one try per endpoint, no retries)
	if maxAttempts == 0 {
		maxAttempts = len(c.endpoints)
	}

	currentEndpointAttempts := 0
	endpointIndex := 0
	baseDelay := 500 * time.Millisecond

	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := call()
		if err == nil {
			return resp, nil
		}

		// Check if this is a transient error (expected during tx confirmation polling)
		isTransientError := strings.Contains(err.Error(), "tx not found") ||
			strings.Contains(err.Error(), "NotFound") ||
			strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "timeout")

		// Determine if this is the last attempt overall
		isLastAttempt := attempt == maxAttempts-1

		// Log appropriately
		logEvent := c.logger.Debug()
		if isLastAttempt && !isTransientError {
			logEvent = c.logger.Error()
		}
		
		logEvent.
			Err(err).
			Str("endpoint", c.CurrentEndpoint()).
			Int("attempt", attempt+1).
			Int("max_attempts", maxAttempts).
			Int("endpoint_attempts", currentEndpointAttempts+1).
			Bool("transient", isTransientError).
			Msg("RPC call failed")

		// Don't retry/failover on last attempt
		if isLastAttempt {
			break
		}

		currentEndpointAttempts++

		// For transient errors, retry on same endpoint with exponential backoff
		// For persistent errors, rotate to next endpoint after 2-3 attempts
		shouldRotate := !isTransientError && currentEndpointAttempts >= 2

		if shouldRotate && len(c.endpoints) > 1 {
			c.Failover()
			endpointIndex = (endpointIndex + 1) % len(c.endpoints)
			currentEndpointAttempts = 0
			time.Sleep(baseDelay) // Brief delay after rotation
		} else {
			// Exponential backoff: 500ms, 1s, 2s, 4s, 8s (capped)
			delay := baseDelay * time.Duration(1<<min(currentEndpointAttempts, 4))
			time.Sleep(delay)
		}
	}

	return zero, fmt.Errorf("all %d attempts failed across gRPC endpoints", maxAttempts)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetAccount retrieves account information (account number and sequence) for the given address.
// Uses failover if the query fails on the current endpoint.
func (c *Client) GetAccount(ctx context.Context, address sdk.AccAddress) (uint64, uint64, error) {
	resp, err := WithFailover(c, func() (*authtypes.QueryAccountResponse, error) {
		return c.AuthClient().Account(ctx, &authtypes.QueryAccountRequest{
			Address: address.String(),
		})
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query account: %w", err)
	}

	var acc authtypes.AccountI
	if err := c.ir.UnpackAny(resp.Account, &acc); err != nil {
		return 0, 0, fmt.Errorf("failed to unpack account: %w", err)
	}

	return acc.GetAccountNumber(), acc.GetSequence(), nil
}

// GetAggregatePrevote retrieves the aggregate prevote for the given validator.
// Uses failover if the query fails on the current endpoint.
func (c *Client) GetAggregatePrevote(ctx context.Context, validator sdk.ValAddress) (*oracletypes.AggregateExchangeRatePrevote, error) {
	resp, err := WithFailover(c, func() (*oracletypes.QueryAggregatePrevoteResponse, error) {
		return c.OracleClient().AggregatePrevote(ctx, &oracletypes.QueryAggregatePrevoteRequest{
			ValidatorAddr: validator.String(),
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregate prevote: %w", err)
	}

	return &resp.AggregatePrevote, nil
}

// GetOracleParams retrieves the oracle module parameters.
// Uses failover if the query fails on the current endpoint.
func (c *Client) GetOracleParams(ctx context.Context) (*oracletypes.Params, error) {
	resp, err := WithFailover(c, func() (*oracletypes.QueryParamsResponse, error) {
		return c.OracleClient().Params(ctx, &oracletypes.QueryParamsRequest{})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query oracle params: %w", err)
	}

	return &resp.Params, nil
}

// BroadcastTx broadcasts a transaction to the chain using BROADCAST_MODE_SYNC.
// Uses failover if the broadcast fails on the current endpoint.
func (c *Client) BroadcastTx(ctx context.Context, txBytes []byte) (*sdk.TxResponse, error) {
	resp, err := WithFailover(c, func() (*txservice.BroadcastTxResponse, error) {
		return c.TxClient().BroadcastTx(ctx, &txservice.BroadcastTxRequest{
			TxBytes: txBytes,
			Mode:    txservice.BroadcastMode_BROADCAST_MODE_SYNC,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to broadcast tx: %w", err)
	}

	return resp.TxResponse, nil
}

// GetTx retrieves a transaction by hash.
// Uses failover with retries since tx may not be in a block yet (up to 10 attempts with exponential backoff).
func (c *Client) GetTx(ctx context.Context, txHash string) (*sdk.TxResponse, error) {
	resp, err := WithFailoverRetry(c, func() (*txservice.GetTxResponse, error) {
		return c.TxClient().GetTx(ctx, &txservice.GetTxRequest{
			Hash: txHash,
		})
	}, 10) // Retry up to 10 times with exponential backoff (max ~16s total)
	if err != nil {
		return nil, fmt.Errorf("failed to get tx: %w", err)
	}

	return resp.TxResponse, nil
}

// QuerySmartContract queries a CosmWasm smart contract with the given query message.
// Uses failover if the query fails on the current endpoint.
func (c *Client) QuerySmartContract(ctx context.Context, contractAddress string, queryMsg []byte) ([]byte, error) {
	resp, err := WithFailover(c, func() (*wasmtypes.QuerySmartContractStateResponse, error) {
		return c.WasmClient().SmartContractState(ctx, &wasmtypes.QuerySmartContractStateRequest{
			Address:   contractAddress,
			QueryData: queryMsg,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query smart contract: %w", err)
	}

	return resp.Data, nil
}
