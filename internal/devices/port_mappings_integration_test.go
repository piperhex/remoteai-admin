//go:build integration

package devices

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPortMappingSyncDatabaseAndOwnership(t *testing.T) {
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 port=15432 user=parity password=local-parity-only dbname=admin_go sslmode=disable"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	migration, err := os.ReadFile("../migrations/015_port_mappings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	owner, device, secondDevice := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err = tx.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`, owner, owner+"@example.test", "fixture").Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{device, secondDevice} {
		if err = tx.Exec(`INSERT INTO remote_devices("ownerId","deviceId",name,platform) VALUES(?,?,?,?)`, owner, id, "Fixture", "Windows").Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{&platform.Dependencies{DB: tx}}
	router := gin.New()
	principal := owner
	router.Use(func(c *gin.Context) { c.Set("principal", &platform.Principal{ID: principal}) })
	s.registerPortMappings(router.Group("/devices"))
	request := func(method, id string, doc *mappingDocument) (int, mappingDocument) {
		var body bytes.Buffer
		if doc != nil {
			if err := json.NewEncoder(&body).Encode(doc); err != nil {
				t.Fatal(err)
			}
		}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/devices/"+id+"/port-mappings", &body)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		var result mappingDocument
		if recorder.Code == 200 {
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return recorder.Code, result
	}
	code, initial := request("GET", device, nil)
	if code != 200 || initial.Revision != 0 || len(initial.Mappings) != 0 {
		t.Fatal("initial document")
	}
	initial.Mappings = []portMapping{{ID: "mapping-1", RemoteHost: "127.0.0.1", RemotePort: 3000, LocalPort: 13000}}
	code, saved := request("PUT", device, &initial)
	if code != 200 || !saved.Accepted || saved.Revision != 1 {
		t.Fatal("save failed")
	}
	_, stale := request("PUT", device, &initial)
	if stale.Accepted || stale.Revision != 1 {
		t.Fatal("stale write accepted")
	}
	saved.Mappings = []portMapping{}
	_, deleted := request("PUT", device, &saved)
	if !deleted.Accepted || len(deleted.Mappings) != 0 {
		t.Fatal("delete failed")
	}
	_, stale = request("PUT", device, &initial)
	if stale.Accepted || len(stale.Mappings) != 0 {
		t.Fatal("stale snapshot resurrected deletion")
	}
	_, separate := request("GET", secondDevice, nil)
	if separate.Revision != 0 || len(separate.Mappings) != 0 {
		t.Fatal("device isolation")
	}
	principal = uuid.NewString()
	code, _ = request("GET", device, nil)
	if code == 200 {
		t.Fatal("foreign owner read")
	}
	code, _ = request("PUT", device, &initial)
	if code == 200 {
		t.Fatal("foreign owner write")
	}
}
