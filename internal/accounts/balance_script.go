package accounts

import "github.com/piperhex/remoteai-admin/internal/providermarket"

func validateBalanceScript(value object, baseURL string) error {
	if len(value) == 0 {
		return nil
	}
	return providermarket.ValidateBalanceScript(value, baseURL)
}

func validateMarketSource(value object) error {
	return providermarket.ValidateMarketSource(value)
}
