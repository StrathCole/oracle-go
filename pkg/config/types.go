// Package config provides configuration loading and validation for oracle-go.
package config

import (
	"fmt"
	"time"
)

// Mode type constants.
const (
	ModeServer = "server"
	ModeFeeder = "feeder"
	ModeBoth   = "both"
)

// Config is the root configuration structure.
type Config struct {
	Mode    string         `yaml:"mode"`
	Server  ServerConfig   `yaml:"server"`
	Feeder  FeederConfig   `yaml:"feeder"`
	Sources []SourceConfig `yaml:"sources"`
	Metrics MetricsConfig  `yaml:"metrics"`
	Logging LoggingConfig  `yaml:"logging"`
}

// ServerConfig configures the price server component.
type ServerConfig struct {
	HTTP          HTTPConfig `yaml:"http"`
	WebSocket     WSConfig   `yaml:"websocket"`
	CacheTTL      Duration   `yaml:"cache_ttl"`
	AggregateMode string     `yaml:"aggregate_mode"`
}

// HTTPConfig configures the HTTP server.
type HTTPConfig struct {
	Addr string    `yaml:"addr"`
	TLS  TLSConfig `yaml:"tls"`
}

// WSConfig configures the WebSocket server.
type WSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
}

// TLSConfig holds TLS certificate configuration.
type TLSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

// FeederConfig configures the feeder component.
type FeederConfig struct {
	ChainID       string            `yaml:"chain_id"`       // Chain ID (e.g., "columbus-5")
	GRPCEndpoints []GRPCEndpoint    `yaml:"grpc_endpoints"` // Multiple gRPC endpoints for failover
	RPCEndpoints  []RPCEndpoint     `yaml:"rpc_endpoints"`  // Multiple Tendermint RPC endpoints for WebSocket failover
	Validators    []string          `yaml:"validators"`     // Validator addresses to vote for
	Mnemonic      string            `yaml:"mnemonic"`       // BIP39 mnemonic (or use MnemonicEnv)
	MnemonicEnv   string            `yaml:"mnemonic_env"`   // Environment variable for mnemonic
	HDPath        string            `yaml:"hd_path"`        // HD derivation path (default: "m/44'/330'/0'/0/0" for Terra Classic)
	CoinType      uint32            `yaml:"coin_type"`      // BIP44 coin type (default: 330 for Terra Classic, 118 for Cosmos)
	GasPrice      string            `yaml:"gas_price"`      // Gas price for fee calculation (uluna)
	FeeDenom      string            `yaml:"fee_denom"`      // Fee denomination (default: uluna for Terra Classic)
	PriceSource   PriceSourceConfig `yaml:"price_source"`   // Where to fetch prices
	VotePeriod    uint64            `yaml:"vote_period"`    // Vote period in blocks (default: 30)
	DryRun        bool              `yaml:"dry_run"`        // Dry run mode: create votes but don't submit (for testing)
	Verify        bool              `yaml:"verify"`         // Verify votes against on-chain rates (requires DryRun)
}

// GRPCEndpoint represents a gRPC endpoint configuration.
type GRPCEndpoint struct {
	Host string `yaml:"host"` // Hostname (e.g., "grpc.terra-classic.hexxagon.io")
	Port uint16 `yaml:"port"` // Port (default: 9090, or 443 for TLS)
	TLS  bool   `yaml:"tls"`  // Use TLS
}

// ToAddress converts GRPCEndpoint to address string.
func (g GRPCEndpoint) ToAddress() string {
	port := g.Port
	if port == 0 {
		if g.TLS {
			port = 443 // Default TLS port
		} else {
			port = 9090 // Default gRPC port
		}
	}

	return fmt.Sprintf("%s:%d", g.Host, port)
}

// RPCEndpoint represents a Tendermint RPC endpoint configuration.
type RPCEndpoint struct {
	Host string `yaml:"host"` // Hostname (e.g., "rpc.hexxagon.io")
	Port uint16 `yaml:"port"` // Port (default: 26657)
	TLS  bool   `yaml:"tls"`  // Use TLS (wss:// vs ws://)
}

// ToURL converts RPCEndpoint to WebSocket URL.
func (r RPCEndpoint) ToURL() string {
	protocol := "ws"
	if r.TLS {
		protocol = "wss"
	}

	port := r.Port
	if port == 0 {
		port = 26657 // Default Tendermint port
	}

	// For standard ports (443 for wss, 80 for ws), omit port from URL
	if (r.TLS && port == 443) || (!r.TLS && port == 80) {
		return fmt.Sprintf("%s://%s/websocket", protocol, r.Host)
	}

	return fmt.Sprintf("%s://%s:%d/websocket", protocol, r.Host, port)
}

// PriceSourceConfig configures where feeder fetches prices.
type PriceSourceConfig struct {
	URL          string   `yaml:"url"`
	FallbackURLs []string `yaml:"fallback_urls"`
	Type         string   `yaml:"type"`
}

// SourceConfig configures a price source.
type SourceConfig struct {
	Type    string                 `yaml:"type"`
	Name    string                 `yaml:"name"`
	Enabled bool                   `yaml:"enabled"`
	Weight  float64                `yaml:"weight"` // Weight for aggregation (default: 1.0, CosmWasm sources: 0.5)
	Config  map[string]interface{} `yaml:"config"`
}

// MetricsConfig configures Prometheus metrics.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	Path    string `yaml:"path"`
}

// LoggingConfig configures logging.
type LoggingConfig struct {
	Level  string        `yaml:"level"`
	Format string        `yaml:"format"`
	Output string        `yaml:"output"`
	File   LogFileConfig `yaml:"file"`
}

// LogFileConfig configures log file rotation.
type LogFileConfig struct {
	Path       string `yaml:"path"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
}

// Duration is a wrapper around time.Duration for YAML parsing.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	td, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(td)
	return nil
}

// ToDuration converts Duration to time.Duration.
func (d Duration) ToDuration() time.Duration {
	return time.Duration(d)
}
