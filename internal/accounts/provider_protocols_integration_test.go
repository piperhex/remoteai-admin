package accounts

import (
	"reflect"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
)

func TestProviderMixedProtocolsDatabaseSyncRoundTrip(t *testing.T) {
	tx := providerOptionsTestDB(t)
	owner := newID()
	if err := tx.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`,
		owner, owner+"@example.test", "fixture-only").Error; err != nil {
		t.Fatal(err)
	}
	s := &service{deps: &platform.Dependencies{DB: tx}}
	incoming := mixedProtocolProviderFixture()
	if _, err := s.upsertProviders(owner, []object{incoming}); err != nil {
		t.Fatal(err)
	}
	pulled, err := s.providers(owner)
	if err != nil {
		t.Fatal(err)
	}
	providers := pulled["providers"].([]object)
	if len(providers) != 1 || providers[0]["apiFormat"] != incoming["apiFormat"] ||
		!reflect.DeepEqual(providers[0]["modelApiFormats"], incoming["modelApiFormats"]) {
		t.Fatalf("database sync changed mixed protocols: %#v", providers)
	}
	// Another desktop changes an unrelated field and uploads its downloaded configuration.
	second := providers[0]
	second["name"] = "Second desktop"
	second["fieldModifiedAt"].(object)["name"] = testNew
	if _, err = s.upsertProviders(owner, []object{second}); err != nil {
		t.Fatal(err)
	}
	pulled, err = s.providers(owner)
	if err != nil {
		t.Fatal(err)
	}
	providers = pulled["providers"].([]object)
	if len(providers) != 1 || providers[0]["name"] != second["name"] ||
		!reflect.DeepEqual(providers[0]["modelApiFormats"], incoming["modelApiFormats"]) {
		t.Fatalf("second desktop upload changed mixed protocols: %#v", providers)
	}
}
