package accounts

import "testing"

func TestBalanceScriptMergePreservesOldClientsAndAllowsExplicitClear(t *testing.T) {
	script := object{"url": "https://example.test/balance", "auth": "bearer",
		"script": "function parseBalance(r){return {amount:r.balance,currency:'USD'};}"}
	incoming := object{"id": "market-personal", "baseUrl": "https://example.test/v1", "name": "Example",
		"balanceScript": script, "lastModifiedAt": "2026-10-29T01:00:00Z"}
	row, err := mergeProvider(nil, "owner", incoming)
	if err != nil || row == nil || len(row.BalanceScript) == 0 {
		t.Fatal("script not persisted", err)
	}
	old := object{"id": "market-personal", "baseUrl": "https://example.test/v1", "name": "Renamed",
		"lastModifiedAt": "2026-10-29T02:00:00Z", "fieldModifiedAt": object{"name": "2026-10-29T02:00:00Z"}}
	merged, err := mergeProvider(row, "owner", old)
	if err != nil || merged == nil || len(merged.BalanceScript) == 0 {
		t.Fatal("old client erased script", err)
	}
	old["balanceScript"] = nil
	old["lastModifiedAt"] = "2026-10-29T03:00:00Z"
	old["fieldModifiedAt"] = object{"balanceScript": "2026-10-29T03:00:00Z"}
	cleared, err := mergeProvider(merged, "owner", old)
	if err != nil || cleared == nil || len(cleared.BalanceScript) != 0 {
		t.Fatal("explicit clear failed", err)
	}
	incoming["baseUrl"] = "https://other.test/v1"
	if _, err := mergeProvider(nil, "owner", incoming); err == nil {
		t.Fatal("cross-origin script accepted")
	}
}
