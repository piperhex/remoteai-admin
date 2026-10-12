package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const modelCatalogSettingsKey = "official-models:settings:v1"
const defaultModelRefreshMinutes = 30
const maxModelRefreshMinutes = 7 * 24 * 60
const maxModelRefreshSettingsBytes = 4 * 1024

type modelRefreshSettings struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"intervalMinutes"`
	Revision        string `json:"revision"`
}

func (s *service) readModelRefreshSettings(ctx context.Context) (*modelRefreshSettings, error) {
	settings := &modelRefreshSettings{Enabled: true, IntervalMinutes: defaultModelRefreshMinutes}
	if s.deps.Redis == nil {
		return nil, platform.NewError(503, "自动更新设置暂时不可用，请稍后重试。")
	}
	raw, err := s.deps.Redis.Get(ctx, modelCatalogSettingsKey).Bytes()
	if err == redis.Nil {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, settings); err != nil {
		return nil, err
	}
	return settings, validateModelRefreshSettings(settings)
}

func validateModelRefreshSettings(settings *modelRefreshSettings) error {
	if settings.IntervalMinutes < 1 || settings.IntervalMinutes > maxModelRefreshMinutes {
		return platform.NewError(400, "更新间隔请输入 1 至 10080 的整数，单位为分钟。")
	}
	return nil
}

var saveModelRefreshSettings = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
local revision = ''
if current then revision = cjson.decode(current).revision end
if revision ~= ARGV[1] then return 0 end
local settings = cjson.decode(ARGV[2])
redis.call('SET', KEYS[1], ARGV[2])
if settings.enabled then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
else
  redis.call('DEL', KEYS[2])
end
return 1
`)

// Settings and the next refresh are changed atomically for every Go instance.
func (s *service) saveModelRefreshSettings(ctx context.Context,
	input modelRefreshSettings) (*modelRefreshSettings, error) {
	if err := validateModelRefreshSettings(&input); err != nil {
		return nil, err
	}
	if s.deps.Redis == nil {
		return nil, platform.NewError(503, "自动更新设置暂时不可用，请稍后重试。")
	}
	previous := input.Revision
	input.Revision = newID()
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	saved, err := saveModelRefreshSettings.Run(ctx, s.deps.Redis,
		[]string{modelCatalogSettingsKey, modelCatalogAttemptKey}, previous, raw,
		(time.Duration(input.IntervalMinutes) * time.Minute).Milliseconds()).Int()
	if err != nil {
		return nil, err
	}
	if saved == 0 {
		return nil, platform.NewError(409, "自动更新设置已更改，请重新加载后再保存。")
	}
	return &input, nil
}

func (s *service) editModelRefreshSettings(c *gin.Context) (interface{}, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxModelRefreshSettingsBytes)
	var input struct {
		Enabled         *bool   `json:"enabled"`
		IntervalMinutes *int    `json:"intervalMinutes"`
		Revision        *string `json:"revision"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Enabled == nil || input.IntervalMinutes == nil || input.Revision == nil {
		return nil, platform.NewError(400, "自动更新设置无效，请检查后重试。")
	}
	settings, err := s.saveModelRefreshSettings(c.Request.Context(), modelRefreshSettings{
		Enabled: *input.Enabled, IntervalMinutes: *input.IntervalMinutes, Revision: *input.Revision,
	})
	if err != nil {
		return nil, err
	}
	return settings, s.record(platform.User(c), auditOptions{
		Action: "official-models.settings", TargetID: settings.Revision,
		Metadata: object{"enabled": settings.Enabled, "intervalMinutes": settings.IntervalMinutes},
	})
}
