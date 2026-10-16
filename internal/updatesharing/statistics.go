package updatesharing

import (
	"time"

	"gorm.io/gorm"
)

var StatisticsZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type DailyStatistics struct {
	Date                string `json:"date"`
	PeerDownloads       int64  `json:"peerDownloads"`
	ServerDownloads     int64  `json:"serverDownloads"`
	ServerUsers         int64  `json:"serverUsers"`
	PeerBytes           int64  `json:"peerBytes"`
	ServerBytes         int64  `json:"serverBytes"`
	FailedDownloads     int64  `json:"failedDownloads"`
	ReportsWithFailures int64  `json:"reportsWithFailures"`
}

// Daily groups by server receipt date in Beijing time. Range requests never inflate counts.
func Daily(db *gorm.DB, now time.Time, days int) ([]DailyStatistics, error) {
	today := now.In(StatisticsZone)
	end := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, StatisticsZone)
	start := end.AddDate(0, 0, -days)
	var values []DailyStatistics
	err := db.Model(&DownloadReport{}).Select(`
		to_char(created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') AS date,
		count(*) FILTER (WHERE outcome = 'success' AND peer_bytes > 0) AS peer_downloads,
		count(*) FILTER (WHERE outcome = 'success' AND server_bytes > 0) AS server_downloads,
		count(DISTINCT user_id) FILTER (WHERE outcome = 'success' AND server_bytes > 0) AS server_users,
		coalesce(sum(peer_bytes), 0) AS peer_bytes, coalesce(sum(server_bytes), 0) AS server_bytes,
		count(*) FILTER (WHERE outcome = 'failed') AS failed_downloads,
		count(*) FILTER (WHERE jsonb_array_length(failures) > 0) AS reports_with_failures`).
		Where("created_at >= ? AND created_at < ?", start, end).Group("date").Scan(&values).Error
	if err != nil {
		return nil, err
	}
	byDate := make(map[string]DailyStatistics, len(values))
	for _, row := range values {
		byDate[row.Date] = row
	}
	rows := make([]DailyStatistics, 0, days)
	for day := end.AddDate(0, 0, -1); !day.Before(start); day = day.AddDate(0, 0, -1) {
		date := day.Format(time.DateOnly)
		row := byDate[date]
		row.Date = date
		rows = append(rows, row)
	}
	return rows, nil
}
