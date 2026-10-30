package providermarket

import (
	"encoding/json"
	"math"
	"testing"
)

func pointer[T any](value T) *T { return &value }
func fixture() draft {
	return draft{Name: "Example", Description: "Public connection", BaseURL: "https://api.example.test/v1",
		PageLink: &PageLink{URL: "https://example.test/dashboard", Query: "source=remoteai", Headers: map[string]string{}},
		Currency: "USD", Unit: "millionTokens", Models: []model{
			{ID: "model-b", Protocol: "openaiResponses", Tiers: []tier{
				{Multiplier: 5, price: price{Input: pointer(0.0)}},
				{Multiplier: 1, price: price{Output: pointer(2.5)}, LongContextThreshold: pointer(int64(200000)),
					LongContext: price{CachedInput: pointer(0.0)}},
			}},
			{ID: "model-a", Protocol: "anthropicMessages", Tiers: []tier{{Multiplier: 2}}},
		}}
}
func TestValidationAndIndependentPrices(t *testing.T) {
	value := fixture()
	if validate(value) != nil {
		t.Fatal("valid fixture rejected")
	}
	encoded, _ := json.Marshal(value)
	var roundtrip draft
	if json.Unmarshal(encoded, &roundtrip) != nil {
		t.Fatal("roundtrip failed")
	}
	if roundtrip.Models[0].ID != "model-b" || roundtrip.Models[0].Tiers[0].Multiplier != 5 ||
		roundtrip.Models[0].Tiers[0].Input == nil || *roundtrip.Models[0].Tiers[0].Input != 0 ||
		roundtrip.Models[0].Tiers[0].Output != nil || *roundtrip.Models[0].Tiers[1].Output != 2.5 {
		t.Fatal("model/tier order or optional prices changed")
	}
	for _, edit := range []func(*draft){
		func(v *draft) { v.Models[1].ID = v.Models[0].ID },
		func(v *draft) { v.Models[0].Tiers[1].Multiplier = 5 },
		func(v *draft) { v.Models[0].Tiers[0].Input = pointer(-1.0) },
		func(v *draft) { v.Models[0].Tiers[0].Output = pointer(math.Inf(1)) },
		func(v *draft) { v.Models[0].Tiers[0].Multiplier = 0 },
		func(v *draft) { v.Models[0].Tiers[0].LongContext.Output = pointer(1.0) },
		func(v *draft) { v.Models[0].Tiers[0].LongContextThreshold = pointer(int64(0)) },
		func(v *draft) { v.Models[0].Protocol = "script" },
		func(v *draft) { v.BaseURL = "https://user:secret@example.test/v1" },
		func(v *draft) { v.BaseURL = "https://example.test/v1?key=secret" },
		func(v *draft) { v.BaseURL = "https://example.test/%73ecret" },
		func(v *draft) { v.Currency = "unknown" },
	} {
		copy := fixture()
		edit(&copy)
		if validate(copy) == nil {
			t.Fatal("invalid draft accepted")
		}
	}
}
func TestBalanceDefinitionIsBoundedAndCannotCarryCredentials(t *testing.T) {
	value := fixture()
	value.BalanceQuery = &balanceQuery{URL: "https://api.example.test/balance", Auth: "bearer",
		Script: "function parseBalance(r){return {amount:r.balance,currency:'USD'};}"}
	if validate(value) != nil {
		t.Fatal("safe script definition rejected")
	}
	value.BalanceQuery.URL = "https://other.example.test/balance"
	if validate(value) == nil {
		t.Fatal("cross-origin key destination accepted")
	}
	if ValidateBalanceScript(map[string]interface{}{"url": "https://api.example.test/balance",
		"auth": "bearer", "script": "x", "apiKey": "secret"}, value.BaseURL) == nil {
		t.Fatal("script credential accepted")
	}
}
