package devices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type updateEndpoint struct {
	client *peer
	seed   sharedUpdateSeed
}
type updateEnvelope struct {
	Endpoint string
	Message  platform.JSON
}

// Redis holds expiring discovery and reservations; pub/sub carries only authenticated control messages.
// A broker outage declines new lookups while existing direct transfers keep their grants.
type updatePeerCluster struct {
	redis     *redis.Client
	instance  string
	native    platform.JSON
	mu        sync.Mutex
	endpoints map[*peer]updateEndpoint
	ready     atomic.Bool
	ctx       context.Context
	stop      context.CancelFunc
}

func newUpdatePeerCluster(service *Service) *updatePeerCluster {
	if service == nil || service.deps.Redis == nil {
		return nil
	}
	native, err := nativeTraversalConfig(service.deps.Config)
	if err != nil || native == nil {
		return nil
	}
	c := &updatePeerCluster{redis: service.deps.Redis, instance: uuid.NewString(), native: native,
		endpoints: map[*peer]updateEndpoint{}}
	c.ctx, c.stop = context.WithCancel(context.Background())
	go c.listen()
	return c
}

func (c *updatePeerCluster) listen() {
	for c.ctx.Err() == nil {
		c.subscription()
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (c *updatePeerCluster) subscription() {
	subscriber := c.redis.Subscribe(c.ctx, updateRedisPrefix+"node:"+c.instance)
	defer subscriber.Close()
	if _, err := subscriber.Receive(c.ctx); err != nil {
		return
	}
	c.ready.Store(true)
	defer c.ready.Store(false)
	channel := subscriber.Channel()
	for {
		select {
		case <-c.ctx.Done():
			return
		case message, ok := <-channel:
			if !ok {
				return
			}
			var envelope updateEnvelope
			if len(message.Payload) <= 32*1024 && json.Unmarshal([]byte(message.Payload), &envelope) == nil {
				if !c.deliver(envelope) && envelope.Message["type"] == "update-peer-offer" {
					c.unreachable(envelope)
				}
			}
		}
	}
}

func (c *updatePeerCluster) unreachable(envelope updateEnvelope) {
	config, ok := envelope.Message["config"].(map[string]interface{})
	if !ok {
		return
	}
	id, _ := config["sessionId"].(string)
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	c.finish(ctx, envelope.Endpoint, id, false)
}

func (c *updatePeerCluster) deliver(envelope updateEnvelope) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, endpoint := range c.endpoints {
		if endpoint.seed.Endpoint == envelope.Endpoint && !endpoint.client.closed.Load() {
			endpoint.client.send(envelope.Message, nil)
			return true
		}
	}
	return false
}

func (c *updatePeerCluster) endpoint(client *peer, session controlSession) sharedUpdateSeed {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.endpoints[client]; ok {
		return existing.seed
	}
	device := session.device
	if device == "" {
		device = "mobile"
	}
	id := uuid.NewString()
	if session.device != "" {
		id = updateIdentity(session.owner, session.device)
	}
	seed := sharedUpdateSeed{Endpoint: id, Instance: c.instance, Owner: session.owner, Device: device,
		Generation: uuid.NewString()}
	c.endpoints[client] = updateEndpoint{client, seed}
	return seed
}

func (c *updatePeerCluster) receive(client *peer, session controlSession, message platform.JSON) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	endpoint := c.endpoint(client, session)
	c.redis.Set(ctx, updateRedisPrefix+"route:"+endpoint.Endpoint, c.instance, updateSeedLifetime)
	switch message["type"] {
	case "update-peer-advertise":
		if session.kind == "device" {
			c.advertise(ctx, endpoint, message)
		}
	case "update-peer-find":
		c.find(ctx, client, endpoint, message)
	case "update-peer-done":
		id, _ := message["sessionId"].(string)
		failed, _ := message["failed"].(bool)
		c.finish(ctx, endpoint.Endpoint, id, failed)
	}
}

func (c *updatePeerCluster) advertise(ctx context.Context, seed sharedUpdateSeed, message platform.JSON) {
	values, ok := message["artifacts"].([]interface{})
	if !ok || len(values) > maxUpdateSeedPackages {
		return
	}
	for _, value := range values {
		id, ok := value.(string)
		if !ok || !validUpdateArtifact(id) {
			return
		}
		seed.Artifacts = append(seed.Artifacts, id)
	}
	seed.Ranges, _ = message["ranges"].(bool)
	seed.OwnOnly, _ = message["ownOnly"].(bool)
	protocol, _ := message["packageProtocol"].(float64)
	seed.Protocol = int(protocol)
	// Failed publication simply expires the previous advertisement; it must not block device control.
	if err := c.publishSeed(ctx, seed); err != nil {
		return
	}
}

