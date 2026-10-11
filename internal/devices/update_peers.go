package devices

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/google/uuid"
)

const (
	updateSeedLifetime     = 70 * time.Second
	updateTransferLifetime = 20 * time.Minute
	maxUpdateSeeds         = 10000
	maxUpdateTransfers     = 1024
	maxUpdateSeedPackages  = 6
	maxUpdateDownloadPeers = 3
	maxUpdateUploads       = 2
	updateFindInterval     = 10 * time.Second
)

type updateSeed struct {
	artifacts []string
	expires   time.Time
	owner     string
	ranges    bool
	protocol  int
	ownOnly   bool
	key       string
}

type updateTransfer struct {
	seed, receiver       *peer
	expires              time.Time
	protocol             int
	seedKey, receiverKey string
}

// Only opaque artifact identifiers and short-lived connection grants cross accounts.
// Neither update metadata nor package bytes are accepted or relayed here.
type updatePeerTracker struct {
	native      platform.JSON
	seeds       map[*peer]updateSeed
	transfers   map[string]updateTransfer
	lastFind    map[*peer]time.Time
	accountFind map[string]time.Time
	failed      map[string]time.Time
	lastPrune   time.Time
}

func newUpdatePeerTracker(service *Service) *updatePeerTracker {
	var native platform.JSON
	if service != nil {
		// The chat gateway validates this same configuration at startup.
		native, _ = nativeTraversalConfig(service.deps.Config)
	}
	return &updatePeerTracker{native: native, seeds: map[*peer]updateSeed{},
		transfers: map[string]updateTransfer{}, lastFind: map[*peer]time.Time{},
		accountFind: map[string]time.Time{}, failed: map[string]time.Time{}}
}

func (g *ControlGateway) receiveUpdatePeer(client *peer, session controlSession, message platform.JSON) {
	g.mu.Lock()
	device := session.kind == "device" && g.sockets[session.owner+":"+session.device] == client
	mobile := session.kind == "subscriber" && !session.expires.IsZero() && time.Now().Before(session.expires)
	if (!device && !mobile) || client.serviceHost.Load() || client.closed.Load() {
		g.mu.Unlock()
		return
	}
	if g.updateCluster != nil {
		g.mu.Unlock()
		g.updateCluster.receive(client, session, message)
		return
	}
	defer g.mu.Unlock()
	t := g.updatePeers
	t.prune(time.Now())
	key := session.owner + ":" + session.device
	if mobile {
		key = session.owner + ":mobile"
	}
	t.reattach(client, key)
	switch message["type"] {
	case "update-peer-advertise":
		if device {
			t.advertise(client, message, session.owner)
			if seed, ok := t.seeds[client]; ok {
				seed.key = key
				t.seeds[client] = seed
			}
		}
	case "update-peer-find":
		t.find(client, message, session.owner, key)
	case "update-peer-done":
		id, _ := message["sessionId"].(string)
		if transfer, ok := t.transfers[id]; ok && (transfer.seed == client || transfer.receiver == client) {
			if failed, _ := message["failed"].(bool); failed && transfer.receiver == client {
				t.failed[transfer.receiverKey+"|"+transfer.seedKey] = time.Now().Add(5 * time.Minute)
			}
			t.finish(id, transfer)
		}
	}
}

