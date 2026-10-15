package accounts

import (
	"reflect"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
)

func TestAggregateSummariesExposeOnlyEnabledPublicChoices(t *testing.T) {
	active := aggregateFixture(t)
	disabled := aggregateFixture(t)
	disabled.ID = "disabled"
	disabled.Config["enabled"] = false
	deleted := aggregateRecord{ID: "deleted"}
	got := aggregateSummaries(aggregatePayload{Aggregates: []aggregateRecord{active, disabled, deleted}})
	want := []platform.JSON{{"id": "aggregate:route-1", "name": "Route", "model": "public",
		"group": "", "kind": "aggregate"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected public choices: %#v", got)
	}
}
