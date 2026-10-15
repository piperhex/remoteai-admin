package accounts

import (
	"os"
	"reflect"
	"testing"

	"github.com/codex-switch/admin-go/internal/migrations"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func providerOptionsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("PROVIDER_OPTIONS_TEST_DSN")
	if dsn == "" {
		t.Skip("set PROVIDER_OPTIONS_TEST_DSN to an isolated disposable PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err = migrations.InitializeEmpty(db); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

func TestProviderRequestOptionsDatabaseRoundTrip(t *testing.T) {
	tx := providerOptionsTestDB(t)
	owner := newID()
	if err := tx.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`,
		owner, owner+"@example.test", "fixture-only").Error; err != nil {
		t.Fatal(err)
	}
	incoming := providerOptionsFixture()
	if err := saveIncomingProvider(tx, owner, incoming); err != nil {
		t.Fatal(err)
	}
	stored := provider{}
	if err := tx.Where(`"ownerId" = ?`, owner).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(providerDTO(stored)["requestOptions"], incoming["requestOptions"]) {
		t.Fatal("database serialization changed advanced settings")
	}
	incoming["requestOptions"] = object{}
	incoming["fieldModifiedAt"] = object{"requestOptions": testNew}
	if err := saveIncomingProvider(tx, owner, incoming); err != nil {
		t.Fatal(err)
	}
	cleared := provider{}
	if err := tx.First(&cleared, `id = ?`, stored.ID).Error; err != nil || len(cleared.RequestOptions) != 0 {
		t.Fatal("cleared settings did not persist", err)
	}
}

func TestProviderRequestOptionsUpgradeIsIdempotent(t *testing.T) {
	tx := providerOptionsTestDB(t)
	// A temporary table shadows the persistent table, so the upgrade test cannot alter stored data.
	if err := tx.Exec(`CREATE TEMP TABLE synced_providers (id text PRIMARY KEY) ON COMMIT DROP;
		INSERT INTO synced_providers(id) VALUES('existing')`).Error; err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../sql/20261015-provider-request-options.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := tx.Exec(string(sql)).Error; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := tx.Raw(`SELECT count(*) FROM synced_providers WHERE id='existing' AND "requestOptions" IS NULL`).
		Scan(&count).Error; err != nil || count != 1 {
		t.Fatal("upgrade changed the preexisting row", err)
	}
}
