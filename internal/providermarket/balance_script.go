package providermarket

import (
	"bytes"
	"encoding/json"
)

// ValidateBalanceScript shares the public bounded balance-only contract with personal cloud synchronization.
// The server stores this definition but never evaluates publisher JavaScript.
func ValidateBalanceScript(value interface{}, baseURL string) error {
	data, err := json.Marshal(value)
	if err != nil {
		return invalid()
	}
	var script *balanceQuery
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&script) != nil || !validBalanceQuery(script, baseURL) {
		return invalid()
	}
	return nil
}
