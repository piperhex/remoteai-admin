package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/filesharing"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	sharingAdmin = "/admin/api/file-sharing"
	sharingChunk = 1 << 20
)

func fileSharingHandler(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	router := transportRouter()
	deps := &platform.Dependencies{DB: db, Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		user := &platform.Principal{ID: "5ec50f71-84b1-4ed6-a42b-3367851b1280", Email: "sharing@example.test"}
		switch c.GetHeader("Authorization") {
		case "Bearer manage":
			user.Permissions = append(user.Permissions, "admin.file-sharing.manage")
			fallthrough
		case "Bearer read":
			user.Permissions = append(user.Permissions, "admin.file-sharing.read")
		case "Bearer no-permissions":
		default:
			return nil, nil
		}
		return user, nil
	}}
	if err := filesharing.Register(router, deps); err != nil {
		t.Fatal(err)
	}
	return Handler(router)
}

func TestKongFileSharingManagementRequiresPermissions(t *testing.T) {
	handler := fileSharingHandler(t, nil)
	idPath := sharingAdmin + "/" + uuid.NewString()
	for _, endpoint := range []struct{ method, path, permission string }{
		{"GET", sharingAdmin, "no-permissions"}, {"GET", idPath, "no-permissions"},
		{"POST", sharingAdmin, "read"}, {"PUT", idPath + "/chunks/0", "read"},
		{"PATCH", idPath, "read"}, {"POST", sharingAdmin + "/batch/delete", "read"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			sharingRequest(t, handler, httptest.NewRequest(endpoint.method, endpoint.path, nil), 401)
			request := httptest.NewRequest(endpoint.method, endpoint.path, nil)
			request.Header.Set("Authorization", "Bearer "+endpoint.permission)
			sharingRequest(t, handler, request, 403)
		})
	}
}

func TestFileSharingPostgresHTTP(t *testing.T) {
	handler := fileSharingHandler(t, fileSharingDatabase(t))
	first := bytes.Repeat([]byte{'x'}, sharingChunk)
	last := []byte("final part")
	id := createTestFile(t, handler, first, last)
	admin, download := sharingAdmin+"/"+id, "/shared-files/"+id

	putChunk(t, handler, admin, 1, last, 409)
	putChunk(t, handler, admin, 0, first, 200)
	putChunk(t, handler, admin, 0, first, 200)
	putChunk(t, handler, admin, 0, bytes.Repeat([]byte{'y'}, sharingChunk), 409)
	putChunk(t, handler, admin, 0, append(bytes.Clone(first), 'x'), 413)
	sharingRequest(t, handler, httptest.NewRequest("GET", download, nil), 404)
	response := sharingJSON(t, handler, "GET", admin, "", 200)
	if !strings.Contains(response.Body.String(), `"uploadedBytes":1048576`) {
		t.Fatalf("stored progress: %s", response.Body)
	}
	putChunk(t, handler, admin, 1, []byte("wrong part"), 409)
	sharingRequest(t, handler, httptest.NewRequest("HEAD", download, nil), 404)
	putChunk(t, handler, admin, 1, last, 200)
	putChunk(t, handler, admin, 1, last, 200)

	t.Run("browser download", func(t *testing.T) {
		testSharedDownload(t, handler, id, first, last)
	})
	emptyID := createTestFile(t, handler, nil, nil)
	empty := sharingRequest(t, handler, httptest.NewRequest("GET", "/shared-files/"+emptyID, nil), 200)
	if empty.Body.Len() != 0 || empty.Header().Get("Content-Length") != "0" {
		t.Fatalf("empty attachment: %v %q", empty.Header(), empty.Body)
	}
	pending := createTestFile(t, handler, first, last)
	putChunk(t, handler, sharingAdmin+"/"+pending, 0, first, 200)
	remove := fmt.Sprintf(`{"ids":[%q,%q,%q]}`, id, emptyID, pending)
	for _, deleted := range []int{3, 0} {
		result := sharingJSON(t, handler, "POST", sharingAdmin+"/batch/delete", remove, 201)
		if !strings.Contains(result.Body.String(), fmt.Sprintf(`"deleted":%d`, deleted)) {
			t.Fatalf("batch delete: %s", result.Body)
		}
	}
	sharingRequest(t, handler, httptest.NewRequest("GET", download, nil), 404)
	putChunk(t, handler, sharingAdmin+"/"+pending, 1, last, 404)
}

