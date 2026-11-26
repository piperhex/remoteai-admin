package devices

import (
	"testing"

	"github.com/piperhex/remoteai-admin/internal/platform"
)

func TestKeyringCloseReasonIsScopedToHost(t *testing.T) {
	for _, hot := range []bool{true, false} {
		for _, fromHost := range []bool{true, false} {
			host, client := queuedPeer(), queuedPeer()
			sessions, _ := bulkTestDelivery(t, host, client)
			if !hot {
				delete(sessions.hot.sessions, "download")
				sessions.sessions["download"] = &chatSession{id: "download", desktop: host, mobile: client}
			}
			from, to := client, host
			if fromHost {
				from, to = host, client
			}
			frame := platform.JSON{"type": "peer-close", "sessionId": "download", "reason": "host-keyring"}
			if err := sessions.route(from, frame); err != nil {
				t.Fatal(err)
			}
			got := updateFrame(t, to)
			if got["type"] != "peer-close" || (got["reason"] == "host-keyring") != fromHost {
				t.Fatalf("hot=%t fromHost=%t: unexpected close frame %v", hot, fromHost, got)
			}
		}
	}
}
