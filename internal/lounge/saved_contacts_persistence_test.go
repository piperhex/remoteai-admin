package lounge

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func savedContactsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := concurrentPolicyDB(t)
	if err := db.Exec(`ALTER TABLE users ADD COLUMN email text NOT NULL DEFAULT '',
		ADD COLUMN disabled boolean NOT NULL DEFAULT false`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSavedContactsPostgresRestoresMutualConversationsAndHonorsDeletion(t *testing.T) {
	db := savedContactsDB(t)
	a, b, outsider := policyUser(t, db), policyUser(t, db), policyUser(t, db)
	ctx := context.Background()
	store := postgresDirectPolicy{db: db}
	h := newHub(nil)
	opening, err := h.directPermission(a, incoming{ID: "first", To: b, Text: "private opening"})
	if err != nil {
		t.Fatal(err)
	}
	if mutual, err := store.AdmitWithContact(ctx, opening); err != nil || mutual {
		t.Fatal("one-way opening became a saved contact", mutual, err)
	}
	assertSavedContacts(t, postgresSavedContacts{db}, a, nil)
	assertSavedContacts(t, postgresSavedContacts{db}, b, nil)
	reply, err := h.directPermission(b, incoming{ID: "reply", To: a, Text: "private reply"})
	if err != nil {
		t.Fatal(err)
	}
	if mutual, err := store.AdmitWithContact(ctx, reply); err != nil || !mutual {
		t.Fatal("first reply was not saved", mutual, err)
	}
	assertSavedContacts(t, postgresSavedContacts{db}, a, []string{b})
	assertSavedContacts(t, postgresSavedContacts{db}, b, []string{a})
	assertSavedContacts(t, postgresSavedContacts{db}, outsider, nil)
	if mutual, err := store.AdmitWithContact(ctx, reply); err != nil || mutual {
		t.Fatal("ordinary reply triggered another full refresh", mutual, err)
	}
	if _, err := store.Reset(ctx, opening); err != nil {
		t.Fatal(err)
	}
	assertSavedContacts(t, postgresSavedContacts{db}, a, nil)
	assertSavedContacts(t, postgresSavedContacts{db}, b, nil)
}

func TestSavedContactsPostgresPaginatesAndExcludesDisabledAccounts(t *testing.T) {
	db := savedContactsDB(t)
	owner := policyUser(t, db)
	for range savedContactsPageSize + 2 {
		other := policyUser(t, db)
		first, second := owner, other
		if first > second {
			first, second = second, first
		}
		if err := db.Exec(`INSERT INTO lounge_direct_permissions(first_user, second_user)
			VALUES (?, ?)`, first, second).Error; err != nil {
			t.Fatal(err)
		}
	}
	store := postgresSavedContacts{db}
	first, err := store.Page(context.Background(), owner, "")
	if err != nil || len(first) != savedContactsPageSize+1 {
		t.Fatal("first page must include a look-ahead row", len(first), err)
	}
	last, err := store.Page(context.Background(), owner, first[savedContactsPageSize-1].ID)
	if err != nil || len(last) != 2 || last[0].ID != first[savedContactsPageSize].ID {
		t.Fatal("pagination skipped or duplicated contacts", last, err)
	}
	if err := db.Exec(`UPDATE users SET disabled = true WHERE id = ?`, last[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	remaining, err := store.Page(context.Background(), owner, first[savedContactsPageSize-1].ID)
	if err != nil || len(remaining) != 1 || remaining[0].ID != last[1].ID {
		t.Fatal("disabled account was restored", remaining, err)
	}
}

func assertSavedContacts(t *testing.T, store postgresSavedContacts, owner string, expected []string) {
	t.Helper()
	users, err := store.Page(context.Background(), owner, "")
	if err != nil || len(users) != len(expected) {
		t.Fatal("unexpected saved contacts", users, err)
	}
	for index, id := range expected {
		if users[index].ID != id {
			t.Fatal("wrong account restored", users)
		}
	}
}
