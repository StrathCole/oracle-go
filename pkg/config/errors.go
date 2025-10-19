// Package config provides configuration loading and validation for oracle-go.
package config

import "errors"

var (
	// ErrInvalidMode indicates that the mode is invalid.
	ErrInvalidMode = errors.New("invalid mode")
	// ErrNoSourcesConfigured indicates that no price sources are configured.
	ErrNoSourcesConfigured = errors.New("at least one price source must be configured")
	// ErrInvalidAggregateMode indicates that the aggregation mode is invalid.
	ErrInvalidAggregateMode = errors.New("invalid aggregate_mode")
	// ErrTLSConfigIncomplete indicates that TLS config is incomplete.
	ErrTLSConfigIncomplete = errors.New("TLS cert and key must be specified when TLS is enabled")
	// ErrTLSCertNotFound indicates that the TLS cert file was not found.
	ErrTLSCertNotFound = errors.New("TLS cert file not found")
	// ErrTLSKeyNotFound indicates that the TLS key file was not found.
	ErrTLSKeyNotFound = errors.New("TLS key file not found")
	// ErrInvalidChainID indicates an invalid chain ID.
	ErrInvalidChainID = errors.New("invalid chain id")
	// ErrChainIDRequired indicates that chain_id must be specified.
	ErrChainIDRequired = errors.New("chain_id must be specified")
	// ErrNoGRPCEndpoints indicates that at least one grpc_endpoint must be specified.
	ErrNoGRPCEndpoints = errors.New("at least one grpc_endpoint must be specified")
	// ErrGRPCHostRequired indicates that grpc endpoint host must be specified.
	ErrGRPCHostRequired = errors.New("grpc endpoint host must be specified")
	// ErrNoRPCEndpoints indicates that at least one rpc_endpoint must be specified.
	ErrNoRPCEndpoints = errors.New("at least one rpc_endpoint must be specified")
	// ErrRPCHostRequired indicates that rpc endpoint host must be specified.
	ErrRPCHostRequired = errors.New("rpc endpoint host must be specified")
	// ErrNoValidators indicates that at least one validator must be specified.
	ErrNoValidators = errors.New("at least one validator must be specified")
	// ErrInvalidValidator indicates that the validator is invalid.
	ErrInvalidValidator = errors.New("validator must start with terravaloper or cosmosvaloper")
	// ErrPlaceholderValidator indicates that the placeholder validator address must be replaced.
	ErrPlaceholderValidator = errors.New("placeholder validator address must be replaced")
	// ErrValidatorTooShort indicates that the validator address is too short.
	ErrValidatorTooShort = errors.New("validator address too short")
	// ErrMnemonicRequired indicates that either mnemonic or mnemonic_env must be specified.
	ErrMnemonicRequired = errors.New("either mnemonic or mnemonic_env must be specified")
	// ErrMnemonicEnvNotSet indicates that the mnemonic environment variable is not set.
	ErrMnemonicEnvNotSet = errors.New("mnemonic environment variable not set")
	// ErrNoMnemonicConfigured indicates that no mnemonic is configured.
	ErrNoMnemonicConfigured = errors.New("no mnemonic configured")
	// ErrFeeConfigRequired indicates that either fee_amount or gas_price must be specified.
	ErrFeeConfigRequired = errors.New("either fee_amount or gas_price must be specified")
	// ErrPriceSourceURLRequired indicates that price_source.url must be specified.
	ErrPriceSourceURLRequired = errors.New("price_source.url must be specified")
	// ErrFeederPriceSourceRequired indicates that feeder price source URL is required.
	ErrFeederPriceSourceRequired = errors.New("feeder price source URL is required")
	// ErrInvalidPriceSourceType indicates that the price_source.type is invalid.
	ErrInvalidPriceSourceType = errors.New("invalid price_source.type")
	// ErrInvalidSourceType indicates that the source type is invalid.
	ErrInvalidSourceType = errors.New("invalid source type")
	// ErrLCDAddressRequired indicates that at least one LCD address is required.
	ErrLCDAddressRequired = errors.New("at least one LCD address is required")
	// ErrValidatorAddressRequired indicates that at least one validator address is required.
	ErrValidatorAddressRequired = errors.New("at least one validator address is required")
	// ErrKeystorePathRequired indicates that keystore path is required.
	ErrKeystorePathRequired = errors.New("keystore path is required")
	// ErrNoSourcesEnabled indicates that no sources are enabled.
	ErrNoSourcesEnabled = errors.New("no sources enabled")
	// ErrSourceTypeRequired indicates that source type is required.
	ErrSourceTypeRequired = errors.New("source type is required")
	// ErrSourceNameRequired indicates that source name is required.
	ErrSourceNameRequired = errors.New("source name is required")
	// ErrUnknownSourceType indicates that the source type is unknown.
	ErrUnknownSourceType = errors.New("unknown source type")
	// ErrSourceWeightMustBeNonNegative indicates that source weight must be >= 0.
	ErrSourceWeightMustBeNonNegative = errors.New("weight must be >= 0")
	// ErrInvalidLogLevel indicates that the log level is invalid.
	ErrInvalidLogLevel = errors.New("invalid log level")
	// ErrInvalidLogFormat indicates that the log format is invalid.
	ErrInvalidLogFormat = errors.New("invalid log format")
)
