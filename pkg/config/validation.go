// Package config provides configuration loading and validation for oracle-go.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Validate checks configuration for errors.
func Validate(cfg *Config) error {
	// Validate mode
	mode := cfg.NormalizeMode()
	if mode != ModeBoth && mode != ModeServer && mode != ModeFeeder {
		return fmt.Errorf("%w: %s (must be 'both', 'server', or 'feeder')", ErrInvalidMode, cfg.Mode)
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
			return ErrNoSourcesConfigured
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
	if mode != "median" && mode != "average" {
		return fmt.Errorf("%w: %s", ErrInvalidAggregateMode, cfg.AggregateMode)
	}

	// Validate TLS config
	if cfg.HTTP.TLS.Enabled {
		if cfg.HTTP.TLS.Cert == "" || cfg.HTTP.TLS.Key == "" {
			return fmt.Errorf("%w", ErrTLSConfigIncomplete)
		}
		if _, err := os.Stat(cfg.HTTP.TLS.Cert); err != nil {
			return fmt.Errorf("%w: %s", ErrTLSCertNotFound, cfg.HTTP.TLS.Cert)
		}
		if _, err := os.Stat(cfg.HTTP.TLS.Key); err != nil {
			return fmt.Errorf("%w: %s", ErrTLSKeyNotFound, cfg.HTTP.TLS.Key)
		}
	}

	return nil
}

func validateFeederConfig(cfg *FeederConfig) error {
	// Validate chain ID
	if cfg.ChainID == "" {
		return fmt.Errorf("%w", ErrChainIDRequired)
	}

	// Validate gRPC endpoints
	if len(cfg.GRPCEndpoints) == 0 {
		return fmt.Errorf("%w", ErrNoGRPCEndpoints)
	}
	for i, ep := range cfg.GRPCEndpoints {
		if ep.Host == "" {
			return fmt.Errorf("%w (grpc_endpoints[%d])", ErrGRPCHostRequired, i)
		}
	}

	// Validate RPC endpoints (required - no fallback to gRPC)
	if len(cfg.RPCEndpoints) == 0 {
		return fmt.Errorf("%w", ErrNoRPCEndpoints)
	}
	for i, ep := range cfg.RPCEndpoints {
		if ep.Host == "" {
			return fmt.Errorf("%w (rpc_endpoints[%d])", ErrRPCHostRequired, i)
		}
	}

	// Validate validators
	if len(cfg.Validators) == 0 {
		return fmt.Errorf("%w", ErrNoValidators)
	}
	for i, val := range cfg.Validators {
		if !strings.HasPrefix(val, "terravaloper") && !strings.HasPrefix(val, "cosmosvaloper") {
			return fmt.Errorf("%w (validator[%d])", ErrInvalidValidator, i)
		}
		// Check for placeholder values
		if strings.Contains(val, "xxx") || strings.Contains(val, "...") {
			return fmt.Errorf("%w: %s (validator[%d])", ErrPlaceholderValidator, val, i)
		}
		// Validate minimum length (bech32 addresses are at least 39 characters)
		if len(val) < 39 {
			return fmt.Errorf("%w (validator[%d])", ErrValidatorTooShort, i)
		}
	}

	// Validate mnemonic (either direct or from env)
	if cfg.Mnemonic == "" && cfg.MnemonicEnv == "" {
		return fmt.Errorf("%w", ErrMnemonicRequired)
	}
	if cfg.MnemonicEnv != "" {
		if os.Getenv(cfg.MnemonicEnv) == "" {
			return fmt.Errorf("%w: %s", ErrMnemonicEnvNotSet, cfg.MnemonicEnv)
		}
	}

	// Validate gas price is configured
	if cfg.GasPrice == "" {
		return fmt.Errorf("%w", ErrFeeConfigRequired)
	}

	// Validate price source
	if cfg.PriceSource.URL == "" {
		return fmt.Errorf("%w", ErrPriceSourceURLRequired)
	}
	srcType := strings.ToLower(cfg.PriceSource.Type)
	if srcType != "http" && srcType != "grpc" && srcType != "websocket" {
		return fmt.Errorf("%w: %s", ErrInvalidPriceSourceType, cfg.PriceSource.Type)
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
		return fmt.Errorf("%w: %s", ErrInvalidSourceType, cfg.Type)
	}

	// Validate name
	if cfg.Name == "" {
		return fmt.Errorf("%w", ErrSourceNameRequired)
	}

	// Weight should be positive (0 defaults to 1.0 at runtime)
	if cfg.Weight < 0 {
		return fmt.Errorf("%w", ErrSourceWeightMustBeNonNegative)
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
		return fmt.Errorf("%w: %s", ErrInvalidLogLevel, cfg.Level)
	}

	// Validate format
	formatValid := strings.ToLower(cfg.Format) == "json" || strings.ToLower(cfg.Format) == "text"
	if !formatValid {
		return fmt.Errorf("%w: %s", ErrInvalidLogFormat, cfg.Format)
	}

	return nil
}
