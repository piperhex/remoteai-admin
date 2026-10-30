package accounts

import (
	"reflect"
	"testing"
)

func TestMarketSourceSurvivesLegacyEditsAndRoundtrip(t *testing.T) {
	source := object{"id": "11111111-1111-4111-8111-111111111111", "page": object{
		"url": "https://example.test/dashboard", "query": "source=remoteai", "headers": object{"X-Region": "east"}}}
	incoming := object{"id": "market-personal", "baseUrl": "https://example.test/v1", "name": "Example",
		"marketSource": source, "lastModifiedAt": "2026-10-30T01:00:00Z"}
	row, err := mergeProvider(nil, "owner", incoming)
	if err != nil || row == nil || obj(providerDTO(*row)["marketSource"])["id"] != source["id"] {
		t.Fatal("market page not persisted", err)
	}
	legacy := object{"id": "market-personal", "baseUrl": "https://example.test/v1", "name": "Renamed",
		"lastModifiedAt": "2026-10-30T02:00:00Z", "fieldModifiedAt": object{"name": "2026-10-30T02:00:00Z"}}
	merged, err := mergeProvider(row, "owner", legacy)
	if err != nil || merged == nil || len(merged.MarketSource) == 0 {
		t.Fatal("old client erased market provenance", err)
	}
	incoming["marketSource"] = object{"id": "forged", "page": source["page"]}
	if _, err := mergeProvider(nil, "owner", incoming); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestMarketSourceDatabaseRoundtrip(t *testing.T) {
	tx := providerOptionsTestDB(t)
	owner := newID()
	if err := tx.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`,
		owner, owner+"@example.test", "fixture-only").Error; err != nil {
		t.Fatal(err)
	}
	incoming := providerOptionsFixture()
	incoming["marketSource"] = object{"id": "11111111-1111-4111-8111-111111111111", "page": object{
		"url": "https://example.test/dashboard", "query": "source=remoteai", "headers": object{"X-Region": "east"}}}
	if err := saveIncomingProvider(tx, owner, incoming); err != nil {
		t.Fatal(err)
	}
	var stored provider
	if err := tx.Where(`"ownerId" = ?`, owner).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(providerDTO(stored)["marketSource"], incoming["marketSource"]) {
		t.Fatal("market page or provenance changed during database roundtrip")
	}
}
