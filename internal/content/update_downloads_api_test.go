package content

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/updatesharing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestDownloadReportAPIUsesAuthenticatedOwnerAndRejectsPrivateDetails(t *testing.T) {
	db := sharingDatabase(t)
	if err := db.AutoMigrate(&updatesharing.DownloadReport{}); err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	router := gin.New()
	s := &service{deps: &platform.Dependencies{DB: db, Authenticate: func(*gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: owner}, nil
	}}}
	s.updateDownloadRoutes(router)
	report := updatesharing.Report{ID: uuid.NewString(), Artifact: strings.Repeat("b", 64),
		Platform: "android", Outcome: "failed", ServerBytes: 20,
		Failures: []updatesharing.Failure{{Source: "download", Stage: "verify", Code: "invalid", Count: 1}}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/update-sharing/reports", bytes.NewReader(data)))
		if response.Code != 201 {
			t.Fatal(response.Code, response.Body)
		}
	}
	var rows []updatesharing.DownloadReport
	if err := db.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].UserID != owner {
		t.Fatal(rows, err)
	}
	for _, extra := range []string{`,"userId":"someone-else"}`, `,"error":"secret file path"}`} {
		response := httptest.NewRecorder()
		body := strings.TrimSuffix(string(data), "}") + extra
		router.ServeHTTP(response, httptest.NewRequest("POST", "/update-sharing/reports", strings.NewReader(body)))
		if response.Code != 400 {
			t.Fatal(response.Code, response.Body)
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/admin/api/update-sharing/daily", nil))
	if response.Code != 403 {
		t.Fatal(response.Code)
	}
}
