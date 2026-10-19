package model

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Search backends (Tavily, Serper, Jina …) are channels without abilities:
// /v1/search picks among them by priority rather than by model and lane, and
// they are billed per call rather than per token. None of that fits the
// routing tables the model health page reads, so a search call leaves its own
// sample here — one row per backend attempt, whether a customer's request or
// the hourly probe — and the health page folds these into a section of its
// own.

const (
	// SearchHealthKindTraffic marks a sample taken from a customer's search.
	SearchHealthKindTraffic = ChannelHealthKindTraffic
	// SearchHealthKindProbe marks the scheduler's fixed-query check.
	SearchHealthKindProbe = ChannelHealthKindProbe
)

// SearchHealthSample is one attempt against one search backend.
type SearchHealthSample struct {
	Id        int64  `json:"id" gorm:"primaryKey"`
	ChannelId int    `json:"channel_id" gorm:"index:idx_search_health_channel_at,priority:1"`
	Provider  string `json:"provider" gorm:"type:varchar(32)"`
	Kind      string `json:"kind" gorm:"type:varchar(16)"`
	Success   bool   `json:"success"`
	LatencyMs int    `json:"latency_ms"`
	// ResultCount is how many results the backend returned. A probe that
	// asks a question with a known answer and gets nothing back has failed,
	// whatever its status code said.
	ResultCount  int       `json:"result_count"`
	ErrorMessage string    `json:"error_message,omitempty" gorm:"type:varchar(512)"`
	SampledAt    time.Time `json:"sampled_at" gorm:"index;index:idx_search_health_channel_at,priority:2"`
}

func (SearchHealthSample) TableName() string {
	return "search_health_sample"
}

// RecordSearchHealthSample stores one attempt. Recording must never fail a
// caller: a lost row only costs one sample on one dashboard cell.
func RecordSearchHealthSample(sample SearchHealthSample) error {
	if CoreDB() == nil || sample.ChannelId <= 0 {
		return nil
	}
	sample.Provider = strings.ToLower(strings.TrimSpace(sample.Provider))
	if sample.Kind != SearchHealthKindProbe {
		sample.Kind = SearchHealthKindTraffic
	}
	if sample.SampledAt.IsZero() {
		sample.SampledAt = time.Now().UTC()
	}
	if len(sample.ErrorMessage) > 512 {
		sample.ErrorMessage = sample.ErrorMessage[:512]
		for len(sample.ErrorMessage) > 0 && !utf8.ValidString(sample.ErrorMessage) {
			sample.ErrorMessage = sample.ErrorMessage[:len(sample.ErrorMessage)-1]
		}
	}
	return CoreDB().Create(&sample).Error
}

// searchHealthSampleLimit bounds one window's read. Search volume is a handful
// of backends probed hourly plus customer calls; a week of that is a few
// thousand rows, so the cap only matters if something upstream loops.
const searchHealthSampleLimit = 50000

// QuerySearchHealthSamples returns the samples since the given time, oldest
// first. The volume is small enough that the health page folds them in Go,
// where percentiles and streaks are plain code rather than per-dialect SQL.
//
// When the cap binds it keeps the NEWEST samples: the current streak, the
// status and the right-hand cells all read from the end of the window, and a
// cap that kept the oldest would report an outage long after recovery.
func QuerySearchHealthSamples(since time.Time) ([]SearchHealthSample, error) {
	db := channelHealthDB()
	if db == nil || !channelHealthHasTable(db, &SearchHealthSample{}) {
		return []SearchHealthSample{}, nil
	}
	var rows []SearchHealthSample
	if err := db.Where("sampled_at >= ?", since).
		Order("sampled_at DESC, id DESC").
		Limit(searchHealthSampleLimit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	return rows, nil
}

// searchHealthLastSample is one backend's newest sample time.
type searchHealthLastSample struct {
	ChannelId int
	SampledAt ChannelHealthTime
}

// LoadSearchHealthLastSamples returns each backend's newest sample of any
// kind, which is what the scheduler needs to decide whether it is due.
func LoadSearchHealthLastSamples(since time.Time) (map[int]time.Time, error) {
	result := map[int]time.Time{}
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&SearchHealthSample{}) {
		return result, nil
	}
	var rows []searchHealthLastSample
	if err := CoreDB().Model(&SearchHealthSample{}).
		Select("channel_id, MAX(sampled_at) AS sampled_at").
		Where("sampled_at >= ?", since).
		Group("channel_id").
		Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		if row.SampledAt.Valid {
			result[row.ChannelId] = row.SampledAt.Time
		}
	}
	return result, nil
}

// PurgeOldSearchHealthSamples trims history the health page no longer reads.
func PurgeOldSearchHealthSamples(before time.Time) error {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&SearchHealthSample{}) {
		return nil
	}
	return CoreDB().Where("sampled_at < ?", before).Delete(&SearchHealthSample{}).Error
}

// QuerySearchHealthRecentSamples reads only enough evidence to decide the
// current failure streak. Each lookup uses the channel/time index, regardless
// of the volume of traffic in the historical window.
func QuerySearchHealthRecentSamples(channelIDs []int, since time.Time) ([]SearchHealthSample, error) {
	db := channelHealthDB()
	if db == nil || !channelHealthHasTable(db, &SearchHealthSample{}) {
		return nil, nil
	}
	var samples []SearchHealthSample
	for _, id := range channelIDs {
		var recent []SearchHealthSample
		if err := db.Where("channel_id = ? AND sampled_at >= ?", id, since).
			Order("sampled_at DESC, id DESC").Limit(RouteHealthFailureThreshold).Find(&recent).Error; err != nil {
			return nil, err
		}
		for i := len(recent) - 1; i >= 0; i-- {
			samples = append(samples, recent[i])
		}
	}
	return samples, nil
}
