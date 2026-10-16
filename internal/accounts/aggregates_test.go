package accounts

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func aggregateFixture(t *testing.T) aggregateRecord {
	t.Helper()
	var entry aggregateRecord
	err := json.Unmarshal([]byte(`{"id":"route-1","modifiedAt":"2026-01-01T00:00:00Z","config":{
		"id":"route-1","name":"Route","model":"public","enabled":true,
		"memberProviderIds":["one","two"],"memberModels":{"one":"native-model"},
		"failoverPolicy":{"routingStrategy":"priority","maxAttempts":3,"recoverySuccessThreshold":2,
		"errorRateThresholdPercent":50,"errorRateMinRequests":10}}}`), &entry)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestAggregateValidationAndDeletionOrdering(t *testing.T) {
	entry := aggregateFixture(t)
	if err := validateAggregates(aggregatePayload{Aggregates: []aggregateRecord{entry}}); err != nil {
		t.Fatal(err)
	}
	deleted := entry
	deleted.Config = nil
	if !shouldReplaceAggregate(entry, deleted) || shouldReplaceAggregate(deleted, entry) {
		t.Fatal("equal revision must favor deletion")
	}
	entry.ModifiedAt = entry.ModifiedAt.Add(time.Second)
	if !shouldReplaceAggregate(deleted, entry) {
		t.Fatal("explicit newer revision should restore a route")
	}
	for _, edit := range []func(*aggregateRecord){
		func(e *aggregateRecord) { e.ID = "../bad" },
		func(e *aggregateRecord) { e.Config["enabled"] = false; delete(e.Config, "memberProviderIds") },
		func(e *aggregateRecord) { e.Config["enabled"] = false; e.Config["memberProviderIds"] = nil },
		func(e *aggregateRecord) { e.Config["memberModels"] = nil },
		func(e *aggregateRecord) { e.Config["name"] = "route\nname" },
		func(e *aggregateRecord) { e.Config["id"] = "other" },
		func(e *aggregateRecord) { e.Config["memberModels"] = object{"missing": "alias"} },
		func(e *aggregateRecord) { e.Config["failoverPolicy"] = object{"recoverySuccessThreshold": 0.0} },
		func(e *aggregateRecord) { e.ModifiedAt = time.Now().Add(time.Hour) },
	} {
		bad := aggregateFixture(t)
		edit(&bad)
		if validateAggregates(aggregatePayload{Aggregates: []aggregateRecord{bad}}) == nil {
			t.Fatal("invalid aggregate accepted")
		}
	}
}

func TestAggregateMigrationMatchesFreshSchema(t *testing.T) {
	upgrade, err := os.ReadFile("../../sql/20261015-aggregate-sync.sql")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := os.ReadFile("../migrations/013_aggregate_sync.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(upgrade), "\r\n", "\n") != strings.ReplaceAll(string(fresh), "\r\n", "\n") {
		t.Fatal("fresh schema and upgrade migration differ")
	}
}

func TestAggregateOfficialAccountMembers(t *testing.T) {
	for _, members := range [][]interface{}{
		{"one", "account:official-one"}, {"account:official-one", "account:official-two"},
	} {
		entry := aggregateFixture(t)
		entry.Config["memberProviderIds"] = members
		entry.Config["memberModels"] = object{"account:official-one": "official-model"}
		if err := validateAggregateConfiguration(entry); err != nil {
			t.Fatalf("valid official members rejected: %v", err)
		}
	}
	for _, id := range []string{"account:", "account:../outside", "account:account:nested", "aggregate:route"} {
		entry := aggregateFixture(t)
		entry.Config["memberProviderIds"] = []interface{}{"one", id}
		if validateAggregateConfiguration(entry) == nil {
			t.Fatalf("invalid member accepted: %s", id)
		}
	}
	entry := aggregateFixture(t)
	entry.Config["memberProviderIds"] = []interface{}{"account:one", "account:one"}
	entry.Config["memberModels"] = object{}
	if validateAggregateConfiguration(entry) == nil {
		t.Fatal("duplicate official member accepted")
	}
}

func TestAggregateDatabaseRoundTripAndOwnerIsolation(t *testing.T) {
	tx := providerOptionsTestDB(t)
	sql, err := os.ReadFile("../../sql/20261015-aggregate-sync.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Exec(string(sql)).Error; err != nil {
		t.Fatal(err)
	}
	owner := newID()
	if err = tx.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`,
		owner, owner+"@example.test", "fixture-only").Error; err != nil {
		t.Fatal(err)
	}
	entry := aggregateFixture(t)
	entry.OwnerID = owner
	if err = tx.Save(&entry).Error; err != nil {
		t.Fatal(err)
	}
	stored, err := listAggregates(tx, owner)
	if err != nil || len(stored.Aggregates) != 1 {
		t.Fatalf("round trip: %v", err)
	}
	if !reflect.DeepEqual(stored.Aggregates[0].Config, entry.Config) {
		t.Fatal("aggregate settings changed")
	}
	other, err := listAggregates(tx, newID())
	if err != nil || len(other.Aggregates) != 0 {
		t.Fatal("owner isolation failed")
	}
	entry.Config = nil
	if err = tx.Save(&entry).Error; err != nil {
		t.Fatal(err)
	}
	stored, err = listAggregates(tx, owner)
	if err != nil || stored.Aggregates[0].Config != nil {
		t.Fatal("deletion was not preserved")
	}
}

func TestAggregatePolicyMatchesDesktopDefaultsAndNullRules(t *testing.T) {
	for _, policy := range []object{
		{"maxAttempts": nil}, {"maxCooldownSeconds": 1.0}, {"initialCooldownSeconds": 61.0},
		{"unknownPolicy": true}, {"routingStrategy": nil},
	} {
		if validateAggregatePolicy(policy) == nil {
			t.Fatalf("accepted incompatible policy: %v", policy)
		}
	}
	for _, policy := range []object{{}, {"errorRateThresholdPercent": nil}, {"maxCooldownSeconds": 5.0}} {
		if err := validateAggregatePolicy(policy); err != nil {
			t.Fatalf("rejected compatible policy: %v", policy)
		}
	}
}
