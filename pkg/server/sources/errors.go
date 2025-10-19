// Package sources provides price source interfaces and implementations.
package sources

import "errors"

var (
	// ErrNoPricesAvailable indicates that no prices are available from the source.
	ErrNoPricesAvailable = errors.New("no prices available")
	// ErrUnexpectedStatus indicates an unexpected HTTP status code.
	ErrUnexpectedStatus = errors.New("unexpected HTTP status code")
	// ErrRateLimitExceeded indicates that a rate limit has been exceeded.
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	// ErrAPIError indicates an API error.
	ErrAPIError = errors.New("API error")
	// ErrInvalidResponse indicates an invalid response from the source.
	ErrInvalidResponse = errors.New("invalid response")
	// ErrWebSocketDisconnect indicates a WebSocket disconnection.
	ErrWebSocketDisconnect = errors.New("websocket disconnected")
	// ErrInvalidSymbol indicates an invalid symbol.
	ErrInvalidSymbol = errors.New("invalid symbol")
	// ErrSourceNotHealthy indicates that the source is not healthy.
	ErrSourceNotHealthy = errors.New("source not healthy")
	// ErrSourceStopped indicates that the source has been stopped.
	ErrSourceStopped = errors.New("source stopped")
	// ErrInvalidConfig indicates that the source configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")
	// ErrNoSymbolsConfigured indicates that no symbols are configured for the source.
	ErrNoSymbolsConfigured = errors.New("no symbols configured")
	// ErrNoTickersInResponse indicates that no tickers are in the response.
	ErrNoTickersInResponse = errors.New("no tickers in response")
	// ErrNoMatchingSymbols indicates that no matching symbols are found in response.
	ErrNoMatchingSymbols = errors.New("no matching symbols found in response")
	// ErrNoValidSymbols indicates that no valid symbols are to fetch.
	ErrNoValidSymbols = errors.New("no valid symbols to fetch")
	// ErrNoPricesExtracted indicates that no prices are extracted from response.
	ErrNoPricesExtracted = errors.New("no prices extracted from response")
	// ErrNoSymbolsForPrice indicates that no prices are available for a symbol.
	ErrNoSymbolsForPrice = errors.New("no prices for symbol")
	// ErrAPIKeyRequired indicates that an API key is required.
	ErrAPIKeyRequired = errors.New("API key is required")
	// ErrKeyNotFound indicates that the key was not found.
	ErrKeyNotFound = errors.New("key not found")
	// ErrZeroLiquidity indicates that there is zero liquidity in the pool.
	ErrZeroLiquidity = errors.New("zero liquidity in pool")
	// ErrInvalidPoolResponse indicates that the pool response is invalid.
	ErrInvalidPoolResponse = errors.New("invalid pool response")
	// ErrNoPairsConfigured indicates that no valid pairs are configured.
	ErrNoPairsConfigured = errors.New("no valid pairs configured")
	// ErrRPCURLRequired indicates that rpc_url is required.
	ErrRPCURLRequired = errors.New("rpc_url is required")
	// ErrChainIDRequired indicates that chain_id is required.
	ErrChainIDRequired = errors.New("chain_id is required")
	// ErrPairsConfigRequired indicates that pairs configuration is required.
	ErrPairsConfigRequired = errors.New("pairs configuration is required")
	// ErrSymbolsMustBeArray indicates that symbols must be an array.
	ErrSymbolsMustBeArray = errors.New("symbols must be an array")
	// ErrClientNotInitialized indicates that the client is not initialized.
	ErrClientNotInitialized = errors.New("client not initialized")
	// ErrSubscribeNotImplemented indicates that subscribe is not implemented.
	ErrSubscribeNotImplemented = errors.New("subscribe not implemented")
	// ErrNoPricesFetched indicates that no prices were fetched.
	ErrNoPricesFetched = errors.New("failed to fetch any prices")
	// ErrNoPairsConfiguredHelper indicates that no pairs are configured.
	ErrNoPairsConfiguredHelper = errors.New("no pairs configured")
	// ErrPairsMustBeArray indicates that pairs must be an array.
	ErrPairsMustBeArray = errors.New("pairs must be an array")
	// ErrInvalidSymbolFormat indicates that the symbol format is invalid.
	ErrInvalidSymbolFormat = errors.New("symbol must be in BASE/QUOTE format")
	// ErrEmptyBaseCurrency indicates that the symbol BASE currency cannot be empty.
	ErrEmptyBaseCurrency = errors.New("symbol BASE currency cannot be empty")
	// ErrEmptyQuoteCurrency indicates that the symbol QUOTE currency cannot be empty.
	ErrEmptyQuoteCurrency = errors.New("symbol QUOTE currency cannot be empty")
)
