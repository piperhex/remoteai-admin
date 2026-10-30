package content

import (
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func parseStatisticsTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestStatisticsDayStart(t *testing.T) {
	for _, test := range []struct{ now, want string }{
		{"2026-10-29T15:59:59.999999Z", "2026-10-29T00:00:00+08:00"},
		{"2026-10-29T16:00:00Z", "2026-10-30T00:00:00+08:00"},
		{"2026-10-30T00:00:00Z", "2026-10-30T00:00:00+08:00"},
		{"2026-10-31T16:00:00Z", "2026-11-01T00:00:00+08:00"},
		{"2026-12-31T08:00:00-08:00", "2027-01-01T00:00:00+08:00"},
	} {
		t.Run(test.now, func(t *testing.T) {
			got := statisticsDayStart(parseStatisticsTime(t, test.now))
			if got.Format(time.RFC3339) != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestDashboardBeijingDayQueries(t *testing.T) {
	db := dashboardDatabase(t)
	today := statisticsDayStart(parseStatisticsTime(t, "2026-10-29T16:30:00Z"))
	start, until := today.AddDate(0, 0, -6), today.AddDate(0, 0, 1)
	seedDashboardBoundaries(t, db, start, today)
	data, err := readDashboard(db, start, until)
	if err != nil {
		t.Fatal(err)
	}
	if data.Summary.DailyActiveUsers != 2 || data.Summary.NewUsers != 5 || data.Summary.NewInstallations != 5 {
		t.Fatalf("incorrect Beijing summary: %+v", data.Summary)
	}
	platformCounts := map[string]int64{}
	for _, row := range data.DailyActive {
		platformCounts[row.Name] = row.Count
	}
	if !reflect.DeepEqual(platformCounts, map[string]int64{"ios": 1, "windows": 1}) {
		t.Fatal(data.DailyActive)
	}
	trend, activity := buildDashboardTrend(data, start, 7)
	for _, check := range []struct {
		index                   int
		date                    string
		users, active, installs int64
	}{
		{0, "2026-10-24", 1, 1, 1},
		{1, "2026-10-25", 0, 0, 0},
		{5, "2026-10-29", 1, 1, 1},
		{6, "2026-10-30", 3, 2, 3},
	} {
		if activity[check.index]["date"] != check.date || activity[check.index]["total"] != check.active ||
			trend[check.index]["users"] != check.users || trend[check.index]["installations"] != check.installs {
			t.Fatalf("day %s: trend %+v, activity %+v", check.date, trend[check.index], activity[check.index])
		}
	}
	verifyDashboardTrafficDays(t, db, today, activity)
}

func seedDashboardBoundaries(t *testing.T, db *gorm.DB, start, today time.Time) {
	t.Helper()
	until := today.AddDate(0, 0, 1)
	device := uuid.NewString()
	times := []time.Time{start.Add(-time.Microsecond), start, today.Add(-time.Microsecond),
		today, today.Add(8 * time.Hour), until.Add(-time.Microsecond), until}
	platforms := []string{"linux", "android", "windows", "windows", "windows", "ios", "android"}
	for index, at := range times {
		id := uuid.NewString()
		for _, query := range []string{
			`INSERT INTO users(id,"createdAt") VALUES (?,?)`,
			`INSERT INTO device_installations("deviceId","firstSeenAt") VALUES (?,?)`,
		} {
			if err := db.Exec(query, id, at).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Exec(`UPDATE device_installations SET platform=? WHERE "deviceId"=?`, platforms[index], id).
			Error; err != nil {
			t.Fatal(err)
		}
		if index >= 2 && index <= 4 {
			id = device // Legacy duplicates count once per day; the same device can count on two days.
		}
		event := DeviceTelemetryEvent{Id: uuid.NewString(), DeviceId: id,
			Platform: platforms[index], EventType: "activity", CreatedAt: at}
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := recordTelemetryEvent(db, installationInput{
		DeviceId: uuid.NewString(), Platform: "linux", EventType: "base_url_changed",
	}, today); err != nil {
		t.Fatal(err)
	}
}

func verifyDashboardTrafficDays(t *testing.T, db *gorm.DB, now time.Time, activity []gin.H) {
	t.Helper()
	for index, at := range []time.Time{now.Add(-time.Hour), now, now.Add(23 * time.Hour), now.Add(24 * time.Hour)} {
		if err := db.Exec(`INSERT INTO chat_relay_traffic(hour_start,bytes) VALUES (?,?)`, at, index+1).Error; err != nil {
			t.Fatal(err)
		}
	}
	traffic, err := ReadTrafficOverview(db, len(activity), now)
	if err != nil {
		t.Fatal(err)
	}
	daily := traffic["daily"].([]gin.H)
	for index, day := range daily {
		if day["date"] != activity[index]["date"] {
			t.Fatalf("traffic/activity dates differ: %v, %v", daily, activity)
		}
	}
	last := daily[len(daily)-1]
	hours := last["hourlyBytes"].([]int64)
	if last["bytes"] != int64(5) || hours[0] != 2 || hours[23] != 3 || daily[len(daily)-2]["bytes"] != int64(1) {
		t.Fatal(traffic)
	}
}

func TestTelemetryDeduplicatesBeijingDays(t *testing.T) {
	db := dashboardDatabase(t)
	input := installationInput{DeviceId: uuid.NewString(), Platform: "windows", EventType: "activity"}
	for _, test := range []struct {
		at    string
		count int64
	}{
		{"2026-12-31T15:59:59.999999Z", 1},
		{"2026-12-31T16:00:00Z", 2},
		{"2026-12-31T16:00:00Z", 2},
		{"2027-01-01T00:00:00Z", 2},
		{"2027-01-01T15:59:59.999999Z", 2},
		{"2027-01-01T16:00:00Z", 3},
	} {
		if err := recordTelemetryEvent(db, input, parseStatisticsTime(t, test.at)); err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&DeviceTelemetryEvent{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != test.count {
			t.Fatalf("%s: %d events, want %d", test.at, count, test.count)
		}
	}
}
