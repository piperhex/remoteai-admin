package devices

import (
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/updatesharing"
)

func TestGlobalUpdateSharingScopeCannotBeExpandedByClients(t *testing.T) {
	for _, messageType := range []string{"update-peer-find", "update-peer-advertise"} {
		message := platform.JSON{"type": messageType, "ownOnly": false, "artifacts": []interface{}{"package"}}
		if !constrainUpdateSharing(message, updatesharing.Own) || message["ownOnly"] != true {
			t.Fatal("global own scope was not enforced", message)
		}
		allowed := constrainUpdateSharing(message, updatesharing.Off)
		if messageType == "update-peer-find" && allowed {
			t.Fatal("disabled sharing allowed discovery")
		}
		if messageType == "update-peer-advertise" && (!allowed || len(message["artifacts"].([]interface{})) != 0) {
			t.Fatal("disabled sharing retained published packages")
		}
	}
	message := platform.JSON{"type": "update-peer-find", "ownOnly": false}
	if !constrainUpdateSharing(message, updatesharing.Everyone) || message["ownOnly"] != false {
		t.Fatal("default sharing restricts cross-account discovery")
	}
}
