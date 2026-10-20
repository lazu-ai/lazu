// Package pricecalendar holds versioned provider billing calendars. No network
// or database reads occur while deciding a request's price.
package pricecalendar

import (
	"fmt"
	"time"
)

// Calendar is also sent to catalog clients so their peak indicator uses the
// same dates as settlement. Dates and weekdays are in UTC+8.
type Calendar struct {
	Years    []int    `json:"years"`
	Holidays []string `json:"holidays"`
}

// State Council notice 国办发明电〔2025〕7号:
// https://www.gov.cn/zhengce/content/202511/content_7047090.htm
// DeepSeek excludes all weekends, including make-up working weekends:
// https://api-docs.deepseek.com/quick_start/pricing/
var deepSeek = Calendar{Years: []int{2026}, Holidays: []string{
	"2026-01-01", "2026-01-02", "2026-01-03",
	"2026-02-15", "2026-02-16", "2026-02-17", "2026-02-18", "2026-02-19", "2026-02-20", "2026-02-21", "2026-02-22", "2026-02-23",
	"2026-04-04", "2026-04-05", "2026-04-06",
	"2026-05-01", "2026-05-02", "2026-05-03", "2026-05-04", "2026-05-05",
	"2026-06-19", "2026-06-20", "2026-06-21",
	"2026-09-25", "2026-09-26", "2026-09-27",
	"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06", "2026-10-07",
}}
var beijing = time.FixedZone("UTC+8", 8*60*60)

func IsDeepSeek(model string) bool {
	switch model {
	case "deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro":
		return true
	}
	return false
}

func ForModel(model string) *Calendar {
	if !IsDeepSeek(model) {
		return nil
	}
	return &Calendar{Years: append([]int(nil), deepSeek.Years...), Holidays: append([]string(nil), deepSeek.Holidays...)}
}

func knownYear(year int) bool {
	for _, y := range deepSeek.Years {
		if y == year {
			return true
		}
	}
	return false
}

func DeepSeekPeakDay(at time.Time) bool {
	day := at.In(beijing)
	// Never invent a surcharge for a year whose holidays are not yet verified.
	// The daily worker check alerts before coverage expires.
	if !knownYear(day.Year()) || day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false
	}
	date := day.Format("2006-01-02")
	for _, holiday := range deepSeek.Holidays {
		if date == holiday {
			return false
		}
	}
	return true
}

func DeepSeekFactor(at time.Time) float64 {
	hour := at.UTC().Hour()
	if DeepSeekPeakDay(at) && ((hour >= 1 && hour < 4) || (hour >= 6 && hour < 10)) {
		return 2
	}
	return 1
}

// CheckCoverage gives operators 45 days to add the next published calendar.
func CheckCoverage(at time.Time) error {
	year := at.In(beijing).AddDate(0, 0, 45).Year()
	if !knownYear(year) {
		return fmt.Errorf("DeepSeek billing calendar needs verified holidays for %d; uncovered dates use off-peak prices", year)
	}
	return nil
}
