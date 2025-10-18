package config

import (
	"fmt"
	"os"
	"strings"
)

// Validate checks configuration for errors
func Validate(cfg *Config) error {
	// Validate mode
	mode := cfg.NormalizeMode()
	if mode != "both" && mode != "server" && mode != "feeder" {
		return fmt.Errorf("invalid mode: %s (must be 'both', 'server', or 'feeder')", cfg.Mode)
	}

	// Validate server config if in server mode
	if cfg.IsServerMode() {
		if err := validateServerConfig(&cfg.Server); err != nil {
			return fmt.Errorf("server config: %w", err)
		}
	}

	// Validate feeder config if in feeder mode
	if cfg.IsFeederMode() {
		if err := validateFeederConfig(&cfg.Feeder); err != nil {
			return fmt.Errorf("feeder config: %w", err)
		}
	}

	// Validate sources if in server mode
	if cfg.IsServerMode() {
		if len(cfg.Sources) == 0 {
			return fmt.Errorf("at least one price source must be configured")
		}
		for i, source := range cfg.Sources {
			if err := validateSourceConfig(&source); err != nil {
				return fmt.Errorf("source %d (%s.%s): %w", i, source.Type, source.Name, err)
			}
		}
	}

	// Validate logging config
	if err := validateLoggingConfig(&cfg.Logging); err != nil {
		return fmt.Errorf("logging config: %w", err)
	}

	return nil
}

func validateServerConfig(cfg *ServerConfig) error {
	// Validate aggregate mode
	mode := strings.ToLower(cfg.AggregateMode)
	if mode != "median" && mode != "average" && mode != "tvwap" {
		return fmt.Errorf("invalid aggregate_mode: %s (must be 'median', 'average', or 'tvwap')", cfg.AggregateMode)
	}

	// Validate TLS config
	if cfg.HTTP.TLS.Enabled {
		if cfg.HTTP.TLS.Cert == "" || cfg.HTTP.TLS.Key == "" {
			return fmt.Errorf("TLS cert and key must be specified when TLS is enabled")
		}
		if _, err := os.Stat(cfg.HTTP.TLS.Cert); err != nil {
			return fmt.Errorf("TLS cert file not found: %s", cfg.HTTP.TLS.Cert)
		}
		if _, err := os.Stat(cfg.HTTP.TLS.Key); err != nil {
			return fmt.Errorf("TLS key file not found: %s", cfg.HTTP.TLS.Key)
		}
	}

	return nil
}

func validateFeederConfig(cfg *FeederConfig) error {
	// Validate chain ID
	if cfg.ChainID == "" {
		return fmt.Errorf("chain_id must be specified")
	}

	// Validate gRPC endpoints
	if len(cfg.GRPCEndpoints) == 0 {
		return fmt.Errorf("at least one grpc_endpoint must be specified")
	}
	for i, ep := range cfg.GRPCEndpoints {
		if ep.Host == "" {
			return fmt.Errorf("grpc_endpoints[%d]: host must be specified", i)
		}
	}

	// Validate RPC endpoints (required - no fallback to gRPC)
	if len(cfg.RPCEndpoints) == 0 {
		return fmt.Errorf("at least one rpc_endpoint must be specified")
	}
	for i, ep := range cfg.RPCEndpoints {
		if ep.Host == "" {
			return fmt.Errorf("rpc_endpoints[%d]: host must be specified", i)
		}
	}

	// Validate validators
	if len(cfg.Validators) == 0 {
		return fmt.Errorf("at least one validator must be specified")
	}
	for i, val := range cfg.Validators {
		if !strings.HasPrefix(val, "terravaloper") && !strings.HasPrefix(val, "cosmosvaloper") {
			return fmt.Errorf("validator[%d] must start with terravaloper or cosmosvaloper", i)
		}
		// Check for placeholder values
		if strings.Contains(val, "xxx") || strings.Contains(val, "...") {
			return fmt.Errorf("validator[%d] is a placeholder value '%s' - please replace with actual validator address", i, val)
		}
		// Validate minimum length (bech32 addresses are at least 39 characters)
		if len(val) < 39 {
			return fmt.Errorf("validator[%d] '%s' is too short - must be a valid bech32 address", i, val)
		}
	}

	// Validate mnemonic (either direct or from env)
	if cfg.Mnemonic == "" && cfg.MnemonicEnv == "" {
		return fmt.Errorf("either mnemonic or mnemonic_env must be specified")
	}
	if cfg.MnemonicEnv != "" {
		if os.Getenv(cfg.MnemonicEnv) == "" {
			return fmt.Errorf("environment variable %s not set (required for mnemonic)", cfg.MnemonicEnv)
		}
	}

	// Validate fee configuration
	if cfg.FeeAmount == "" && cfg.GasPrice == "" {
		return fmt.Errorf("either fee_amount or gas_price must be specified")
	}

	// Validate price source
	if cfg.PriceSource.URL == "" {
		return fmt.Errorf("price_source.url must be specified")
	}
	srcType := strings.ToLower(cfg.PriceSource.Type)
	if srcType != "http" && srcType != "grpc" && srcType != "websocket" {
		return fmt.Errorf("invalid price_source.type: %s (must be 'http', 'grpc', or 'websocket')", cfg.PriceSource.Type)
	}

	// Validate vote period (default to 30 if not specified)
	if cfg.VotePeriod == 0 {
		cfg.VotePeriod = 30 // Default Terra Classic vote period
	}

	return nil
}

func validateSourceConfig(cfg *SourceConfig) error {
	// Validate type
	validTypes := []string{"cex", "cosmwasm", "evm", "oracle", "fiat", "sdr"}
	typeValid := false
	for _, t := range validTypes {
		if strings.ToLower(cfg.Type) == t {
			typeValid = true
			break
		}
	}
	if !typeValid {
		return fmt.Errorf("invalid type: %s (must be one of: %s)", cfg.Type, strings.Join(validTypes, ", "))
	}

	// Validate name
	if cfg.Name == "" {
		return fmt.Errorf("name must be specified")
	}

	// Weight should be positive (0 defaults to 1.0 at runtime)
	if cfg.Weight < 0 {
		return fmt.Errorf("weight must be >= 0")
	}

	return nil
}

func validateLoggingConfig(cfg *LoggingConfig) error {
	// Validate level
	validLevels := []string{"debug", "info", "warn", "error"}
	levelValid := false
	for _, l := range validLevels {
		if strings.ToLower(cfg.Level) == l {
			levelValid = true
			break
		}
	}
	if !levelValid {
		return fmt.Errorf("invalid level: %s (must be one of: %s)", cfg.Level, strings.Join(validLevels, ", "))
	}

	// Validate format
	formatValid := strings.ToLower(cfg.Format) == "json" || strings.ToLower(cfg.Format) == "text"
	if !formatValid {
		return fmt.Errorf("invalid format: %s (must be 'json' or 'text')", cfg.Format)
	}

	return nil
}
