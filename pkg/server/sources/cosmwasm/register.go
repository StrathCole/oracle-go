package cosmwasm

import (
	"fmt"
	"sync"

	"tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/server/sources"
)

var (
	// Global gRPC client for CosmWasm sources
	globalGRPCClient *client.Client
	clientMu         sync.RWMutex
)

// SetGRPCClient sets the global gRPC client for CosmWasm sources
func SetGRPCClient(c *client.Client) {
	clientMu.Lock()
	defer clientMu.Unlock()
	globalGRPCClient = c
}

// GetGRPCClient returns the global gRPC client
func GetGRPCClient() *client.Client {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return globalGRPCClient
}

func init() {
	// Register CosmWasm sources with factory functions that use the global gRPC client
	sources.Register("cosmwasm.terraport", func(config map[string]interface{}) (sources.Source, error) {
		grpcClient := GetGRPCClient()
		if grpcClient == nil {
			return nil, fmt.Errorf("gRPC client not initialized for CosmWasm sources")
		}
		return NewTerraportSource(config, grpcClient)
	})

	sources.Register("cosmwasm.terraswap", func(config map[string]interface{}) (sources.Source, error) {
		grpcClient := GetGRPCClient()
		if grpcClient == nil {
			return nil, fmt.Errorf("gRPC client not initialized for CosmWasm sources")
		}
		return NewTerraswapSource(config, grpcClient)
	})

	sources.Register("cosmwasm.garuda", func(config map[string]interface{}) (sources.Source, error) {
		grpcClient := GetGRPCClient()
		if grpcClient == nil {
			return nil, fmt.Errorf("gRPC client not initialized for CosmWasm sources")
		}
		return NewGarudaSource(config, grpcClient)
	})
}
