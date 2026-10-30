//go:build integration

package devices

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/google/uuid"
)

func TestAssistanceChatAccountUsesIDAndDoesNotRevealEmail(t *testing.T) {
	f := newAssistanceHTTPFixture(t)
	db := f.gateway.service.deps.DB
	if err := db.Exec(`UPDATE users SET email = ? WHERE id = ?`, "shared@one.test", f.helper.owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`,
		uuid.NewString(), "shared@two.test", "fixture-only").Error; err != nil {
		t.Fatal(err)
	}
	created := f.call(f.host, "", platform.JSON{"deviceId": f.host.device, "userId": f.helper.owner})
	var invitation assistanceRequest
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &invitation) != nil {
		t.Fatal(created.Code, created.Body.String())
	}
	if invitation.HelperEmail != "shared" || invitation.HostEmail != f.host.owner ||
		strings.Contains(created.Body.String(), "@") ||
		f.gateway.sessions.assistance[invitation.ID].HelperOwner != f.helper.owner {
		t.Fatal("chat invitation disclosed an address or resolved the wrong account", created.Body.String())
	}
	listed := f.call(f.helper, "?deviceId="+f.helper.device, nil)
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), invitation.ID) ||
		strings.Contains(listed.Body.String(), "@") {
		t.Fatal("recipient list disclosed an address or lost invitation", listed.Body.String())
	}
}

func TestAssistanceRejectsAmbiguousAndMalformedRecipientInput(t *testing.T) {
	f := newAssistanceHTTPFixture(t)
	for _, fields := range []platform.JSON{
		{"userId": "../users"}, {"userId": f.helper.owner, "email": "someone@example.test"},
		{"email": "shared"}, {},
	} {
		fields["deviceId"] = f.host.device
		if response := f.call(f.host, "", fields); response.Code != 400 {
			t.Fatal("invalid recipient accepted", response.Code, response.Body.String())
		}
	}
}
