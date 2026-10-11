package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/redis/go-redis/v9"
)

const modelCatalogKey = "official-models:catalog:v1"
const modelCatalogAttemptKey = "official-models:next-refresh:v1"
const modelCatalogLockKey = "official-models:refresh-lock:v1"
const modelCatalogInterval = 30 * time.Minute
const maxModelCatalogBytes = 2 * 1024 * 1024

// The last valid catalog has no TTL: upstream outages must not erase working models.
type modelCatalogSnapshot struct {
	Catalog       json.RawMessage `json:"catalog"`
	Revision      string          `json:"revision"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	ClientVersion string          `json:"clientVersion"`
	Source        string          `json:"source"`
}

func validateModelCatalog(raw []byte) error {
	var value struct {
		Models []struct {
			Slug       string `json:"slug"`
			ID         string `json:"id"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if len(raw) > maxModelCatalogBytes || json.Unmarshal(raw, &value) != nil || len(value.Models) == 0 {
		return platform.NewError(400, "模型列表无效，请提供包含模型的完整列表。")
	}
	seen := make(map[string]bool)
	visible := false
	for _, model := range value.Models {
		name := strings.TrimSpace(first(model.Slug, model.ID))
		if name == "" || seen[name] {
			return platform.NewError(400, "请检查模型名称，名称不能为空或重复。")
		}
		seen[name] = true
		visible = visible || model.Visibility != "hide"
	}
	if !visible {
		return platform.NewError(400, "模型列表至少需要包含一个可用模型。")
	}
	return nil
}

func (s *service) readModelCatalog(ctx context.Context) (*modelCatalogSnapshot, error) {
	if s.deps.Redis == nil {
		return nil, platform.NewError(503, "模型列表暂时不可用，请稍后重试。")
	}
	raw, err := s.deps.Redis.Get(ctx, modelCatalogKey).Bytes()
	if err == redis.Nil {
		return &modelCatalogSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot modelCatalogSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, validateModelCatalog(snapshot.Catalog)
}

var saveModelCatalog = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
local revision = ''
if current then revision = cjson.decode(current).revision end
if revision ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
return 1
`)

// Compare revisions so a slow refresh cannot overwrite a manual edit made while it was running.
func (s *service) saveModelCatalog(ctx context.Context, snapshot *modelCatalogSnapshot, previous string) error {
	if err := validateModelCatalog(snapshot.Catalog); err != nil {
		return err
	}
	if s.deps.Redis == nil {
		return platform.NewError(503, "模型列表暂时不可用，请稍后重试。")
	}
	snapshot.Revision = newID()
	snapshot.UpdatedAt = now()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	saved, err := saveModelCatalog.Run(ctx, s.deps.Redis,
		[]string{modelCatalogKey, modelCatalogAttemptKey}, previous, raw, modelCatalogInterval.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if saved == 0 {
		return platform.NewError(409, "模型列表已更新，请重新加载后再操作。")
	}
	return nil
}

func modelCatalogETag(raw []byte) string {
	return fmt.Sprintf(`"%x"`, sha256.Sum256(raw))
}
