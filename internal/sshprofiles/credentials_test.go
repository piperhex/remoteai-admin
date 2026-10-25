package sshprofiles

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func testCredentialConfig() platform.Config {
	return platform.Config{credentialKeySetting: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))}
}

func secretString(value string) *string { return &value }

func TestCredentialEncryptionBindsOwnerAndTarget(t *testing.T) {
	config := testCredentialConfig()
	value := fixture()
	secret := credentials{PrivateKey: secretString("PRIVATE KEY fixture"), Passphrase: secretString("phrase")}
	sealed, err := sealCredentials(config, "owner", value, secret)
	if err != nil || strings.Contains(sealed, "PRIVATE KEY") || strings.Contains(sealed, "phrase") {
		t.Fatal("credential encryption failed", err)
	}
	again, err := sealCredentials(config, "owner", value, secret)
	if err != nil || again == sealed {
		t.Fatal("nonces must be unique", err)
	}
	row := record{OwnerID: "owner", ID: value.ID, Config: &value, EncryptedCredentials: &sealed}
	opened, err := openCredentials(config, row)
	if err != nil || opened.PrivateKey == nil || *opened.PrivateKey != *secret.PrivateKey ||
		opened.Passphrase == nil || *opened.Passphrase != *secret.Passphrase {
		t.Fatal("credential round trip failed", err)
	}
	for _, edit := range []func(*record){
		func(row *record) { row.OwnerID = "other" },
		func(row *record) { row.Config.ID = "other" },
		func(row *record) { row.Config.Host = "other.test" },
		func(row *record) { row.Config.Username = "other" },
		func(row *record) { row.Config.Port++ },
		func(row *record) { row.Config.Auth = "password" },
	} {
		copy := row
		profileCopy := value
		copy.Config = &profileCopy
		edit(&copy)
		if _, err := openCredentials(config, copy); err == nil {
			t.Fatal("ciphertext reused with a different owner or login target")
		}
	}
}

func TestCredentialEncryptionFailsClosed(t *testing.T) {
	value := fixture()
	secret := credentials{PrivateKey: secretString("key")}
	for _, key := range []string{"", "invalid-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := sealCredentials(platform.Config{credentialKeySetting: key}, "owner", value, secret); err == nil {
			t.Fatal("missing or invalid key accepted")
		}
	}
	config := testCredentialConfig()
	sealed, err := sealCredentials(config, "owner", value, secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{"v2:" + sealed[3:], "v1:bad", "v1:", sealed[:len(sealed)-4] + "AAAA"} {
		row := record{OwnerID: "owner", Config: &value, EncryptedCredentials: &encoded}
		if _, err := openCredentials(config, row); err == nil {
			t.Fatal("tampered ciphertext accepted")
		}
	}
	config[credentialKeySetting] = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	if _, err := openCredentials(config, record{OwnerID: "owner", Config: &value,
		EncryptedCredentials: &sealed}); err == nil {
		t.Fatal("wrong encryption key accepted")
	}
}

func TestCredentialValidation(t *testing.T) {
	for _, value := range []credentials{
		{Password: secretString(strings.Repeat("x", maxPasswordBytes+1))},
		{Password: secretString("bad\x00password")},
		{PrivateKey: secretString(strings.Repeat("x", maxPrivateKeyBytes+1))},
		{Passphrase: secretString("missing key")},
		{Password: secretString("password"), PrivateKey: secretString("key")},
	} {
		if value.valid("password") || value.valid("key") {
			t.Fatal("invalid credential accepted")
		}
	}
	if !(credentials{Password: secretString("")}).valid("password") {
		t.Fatal("empty passwords must round trip")
	}
}

func credentialRequest(router *gin.Engine, method, path, owner string, body interface{}) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", owner)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestCredentialHTTPStorageAndCompatibility(t *testing.T) {
	db := testDB(t)
	owner, other := testOwner(t, db), testOwner(t, db)
	router := gin.New()
	router.Use(platform.RequestBodyParser())
	deps := &platform.Dependencies{DB: db, Config: testCredentialConfig(),
		Authenticate: func(c *gin.Context) (*platform.Principal, error) {
			id := c.GetHeader("Authorization")
			if id != owner && id != other {
				return nil, nil
			}
			return &platform.Principal{ID: id}, nil
		}}
	if err := Register(router, deps); err != nil {
		t.Fatal(err)
	}
	value := fixture()
	secret := credentials{PrivateKey: secretString("PRIVATE KEY test-only"), Passphrase: secretString("test-passphrase")}
	response := credentialRequest(router, "PUT", "/sync/ssh-profiles/server", owner,
		saveRequest{profile: value, Credentials: &secret})
	if response.Code != 200 || strings.Contains(response.Body.String(), "PRIVATE KEY") ||
		strings.Contains(response.Body.String(), "credentials") {
		t.Fatal("save must return metadata only", response.Code)
	}
	var row record
	if err := db.Where(`"ownerId" = ? AND "profileId" = ?`, owner, value.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.EncryptedCredentials == nil || !strings.HasPrefix(*row.EncryptedCredentials, "v1:") {
		t.Fatal("missing ciphertext")
	}
	var stored string
	if err := db.Raw(`SELECT config::text FROM synced_ssh_profiles WHERE "ownerId" = ? AND "profileId" = ?`,
		owner, value.ID).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored+*row.EncryptedCredentials, "PRIVATE KEY") ||
		strings.Contains(stored+*row.EncryptedCredentials, "test-passphrase") {
		t.Fatal("plaintext credential persisted")
	}
	path := "/sync/ssh-profiles/server/credentials"
	for _, attempt := range []struct {
		owner    string
		revision int
		status   int
	}{
		{"", 1, 401}, {other, 1, 409}, {owner, 2, 409}, {owner, 0, 400},
	} {
		response := credentialRequest(router, "POST", path, attempt.owner, gin.H{"revision": attempt.revision})
		if response.Code != attempt.status || strings.Contains(response.Body.String(), "PRIVATE KEY") {
			t.Fatal("unauthorized or stale credential access", response.Code)
		}
	}
	response = credentialRequest(router, "POST", path, owner, gin.H{"revision": 1})
	if response.Code != 201 || response.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(response.Body.String(), "PRIVATE KEY test-only") {
		t.Fatal("authorized credential fetch failed")
	}
	verifyCredentialEdits(t, deps, owner, value)
}

