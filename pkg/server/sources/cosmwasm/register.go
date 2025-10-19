// Package cosmwasm provides CosmWasm-based price sources.
package cosmwasm

import (
	"fmt"
	"sync"

	"github.com/StrathCole/oracle-go/pkg/feeder/client"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

var (
	// Global gRPC client for CosmWasm sources.
	globalGRPCClient *client.Client
	clientMu         sync.RWMutex
)

// SetGRPCClient sets the global gRPC client for CosmWasm sources.
func SetGRPCClient(c *client.Client) {
	clientMu.Lock()
	defer clientMu.Unlock()
	globalGRPCClient = c
}

// GetGRPCClient returns the global gRPC client.
func GetGRPCClient() *client.Client {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return globalGRPCClient
}

func init() {
	// Register CosmWasm sources with factory functions that use the global gRPC client.
	sources.Register("cosmwasm.terraport", func(config map[string]interface{}) (sources.Source, error) {
		grpcClient := GetGRPCClient()
		if grpcClient == nil {
			return nil, fmt.Errorf("%w", ErrGRPCNotInitialized)
		}
		return NewTerraportSource(config, grpcClient)
	})

	sources.Register("cosmwasm.terraswap", func(config map[string]interface{}) (sources.Source, error) {
		grpcClient := GetGRPCClient()
		if grpcClient == nil {
			return nil, fmt.Errorf("%w", ErrGRPCNotInitialized)
		}
		return NewTerraswapSource(config, grpcClient)
	})

	sources.Register("cosmwasm.garuda", func(config map[string]interface{}) (sources.Source, error) {
		grpcClient := GetGRPCClient()
		if grpcClient == nil {
			return nil, fmt.Errorf("%w", ErrGRPCNotInitialized)
		}
		return NewGarudaSource(config, grpcClient)
	})
}
