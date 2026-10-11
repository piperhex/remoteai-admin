package devices

import (
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
)

func TestUpdatePeersAcceptSixPackagesAndMatchDependencyAcrossAccounts(t *testing.T) {
	g, seed, receiver := updatePeersFixture()
	artifacts := make([]interface{}, 0, 6)
	for _, character := range "abcdef" {
		artifacts = append(artifacts, strings.Repeat(string(character), 64))
	}
	g.receiveUpdatePeer(seed, g.sessions[seed], platform.JSON{
		"type": "update-peer-advertise", "artifacts": artifacts, "ranges": true,
	})
	if len(g.updatePeers.seeds[seed].artifacts) != 6 {
		t.Fatal("GUI dependencies must coexist with desktop and Android updates")
	}
	g.receiveUpdatePeer(seed, g.sessions[seed], platform.JSON{
		"type": "update-peer-advertise", "artifacts": append(artifacts, strings.Repeat("0", 64)),
	})
	if len(g.updatePeers.seeds[seed].artifacts) != 6 {
		t.Fatal("oversized advertisements must leave the previous valid announcement intact")
	}
	findUpdate(g, receiver, artifacts[5].(string))
	if updateFrame(t, receiver)["artifact"] != artifacts[5] {
		t.Fatal("new dependency slots did not participate in cross-account matching")
	}
}
