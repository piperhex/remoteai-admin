package devices

import (
	"context"
	"encoding/json"

	"github.com/codex-switch/admin-go/internal/platform"
)

// Existing grants are kept alive; refresh may only fill vacant slots for the same package.
func (c *updatePeerCluster) refillCapacity(ctx context.Context, key, artifact string, message platform.JSON) int {
	ids, err := c.redis.ZRange(ctx, key, 0, -1).Result()
	if err != nil || len(ids) >= maxUpdateDownloadPeers {
		return 0
	}
	refill, _ := message["refill"].(bool)
	protocol, _ := message["packageProtocol"].(float64)
	for _, id := range ids {
		if !refill || protocol < 2 {
			return 0
		}
		var lease sharedUpdateLease
		data, err := c.redis.Get(ctx, updateLeaseKey(id)).Bytes()
		if err != nil || json.Unmarshal(data, &lease) != nil || lease.Protocol < 2 || lease.Artifact != artifact {
			return 0
		}
	}
	return maxUpdateDownloadPeers - len(ids)
}
