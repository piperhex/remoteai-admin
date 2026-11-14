// Package browserextensions manages the curated built-in browser extension market.
package browserextensions

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"strings"
	"unicode"
)

const (
	MaxArchiveBytes  = 64 << 20
	maxExpandedBytes = 256 << 20
	maxFiles         = 20_000
	maxMetadataBytes = 1 << 20
)

var errInvalidArchive = errors.New("invalid extension archive")

type Upload struct {
	Item Item
	Data []byte
}

// ReadArchive validates every entry, including CRCs, without extracting files on the server.
// CRX signatures are not a trust boundary: publishing is an explicit administrator decision.
func ReadArchive(data []byte) (Upload, error) {
	if len(data) == 0 || len(data) > MaxArchiveBytes {
		return Upload{}, errInvalidArchive
	}
	archive, err := zipData(data)
	if err != nil {
		return Upload{}, err
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) > maxFiles {
		return Upload{}, errInvalidArchive
	}
	files, err := validateEntries(reader.File)
	if err != nil {
		return Upload{}, err
	}
	root, err := manifestRoot(files)
	if err != nil {
		return Upload{}, err
	}
	item, err := readManifest(files, root)
	if err != nil {
		return Upload{}, err
	}
	digest := sha256.Sum256(archive)
	item.SHA256, item.Size = hex.EncodeToString(digest[:]), int64(len(archive))
	return Upload{Item: item, Data: archive}, nil
}

func zipData(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte("Cr24")) {
		return data, nil
	}
	if len(data) < 12 {
		return nil, errInvalidArchive
	}
	var offset uint64
	switch binary.LittleEndian.Uint32(data[4:8]) {
	case 2:
		if len(data) < 16 {
			return nil, errInvalidArchive
		}
		offset = 16 + uint64(binary.LittleEndian.Uint32(data[8:12])) + uint64(binary.LittleEndian.Uint32(data[12:16]))
	case 3:
		offset = 12 + uint64(binary.LittleEndian.Uint32(data[8:12]))
	default:
		return nil, errInvalidArchive
	}
	if offset >= uint64(len(data)) {
		return nil, errInvalidArchive
	}
	return data[offset:], nil
}

func safePath(name string) bool {
	if name == "" || len(name) > 512 || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	if strings.ContainsAny(name, "\\:*?\"<>|") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
			return false
		}
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
			base[3] >= '1' && base[3] <= '9' {
			return false
		}
	}
	return true
}

func validateEntries(entries []*zip.File) (map[string]*zip.File, error) {
	files, seen := map[string]*zip.File{}, map[string]bool{}
	regular := map[string]bool{}
	var total uint64
	for _, file := range entries {
		name := strings.TrimSuffix(file.Name, "/")
		key := strings.ToLower(name)
		if !safePath(name) || seen[key] || (!file.Mode().IsRegular() && !file.Mode().IsDir()) {
			return nil, errInvalidArchive
		}
		seen[key] = true
		if file.FileInfo().IsDir() {
			continue
		}
		if file.UncompressedSize64 > maxExpandedBytes-total {
			return nil, errInvalidArchive
		}
		total += file.UncompressedSize64
		stream, err := file.Open()
		if err != nil {
			return nil, errInvalidArchive
		}
		n, readErr := io.Copy(io.Discard, io.LimitReader(stream, int64(file.UncompressedSize64)+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || uint64(n) != file.UncompressedSize64 {
			return nil, errInvalidArchive
		}
		files[file.Name] = file
		regular[key] = true
	}
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if regular[strings.ToLower(parent)] {
				return nil, errInvalidArchive
			}
		}
	}
	return files, nil
}

func manifestRoot(files map[string]*zip.File) (string, error) {
	if files["manifest.json"] != nil {
		return "", nil
	}
	root := ""
	for name := range files {
		prefix, _, ok := strings.Cut(name, "/")
		if !ok || (root != "" && root != prefix+"/") {
			return "", errInvalidArchive
		}
		root = prefix + "/"
	}
	if files[root+"manifest.json"] == nil {
		return "", errInvalidArchive
	}
	return root, nil
}

func readFile(file *zip.File, limit int64) ([]byte, error) {
	if file == nil || file.UncompressedSize64 > uint64(limit) {
		return nil, errInvalidArchive
	}
	stream, err := file.Open()
	if err != nil {
		return nil, errInvalidArchive
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errInvalidArchive
	}
	return data, nil
}
