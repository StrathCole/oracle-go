package sources

import (
	"strings"
)

// Symbol normalization maps trading pairs to oracle canonical pairs
// This ensures that LUNC/USDT, LUNC/USD, LUNC/USDC all map to the same oracle symbol

// Stablecoin aliases - all considered equivalent to USD
var stablecoinAliases = map[string]string{
	"USDT": "USD",
	"USDC": "USD",
	"BUSD": "USD",
	"DAI":  "USD",
	"TUSD": "USD",
	"USDD": "USD",
	"USDP": "USD",
}

// Base currency aliases
var baseCurrencyAliases = map[string]string{
	"WBTC":  "BTC",
	"WETH":  "ETH",
	"STETH": "ETH",
}

// NormalizeSymbol converts a trading pair symbol to its canonical oracle form
// Examples:
//   - LUNC/USDT -> LUNC/USD
//   - BTC/USDC -> BTC/USD
//   - ETH/USDT -> ETH/USD
//   - WBTC/USD -> BTC/USD
//   - LUNC/EUR -> LUNC/EUR (no change)
func NormalizeSymbol(symbol string) string {
	parts := strings.Split(symbol, "/")
	if len(parts) != 2 {
		return symbol
	}

	base := strings.ToUpper(parts[0])
	quote := strings.ToUpper(parts[1])

	// Normalize base currency if it's an alias
	if normalized, ok := baseCurrencyAliases[base]; ok {
		base = normalized
	}

	// Normalize quote currency (mainly stablecoins)
	if normalized, ok := stablecoinAliases[quote]; ok {
		quote = normalized
	}

	return base + "/" + quote
}

// GetSymbolAliases returns all known aliases for a canonical symbol
// For example, LUNC/USD would return [LUNC/USD, LUNC/USDT, LUNC/USDC, LUNC/BUSD, etc.]
func GetSymbolAliases(canonicalSymbol string) []string {
	parts := strings.Split(canonicalSymbol, "/")
	if len(parts) != 2 {
		return []string{canonicalSymbol}
	}

	base := parts[0]
	quote := parts[1]

	aliases := []string{canonicalSymbol}

	// If the quote is USD, add all stablecoin variants
	if quote == "USD" {
		for stablecoin := range stablecoinAliases {
			if stablecoin != "USD" {
				aliases = append(aliases, base+"/"+stablecoin)
			}
		}
	}

	// If the base has wrapped variants, add those too
	for wrapped, canonical := range baseCurrencyAliases {
		if canonical == base {
			aliases = append(aliases, wrapped+"/"+quote)
			// Also add wrapped + stablecoin combos if quote is USD
			if quote == "USD" {
				for stablecoin := range stablecoinAliases {
					if stablecoin != "USD" {
						aliases = append(aliases, wrapped+"/"+stablecoin)
					}
				}
			}
		}
	}

	return aliases
}

// IsEquivalentSymbol checks if two symbols are equivalent after normalization
func IsEquivalentSymbol(symbol1, symbol2 string) bool {
	return NormalizeSymbol(symbol1) == NormalizeSymbol(symbol2)
}
