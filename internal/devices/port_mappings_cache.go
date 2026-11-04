package devices

import (
	"context"
	"time"

	"github.com/piperhex/remoteai-admin/internal/platform"
	"github.com/piperhex/remoteai-admin/internal/readcache"
)

func (s *Service) cachedPortMappings(ctx context.Context, owner, device string) (mappingDocument, error) {
	ctx, cancel := platform.DatabaseContext(ctx)
	defer cancel()
	result, err := readcache.Load(ctx, readcache.Options{Client: s.deps.Redis,
		Key: platform.PortMappingsCacheKey(owner, device), Lifetime: 5 * time.Second},
		func(ctx context.Context) (mappingDocument, error) {
			return readPortMappings(s.deps.DB.WithContext(ctx), owner, device)
		})
	// Internal identity fields are excluded from JSON; GET never acknowledges a write.
	result.OwnerID, result.DeviceID, result.Accepted = owner, device, false
	return result, err
}
