// Package fiat provides fiat currency and SDR price sources.
package fiat

import "errors"

var (
	// ErrSymbolsMustBeArray indicates that symbols configuration must be an array.
	ErrSymbolsMustBeArray = errors.New("symbols must be an array")
	// ErrNoValidSymbolsExchange indicates no valid symbols for ExchangeRate.
	ErrNoValidSymbolsExchange = errors.New("no valid symbols for ExchangeRate")
	// ErrAPIKeyRequiredExchange indicates that API key is required for ExchangeRate.
	ErrAPIKeyRequiredExchange = errors.New("api_key is required for ExchangeRate")
	// ErrLoggerNotProvided indicates that a logger was not provided in config.
	ErrLoggerNotProvided = errors.New("logger not provided in config")
	// ErrNoCurrenciesToFetch indicates that no valid currencies are available to fetch.
	ErrNoCurrenciesToFetch = errors.New("no valid currencies to fetch")
	// ErrIMFRequiresSDR indicates that IMF source requires SDR symbol.
	ErrIMFRequiresSDR = errors.New("IMF source requires SDR symbol")
	// ErrSDRRateNotFound indicates that SDR rate was not found in IMF page.
	ErrSDRRateNotFound = errors.New("failed to find SDR rate in IMF page")
	// ErrNoFiatSourcesAvailable indicates that no fiat sources are available for SDR calculation.
	ErrNoFiatSourcesAvailable = errors.New("no fiat sources available for SDR calculation")
	// ErrSymbolCannotBeEmpty indicates that a symbol cannot be empty.
	ErrSymbolCannotBeEmpty = errors.New("symbol cannot be empty")
	// ErrInvalidSymbolFormat indicates that the symbol format is invalid.
	ErrInvalidSymbolFormat = errors.New("symbol must be in BASE/QUOTE format")
	// ErrEmptyBaseCurrency indicates that the symbol BASE currency cannot be empty.
	ErrEmptyBaseCurrency = errors.New("symbol BASE currency cannot be empty")
	// ErrEmptyQuoteCurrency indicates that the symbol QUOTE currency cannot be empty.
	ErrEmptyQuoteCurrency = errors.New("symbol QUOTE currency cannot be empty")
	// ErrInvalidSDRRate indicates that the SDR rate is invalid.
	ErrInvalidSDRRate = errors.New("invalid SDR rate")
	// ErrSourceStoppedRetry indicates that the source stopped during retry.
	ErrSourceStoppedRetry = errors.New("source stopped during retry")
	// ErrSourceStoppedBackoff indicates that the source stopped during backoff.
	ErrSourceStoppedBackoff = errors.New("source stopped during backoff")
	// ErrMissingSymbolsInConfig indicates that symbols are missing in the configuration.
	ErrMissingSymbolsInConfig = errors.New("missing 'symbols' in config")
	// ErrInvalidSymbolsType indicates that the symbols type is invalid.
	ErrInvalidSymbolsType = errors.New("invalid 'symbols' type, expected list")
	// ErrNoValidSymbolsAPI indicates that no valid symbols are available for ExchangeRate-API Free.
	ErrNoValidSymbolsAPI = errors.New("no valid symbols for ExchangeRate-API Free")
	// ErrInvalidResponse indicates that the response is invalid.
	ErrInvalidResponse = errors.New("invalid response")
	// ErrNoPricesUpdated indicates that no prices were updated from the API response.
	ErrNoPricesUpdated = errors.New("no prices updated from API response")
	// ErrNoValidSymbolsFixer indicates that no valid symbols are available for Fixer.
	ErrNoValidSymbolsFixer = errors.New("no valid symbols for Fixer")
	// ErrAPIKeyRequiredFixer indicates that an API key is required for Fixer.
	ErrAPIKeyRequiredFixer = errors.New("api_key is required for Fixer")
	// ErrNoValidSymbolsFrankfurt indicates that no valid symbols are available for Frankfurter.
	ErrNoValidSymbolsFrankfurt = errors.New("no valid symbols for Frankfurter")
	// ErrMissingBasketCurr indicates that basket currencies are missing.
	ErrMissingBasketCurr = errors.New("missing basket currencies")
	// ErrInvalidSDRValue indicates that the SDR value is invalid.
	ErrInvalidSDRValue = errors.New("invalid SDR value")
)
