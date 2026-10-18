// Package updatecache retains verified public release files for HTTP range distribution.
package updatecache

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	releaseAPI             = "https://api.github.com/repos/piperhex/remoteai/releases?per_page=100"
	releasePrefix          = "https://github.com/piperhex/remoteai/releases/download/"
	retainedVersions       = 3
	maxAssetBytes    int64 = 512 * 1024 * 1024
	maxCatalogBytes        = 4 * 1024 * 1024
)

var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,199}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type asset struct {
	ID     int64  `json:"id,omitempty"`
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}
type record struct {
	Version string `json:"version"`
	Asset   asset  `json:"asset"`
}

func versionParts(tag string) []uint64 {
	match := versionPattern.FindStringSubmatch(tag)
	if match == nil {
		return nil
	}
	parts := make([]uint64, 3)
	for i := range parts {
		value, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return nil
		}
		parts[i] = value
	}
	return parts
}

func supportedAsset(tag string, value asset) bool {
	if !namePattern.MatchString(value.Name) || !digestPattern.MatchString(value.Digest) ||
		value.Size <= 0 || value.Size > maxAssetBytes {
		return false
	}
	if value.URL != releasePrefix+tag+"/"+url.PathEscape(value.Name) {
		return false
	}
	for _, extension := range []string{".apk", ".exe", ".msi", ".zip", ".tar.gz", ".AppImage", ".deb", ".dmg"} {
		if strings.HasSuffix(value.Name, extension) {
			return true
		}
	}
	return false
}

func selectReleases(values []release) ([]release, error) {
	selected := make([]release, 0, retainedVersions)
	seen := map[string]bool{}
	for _, value := range values {
		parts := versionParts(value.Tag)
		if value.Draft || value.Prerelease || parts == nil {
			continue
		}
		identity := fmt.Sprint(parts)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		value.Assets = slices.DeleteFunc(value.Assets, func(a asset) bool { return !supportedAsset(value.Tag, a) })
		selected = append(selected, value)
	}
	slices.SortFunc(selected, func(a, b release) int {
		left, right := versionParts(a.Tag), versionParts(b.Tag)
		for i := range left {
			if order := cmp.Compare(right[i], left[i]); order != 0 {
				return order
			}
		}
		return 0
	})
	if len(selected) == 0 {
		return nil, fmt.Errorf("no stable official releases")
	}
	return selected[:min(retainedVersions, len(selected))], nil
}

func (s *cache) catalog(ctx context.Context) ([]release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.catalogURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "RemoteAI-UpdateCache")
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release metadata unavailable: %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	if err != nil || len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("invalid release metadata")
	}
	var values []release
	if json.Unmarshal(data, &values) != nil {
		return nil, fmt.Errorf("invalid release metadata")
	}
	return selectReleases(values)
}