func (c *updatePeerCluster) find(ctx context.Context, client *peer, receiver sharedUpdateSeed, message platform.JSON) {
	request, _ := message["requestId"].(string)
	artifact, _ := message["artifact"].(string)
	if len(request) == 0 || len(request) > 80 {
		return
	}
	unavailable := func() { client.send(platform.JSON{"type": "update-peer-unavailable", "requestId": request}, nil) }
	if !validUpdateArtifact(artifact) || !c.ready.Load() {
		unavailable()
		return
	}
	rateKey := updateRedisPrefix + "find:" + updateIdentity(receiver.Owner, receiver.Device)
	allowed, err := c.redis.SetNX(ctx, rateKey, "1", updateFindInterval).Result()
	if err != nil || !allowed {
		unavailable()
		return
	}
	key := updateReceiverSlots(receiver.Endpoint)
	if err := c.redis.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprint(time.Now().UnixMilli())).Err(); err != nil {
		unavailable()
		return
	}
	if active, err := c.redis.ZCard(ctx, key).Result(); err != nil || active != 0 {
		unavailable()
		return
	}
	count := 1
	if requested, _ := message["maxPeers"].(float64); requested >= 2 {
		count = maxUpdateDownloadPeers
	}
	seeds, err := c.candidates(ctx, artifact, receiver.Owner)
	if err != nil {
		unavailable()
		return
	}
	configs := c.match(ctx, seeds, receiver, matchUpdate{artifact: artifact, message: message, count: count})
	if len(configs) == 0 {
		unavailable()
		return
	}
	if count == 1 {
		client.send(platform.JSON{"type": "update-peer-offer", "requestId": request, "artifact": artifact, "config": configs[0]}, nil)
	} else {
		client.send(platform.JSON{"type": "update-peer-offers", "requestId": request, "artifact": artifact, "configs": configs}, nil)
	}
}

type matchUpdate struct {
	artifact string
	message  platform.JSON
	count    int
}

func (c *updatePeerCluster) match(
	ctx context.Context, seeds []sharedUpdateSeed, receiver sharedUpdateSeed, request matchUpdate,
) []platform.JSON {
	own, _ := request.message["ownOnly"].(bool)
	protocol, _ := request.message["packageProtocol"].(float64)
	var configs []platform.JSON
	for _, seed := range seeds {
		if seed.Endpoint == receiver.Endpoint || !slices.Contains(seed.Artifacts, request.artifact) ||
			((own || seed.OwnOnly) && seed.Owner != receiver.Owner) ||
			(request.count > 1 && !seed.Ranges) || (protocol >= 2 && seed.Protocol < 2) {
			continue
		}
		failed, err := c.redis.Exists(ctx, updateFailureKey(receiver, seed)).Result()
		if err != nil || failed != 0 {
			continue
		}
		version := 0
		if protocol >= 2 {
			version = 2
		}
		lease := sharedUpdateLease{ID: "update-" + uuid.NewString(), Seed: seed, Receiver: receiver, Protocol: version}
		if config := c.grant(ctx, lease, request.artifact); config != nil {
			configs = append(configs, config)
		}
		if len(configs) == request.count {
			break
		}
	}
	return configs
}

func (c *updatePeerCluster) grant(ctx context.Context, lease sharedUpdateLease, artifact string) platform.JSON {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil || !c.reserve(ctx, lease) {
		return nil
	}
	config := func(desktop bool) platform.JSON {
		return platform.JSON{"sessionId": lease.ID, "secret": hex.EncodeToString(secret), "desktop": desktop,
			"expiresAt": time.Now().Add(updateTransferLifetime).UnixMilli(), "servers": c.native["servers"],
			"stunServers": c.native["stunServers"], "packageProtocol": lease.Protocol,
			"sameAccount": lease.Seed.Owner == lease.Receiver.Owner}
	}
	if !c.send(ctx, lease.Seed, platform.JSON{"type": "update-peer-offer", "artifact": artifact, "config": config(true)}) {
		c.release(ctx, lease.ID, lease.Receiver.Endpoint)
		return nil
	}
	return config(false)
}

func (c *updatePeerCluster) send(ctx context.Context, endpoint sharedUpdateSeed, message platform.JSON) bool {
	envelope := updateEnvelope{Endpoint: endpoint.Endpoint, Message: message}
	if current, err := c.redis.Get(ctx, updateRedisPrefix+"route:"+endpoint.Endpoint).Result(); err == nil {
		endpoint.Instance = current
	}
	if endpoint.Instance == c.instance {
		return c.deliver(envelope)
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return false
	}
	count, err := c.redis.Publish(ctx, updateRedisPrefix+"node:"+endpoint.Instance, data).Result()
	return err == nil && count > 0
}

func (c *updatePeerCluster) finish(ctx context.Context, endpoint, id string, failed bool) {
	lease := c.release(ctx, id, endpoint)
	if lease == nil {
		return
	}
	if failed && lease.Receiver.Endpoint == endpoint {
		c.redis.Set(ctx, updateFailureKey(lease.Receiver, lease.Seed), "1", 5*time.Minute)
	}
	message := platform.JSON{"type": "update-peer-cancel", "sessionId": id}
	c.send(ctx, lease.Seed, message)
	c.send(ctx, lease.Receiver, message)
}

func (c *updatePeerCluster) disconnect(client *peer) {
	c.mu.Lock()
	endpoint, exists := c.endpoints[client]
	delete(c.endpoints, client)
	c.mu.Unlock()
	if !exists {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		removeUpdateSeed.Run(ctx, c.redis, []string{updateRedisPrefix + "seed:" + endpoint.seed.Endpoint}, endpoint.seed.Generation)
		// v2 streams deliberately survive control reconnects. All reservations still expire.
		for _, key := range []string{updateSeedSlots(endpoint.seed), updateReceiverSlots(endpoint.seed.Endpoint)} {
			ids, err := c.redis.ZRange(ctx, key, 0, -1).Result()
			if err != nil {
				continue
			}
			for _, id := range ids {
				var lease sharedUpdateLease
				data, err := c.redis.Get(ctx, updateLeaseKey(id)).Bytes()
				if err == nil && json.Unmarshal(data, &lease) == nil && lease.Protocol < 2 {
					c.finish(ctx, endpoint.seed.Endpoint, id, false)
				}
			}
		}
	}()
}
