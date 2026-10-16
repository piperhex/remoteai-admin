package content

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/updatesharing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestDownloadReportValidationAndAuthentication(t *testing.T) {
	valid := updatesharing.Report{ID: uuid.NewString(), Artifact: strings.Repeat("a", 64),
		Platform: "android", Outcome: "success", PeerBytes: 20, ServerBytes: 30}
	for _, change := range []func(*updatesharing.Report){
		func(r *updatesharing.Report) { r.ID = "bad" },
		func(r *updatesharing.Report) { r.Artifact = "https://private.test" },
		func(r *updatesharing.Report) { r.ServerBytes = -1 },
		func(r *updatesharing.Report) { r.ServerBytes = updatesharing.MaxPackageBytes },
		func(r *updatesharing.Report) { r.Outcome = "failed" },
		func(r *updatesharing.Report) { r.Platform = "unknown" },
		func(r *updatesharing.Report) {
			r.Failures = []updatesharing.Failure{{Source: "peer", Stage: "connect",
				Code: "secret-token", Count: 1}}
		},
	} {
		r := valid
		change(&r)
		if r.Validate() == nil {
			t.Fatalf("accepted invalid report: %+v", r)
		}
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/update-sharing/reports", "/admin/api/update-sharing/daily",
		"/admin/api/update-sharing/failures"} {
		router := gin.New()
		s := &service{deps: &platform.Dependencies{}}
		s.updateDownloadRoutes(router)
		method := http.MethodGet
		if endpoint == "/update-sharing/reports" {
			method = http.MethodPost
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, endpoint, strings.NewReader(`{}`)))
		if response.Code != 401 {
			t.Fatal(endpoint, response.Code)
		}
	}
}

func TestDownloadReportsDailyDeduplicationAndFailures(t *testing.T) {
	db := sharingDatabase(t)
	schema, err := os.ReadFile("../migrations/016_update_download_reports.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(strings.ReplaceAll(string(schema), "public.", "")).Error; err != nil {
		t.Fatal(err)
	}
	owner, other := uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 17, 0, 30, 0, 0, updatesharing.StatisticsZone)
	report := updatesharing.Report{ID: uuid.NewString(), Artifact: strings.Repeat("a", 64),
		Platform: "windows", Outcome: "success", PeerBytes: 10, ServerBytes: 20}
	for range 2 {
		if err := updatesharing.Store(db, owner, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&updatesharing.DownloadReport{}).Where("id = ?", report.ID).
		Update("created_at", now).Error; err != nil {
		t.Fatal(err)
	}
	rows := []updatesharing.DownloadReport{
		{Report: report, UserID: other, CreatedAt: now},
		{Report: report, UserID: owner, CreatedAt: now.Add(-time.Hour)},
		{Report: report, UserID: owner, CreatedAt: now},
		{Report: report, UserID: owner, CreatedAt: now},
		{Report: report, UserID: owner, CreatedAt: now},
	}
	for i := range rows {
		rows[i].ID = uuid.NewString()
		rows[i].Failures = []updatesharing.Failure{}
	}
	rows[2].Outcome = "failed"
	rows[2].Failures = []updatesharing.Failure{{Source: "download", Stage: "verify", Code: "invalid", Count: 1}}
	rows[3].PeerBytes, rows[3].ServerBytes, rows[3].OriginBytes = 0, 0, 30
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	daily, err := updatesharing.Daily(db, now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 7 || daily[0].Date != "2026-10-17" || daily[1].Date != "2026-10-16" ||
		daily[0].PeerDownloads != 3 || daily[0].ServerDownloads != 3 || daily[0].ServerUsers != 2 ||
		daily[0].ServerBytes != 80 || daily[0].FailedDownloads != 1 || daily[0].ReportsWithFailures != 1 ||
		daily[1].ServerDownloads != 1 || daily[2].ServerDownloads != 0 {
		t.Fatalf("%+v", daily)
	}

	router := gin.New()
	s := &service{deps: &platform.Dependencies{DB: db, Authenticate: func(*gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: owner, Permissions: []string{"admin.update-sharing.read"}}, nil
	}}}
	s.updateDownloadRoutes(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/admin/api/update-sharing/failures?date=2026-10-17", nil))
	var page struct {
		Items []updatesharing.DownloadReport
		Total int
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Total != 1 ||
		page.Items[0].Failures[0].Stage != "verify" {
		t.Fatal(response.Code, response.Body)
	}
}
