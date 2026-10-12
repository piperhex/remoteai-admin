package updatecache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const refreshInterval = 15 * time.Minute

type cache struct {
	root       string
	client     *http.Client
	catalogURL string
	mu         sync.RWMutex
	records    map[string]record
	versions   []string
	done       chan struct{}
}

func key(version, name string) string { return version + "/" + name }

func (s *cache) run(ctx context.Context) {
	defer close(s.done)
	if err := os.MkdirAll(s.root, 0700); err != nil {
		slog.Error("update cache directory unavailable")
		return
	}
	if err := s.cleanTemporary(); err != nil {
		slog.Warn("update cache temporary cleanup incomplete")
	}
	if err := s.restore(); err != nil {
		slog.Warn("update cache index will be rebuilt")
	}
	for ctx.Err() == nil {
		if err := s.refresh(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("update cache refresh incomplete")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(refreshInterval):
		}
	}
}

func (s *cache) refresh(ctx context.Context) error {
	metadataContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	versions, err := s.catalog(metadataContext)
	cancel()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, version := range versions {
		keep[version.Tag] = true
	}
	// A valid catalog is the authority for retention; request traffic can never resurrect old releases.
	if err := s.retain(keep); err != nil {
		return err
	}
	var incomplete bool
	for _, version := range versions {
		for _, value := range version.Assets {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := s.fetch(ctx, record{Version: version.Tag, Asset: value}); err != nil {
				incomplete = true
			}
		}
	}
	if incomplete {
		return fmt.Errorf("one or more release files unavailable")
	}
	return nil
}

func (s *cache) retain(keep map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, entry := range s.records {
		if !keep[entry.Version] {
			delete(s.records, name)
		}
	}
	s.versions = s.versions[:0]
	for version := range keep {
		s.versions = append(s.versions, version)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Only version directories owned by this cache are eligible; never follow arbitrary names/symlinks.
		if versionParts(entry.Name()) == nil || keep[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.root, entry.Name())); err != nil {
			return err
		}
	}
	return s.saveIndex()
}

func (s *cache) restore() error {
	path := filepath.Join(s.root, "index.json")
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxCatalogBytes {
		return fmt.Errorf("cache index unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var entries []record
	if json.Unmarshal(data, &entries) != nil {
		return fmt.Errorf("invalid cache index")
	}
	releases := []release{}
	for _, entry := range entries {
		releases = append(releases, release{Tag: entry.Version})
	}
	latest, err := selectReleases(releases)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, value := range latest {
		keep[value.Tag] = true
	}
	for _, entry := range entries {
		if !keep[entry.Version] || !supportedAsset(entry.Version, entry.Asset) {
			continue
		}
		if s.verifyFile(entry) == nil {
			s.mu.Lock()
			s.records[key(entry.Version, entry.Asset.Name)] = entry
			s.mu.Unlock()
		}
	}
	return s.retain(keep)
}

func (s *cache) cleanTemporary() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasPrefix(entry.Name(), ".download-") || strings.HasPrefix(entry.Name(), ".index-")) {
			continue
		}
		if err := os.Remove(filepath.Join(s.root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Called with the index mutex held. Payload publication precedes index publication.
func (s *cache) saveIndex() error {
	entries := make([]record, 0, len(s.records))
	for _, value := range s.records {
		entries = append(entries, value)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.root, ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(s.root, "index.json"))
}
