package sshprofiles

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/migrations"
	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func fixture() profile {
	return profile{ID: "server", Name: "Server", Host: "::1", Port: 22, Username: "user", Auth: "key"}
}

func TestValidation(t *testing.T) {
	if err := validate(fixture()); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*profile){
		func(p *profile) { p.ID = "../other" }, func(p *profile) { p.Host = "bad host" },
		func(p *profile) { p.Username = "\nuser" }, func(p *profile) { p.Name = "\x00" },
		func(p *profile) { p.Port = 65536 }, func(p *profile) { p.Auth = "other" },
		func(p *profile) { p.Revision = -1 }, func(p *profile) { p.Fingerprint = strings.Repeat("x", 257) },
	} {
		value := fixture()
		edit(&value)
		if validate(value) == nil {
			t.Fatalf("accepted invalid profile: %#v", value)
		}
	}
}

func TestStrictBodyAndAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	deps := &platform.Dependencies{Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") == "fixture" {
			return &platform.Principal{ID: "owner"}, nil
		}
		return nil, nil
	}}
	if err := Register(router, deps); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT", "DELETE", "POST"} {
		path := "/sync/ssh-profiles/server"
		if method == "GET" {
			path = "/sync/ssh-profiles"
		}
		if method == "POST" {
			path = "/sync/ssh-profiles/import"
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != 401 {
			t.Fatal(method, response.Code)
		}
	}
	for _, body := range []string{
		`{"id":"server","password":"secret"}`, `{"id":"server","privateKey":"secret"}`,
		`{"id":"server","passphrase":"secret"}`, `{"id":"server","ownerId":"other"}`,
		`null`, `{}`, `[]`, `{} {}`, strings.Repeat("x", maxBodyBytes+1),
	} {
		request := httptest.NewRequest("PUT", "/sync/ssh-profiles/server", strings.NewReader(body))
		request.Header.Set("Authorization", "fixture")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 400 || !strings.Contains(response.Body.String(), "client.ssh.invalid") {
			t.Fatalf("invalid body accepted: %d", response.Code)
		}
	}
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("SSH_PROFILES_TEST_DSN")
	if dsn == "" {
		t.Skip("set SSH_PROFILES_TEST_DSN to an isolated PostgreSQL database")
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
	if err := migrations.InitializeEmpty(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func testOwner(t *testing.T, db *gorm.DB) string {
	t.Helper()
	owner := uuid.NewString()
	if err := db.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`,
		owner, owner+"@example.test", "test-only").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = ?", owner) })
	return owner
}

func TestDatabaseSyncAndOwnerIsolation(t *testing.T) {
	db := testDB(t)
	owner := testOwner(t, db)
	other := testOwner(t, db)
	first, err := mutate(db, owner, change{Value: fixture()})
	if err != nil || len(first) != 1 || first[0].Revision != 1 {
		t.Fatal(first, err)
	}
	second := fixture()
	second.ID = "phone"
	if _, err := mutate(db, owner, change{Value: second}); err != nil {
		t.Fatal(err)
	}
	first[0].Name = "Edited on PC"
	if _, err := mutate(db, owner, change{Value: first[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := mutate(db, owner, change{Value: first[0]}); err == nil {
		t.Fatal("stale write accepted")
	}
	if _, err := mutate(db, owner, change{Value: first[0], Delete: true}); err == nil {
		t.Fatal("stale delete accepted")
	}
	values, err := list(db, other)
	if err != nil || len(values) != 0 {
		t.Fatal("owner leak", values, err)
	}
	if _, err := mutate(db, other, change{Value: first[0]}); err == nil {
		t.Fatal("cross-owner edit accepted")
	}
	first[0].Revision = 2
	values, err = mutate(db, owner, change{Value: first[0], Delete: true})
	if err != nil || len(values) != 1 || values[0].ID != "phone" {
		t.Fatal(values, err)
	}
	values, err = migrate(db, owner, []profile{fixture()})
	if err != nil || len(values) != 1 {
		t.Fatal("deleted profile resurrected", values, err)
	}
	if _, err = mutate(db, owner, change{Value: fixture()}); err == nil {
		t.Fatal("deleted ID recreated")
	}
	values, err = mutate(db, other, change{Value: fixture()})
	if err != nil || len(values) != 1 {
		t.Fatal("same ID must be independent across owners", err)
	}
}

func TestImportIsAtomicAndNeverOverwrites(t *testing.T) {
	db := testDB(t)
	owner := testOwner(t, db)
	profiles := make([]profile, maxProfiles)
	for i := range profiles {
		profiles[i] = fixture()
		profiles[i].ID = uuid.NewString()
	}
	first, err := migrate(db, owner, profiles)
	if err != nil || len(first) != maxProfiles {
		t.Fatal(err)
	}
	first[0].Name = "Newer name"
	if _, err := mutate(db, owner, change{Value: first[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(db, owner, profiles); err != nil {
		t.Fatal(err)
	}
	stored, _ := list(db, owner)
	if stored[0].Name != "Newer name" {
		t.Fatal("import overwrote current value")
	}
	if _, err := migrate(db, owner, []profile{fixture()}); err == nil {
		t.Fatal("capacity bypass")
	}
	if _, err := mutate(db, owner, change{Value: fixture()}); err == nil {
		t.Fatal("create capacity bypass")
	}
	stored, _ = list(db, owner)
	if len(stored) != maxProfiles {
		t.Fatal("failed write damaged existing data")
	}
}

func TestMigrationMatchesAndIsIdempotent(t *testing.T) {
	upgrade, err := os.ReadFile("../../sql/20261024-ssh-profiles.sql")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := os.ReadFile("../migrations/017_ssh_profiles.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(upgrade), "\r\n", "\n") != strings.ReplaceAll(string(fresh), "\r\n", "\n") {
		t.Fatal("fresh and upgrade schema differ")
	}
	db := testDB(t)
	for i := 0; i < 2; i++ {
		if err := db.Exec(string(upgrade)).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPRoundTrip(t *testing.T) {
	db := testDB(t)
	owner := testOwner(t, db)
	router := gin.New()
	router.Use(platform.RequestBodyParser())
	deps := &platform.Dependencies{DB: db, Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: owner}, nil
	}}
	if err := Register(router, deps); err != nil {
		t.Fatal(err)
	}
	value := fixture()
	value.X11 = &x11Options{Display: "localhost:1"}
	body, _ := json.Marshal(value)
	request := httptest.NewRequest("PUT", "/sync/ssh-profiles/server", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response)
	}
	var values []profile
	if json.Unmarshal(response.Body.Bytes(), &values) != nil || len(values) != 1 || values[0].Revision != 1 {
		t.Fatal(response.Body.String())
	}
	if values[0].X11 == nil || values[0].X11.Display != value.X11.Display {
		t.Fatal("X11 settings did not survive database round trip")
	}
}

func TestConcurrentCreatesRespectCapacity(t *testing.T) {
	db := testDB(t)
	owner := testOwner(t, db)
	profiles := make([]profile, maxProfiles-1)
	for i := range profiles {
		profiles[i] = fixture()
		profiles[i].ID = uuid.NewString()
	}
	if _, err := migrate(db, owner, profiles); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			value := fixture()
			value.ID = uuid.NewString()
			_, err := mutate(db, owner, change{Value: value})
			results <- err
		}()
	}
	succeeded := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected one winning create, got %d", succeeded)
	}
	values, err := list(db, owner)
	if err != nil || len(values) != maxProfiles {
		t.Fatal(len(values), err)
	}
}
