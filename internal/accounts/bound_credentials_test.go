package accounts

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func boundAuthFixture(user, marker, refreshed string) object {
	token := tokenFixture(object{"sub": user, "marker": marker, "email": "bound@example.test",
		"https://api.openai.com/auth": object{"chatgpt_account_id": "bound-workspace"}})
	return object{"tokens": object{"access_token": token, "id_token": token, "refresh_token": marker},
		"last_refresh": refreshed}
}

func TestBoundCredentialRotationRequiresSameIdentityNewerTimeAndRenewableTokens(t *testing.T) {
	old := boundAuthFixture("bound-user", "old-rt", testOld)
	identity, err := authIdentity(old)
	if err != nil {
		t.Fatal(err)
	}
	current := systemAccount{Auth: old, SyncAccountID: str(identity["syncAccountId"])}
	if !newerBoundCredentials(current, boundAuthFixture("bound-user", "new-rt", testNew)) {
		t.Fatal("valid rotation rejected")
	}
	for _, candidate := range []object{
		boundAuthFixture("other-user", "new-rt", testNew),
		boundAuthFixture("bound-user", "new-rt", testOld),
		boundAuthFixture("bound-user", "old-rt", testNew),
		boundAuthFixture("bound-user", "", testNew),
		boundAuthFixture("bound-user", "new-rt", "invalid"),
	} {
		if newerBoundCredentials(current, candidate) {
			t.Fatal("unsafe rotation accepted")
		}
	}
	mismatched := boundAuthFixture("bound-user", "new-rt", testNew)
	obj(mismatched["tokens"])["access_token"] = obj(boundAuthFixture("other-user", "other", testNew)["tokens"])["access_token"]
	if newerBoundCredentials(current, mismatched) {
		t.Fatal("mismatched access-token identity accepted")
	}
	s := &service{}
	if err := s.refreshBoundCredentials(writeOptions{}, current,
		boundAuthFixture("bound-user", "new-rt", testNew)); err != nil {
		t.Fatal("metadata-only caller attempted a credential write", err)
	}
}

func TestBoundCredentialUpdateWritesOnlyTheAuthAndVersionColumns(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=127.0.0.1 port=1 user=fixture dbname=fixture sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	current := systemAccount{ID: "bound-system-id", Note: "protected", Email: "protected@example.test"}
	result := updateBoundCredentialRow(db, &current, boundAuthFixture("bound-user", "new-rt", testNew))
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	sql := result.Statement.SQL.String()
	if !strings.Contains(sql, `"auth"=`) || !strings.Contains(sql, `"lastModifiedAt"=`) ||
		strings.Contains(sql, `"note"`) || strings.Contains(sql, `"email"`) {
		t.Fatal("credential rotation attempted to change protected metadata", sql)
	}
}

func TestBoundCredentialSyncUpdatesCentralAuthAndNotifiesEveryAuthorizedOwner(t *testing.T) {
	s, store, owner := batchFixture(t)
	old := boundAuthFixture("bound-user", "old-rt", testOld)
	identity, _ := authIdentity(old)
	current := systemAccount{ID: uuid.NewString(), SyncAccountID: str(identity["syncAccountId"]),
		Note: "protected", Auth: old, LastModifiedAt: date(testOld)}
	if err := store.DB.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	other := uuid.NewString()
	for _, id := range []string{owner, other} {
		if err := store.DB.Create(&binding{SystemAccountID: current.ID, UserID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	notified := map[string]bool{}
	s.deps.AccountAuthChanged = func(owner string, ids []string) {
		if len(ids) != 1 || ids[0] != current.SyncAccountID {
			t.Fatal("incorrect notification")
		}
		notified[owner] = true
	}
	incoming := object{"id": current.SyncAccountID, "note": "protected", "expiresAt": "",
		"auth": boundAuthFixture("bound-user", "new-rt", testNew), "lastModifiedAt": testNew}
	options := writeOptions{Owner: owner, RejectMetadata: true, RefreshCredentials: true}
	if _, err := s.upsertAccounts(options, []object{incoming}); err != nil {
		t.Fatal(err)
	}
	var saved systemAccount
	if err := store.DB.First(&saved, "id = ?", current.ID).Error; err != nil {
		t.Fatal(err)
	}
	if str(obj(saved.Auth["tokens"])["refresh_token"]) != "new-rt" || saved.Note != "protected" ||
		!notified[owner] || !notified[other] || len(notified) != 2 {
		t.Fatal("bound rotation did not preserve metadata and propagate to bound users")
	}
}
