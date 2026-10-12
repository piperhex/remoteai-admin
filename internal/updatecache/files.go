package updatecache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func (s *cache) verifyFile(entry record) error {
	path := filepath.Join(s.root, entry.Version, entry.Asset.Name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Asset.Size {
		return fmt.Errorf("invalid cached file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != entry.Asset.Digest {
		return fmt.Errorf("cached file digest mismatch")
	}
	return nil
}

func (s *cache) fetch(ctx context.Context, entry record) error {
	s.mu.RLock()
	existing, ok := s.records[key(entry.Version, entry.Asset.Name)]
	s.mu.RUnlock()
	if ok && existing.Asset == entry.Asset {
		return nil
	}
	if s.verifyFile(entry) == nil {
		return s.publish(entry)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	return s.storeWith(entry, func(writer io.Writer) (int64, error) { return s.download(ctx, entry.Asset, writer) })
}

func (s *cache) store(entry record, reader io.Reader) error {
	return s.storeWith(entry, func(writer io.Writer) (int64, error) {
		return io.Copy(writer, io.LimitReader(reader, entry.Asset.Size+1))
	})
}

func (s *cache) storeWith(entry record, write func(io.Writer) (int64, error)) error {
	file, err := os.CreateTemp(s.root, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	hash := sha256.New()
	size, err := write(io.MultiWriter(file, hash))
	if err != nil || size != entry.Asset.Size || fmt.Sprintf("sha256:%x", hash.Sum(nil)) != entry.Asset.Digest {
		file.Close()
		return fmt.Errorf("release file validation failed")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	directory := filepath.Join(s.root, entry.Version)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(directory, entry.Asset.Name)); err != nil {
		return err
	}
	return s.publish(entry)
}

func (s *cache) download(ctx context.Context, value asset, writer io.Writer) (int64, error) {
	const rangeBytes = 8 * 1024 * 1024
	var offset int64
	for offset < value.Size {
		end := min(value.Size, offset+rangeBytes)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, value.URL, nil)
		if err != nil {
			return offset, err
		}
		request.Header.Set("User-Agent", "RemoteAI-UpdateCache")
		request.Header.Set("Accept-Encoding", "identity")
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end-1))
		response, err := s.client.Do(request)
		if err != nil {
			return offset, err
		}
		length, err := copyRange(response, writer, [3]int64{offset, end, value.Size})
		response.Body.Close()
		offset += length
		if err != nil {
			return offset, err
		}
	}
	return offset, nil
}

func copyRange(response *http.Response, writer io.Writer, bounds [3]int64) (int64, error) {
	start, end, size := bounds[0], bounds[1], bounds[2]
	if response.StatusCode == http.StatusOK && start == 0 {
		end = size
	} else if response.StatusCode != http.StatusPartialContent ||
		response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end-1, size) {
		return 0, fmt.Errorf("invalid release range response")
	}
	if text := response.Header.Get("Content-Length"); text != "" && text != strconv.FormatInt(end-start, 10) {
		return 0, fmt.Errorf("invalid release range size")
	}
	count, err := io.Copy(writer, io.LimitReader(response.Body, end-start+1))
	if err != nil || count != end-start {
		return count, fmt.Errorf("incomplete release range")
	}
	return count, nil
}

func (s *cache) publish(entry record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key(entry.Version, entry.Asset.Name)] = entry
	return s.saveIndex()
}
