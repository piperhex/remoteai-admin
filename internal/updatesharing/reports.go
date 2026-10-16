package updatesharing

import (
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxPackageBytes = 512 * 1024 * 1024

var artifactPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Failure uses bounded codes, never client exception text, URLs, paths, or credentials.
type Failure struct {
	Source string `json:"source"`
	Stage  string `json:"stage"`
	Code   string `json:"code"`
	Count  int    `json:"count"`
}

type Report struct {
	ID          string    `json:"id" gorm:"primaryKey;type:uuid"`
	Artifact    string    `json:"artifact"`
	Platform    string    `json:"platform"`
	Outcome     string    `json:"outcome"`
	PeerBytes   int64     `json:"peerBytes"`
	ServerBytes int64     `json:"serverBytes"`
	OriginBytes int64     `json:"originBytes"`
	Failures    []Failure `json:"failures" gorm:"serializer:json;type:jsonb"`
}

type DownloadReport struct {
	Report
	UserID    string    `json:"userId" gorm:"primaryKey;type:uuid"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

func (DownloadReport) TableName() string { return "update_download_reports" }

func (r Report) Validate() error {
	invalid := errors.New("invalid download report")
	if _, err := uuid.Parse(r.ID); err != nil || !artifactPattern.MatchString(r.Artifact) {
		return invalid
	}
	if !oneOf(r.Platform, "windows", "macos", "linux", "android") ||
		!oneOf(r.Outcome, "success", "failed") || len(r.Failures) > 16 {
		return invalid
	}
	for _, size := range []int64{r.PeerBytes, r.ServerBytes, r.OriginBytes} {
		if size < 0 || size > MaxPackageBytes {
			return invalid
		}
	}
	if r.PeerBytes+r.ServerBytes+r.OriginBytes > MaxPackageBytes || (r.Outcome == "failed" && len(r.Failures) == 0) {
		return invalid
	}
	for _, failure := range r.Failures {
		if !oneOf(failure.Source, "peer", "server", "origin", "download") ||
			!oneOf(failure.Stage, "lookup", "connect", "transfer", "verify", "storage", "policy") ||
			!oneOf(failure.Code, "unavailable", "invalid", "io", "network", "timeout", "policy") ||
			failure.Count < 1 || failure.Count > 1000 {
			return invalid
		}
	}
	return nil
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

// Store is idempotent across retries and Go instances. Identity always comes from authentication.
func Store(db *gorm.DB, userID string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if report.Failures == nil {
		report.Failures = []Failure{}
	}
	row := DownloadReport{Report: report, UserID: userID}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	for _, failure := range report.Failures {
		slog.Warn("update download failure", "reportId", report.ID, "userId", userID,
			"artifact", report.Artifact, "platform", report.Platform, "outcome", report.Outcome,
			"source", failure.Source, "stage", failure.Stage, "code", failure.Code, "count", failure.Count)
	}
	return nil
}
