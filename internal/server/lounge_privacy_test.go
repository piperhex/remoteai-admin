package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func TestLoungeFailuresNeverWriteSharedRequestLogs(t *testing.T) {
	var output bytes.Buffer
	previousWriter, previousLogger := gin.DefaultErrorWriter, slog.Default()
	gin.DefaultErrorWriter = &output
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { gin.DefaultErrorWriter = previousWriter; slog.SetDefault(previousLogger) })
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/lounge/ticket", "/lounge/socket", "/admin/api/lounge/users/example",
		"/admin/api/lounge/presence", "/ordinary"} {
		for _, panicFailure := range []bool{false, true} {
			output.Reset()
			router := gin.New()
			router.Use(recovery())
			router.POST(path, func(c *gin.Context) {
				if panicFailure {
					panic("private-lounge-marker")
				}
				platform.Respond(c, nil, errors.New("private-lounge-marker"))
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", path, nil))
			if response.Code != 500 || strings.Contains(response.Body.String(), "private-lounge-marker") {
				t.Fatalf("unsafe error response for %s: %d", path, response.Code)
			}
			if path == "/ordinary" && output.Len() == 0 {
				t.Fatal("unrelated request diagnostics were disabled")
			}
			if path != "/ordinary" && output.Len() != 0 {
				t.Fatalf("lounge request was logged: %s", path)
			}
		}
	}
}
