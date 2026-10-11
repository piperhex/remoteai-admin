package devices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const updateRedisPrefix = "csw:update:v2:"

type sharedUpdateSeed struct {
	Endpoint, Instance, Owner, Device string
	Generation                        string
	Artifacts                         []string
	Ranges, OwnOnly                   bool
	Protocol                          int
}

type sharedUpdateLease struct {
	ID             string
	Seed, Receiver sharedUpdateSeed
	Protocol       int
}

func updateIdentity(owner, device string) string {
	digest := sha256.Sum256([]byte(owner + "\x00" + device))
	return hex.EncodeToString(digest[:])
}

func updateSeedSlots(seed sharedUpdateSeed) string {
	return updateRedisPrefix + "uploads:" + updateIdentity(seed.Owner, seed.Device)
}

func updateReceiverSlots(endpoint string) string { return updateRedisPrefix + "downloads:" + endpoint }
func updateLeaseKey(id string) string            { return updateRedisPrefix + "lease:" + id }
func updateFailureKey(receiver, seed sharedUpdateSeed) string {
	return updateRedisPrefix + "failed:" + updateIdentity(receiver.Owner, receiver.Device) + ":" +
		updateIdentity(seed.Owner, seed.Device)
}

// Capacity and publication are one transaction across all Go instances.
var reserveUpdate = redis.NewScript(`
local now, expiry = tonumber(ARGV[1]), tonumber(ARGV[2])
for i=1,3 do redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now) end
if redis.call('ZCARD', KEYS[1]) >= 2 or redis.call('ZCARD', KEYS[2]) >= 3 or
   redis.call('ZCARD', KEYS[3]) >= 1024 then return 0 end
for i=1,3 do
  redis.call('ZADD', KEYS[i], expiry, ARGV[3])
  redis.call('PEXPIRE', KEYS[i], ARGV[5])
end
redis.call('SET', KEYS[4], ARGV[4], 'PX', ARGV[5])
return 1`)

var releaseUpdate = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if not value then return '' end
local lease = cjson.decode(value)
if lease.Seed.Endpoint ~= ARGV[1] and lease.Receiver.Endpoint ~= ARGV[1] then return '' end
redis.call('DEL', KEYS[1])
for i=2,4 do redis.call('ZREM', KEYS[i], lease.ID) end
return value`)

var removeUpdateSeed = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if value and cjson.decode(value).Generation == ARGV[1] then redis.call('DEL', KEYS[1]) end
return 1`)

var registerUpdateSeed = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= 10000 and not redis.call('ZSCORE', KEYS[1], ARGV[3]) then return 0 end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1`)

func (c *updatePeerCluster) reserve(ctx context.Context, lease sharedUpdateLease) bool {
	data, err := json.Marshal(lease)
	if err != nil {
		return false
	}
	now := time.Now()
	keys := []string{updateSeedSlots(lease.Seed), updateReceiverSlots(lease.Receiver.Endpoint),
		updateRedisPrefix + "transfers", updateLeaseKey(lease.ID)}
	result, err := reserveUpdate.Run(ctx, c.redis, keys, now.UnixMilli(),
		now.Add(updateTransferLifetime).UnixMilli(), lease.ID, string(data), updateTransferLifetime.Milliseconds()).Int()
	return err == nil && result == 1
}

func (c *updatePeerCluster) release(ctx context.Context, id, endpoint string) *sharedUpdateLease {
	if len(id) > 80 {
		return nil
	}
	var lease sharedUpdateLease
	data, err := c.redis.Get(ctx, updateLeaseKey(id)).Bytes()
	if err != nil || json.Unmarshal(data, &lease) != nil {
		return nil
	}
	keys := []string{updateLeaseKey(id), updateSeedSlots(lease.Seed),
		updateReceiverSlots(lease.Receiver.Endpoint), updateRedisPrefix + "transfers"}
	value, err := releaseUpdate.Run(ctx, c.redis, keys, endpoint).Text()
	if err != nil || value == "" {
		return nil
	}
	return &lease
}

func (c *updatePeerCluster) publishSeed(ctx context.Context, seed sharedUpdateSeed) error {
	if len(seed.Artifacts) == 0 {
		pipe := c.redis.TxPipeline()
		pipe.Del(ctx, updateRedisPrefix+"seed:"+seed.Endpoint)
		pipe.ZRem(ctx, updateRedisPrefix+"seeds", seed.Endpoint)
		_, err := pipe.Exec(ctx)
		return err
	}
	now := time.Now()
	registered, err := registerUpdateSeed.Run(ctx, c.redis, []string{updateRedisPrefix + "seeds"},
		now.UnixMilli(), now.Add(updateSeedLifetime).UnixMilli(), seed.Endpoint, updateSeedLifetime.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if registered != 1 {
		return fmt.Errorf("update seed capacity reached")
	}
	data, err := json.Marshal(seed)
	if err != nil {
		return err
	}
	pipe := c.redis.TxPipeline()
	pipe.Set(ctx, updateRedisPrefix+"seed:"+seed.Endpoint, data, updateSeedLifetime)
	for _, artifact := range seed.Artifacts {
		for _, key := range []string{updateRedisPrefix + "artifact:" + artifact,
			updateRedisPrefix + "own:" + updateIdentity(seed.Owner, "") + ":" + artifact} {
			pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprint(time.Now().UnixMilli()))
			pipe.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Add(updateSeedLifetime).UnixMilli()), Member: seed.Endpoint})
			pipe.Expire(ctx, key, updateSeedLifetime)
		}
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (c *updatePeerCluster) candidates(ctx context.Context, artifact, owner string) ([]sharedUpdateSeed, error) {
	var endpoints []string
	for _, key := range []string{updateRedisPrefix + "own:" + updateIdentity(owner, "") + ":" + artifact,
		updateRedisPrefix + "artifact:" + artifact} {
		values, err := c.redis.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: fmt.Sprint(time.Now().UnixMilli()),
			Max: "+inf", Count: 128}).Result()
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, values...)
	}
	seen := map[string]bool{}
	var keys []string
	for _, endpoint := range endpoints {
		if !seen[endpoint] {
			keys = append(keys, updateRedisPrefix+"seed:"+endpoint)
			seen[endpoint] = true
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	values, err := c.redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	var seeds []sharedUpdateSeed
	for _, value := range values {
		var seed sharedUpdateSeed
		if text, ok := value.(string); ok && json.Unmarshal([]byte(text), &seed) == nil {
			seeds = append(seeds, seed)
		}
	}
	return seeds, nil
}
