package accounts

import (
	"strings"
	"testing"
)

func TestAccountAliasRoundTripAndLegacyUpdates(t *testing.T) {
	input := accountFixture()
	input["privateDetails"] = object{"alias": "  工作号  "}
	stored, _, err := mergeAccount(nil, "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if obj(accountDTO(*stored)["privateDetails"])["alias"] != "工作号" {
		t.Fatal("alias omitted from sync DTO")
	}
	update := accountFixture()
	update["fieldModifiedAt"] = object{"privateDetails": testNew}
	update["privateDetails"] = object{"phoneNumber": "123"}
	merged, _, err := mergeAccount(stored, "owner", update)
	if err != nil {
		t.Fatal(err)
	}
	if merged.PrivateDetails["alias"] != "工作号" {
		t.Fatal("legacy save cleared the alias")
	}
	update["privateDetails"] = object{"alias": ""}
	merged, _, err = mergeAccount(stored, "owner", update)
	if err != nil {
		t.Fatal(err)
	}
	if obj(accountDTO(*merged)["privateDetails"])["alias"] != "" {
		t.Fatal("explicit clear must synchronize")
	}
}

func TestAccountAliasValidation(t *testing.T) {
	for _, value := range []interface{}{nil, 12, "bad\nname", strings.Repeat("中", 17)} {
		if normalizeAccountAlias(object{"alias": value}) == nil {
			t.Fatalf("invalid alias accepted: %v", value)
		}
	}
	if err := normalizeAccountAlias(object{"alias": strings.Repeat("中", 16)}); err != nil {
		t.Fatal(err)
	}
}
