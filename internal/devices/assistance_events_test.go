package devices

import (
	"testing"
	"time"

	"github.com/piperhex/remoteai-admin/internal/platform"
)

func TestAssistanceEventsFollowInvitationVisibility(t *testing.T) {
	s := newChatSessions(nil)
	join := func(owner, device string, events, service bool) *peer {
		t.Helper()
		p := queuedPeer()
		p.serviceHost.Store(service)
		identity := chatIdentity{owner, device, "desktop", time.Now().Add(time.Hour)}
		err := s.join(p, identity, platform.JSON{"transportVersion": float64(2),
			"remoteAssistance": true, "assistanceEvents": events}, nil)
		if err != nil {
			t.Fatal(err)
		}
		frame := updateFrame(t, p)
		if frame["type"] != "registered" || (frame["assistanceEvents"] == true) != (events && !service) {
			t.Fatalf("unexpected registration %v", frame)
		}
		return p
	}
	host := join("owner", "pc", true, false)
	helper := join("helper", "one", true, false)
	sibling := join("helper", "two", true, false)
	excluded := []*peer{join("other", "one", true, false), join("owner", "other-pc", true, false),
		join("helper", "legacy", false, false), join("helper", "unattended", true, true)}
	changed := func(peers ...*peer) {
		t.Helper()
		for _, p := range peers {
			if frame := updateFrame(t, p); len(frame) != 1 || frame["type"] != "assistance-changed" {
				t.Fatalf("expected a content-free invalidation, got %v", frame)
			}
		}
	}
	request, err := s.createAssistance(assistanceRequest{HostOwner: "owner", HostDeviceID: "pc", HelperOwner: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	changed(host, helper, sibling)
	if _, err = s.updateAssistance(assistanceActor{"helper", "one", request.ID}, "accept"); err != nil {
		t.Fatal(err)
	}
	changed(host, helper, sibling)
	if len(s.listAssistance("helper", "two")) != 0 {
		t.Fatal("accepted invitation still visible on sibling")
	}
	if _, err = s.updateAssistance(assistanceActor{"owner", "pc", request.ID}, "end"); err != nil {
		t.Fatal(err)
	}
	changed(host, helper)
	for _, p := range append(excluded, sibling) {
		if len(p.queue) != 0 {
			t.Fatal("notified a device outside the audience")
		}
	}
}
