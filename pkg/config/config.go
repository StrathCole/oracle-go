// Package config provides configuration loading and validation for oracle-go.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load loads configuration from YAML file and environment variables.
func Load(path string) (*Config, error) {
	// Validate and sanitize path
	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("invalid config path: %w", err)
	}

	// Read config file
	data, err := os.ReadFile(absPath) // #nosec G304 -- Path sanitized with filepath.Clean and filepath.Abs
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Expand environment variables in YAML
	expanded := os.ExpandEnv(string(data))

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Apply defaults
	applyDefaults(&cfg)

	return &cfg, nil
}

// applyDefaults sets default values for optional fields.
func applyDefaults(cfg *Config) {
	// Default mode
	if cfg.Mode == "" {
		cfg.Mode = ModeBoth
	}

	// Server defaults
	if cfg.Server.HTTP.Addr == "" {
		cfg.Server.HTTP.Addr = ":8080"
	}
	if cfg.Server.CacheTTL.ToDuration() == 0 {
		cfg.Server.CacheTTL = Duration(60 * 1e9) // 60 seconds
	}
	if cfg.Server.AggregateMode == "" {
		cfg.Server.AggregateMode = "median"
	}

	// Metrics defaults
	if cfg.Metrics.Enabled && cfg.Metrics.Addr == "" {
		cfg.Metrics.Addr = ":9091"
	}
	if cfg.Metrics.Path == "" {
		cfg.Metrics.Path = "/metrics"
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if cfg.Logging.Output == "" {
		cfg.Logging.Output = "stdout"
	}

	// Feeder defaults
	if cfg.Feeder.PriceSource.Type == "" {
		cfg.Feeder.PriceSource.Type = "http"
	}
	if cfg.Feeder.VotePeriod == 0 {
		cfg.Feeder.VotePeriod = 30 // Default from chain params
	}
	// HD path defaults for Terra Classic
	if cfg.Feeder.HDPath == "" {
		// Use CoinType if specified, otherwise default to Terra Classic (330)
		coinType := cfg.Feeder.CoinType
		if coinType == 0 {
			coinType = 330 // Terra Classic default
			cfg.Feeder.CoinType = 330
		}
		cfg.Feeder.HDPath = fmt.Sprintf("m/44'/%d'/0'/0/0", coinType)
	}
}

// GetString retrieves a string value from the source configuration.
func (sc *SourceConfig) GetString(key, defaultValue string) string {
	if val, ok := sc.Config[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultValue
}

// GetStringSlice retrieves a string slice from source config.
func (sc *SourceConfig) GetStringSlice(key string) []string {
	if val, ok := sc.Config[key]; ok {
		if slice, ok := val.([]interface{}); ok {
			result := make([]string, 0, len(slice))
			for _, item := range slice {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	return nil
}

// GetInt retrieves an integer from source config.
func (sc *SourceConfig) GetInt(key string, defaultValue int) int {
	if val, ok := sc.Config[key]; ok {
		if i, ok := val.(int); ok {
			return i
		}
	}
	return defaultValue
}

// GetBool retrieves a boolean from source config.
func (sc *SourceConfig) GetBool(key string, defaultValue bool) bool {
	if val, ok := sc.Config[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultValue
}

// NormalizeMode converts mode string to lowercase.
func (c *Config) NormalizeMode() string {
	return strings.ToLower(c.Mode)
}

// IsServerMode returns true if server should run.
func (c *Config) IsServerMode() bool {
	mode := c.NormalizeMode()
	return mode == "both" || mode == "server"
}

// IsFeederMode returns true if feeder should run.
func (c *Config) IsFeederMode() bool {
	mode := c.NormalizeMode()
	return mode == "both" || mode == "feeder"
}
