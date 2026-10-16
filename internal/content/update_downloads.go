package content

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/updatesharing"
	"github.com/gin-gonic/gin"
)

func (s *service) updateDownloadRoutes(r *gin.Engine) {
	r.POST("/update-sharing/reports", s.deps.RequireAuth(), s.reportUpdateDownload)
	read := s.deps.RequirePermissions("admin.update-sharing.read")
	r.GET("/admin/api/update-sharing/daily", read, noStore, s.dailyUpdateDownloads)
	r.GET("/admin/api/update-sharing/failures", read, noStore, s.updateDownloadFailures)
}

func (s *service) reportUpdateDownload(c *gin.Context) {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
	decoder.DisallowUnknownFields()
	var report updatesharing.Report
	if err := decoder.Decode(&report); err != nil {
		platform.Fail(c, 400, "下载记录无效。")
		return
	}
	if decoder.Decode(new(interface{})) != io.EOF || report.Validate() != nil {
		platform.Fail(c, 400, "下载记录无效。")
		return
	}
	err := updatesharing.Store(s.deps.DB.WithContext(c.Request.Context()), platform.User(c).ID, report)
	platform.Respond(c, ok(), err)
}

func (s *service) dailyUpdateDownloads(c *gin.Context) {
	days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
	if err != nil || (days != 7 && days != 30 && days != 90) {
		platform.Fail(c, 400, "请选择 7、30 或 90 天。")
		return
	}
	rows, err := updatesharing.Daily(s.deps.DB.WithContext(c.Request.Context()), time.Now(), days)
	platform.Respond(c, gin.H{"timezone": "Asia/Shanghai", "items": rows}, err)
}

func (s *service) updateDownloadFailures(c *gin.Context) {
	day, err := time.ParseInLocation(time.DateOnly, c.Query("date"), updatesharing.StatisticsZone)
	if err != nil {
		platform.Fail(c, 400, "请选择有效日期。")
		return
	}
	p, size := page(c)
	if size > 100 || p > 10000 {
		platform.Fail(c, 400, "请求的记录范围过大。")
		return
	}
	query := s.deps.DB.WithContext(c.Request.Context()).Model(&updatesharing.DownloadReport{}).
		Where("created_at >= ? AND created_at < ? AND jsonb_array_length(failures) > 0", day, day.AddDate(0, 0, 1))
	value, err := paginate[updatesharing.DownloadReport](c, query, "created_at DESC, id DESC")
	platform.Respond(c, value, err)
}
