package devices

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"github.com/piperhex/remoteai-admin/internal/readcache"
	"github.com/piperhex/remoteai-admin/internal/readcache/testfixture"
)

func TestDeviceDisplayIP(t *testing.T) {
	for _, input := range []any{nil, "hostname", "192.168.1.21:443", "127.0.0.1", "fe80::1%eth0"} {
		if deviceIP(input) != nil {
			t.Fatalf("accepted %v", input)
		}
	}
	for input, want := range map[string]string{"192.168.1.21": "192.168.1.21", "::ffff:192.168.1.21": "192.168.1.21", "2001:db8::2": "2001:db8::2"} {
		if got := deviceIP(input); got == nil || *got != want {
			t.Fatalf("incorrect address for %q", input)
		}
	}
}

func TestDeviceDirectoryCachesMetadataAndKeepsPresenceLive(t *testing.T) {
	store := testfixture.Open(t)
	if err := store.DB.AutoMigrate(&Device{}); err != nil {
		t.Fatal(err)
	}
	s := &Service{&platform.Dependencies{DB: store.DB, Redis: store.Redis}}
	owner, id := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		if err := readcache.Invalidate(context.Background(), store.Redis, platform.DeviceDirectoryCacheKey(owner)); err != nil {
			t.Error(err)
		}
	})
	input := platform.JSON{"deviceId": id, "name": "first", "platform": "windows", "capabilities": []interface{}{"remote-command"}}
	input["ipAddress"] = "192.168.1.21"
	created, err := s.register(owner, input)
	if err != nil {
		t.Fatal(err)
	}
	g := &ControlGateway{service: s, sockets: map[string]*peer{}}
	store.Queries.Count.Store(0)
	first, err := g.statuses(owner)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	if first[0].OwnerID != owner || first[0].CreatedAt.Sub(created.CreatedAt).Abs() > time.Microsecond {
		t.Fatal("internal metadata lost")
	}
	if first[0].IPAddress == nil || *first[0].IPAddress != "192.168.1.21" {
		t.Fatal("missing address in directory")
	}
	first[0].Capabilities[0] = "mutated"
	g.sockets[owner+":"+id] = &peer{}
	second, err := g.statuses(owner)
	if err != nil || !second[0].Online || second[0].Capabilities[0] != "remote-command" {
		t.Fatal(second, err)
	}
	if got := store.Queries.Count.Load(); got != 1 {
		t.Fatalf("directory queries=%d, want 1", got)
	}
	input["name"] = "renamed"
	input["ipAddress"] = "2001:db8::2"
	if _, err := s.register(owner, input); err != nil {
		t.Fatal(err)
	}
	third, err := g.statuses(owner)
	if err != nil || third[0].Name != "renamed" || third[0].IPAddress == nil || *third[0].IPAddress != "2001:db8::2" {
		t.Fatal(third, err)
	}
	if other, err := s.list(uuid.NewString()); err != nil || len(other) != 0 {
		t.Fatal("owner isolation", other, err)
	}
}
