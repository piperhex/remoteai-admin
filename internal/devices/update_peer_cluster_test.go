package devices

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func clusterFixture(t *testing.T) (*updatePeerCluster, *updatePeerCluster) {
	t.Helper()
	address := os.Getenv("UPDATE_PEER_TEST_REDIS")
	if address == "" {
		t.Skip("set UPDATE_PEER_TEST_REDIS to an isolated Redis for distributed matching tests")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	service := &Service{deps: &platform.Dependencies{Redis: client, Config: platform.Config{
		"CHAT_NATIVE_PEERS": "udp://127.0.0.1:11010", "CHAT_NATIVE_STUN": "127.0.0.1:3478,127.0.0.1:3479",
	}}}
	a, b := newUpdatePeerCluster(service), newUpdatePeerCluster(service)
	t.Cleanup(func() { a.stop(); b.stop(); client.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for !a.ready.Load() || !b.ready.Load() {
		if time.Now().After(deadline) {
			t.Fatal("Redis subscriptions did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return a, b
}

func clusterFrame(t *testing.T, client *peer) platform.JSON {
	t.Helper()
	select {
	case frame := <-client.queue:
		var message platform.JSON
		if err := json.Unmarshal(frame.bytes, &message); err != nil {
			t.Fatal(err)
		}
		return message
	case <-time.After(3 * time.Second):
		t.Fatal("missing distributed frame")
	}
	return nil
}

func TestUpdateClusterMatchesAcrossInstancesAndSurvivesReconnect(t *testing.T) {
	a, b := clusterFixture(t)
	owner := uuid.NewString()
	artifact := updateIdentity(owner, "artifact")
	seed, receiver := queuedPeer(), queuedPeer()
	host := controlSession{owner: owner, device: "seed", kind: "device"}
	viewer := controlSession{owner: owner, device: "receiver", kind: "device"}
	advertise := platform.JSON{"type": "update-peer-advertise", "artifacts": []interface{}{artifact},
		"ranges": true, "packageProtocol": float64(2), "ownOnly": true}
	a.receive(seed, host, advertise)
	b.receive(receiver, viewer, platform.JSON{"type": "update-peer-find", "requestId": "one", "artifact": artifact,
		"maxPeers": float64(3), "packageProtocol": float64(2)})
	message := clusterFrame(t, receiver)
	configs, ok := message["configs"].([]interface{})
	if !ok || len(configs) != 1 {
		t.Fatalf("no distributed match: %v", message)
	}
	config := configs[0].(map[string]interface{})
	if config["packageProtocol"] != float64(2) || config["sameAccount"] != true {
		t.Fatal("invalid grant")
	}
	clusterFrame(t, seed)
	id := config["sessionId"].(string)
	a.disconnect(seed)
	if a.redis.Exists(context.Background(), updateLeaseKey(id)).Val() != 1 {
		t.Fatal("signaling disconnect canceled a direct transfer")
	}
	// A replacement socket keeps the stable endpoint identity, even on another instance.
	replacement := queuedPeer()
	b.receive(replacement, host, advertise)
	b.receive(replacement, host, platform.JSON{"type": "update-peer-done", "sessionId": id})
	if clusterFrame(t, receiver)["type"] != "update-peer-cancel" {
		t.Fatal("reconnected seed could not finish")
	}
	if exists := b.redis.Exists(context.Background(), updateLeaseKey(id)).Val(); exists != 0 {
		t.Fatal("reservation remained after completion")
	}
}

func TestUpdateClusterReservationsAreAtomicAndParticipantScoped(t *testing.T) {
	a, b := clusterFixture(t)
	ctx := context.Background()
	owner := uuid.NewString()
	seed := sharedUpdateSeed{Owner: owner, Device: "seed", Endpoint: uuid.NewString()}
	var accepted atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 3; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			lease := sharedUpdateLease{ID: "update-" + uuid.NewString(), Seed: seed,
				Receiver: sharedUpdateSeed{Owner: owner, Device: uuid.NewString(), Endpoint: uuid.NewString()}, Protocol: 2}
			cluster := []*updatePeerCluster{a, b}[index%2]
			if !cluster.reserve(ctx, lease) {
				return
			}
			accepted.Add(1)
			if cluster.release(ctx, lease.ID, "intruder") != nil {
				t.Error("unrelated endpoint released a lease")
			}
		}(index)
	}
	wait.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("upload capacity was not atomic: %d", accepted.Load())
	}
}

func TestUpdateClusterOwnScopeRejectsOtherAccounts(t *testing.T) {
	a, b := clusterFixture(t)
	owner := uuid.NewString()
	artifact := updateIdentity(owner, "artifact")
	a.receive(queuedPeer(), controlSession{owner: owner, device: "seed", kind: "device"}, platform.JSON{
		"type": "update-peer-advertise", "artifacts": []interface{}{artifact}, "ownOnly": true,
		"ranges": true, "packageProtocol": float64(2),
	})
	receiver := queuedPeer()
	b.receive(receiver, controlSession{owner: uuid.NewString(), device: "receiver", kind: "device"}, platform.JSON{
		"type": "update-peer-find", "requestId": "other", "artifact": artifact,
		"maxPeers": float64(3), "packageProtocol": float64(2),
	})
	if clusterFrame(t, receiver)["type"] != "update-peer-unavailable" {
		t.Fatal("shared across restricted accounts")
	}
}

func TestUpdateClusterRefillNeverDuplicatesAnActiveSeedAcrossInstances(t *testing.T) {
	a, b := clusterFixture(t)
	ctx := context.Background()
	owner := uuid.NewString()
	artifact := updateIdentity(owner, "refill")
	receiver := sharedUpdateSeed{Owner: owner, Device: "receiver", Endpoint: uuid.NewString()}
	first := sharedUpdateSeed{Owner: owner, Device: "first", Endpoint: uuid.NewString(),
		Artifacts: []string{artifact}, Protocol: 2, Ranges: true}
	lease := sharedUpdateLease{ID: "update-" + uuid.NewString(), Seed: first, Receiver: receiver, Protocol: 2, Artifact: artifact}
	if !a.reserve(ctx, lease) {
		t.Fatal("initial reservation rejected")
	}
	duplicate := lease
	duplicate.ID = "update-" + uuid.NewString()
	if b.reserve(ctx, duplicate) {
		t.Fatal("same receiver and seed reserved twice")
	}
	message := platform.JSON{"refill": true, "packageProtocol": float64(2)}
	key := updateReceiverSlots(receiver.Endpoint)
	if b.refillCapacity(ctx, key, artifact, message) != 2 {
		t.Fatal("healthy grant prevented refill")
	}
	if b.refillCapacity(ctx, key, updateIdentity(owner, "other"), message) != 0 {
		t.Fatal("refill changed the active package")
	}
	if b.refillCapacity(ctx, key, artifact, platform.JSON{}) != 0 {
		t.Fatal("legacy find bypassed active download guard")
	}
	if b.release(ctx, lease.ID, receiver.Endpoint) == nil || !b.reserve(ctx, duplicate) {
		t.Fatal("completed reservation did not release its pair slot")
	}
}
