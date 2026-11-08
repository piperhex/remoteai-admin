package lounge

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestAccountDatabaseScanIgnoresLiveClientMetadata(t *testing.T) {
	parsed, err := schema.Parse(&account{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("account queries cannot parse their result type: %v", err)
	}
	for _, name := range []string{"ClientVersions", "Clients"} {
		if field := parsed.LookUpField(name); field != nil && field.DBName != "" {
			t.Fatalf("live client metadata %s must not be read from account rows", name)
		}
	}
	for _, name := range []string{"ID", "Account"} {
		if field := parsed.LookUpField(name); field == nil || field.DBName == "" {
			t.Fatalf("account query column %s must remain available", name)
		}
	}
}