func verifyCredentialEdits(t *testing.T, deps *platform.Dependencies, owner string, value profile) {
	t.Helper()
	value.Revision, value.Name = 1, "Renamed by an older client"
	if _, err := mutate(deps.DB, owner, change{Value: value}); err != nil {
		t.Fatal(err)
	}
	var row record
	read := func() {
		t.Helper()
		row = record{}
		if err := deps.DB.Where(`"ownerId" = ? AND "profileId" = ?`, owner, value.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	read()
	if secret, err := openCredentials(deps.Config, row); err != nil || secret == nil {
		t.Fatal("metadata edit lost credentials")
	}
	value.Revision, value.Host = 2, "new-host.test"
	if _, err := mutate(deps.DB, owner, change{Value: value}); err != nil {
		t.Fatal(err)
	}
	read()
	if row.EncryptedCredentials != nil {
		t.Fatal("old credentials retained for a different host")
	}
	value.Revision = 3
	sealed, err := sealCredentials(deps.Config, owner, value, credentials{PrivateKey: secretString("replacement")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutate(deps.DB, owner, change{Value: value, EncryptedCredentials: &sealed}); err != nil {
		t.Fatal(err)
	}
	value.Revision = 4
	if _, err := mutate(deps.DB, owner, change{Value: value, Delete: true}); err != nil {
		t.Fatal(err)
	}
	read()
	if row.EncryptedCredentials != nil || row.Config != nil {
		t.Fatal("deleted profile retained credentials")
	}
}

func TestCredentialMigrationMatchesAndIsIdempotent(t *testing.T) {
	upgrade, err := os.ReadFile("../../sql/20261025-ssh-credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := os.ReadFile("../migrations/018_ssh_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(upgrade), "\r\n", "\n") != strings.ReplaceAll(string(fresh), "\r\n", "\n") {
		t.Fatal("fresh and upgrade schema differ")
	}
	db := testDB(t)
	for range 2 {
		if err := db.Exec(string(upgrade)).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCredentialSaveWithoutKeyLeavesDatabaseUnchanged(t *testing.T) {
	db := testDB(t)
	owner := testOwner(t, db)
	router := gin.New()
	deps := &platform.Dependencies{DB: db, Config: platform.Config{},
		Authenticate: func(c *gin.Context) (*platform.Principal, error) {
			return &platform.Principal{ID: owner}, nil
		}}
	if err := Register(router, deps); err != nil {
		t.Fatal(err)
	}
	secret := credentials{PrivateKey: secretString("test-only-private-key")}
	response := credentialRequest(router, "PUT", "/sync/ssh-profiles/server", owner,
		saveRequest{profile: fixture(), Credentials: &secret})
	if response.Code != 503 || strings.Contains(response.Body.String(), "test-only-private-key") {
		t.Fatal("missing encryption key must fail safely", response.Code)
	}
	values, err := list(db, owner)
	if err != nil || len(values) != 0 {
		t.Fatal("failed encryption must not create a profile", err)
	}
	response = credentialRequest(router, "PUT", "/sync/ssh-profiles/server", owner, fixture())
	if response.Code != 200 {
		t.Fatal("legacy metadata save must still work without an encryption key", response.Code)
	}
}

func TestCredentialStorageClearsOnTargetChangesAndExplicitRemoval(t *testing.T) {
	value := fixture()
	sealed := "ciphertext"
	for _, edit := range []func(*profile){
		func(value *profile) { value.Host = "other.test" },
		func(value *profile) { value.Port++ },
		func(value *profile) { value.Username = "other-user" },
		func(value *profile) { value.Auth = "password" },
	} {
		changed := value
		edit(&changed)
		row := record{Config: &value, EncryptedCredentials: &sealed}
		updateCredentialStorage(&row, change{Value: changed})
		if row.EncryptedCredentials != nil {
			t.Fatal("target change retained old credentials")
		}
	}
	row := record{Config: &value, EncryptedCredentials: &sealed}
	cleared, err := sealCredentials(platform.Config{}, "owner", value, credentials{})
	if err != nil {
		t.Fatal(err)
	}
	updateCredentialStorage(&row, change{Value: value, EncryptedCredentials: &cleared})
	if row.EncryptedCredentials != nil {
		t.Fatal("explicit removal retained credentials")
	}
}
