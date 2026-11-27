package server

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFileSharingPostgresUploadWindow(t *testing.T) {
	handler := fileSharingHandler(t, fileSharingDatabase(t))
	first := bytes.Repeat([]byte{'a'}, sharingChunk)
	middle := bytes.Repeat([]byte{'b'}, sharingChunk)
	last := []byte("end")
	id := createTestFile(t, handler, first, middle, last)
	admin, download := sharingAdmin+"/"+id, "/shared-files/"+id

	waiting := windowChunk(admin, 2, last)
	sharingRequest(t, handler, waiting, 204)
	sharingRequest(t, handler, windowChunk(admin, 2, last), 204)
	sharingRequest(t, handler, windowChunk(admin, 2, []byte("bad")), 409)
	wrong := windowChunk(admin, 1, middle)
	wrong.Header.Set("X-Upload-SHA256", fmt.Sprintf("%x", sha256.Sum256(first)))
	sharingRequest(t, handler, wrong, 409)

	status := sharingJSON(t, handler, "GET", admin, "", 200).Body.String()
	for _, field := range []string{`"receivedChunks":[2]`, `"receivedBytes":3`, `"uploadedBytes":0`} {
		if !strings.Contains(status, field) {
			t.Fatalf("resumable offset and received chunks: %s", status)
		}
	}
	library := sharingJSON(t, handler, "GET", sharingAdmin, "", 200).Body.String()
	if !strings.Contains(library, `"uploadWindow":6`) || !strings.Contains(library, `"receivedBytes":3`) {
		t.Fatalf("file list omitted the window size or saved progress: %s", library)
	}
	sharingJSON(t, handler, "POST", admin+"/complete", "", 409)
	putWindowChunks(t, handler, admin, [][]byte{first, middle})
	sharingRequest(t, handler, httptest.NewRequest("HEAD", download, nil), 404)
	for range 2 {
		body := sharingJSON(t, handler, "POST", admin+"/complete", "", 201).Body.String()
		if !strings.Contains(body, `"uploadedBytes":2097155`) {
			t.Fatalf("completion returned an unfinished upload: %s", body)
		}
	}
	attachment := sharingRequest(t, handler, httptest.NewRequest("GET", download, nil), 200)
	if !bytes.Equal(attachment.Body.Bytes(), bytes.Join([][]byte{first, middle, last}, nil)) {
		t.Fatal("download differs from the confirmed chunks")
	}
}

func TestFileSharingWindowResumesLegacyUpload(t *testing.T) {
	handler := fileSharingHandler(t, fileSharingDatabase(t))
	first := bytes.Repeat([]byte{'x'}, sharingChunk)
	last := []byte("end")
	id := createTestFile(t, handler, first, last)
	admin := sharingAdmin + "/" + id
	putChunk(t, handler, admin, 0, first, 200)
	state := sharingJSON(t, handler, "GET", admin, "", 200).Body.String()
	if !strings.Contains(state, `"receivedChunks":[0]`) || !strings.Contains(state, `"receivedBytes":1048576`) {
		t.Fatalf("initial legacy prefix is unavailable to window uploads: %s", state)
	}
	sharingRequest(t, handler, windowChunk(admin, 1, last), 204)
	putChunk(t, handler, admin, 1, last, 200)
	sharingJSON(t, handler, "POST", admin+"/complete", "", 201)
	sharingRequest(t, handler, windowChunk(admin, 1, last), 204)
}

func putWindowChunks(t *testing.T, handler http.Handler, path string, parts [][]byte) {
	t.Helper()
	ready := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, len(parts))
	for index, data := range parts {
		go func() {
			<-ready
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, windowChunk(path, index, data))
			responses <- response
		}()
	}
	close(ready)
	for range parts {
		response := <-responses
		if response.Code != 204 {
			t.Errorf("parallel chunk returned %d: %s", response.Code, response.Body)
		}
	}
}

func windowChunk(path string, index int, data []byte) *http.Request {
	request := httptest.NewRequest("PUT", fmt.Sprintf("%s/chunks/%d", path, index), bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Authorization", "Bearer manage")
	request.Header.Set("X-Upload-SHA256", fmt.Sprintf("%x", sha256.Sum256(data)))
	return request
}
