package lounge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClientVersionValidation(t *testing.T) {
	for _, version := range []string{"2.2.7", "2.2.7-beta.1+build.12"} {
		if validClientVersion(version) != version {
			t.Fatalf("rejected version %q", version)
		}
	}
	for _, version := range []string{"", "unknown", "v2.2.7", "<script>", "1.2.3-" + strings.Repeat("a", 64)} {
		if validClientVersion(version) != "" {
			t.Fatalf("accepted invalid version %q", version)
		}
	}
}

func TestClientDetailsKeepPlatformsDistinct(t *testing.T) {
	h := newHub(nil)
	user := uuid.NewString()
	for _, platform := range []clientPlatform{mobileClient, desktopClient, desktopClient} {
		client := presenceTestPeer(h, user)
		client.user.Clients = []clientInfo{{Version: "2.2.7", Platform: platform}}
	}
	expected := []clientInfo{{Version: "2.2.7", Platform: desktopClient}, {Version: "2.2.7", Platform: mobileClient}}
	if !reflect.DeepEqual(h.clientDetailsLocked(user), expected) {
		t.Fatal("platform versions were merged or unordered")
	}
	if validClientPlatform("forged") != "" {
		t.Fatal("invalid platform was accepted")
	}
}

func TestClientVersionsFollowLiveDevicesAndPrivacy(t *testing.T) {
	h := newHub(nil)
	user := uuid.NewString()
	viewer := presenceTestPeer(h, uuid.NewString())
	viewer.versionWatch = true
	viewer.profileWatch = map[string]bool{user: true}
	first := presenceTestPeer(h, user)
	first.user.ClientVersions = []string{"2.2.7"}
	second := presenceTestPeer(h, user)
	second.user.ClientVersions = []string{"2.2.4", "2.2.7"}
	h.publishClientVersionsLocked(user)
	assertClientVersions(t, viewer, []string{"2.2.4", "2.2.7"})
	second.expires = time.Now().Add(-time.Second)
	h.publishClientVersionsLocked(user)
	assertClientVersions(t, viewer, []string{"2.2.7"})
	viewer.blockedBy[user] = true
	h.publishClientVersionsLocked(user)
	assertClientVersions(t, viewer, []string{})
	viewer.blockedBy[user] = false
	first.close()
	h.publishClientVersionsLocked(user)
	assertClientVersions(t, viewer, []string{})
	viewer.versionWatch = false
	h.publishClientVersionsLocked(user)
	noQueuedPresence(t, viewer)
}

func assertClientVersions(t *testing.T, client *peer, versions []string) {
	t.Helper()
	select {
	case raw := <-client.queue:
		var frame struct {
			Type  string
			Users []clientVersions
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != "client-versions" || len(frame.Users) != 1 || !reflect.DeepEqual(frame.Users[0].Versions, versions) {
			t.Fatalf("unexpected version update: %s", raw)
		}
	default:
		t.Fatal("missing version update")
	}
}

func TestClientVersionsSocketSubscriptionAndRenewal(t *testing.T) {
	h, url := fixture(t)
	h.profiles = &memoryProfiles{avatars: make(map[string]string)}
	owner := uuid.NewString()
	device := connect(t, h, url, "")
	ticket := credential(t, h, owner, time.Now().Add(time.Minute))
	write(t, device, incoming{Type: "authenticate", Ticket: ticket, ClientVersion: "2.2.7", ClientPlatform: "desktop"})
	ready := read(t, device)
	if ready["type"] != "ready" {
		t.Fatal("missing ready")
	}
	user := ready["user"].(map[string]interface{})
	if !reflect.DeepEqual(user["clientVersions"], []interface{}{"2.2.7"}) {
		t.Fatal("missing reported version")
	}
	clients := user["clients"].([]interface{})
	if clients[0].(map[string]interface{})["platform"] != "desktop" {
		t.Fatal("platform was not reported")
	}
	viewer := connect(t, h, url, uuid.NewString())
	write(t, viewer, incoming{Type: "profile-watch", Watch: []string{owner}, ClientVersions: true})
	update := read(t, viewer)
	if update["type"] != "client-versions" {
		t.Fatal("missing subscribed versions")
	}
	assertProfile(t, viewer, avatarProfile{ID: owner})
	write(t, device, incoming{Type: "authenticate", Ticket: ticket, ClientVersion: "2.2.8", ClientPlatform: "mobile"})
	read(t, device)
	update = read(t, viewer)
	users := update["users"].([]interface{})
	versions := users[0].(map[string]interface{})["clientVersions"]
	clients = users[0].(map[string]interface{})["clients"].([]interface{})
	if clients[0].(map[string]interface{})["platform"] != "mobile" {
		t.Fatal("renewal did not update platform")
	}
	if !reflect.DeepEqual(versions, []interface{}{"2.2.8"}) {
		t.Fatal("renewal did not update versions")
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	update = read(t, viewer)
	users = update["users"].([]interface{})
	versions = users[0].(map[string]interface{})["clientVersions"]
	if !reflect.DeepEqual(versions, []interface{}{}) {
		t.Fatal("disconnected version remains visible")
	}
}
