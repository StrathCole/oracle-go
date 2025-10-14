package config

import "time"

// Config is the root configuration structure
type Config struct {
	Mode    string         `yaml:"mode"`
	Server  ServerConfig   `yaml:"server"`
	Feeder  FeederConfig   `yaml:"feeder"`
	Sources []SourceConfig `yaml:"sources"`
	Metrics MetricsConfig  `yaml:"metrics"`
	Logging LoggingConfig  `yaml:"logging"`
}

// ServerConfig configures the price server component
type ServerConfig struct {
	HTTP          HTTPConfig `yaml:"http"`
	WebSocket     WSConfig   `yaml:"websocket"`
	CacheTTL      Duration   `yaml:"cache_ttl"`
	AggregateMode string     `yaml:"aggregate_mode"`
}

// HTTPConfig configures the HTTP server
type HTTPConfig struct {
	Addr string    `yaml:"addr"`
	TLS  TLSConfig `yaml:"tls"`
}

// WSConfig configures the WebSocket server
type WSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
}

// TLSConfig holds TLS certificate configuration
type TLSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

// FeederConfig configures the feeder component
type FeederConfig struct {
	ChainID       string            `yaml:"chain_id"`       // Chain ID (e.g., "columbus-5")
	GRPCEndpoint  string            `yaml:"grpc_endpoint"`  // Primary gRPC endpoint
	GRPCEndpoints []string          `yaml:"grpc_endpoints"` // Multiple gRPC endpoints for failover
	EnableTLS     bool              `yaml:"enable_tls"`     // Enable TLS for gRPC
	Validators    []string          `yaml:"validators"`     // Validator addresses to vote for
	Mnemonic      string            `yaml:"mnemonic"`       // BIP39 mnemonic (or use MnemonicEnv)
	MnemonicEnv   string            `yaml:"mnemonic_env"`   // Environment variable for mnemonic
	FeeAmount     string            `yaml:"fee_amount"`     // Fee amount (e.g., "100000uluna")
	GasLimit      uint64            `yaml:"gas_limit"`      // Gas limit (0 = auto estimate)
	GasPrice      string            `yaml:"gas_price"`      // Gas price for fee calculation
	PriceSource   PriceSourceConfig `yaml:"price_source"`   // Where to fetch prices
	VotePeriod    uint64            `yaml:"vote_period"`    // Vote period in blocks (default: 30)
}

// PriceSourceConfig configures where feeder fetches prices
type PriceSourceConfig struct {
	URL          string   `yaml:"url"`
	FallbackURLs []string `yaml:"fallback_urls"`
	Type         string   `yaml:"type"`
}

// SourceConfig configures a price source
type SourceConfig struct {
	Type     string                 `yaml:"type"`
	Name     string                 `yaml:"name"`
	Enabled  bool                   `yaml:"enabled"`
	Priority int                    `yaml:"priority"`
	Config   map[string]interface{} `yaml:"config"`
}

// MetricsConfig configures Prometheus metrics
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	Path    string `yaml:"path"`
}

// LoggingConfig configures logging
type LoggingConfig struct {
	Level  string        `yaml:"level"`
	Format string        `yaml:"format"`
	Output string        `yaml:"output"`
	File   LogFileConfig `yaml:"file"`
}

// LogFileConfig configures log file rotation
type LogFileConfig struct {
	Path       string `yaml:"path"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
}

// Duration is a wrapper around time.Duration for YAML parsing
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler
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

// ToDuration converts Duration to time.Duration
func (d Duration) ToDuration() time.Duration {
	return time.Duration(d)
}
