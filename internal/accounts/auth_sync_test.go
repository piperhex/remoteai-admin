package accounts

import "testing"

func TestAccountAuthChangesAreNotifiedAfterCommitAndNeverOnStaleUploads(t *testing.T) {
	s, store, owner := batchFixture(t)
	rows := batchRecords(1)
	rows[0]["auth"] = object{"tokens": object{"access_token": "old", "refresh_token": "old-rt"}}
	if _, err := s.upsertAccounts(writeOptions{Owner: owner}, rows); err != nil {
		t.Fatal(err)
	}
	var notified [][]string
	s.deps.AccountAuthChanged = func(actualOwner string, ids []string) {
		if actualOwner != owner {
			t.Fatal("wrong notification owner")
		}
		var saved account
		if err := store.DB.Where(`"ownerId" = ? AND "accountId" = ?`, owner, ids[0]).First(&saved).Error; err != nil || str(obj(saved.Auth["tokens"])["refresh_token"]) != "new-rt" {
			t.Fatal("notification arrived before credentials were committed", err)
		}
		notified = append(notified, ids)
	}
	rows[0]["auth"] = object{"tokens": object{"access_token": "new", "refresh_token": "new-rt"}}
	rows[0]["lastModifiedAt"] = testNew
	if _, err := s.upsertAccounts(writeOptions{Owner: owner}, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertAccounts(writeOptions{Owner: owner}, rows); err != nil {
		t.Fatal(err)
	}
	rows[0]["lastModifiedAt"] = testOld
	rows[0]["auth"] = object{"tokens": object{"refresh_token": "stale-rt"}}
	if _, err := s.upsertAccounts(writeOptions{Owner: owner}, rows); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 || len(notified[0]) != 1 || notified[0][0] != "fixture-0" {
		t.Fatal("unchanged or stale credentials caused extra notifications", notified)
	}
}
