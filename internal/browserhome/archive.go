// Package browserhome hosts the static home page used by the desktop navigation browser.
package browserhome

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"path"
	"strings"
	"unicode"
)

const (
	MaxArchiveBytes  = 20 << 20
	maxExpandedBytes = 100 << 20
	maxFileBytes     = 10 << 20
	maxFiles         = 2000
)

var errInvalidArchive = errors.New("invalid browser home archive")
var errMissingIndex = errors.New("browser home index missing")

type Asset struct {
	Revision    string `json:"-" gorm:"primaryKey"`
	Path        string `json:"path" gorm:"primaryKey"`
	ContentType string `json:"contentType"`
	Data        []byte `json:"data"`
}

func (Asset) TableName() string { return "browser_home_assets" }

// No uploaded paths are ever extracted to the server's filesystem.
func ReadArchive(data []byte) ([]Asset, error) {
	if len(data) == 0 || len(data) > MaxArchiveBytes {
		return nil, errInvalidArchive
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) > maxFiles {
		return nil, errInvalidArchive
	}
	assets := make([]Asset, 0, len(reader.File))
	seen := map[string]bool{}
	var expanded uint64
	for _, file := range reader.File {
		if !safePath(strings.TrimSuffix(file.Name, "/")) || (!file.Mode().IsRegular() && !file.Mode().IsDir()) {
			return nil, errInvalidArchive
		}
		if file.FileInfo().IsDir() {
			continue
		}
		key := strings.ToLower(file.Name)
		if seen[key] || file.UncompressedSize64 > maxFileBytes {
			return nil, errInvalidArchive
		}
		seen[key] = true
		expanded += file.UncompressedSize64
		if expanded > maxExpandedBytes {
			return nil, errInvalidArchive
		}
		asset, err := readAsset(file)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return stripArchiveRoot(assets)
}

func safePath(name string) bool {
	if name == "" || len(name) > 512 || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	return !strings.ContainsAny(name, "\\:%?#\"'<>`") && strings.IndexFunc(name, unicode.IsControl) < 0
}

func readAsset(file *zip.File) (Asset, error) {
	kind := assetMIME(path.Ext(file.Name))
	if kind == "" {
		return Asset{}, errInvalidArchive
	}
	stream, err := file.Open()
	if err != nil {
		return Asset{}, errInvalidArchive
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return Asset{}, errInvalidArchive
	}
	return Asset{Path: file.Name, ContentType: kind, Data: data}, nil
}

func stripArchiveRoot(assets []Asset) ([]Asset, error) {
	for _, asset := range assets {
		if asset.Path == "index.html" {
			return assets, nil
		}
	}
	if len(assets) == 0 {
		return nil, errMissingIndex
	}
	root, _, found := strings.Cut(assets[0].Path, "/")
	if !found {
		return nil, errMissingIndex
	}
	index := false
	for i := range assets {
		if !strings.HasPrefix(assets[i].Path, root+"/") {
			return nil, errMissingIndex
		}
		assets[i].Path = strings.TrimPrefix(assets[i].Path, root+"/")
		index = index || assets[i].Path == "index.html"
	}
	if !index {
		return nil, errMissingIndex
	}
	return assets, nil
}

func assetMIME(extension string) string {
	return map[string]string{
		".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
		".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
		".css": "text/css; charset=utf-8", ".json": "application/json", ".map": "application/json",
		".webmanifest": "application/manifest+json", ".txt": "text/plain; charset=utf-8",
		".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".webp": "image/webp", ".gif": "image/gif", ".ico": "image/x-icon", ".avif": "image/avif",
		".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
		".wasm": "application/wasm", ".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg",
	}[strings.ToLower(extension)]
}
