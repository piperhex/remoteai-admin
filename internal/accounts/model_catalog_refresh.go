package accounts

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/redis/go-redis/v9"
)

const modelCatalogRefreshTimeout = 2 * time.Minute
const modelCatalogPollInterval = time.Minute
const modelCatalogAccountLimit = 3
const defaultModelClientVersion = "0.152.0"
const officialModelsURL = "https://chatgpt.com/backend-api/codex/models"
const codexReleaseURL = "https://registry.npmjs.org/@openai/codex/latest"

var stableModelVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var claimModelRefresh = redis.NewScript(`
local settings = {enabled = true, intervalMinutes = tonumber(ARGV[4]), revision = ''}
local savedSettings = redis.call('GET', KEYS[3])
if savedSettings then settings = cjson.decode(savedSettings) end
if ARGV[1] == '0' and (not settings.enabled or settings.revision ~= ARGV[5]) then return 0 end
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
if ARGV[1] == '0' and redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
if settings.enabled then redis.call('SET', KEYS[2], '1', 'PX', settings.intervalMinutes * 60000) end
return 1
`)
var releaseModelRefresh = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

// StartModelCatalogRefresh follows the shared refresh settings. Stop it before closing Redis or SQL.
func StartModelCatalogRefresh(deps *platform.Dependencies) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s := &service{deps: deps}
	go func() {
		defer close(done)
		ticker := time.NewTicker(modelCatalogPollInterval)
		defer ticker.Stop()
		for {
			if _, err := s.refreshModelCatalog(ctx, false); err != nil && ctx.Err() == nil {
				slog.Warn("official model prefetch failed; retaining previous catalog", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (s *service) refreshModelCatalog(parent context.Context, force bool) (*modelCatalogSnapshot, error) {
	return s.refreshModelCatalogWith(parent, force, s.loadModelCatalog)
}

type modelCatalogLoader func(context.Context, string) (*modelCatalogSnapshot, error)

func (s *service) refreshModelCatalogWith(parent context.Context, force bool,
	load modelCatalogLoader) (*modelCatalogSnapshot, error) {
	if s.deps.Redis == nil {
		return nil, platform.NewError(503, "模型列表暂时不可用，请稍后重试。")
	}
	ctx, cancel := context.WithTimeout(parent, modelCatalogRefreshTimeout)
	defer cancel()
	settings, err := s.readModelRefreshSettings(ctx)
	if err != nil {
		return nil, err
	}
	token := newID()
	claimed, err := claimModelRefresh.Run(ctx, s.deps.Redis,
		[]string{modelCatalogLockKey, modelCatalogAttemptKey, modelCatalogSettingsKey}, force, token,
		(modelCatalogRefreshTimeout + time.Minute).Milliseconds(), defaultModelRefreshMinutes, settings.Revision).Int()
	if err != nil {
		return nil, err
	}
	if claimed == 0 {
		if force {
			return nil, platform.NewError(409, "模型列表正在刷新，请稍后重试。")
		}
		return nil, nil
	}
	defer s.releaseModelCatalogLock(token)
	previous, err := s.readModelCatalog(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := load(ctx, previous.ClientVersion)
	if err != nil {
		return nil, err
	}
	return snapshot, s.persistModelCatalog(ctx, snapshot, modelCatalogSaveOptions{
		PreviousRevision: previous.Revision, Automatic: !force, SettingsRevision: settings.Revision,
	})
}

func (s *service) loadModelCatalog(ctx context.Context, previousVersion string) (*modelCatalogSnapshot, error) {
	version := s.latestModelClientVersion(ctx, previousVersion)
	catalog, err := s.fetchModelCatalog(ctx, version)
	return &modelCatalogSnapshot{Catalog: catalog, ClientVersion: version, Source: "official"}, err
}

func (s *service) releaseModelCatalogLock(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := releaseModelRefresh.Run(ctx, s.deps.Redis, []string{modelCatalogLockKey}, token).Err(); err != nil {
		slog.Warn("could not release model refresh lock", "error", err)
	}
}

func (s *service) latestModelClientVersion(ctx context.Context, previous string) string {
	response, err := s.request(requestOptionsHTTP{URL: codexReleaseURL, Proxy: true, Context: ctx,
		MaxBytes: maxModelCatalogBytes})
	var release struct{ Version string }
	if err == nil && response.ok() && json.Unmarshal(response.Raw, &release) == nil &&
		stableModelVersion.MatchString(release.Version) {
		return release.Version
	}
	if stableModelVersion.MatchString(previous) {
		return previous
	}
	return defaultModelClientVersion
}

func (s *service) fetchModelCatalog(ctx context.Context, version string) (json.RawMessage, error) {
	var rows []systemAccount
	// Only the background/manual refresh reads credentials; catalog readers never query SQL.
	err := s.deps.DB.WithContext(ctx).Order(`"updatedAt" DESC`).Limit(modelCatalogAccountLimit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for index := range rows {
		response, err := s.codexRequest(&storedAccount{System: &rows[index]}, modelCatalogRequest(ctx, version))
		if err == nil && response.ok() && validateModelCatalog(response.Raw) == nil {
			return json.RawMessage(response.Raw), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, platform.NewError(502, "暂时无法获取官方模型列表，请检查官方账户后重试。现有列表已保留。")
}

func modelCatalogRequest(ctx context.Context, version string) requestOptionsHTTP {
	return requestOptionsHTTP{
		URL: officialModelsURL + "?client_version=" + url.QueryEscape(version), Context: ctx,
		MaxBytes: maxModelCatalogBytes, Headers: map[string]string{"User-Agent": "codex_cli_rs/" + version},
	}
}