func testSharedDownload(t *testing.T, handler http.Handler, id string, first, last []byte) {
	t.Helper()
	path := "/shared-files/" + id
	body := append(bytes.Clone(first), last...)
	head := sharingRequest(t, handler, httptest.NewRequest("HEAD", path, nil), 200)
	tag := head.Header().Get("ETag")
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(body)) ||
		head.Header().Get("Accept-Ranges") != "bytes" || !strings.HasPrefix(tag, `"`) {
		t.Fatalf("HEAD metadata: %v", head.Header())
	}
	sharingJSON(t, handler, "PATCH", sharingAdmin+"/"+id, `{"name":"../wrong"}`, 400)
	sharingJSON(t, handler, "PATCH", sharingAdmin+"/"+id, `{"name":"共享资料.bin"}`, 200)

	partial := httptest.NewRequest("GET", path, nil)
	partial.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(first)-2))
	partial.Header.Set("If-Range", tag)
	partial.Header.Set("Accept-Encoding", "gzip")
	response := sharingRequest(t, handler, partial, 206)
	disposition := response.Header().Get("Content-Disposition")
	kind, params, err := mime.ParseMediaType(disposition)
	rangeHeader := fmt.Sprintf("bytes %d-%d/%d", len(first)-2, len(body)-1, len(body))
	if err != nil || kind != "attachment" || params["filename"] != "共享资料.bin" ||
		!strings.Contains(disposition, "filename=download.bin; filename*=") ||
		response.Header().Get("ETag") != tag || response.Header().Get("Content-Range") != rangeHeader ||
		response.Header().Get("Content-Encoding") != "" || !bytes.Equal(response.Body.Bytes(), body[len(first)-2:]) {
		t.Fatalf("resumed download: headers %v, body %q", response.Header(), response.Body)
	}
	partial.Header.Set("If-Range", `"outdated"`)
	full := sharingRequest(t, handler, partial, 200)
	if !bytes.Equal(full.Body.Bytes(), body) || full.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("full attachment: headers %v, bytes %d", full.Header(), full.Body.Len())
	}
	partial.Header.Del("If-Range")
	partial.Header.Set("Range", "bytes=5000000-")
	invalid := sharingRequest(t, handler, partial, 416)
	if invalid.Header().Get("Content-Range") != fmt.Sprintf("bytes */%d", len(body)) {
		t.Fatalf("invalid byte range: %v", invalid.Header())
	}
}

func fileSharingDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	migration, err := os.ReadFile("../../sql/20261127-file-sharing.sql")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := os.ReadFile("../migrations/035_file_sharing.sql")
	if err != nil || !bytes.Equal(migration, initial) {
		t.Fatal("fresh and upgrade file-sharing schemas differ")
	}
	dsn := os.Getenv("FILE_SHARING_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("FILE_SHARING_TEST_POSTGRES is not set")
	}
	address, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(address.Path, "_test") {
		t.Fatal("use a PostgreSQL fixture database ending in _test")
	}
	root := openSharingDatabase(t, address.String())
	schema := "file_sharing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	query := address.Query()
	query.Set("search_path", schema)
	address.RawQuery = query.Encode()
	db := openSharingDatabase(t, address.String())
	for range 2 {
		if err := db.Exec(strings.ReplaceAll(string(migration), "public.", "")).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE admin_audit_logs (id uuid, "actorId" uuid, "actorEmail" text,
		action text, "targetType" text, "targetId" text, "createdAt" timestamptz)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func openSharingDatabase(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return db
}

func createTestFile(t *testing.T, handler http.Handler, first, last []byte) string {
	t.Helper()
	var manifest []byte
	for _, data := range [][]byte{first, last} {
		if len(data) > 0 {
			digest := sha256.Sum256(data)
			manifest = append(manifest, digest[:]...)
		}
	}
	init := fmt.Sprintf(`{"name":"file.bin","size":%d,"fingerprint":"%x"}`, len(first)+len(last), sha256.Sum256(manifest))
	response := sharingJSON(t, handler, "POST", sharingAdmin, init, 201)
	var item struct{ ID string }
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || item.ID == "" {
		t.Fatalf("missing file ID: %s (%v)", response.Body, err)
	}
	return item.ID
}

func putChunk(t *testing.T, handler http.Handler, admin string, index int, data []byte, expected int) {
	t.Helper()
	request := httptest.NewRequest("PUT", fmt.Sprintf("%s/chunks/%d", admin, index), bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Authorization", "Bearer manage")
	sharingRequest(t, handler, request, expected)
}

func sharingJSON(t *testing.T, handler http.Handler, method, path, body string, expected int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer manage")
	return sharingRequest(t, handler, request, expected)
}

func sharingRequest(t *testing.T, handler http.Handler, request *http.Request, expected int) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != expected {
		t.Fatalf("%s %s: want %d, got %d %s", request.Method, request.URL, expected, response.Code, response.Body)
	}
	return response
}
