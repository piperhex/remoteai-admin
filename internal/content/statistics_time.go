package content

import "time"

const statisticsTimeZone = "Asia/Shanghai"

// Statistics use modern Beijing civil days, independently of the server's local timezone.
var statisticsLocation = time.FixedZone(statisticsTimeZone, 8*60*60)

func statisticsDayStart(now time.Time) time.Time {
	local := now.In(statisticsLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, statisticsLocation)
}