func validUpdateArtifact(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func (t *updatePeerTracker) advertise(client *peer, message platform.JSON, owner string) {
	values, ok := message["artifacts"].([]interface{})
	if !ok || len(values) > maxUpdateSeedPackages || t.native == nil {
		return
	}
	artifacts := make([]string, 0, len(values))
	for _, value := range values {
		artifact, ok := value.(string)
		if !ok || !validUpdateArtifact(artifact) {
			return
		}
		artifacts = append(artifacts, artifact)
	}
	if len(artifacts) == 0 {
		delete(t.seeds, client)
		return
	}
	if _, exists := t.seeds[client]; !exists && len(t.seeds) >= maxUpdateSeeds {
		return
	}
	ranges, _ := message["ranges"].(bool)
	protocol, _ := message["packageProtocol"].(float64)
	ownOnly, _ := message["ownOnly"].(bool)
	t.seeds[client] = updateSeed{artifacts: artifacts, expires: time.Now().Add(updateSeedLifetime),
		owner: owner, ranges: ranges, protocol: int(protocol), ownOnly: ownOnly}
}

func (t *updatePeerTracker) find(client *peer, message platform.JSON, owner, key string) {
	request, _ := message["requestId"].(string)
	artifact, _ := message["artifact"].(string)
	if len(request) == 0 || len(request) > 80 {
		return
	}
	unavailable := func() { client.send(platform.JSON{"type": "update-peer-unavailable", "requestId": request}, nil) }
	if !validUpdateArtifact(artifact) || t.native == nil || len(t.transfers) >= maxUpdateTransfers ||
		t.downloading(client) || time.Since(t.accountFind[key]) < updateFindInterval {
		unavailable()
		return
	}
	t.lastFind[client] = time.Now()
	t.accountFind[key] = time.Now()
	count := 1
	if requested, ok := message["maxPeers"].(float64); ok && requested >= 2 {
		count = maxUpdateDownloadPeers
	}
	seeds := t.selectSeeds(client, artifact, owner, count)
	requestedProtocol, _ := message["packageProtocol"].(float64)
	ownOnly, _ := message["ownOnly"].(bool)
	selected := seeds[:0]
	for _, seed := range seeds {
		entry := t.seeds[seed]
		if (ownOnly && entry.owner != owner) || (requestedProtocol >= 2 && entry.protocol < 2) ||
			time.Now().Before(t.failed[key+"|"+entry.key]) {
			continue
		}
		selected = append(selected, seed)
	}
	seeds = selected[:min(count, len(selected))]
	if len(seeds) == 0 || len(t.transfers)+len(seeds) > maxUpdateTransfers {
		unavailable()
		return
	}
	configs := make([]platform.JSON, 0, len(seeds))
	for _, seed := range seeds {
		config := t.grant(seed, client, updateGrantRequest{artifact, owner, int(requestedProtocol)})
		if config != nil {
			id := config["sessionId"].(string)
			transfer := t.transfers[id]
			transfer.receiverKey = key
			t.transfers[id] = transfer
			configs = append(configs, config)
		}
	}
	if len(configs) == 0 {
		unavailable()
	} else if count == 1 {
		client.send(platform.JSON{"type": "update-peer-offer", "requestId": request,
			"artifact": artifact, "config": configs[0]}, nil)
	} else {
		client.send(platform.JSON{"type": "update-peer-offers", "requestId": request,
			"artifact": artifact, "configs": configs}, nil)
	}
}

type updateGrantRequest struct {
	artifact, owner string
	protocol        int
}

func (t *updatePeerTracker) grant(seed, client *peer, request updateGrantRequest) platform.JSON {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil
	}
	id := "update-" + uuid.NewString()
	expires := time.Now().Add(updateTransferLifetime)
	protocol := 0
	if request.protocol >= 2 && t.seeds[seed].protocol >= 2 {
		protocol = 2
	}
	t.transfers[id] = updateTransfer{seed: seed, receiver: client, expires: expires,
		protocol: protocol, seedKey: t.seeds[seed].key}
	grant := func(desktop bool) platform.JSON {
		return platform.JSON{"sessionId": id, "secret": hex.EncodeToString(secret), "desktop": desktop,
			"expiresAt": expires.UnixMilli(), "servers": t.native["servers"], "stunServers": t.native["stunServers"],
			"packageProtocol": protocol, "sameAccount": t.seeds[seed].owner == request.owner}
	}
	seed.send(platform.JSON{"type": "update-peer-offer", "artifact": request.artifact, "config": grant(true)}, nil)
	return grant(false)
}

func (t *updatePeerTracker) downloading(client *peer) bool {
	for _, transfer := range t.transfers {
		if transfer.receiver == client {
			return true
		}
	}
	return false
}

func (t *updatePeerTracker) selectSeeds(receiver *peer, artifact, owner string, count int) []*peer {
	uploads := map[*peer]int{}
	for _, transfer := range t.transfers {
		uploads[transfer.seed]++
	}
	var candidates []*peer
	for client, seed := range t.seeds {
		if client == receiver || client.closed.Load() || uploads[client] >= maxUpdateUploads ||
			(count > 1 && !seed.ranges) || (seed.ownOnly && seed.owner != owner) {
			continue
		}
		for _, available := range seed.artifacts {
			if available != artifact {
				continue
			}
			candidates = append(candidates, client)
			break
		}
	}
	// Prefer the receiver's own PCs, then idle seeds; never disclose account identities.
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if (t.seeds[a].owner == owner) != (t.seeds[b].owner == owner) {
			return t.seeds[a].owner == owner
		}
		return uploads[a] < uploads[b]
	})
	return candidates
}

func (t *updatePeerTracker) prune(now time.Time) {
	if now.Sub(t.lastPrune) < time.Second {
		return
	}
	t.lastPrune = now
	for client, seed := range t.seeds {
		if client.closed.Load() || !now.Before(seed.expires) {
			delete(t.seeds, client)
		}
	}
	for id, transfer := range t.transfers {
		if !now.Before(transfer.expires) {
			t.finish(id, transfer)
		}
	}
	for client, last := range t.lastFind {
		if now.Sub(last) > time.Minute {
			delete(t.lastFind, client)
		}
	}
	for key, last := range t.accountFind {
		if now.Sub(last) > time.Minute {
			delete(t.accountFind, key)
		}
	}
	for key, until := range t.failed {
		if !now.Before(until) {
			delete(t.failed, key)
		}
	}
}

func (t *updatePeerTracker) disconnect(client *peer) {
	delete(t.seeds, client)
	delete(t.lastFind, client)
	for id, transfer := range t.transfers {
		if transfer.protocol < 2 && (transfer.seed == client || transfer.receiver == client) {
			t.finish(id, transfer)
		}
	}
}

func (t *updatePeerTracker) reattach(client *peer, key string) {
	for id, transfer := range t.transfers {
		if transfer.protocol < 2 {
			continue
		}
		if transfer.seedKey == key {
			transfer.seed = client
		}
		if transfer.receiverKey == key {
			transfer.receiver = client
		}
		t.transfers[id] = transfer
	}
}

func (t *updatePeerTracker) finish(id string, transfer updateTransfer) {
	delete(t.transfers, id)
	message := platform.JSON{"type": "update-peer-cancel", "sessionId": id}
	transfer.seed.send(message, nil)
	transfer.receiver.send(message, nil)
}
