package devices

import (
	"fmt"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
)

func TestUpdatePeersRefillRetainsHealthyLeasesAndExcludesExistingSeeds(t *testing.T) {
	g, first, receiver := updatePeersFixture()
	artifact := strings.Repeat("d", 64)
	advertise := func(seed *peer) {
		g.receiveUpdatePeer(seed, g.sessions[seed], platform.JSON{"type": "update-peer-advertise",
			"artifacts": []interface{}{artifact}, "ranges": true, "packageProtocol": float64(2)})
	}
	find := func(id, target string) platform.JSON {
		delete(g.updatePeers.accountFind, "bob:pc-b")
		g.receiveUpdatePeer(receiver, g.sessions[receiver], platform.JSON{"type": "update-peer-find",
			"requestId": id, "artifact": target, "maxPeers": float64(3), "packageProtocol": float64(2), "refill": true})
		return updateFrame(t, receiver)
	}
	advertise(first)
	initial := find("initial", artifact)["configs"].([]interface{})
	if len(initial) != 1 {
		t.Fatal("missing initial peer")
	}
	updateFrame(t, first)
	if find("other-file", strings.Repeat("e", 64))["type"] != "update-peer-unavailable" {
		t.Fatal("refill switched an active download to another package")
	}
	for i := 0; i < 3; i++ {
		seed := queuedPeer()
		device := fmt.Sprintf("new-%d", i)
		g.sessions[seed] = controlSession{owner: "alice", device: device, kind: "device"}
		g.sockets["alice:"+device] = seed
		advertise(seed)
	}
	added := find("refill", artifact)["configs"].([]interface{})
	if len(added) != 2 || len(g.updatePeers.transfers) != 3 {
		t.Fatal("refill did not honor the remaining capacity")
	}
	count := 0
	for _, lease := range g.updatePeers.transfers {
		if lease.seed == first {
			count++
		}
	}
	if count != 1 {
		t.Fatal("refill granted a duplicate connection to the initial seed")
	}
	if find("full", artifact)["type"] != "update-peer-unavailable" {
		t.Fatal("exceeded download slots")
	}
}
