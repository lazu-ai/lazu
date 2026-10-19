package model

import (
	"fmt"

	"gorm.io/gorm"
)

// Schema v16 adds the official-price comparison, per-model lanes and the
// last-failure time to agent sessions. Sessions recorded before it carry
// none of them (stats_version 0); this refolds them from their request logs
// once, then marks them current.

type sessionStatsModel struct {
	official, compared    int64
	stable, eco, fallback int64
}

const sessionStatsBatch = 200

func backfillAgentSessionStats(db *gorm.DB, logDB *gorm.DB) error {
	if db == nil || logDB == nil || !db.Migrator().HasColumn(&AgentSession{}, "stats_version") {
		return nil
	}
	var lastID uint
	for {
		var sessions []AgentSession
		if err := db.Select("id", "user_id", "session_id", "source", "started_at", "last_at").
			Where("stats_version = 0 AND id > ?", lastID).
			Order("id").Limit(sessionStatsBatch).Find(&sessions).Error; err != nil {
			return err
		}
		if len(sessions) == 0 {
			return nil
		}
		for _, session := range sessions {
			lastID = session.ID
			if err := refoldAgentSessionStats(db, logDB, session); err != nil {
				return fmt.Errorf("session %d: %w", session.ID, err)
			}
		}
	}
}

func refoldAgentSessionStats(db *gorm.DB, logDB *gorm.DB, session AgentSession) error {
	var official, compared, lastErrorAt int64
	models := map[string]*sessionStatsModel{}
	if session.Source != "token" {
		var logs []Log
		if err := logDB.Select("type", "created_at", "model_name", "amount_microusd", "group", "other", "line_items").
			Where("user_id = ? AND session_id = ? AND type IN ?", session.UserID, session.SessionID,
				[]int{LogTypeConsume, LogTypeError}).
			Where("created_at >= ? AND created_at <= ?", session.StartedAt, session.LastAt).
			Find(&logs).Error; err != nil {
			return err
		}
		for i := range logs {
			log := &logs[i]
			if log.Type == LogTypeError {
				if log.CreatedAt > lastErrorAt {
					lastErrorAt = log.CreatedAt
				}
				continue
			}
			if log.ModelName == "" {
				continue
			}
			stats := models[log.ModelName]
			if stats == nil {
				stats = &sessionStatsModel{}
				models[log.ModelName] = stats
			}
			if price, ok := sessionOfficialPrice(log.LineItems, log.AmountMicrousd.Int64()); ok {
				official += price
				compared += log.AmountMicrousd.Int64()
				stats.official += price
				stats.compared += log.AmountMicrousd.Int64()
			}
			switch sessionLogLane(log) {
			case sessionLaneStable:
				stats.stable++
			case sessionLaneEco:
				stats.eco++
			case sessionLaneFallback:
				stats.fallback++
			}
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for name, stats := range models {
			if err := tx.Model(&AgentSessionModel{}).
				Where("session_pk = ? AND model_name = ?", session.ID, name).
				Updates(map[string]any{
					"official_microusd": stats.official,
					"compared_microusd": stats.compared,
					"stable_requests":   stats.stable,
					"eco_requests":      stats.eco,
					"fallback_requests": stats.fallback,
				}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&AgentSession{}).Where("id = ?", session.ID).Updates(map[string]any{
			"official_microusd": official,
			"compared_microusd": compared,
			"last_error_at":     lastErrorAt,
			"stats_version":     1,
		}).Error
	})
}
