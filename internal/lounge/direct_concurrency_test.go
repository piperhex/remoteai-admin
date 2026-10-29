package lounge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Separate pooled transactions exercise the database lock, including across Go service instances.
func concurrentPolicyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LOUNGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LOUNGE_TEST_DATABASE_URL is required for isolated PostgreSQL policy tests")
	}
	schema := "lounge_concurrency_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	base := openPolicyDB(t, dsn)
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := base.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	connection, err := url.Parse(dsn)
	if err != nil || connection.Scheme == "" {
		t.Fatal("concurrency test requires a PostgreSQL URL")
	}
	query := connection.Query()
	query.Set("search_path", schema)
	connection.RawQuery = query.Encode()
	db := openPolicyDB(t, connection.String())
	if err := db.Exec(`CREATE TABLE users (id uuid PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../migrations/021_lounge_direct_permissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	applyDirectResetSchema(t, db)
	return db
}

func openPolicyDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return db
}

func TestDirectPermissionConcurrentOpeningsAdmitExactlyOne(t *testing.T) {
	db := concurrentPolicyDB(t)
	a, b := policyUser(t, db), policyUser(t, db)
	h := newHub(nil)
	h.directKey = []byte("test-only-key")
	var admitted atomic.Int32
	var tasks sync.WaitGroup
	start := make(chan struct{})
	for index := range 12 {
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			<-start
			request, err := h.directPermission(a, incoming{ID: fmt.Sprint(index), To: b, Text: "opening"})
			if err == nil {
				err = (postgresDirectPolicy{db: db}).Admit(context.Background(), request)
			}
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, errAwaitingReply) {
				t.Error(err)
			}
		}()
	}
	close(start)
	tasks.Wait()
	if admitted.Load() != 1 {
		t.Fatal("concurrent opening count:", admitted.Load())
	}
}
