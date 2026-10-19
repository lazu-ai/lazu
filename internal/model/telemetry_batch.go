package model

import (
	"fmt"
	"sort"
	"strings"
	"time"

	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func sortedBatch[T any](rows map[string]*T) []T {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]T, 0, len(keys))
	for _, key := range keys {
		out = append(out, *rows[key])
	}
	return out
}
func batchUpsert(tx *gorm.DB, rows interface{}, table string, keys, counters []string, extra map[string]interface{}) error {
	columns := make([]clause.Column, len(keys))
	for i, key := range keys {
		columns[i] = clause.Column{Name: key}
	}
	updates := map[string]interface{}{}
	for _, field := range counters {
		updates[field] = gorm.Expr(table + "." + field + " + excluded." + field)
	}
	for key, value := range extra {
		updates[key] = value
	}
	return tx.Clauses(clause.OnConflict{Columns: columns, DoUpdates: clause.Assignments(updates)}).CreateInBatches(rows, 500).Error
}
func excludedExtreme(table, field string, greater bool) clause.Expr {
	op := "<"
	if !greater {
		op = ">"
	}
	return gorm.Expr("CASE WHEN " + table + "." + field + " " + op + " excluded." + field + " THEN excluded." + field + " ELSE " + table + "." + field + " END")
}

func recordIncrementalBatchTx(tx *gorm.DB, logs []Log) error {
	daily := map[string]*DashboardUsageDailyDimension{}
	digests := map[string]*DigestUsageHour{}
	models := map[string]*ModelRequestCounter{}
	for i := range logs {
		row := &logs[i]
		if row.Type != LogTypeConsume && row.Type != LogTypeError {
			continue
		}
		if err := validateTelemetryLogNumbers(row); err != nil {
			return err
		}
		at := row.CreatedAt
		if at <= 0 {
			at = time.Now().Unix()
		}
		for _, dimension := range []struct {
			kind string
			id   int
		}{{"user", row.UserId}, {"key", row.TokenId}, {"project", row.ProjectID}, {"model", 0}} {
			if dimension.id <= 0 && dimension.kind != "model" {
				continue
			}
			key := fmt.Sprintf("%d|%s|%d|%s", at-at%86400, dimension.kind, dimension.id, row.ModelName)
			item := daily[key]
			if item == nil {
				item = &DashboardUsageDailyDimension{DayStart: at - at%86400, DimensionType: dimension.kind, DimensionID: dimension.id, ModelName: row.ModelName, UpdatedAt: time.Now().UTC()}
				daily[key] = item
			}
			if row.Type == LogTypeError {
				if err := addTelemetryCounters(telemetryDelta{&item.ErrorCount, 1}); err != nil {
					return err
				}
			} else {
				if err := addTelemetryCounters(telemetryDelta{&item.RequestCount, 1}, telemetryDelta{&item.AmountMicrousd, row.AmountMicrousd.Int64()}, telemetryDelta{&item.PromptTokens, int64(row.PromptTokens)}, telemetryDelta{&item.CompletionTokens, int64(row.CompletionTokens)}); err != nil {
					return err
				}
			}
		}
		payer := row.PayerUserID
		if payer <= 0 {
			payer = row.UserId
		}
		if payer > 0 {
			model := truncateString(strings.TrimSpace(row.ModelName), 128)
			source := DigestSourceKey(row.ClientFramework)
			key := fmt.Sprintf("%d|%d|%s|%s", payer, at-at%3600, model, source)
			item := digests[key]
			if item == nil {
				item = &DigestUsageHour{PayerUserID: payer, HourStart: at - at%3600, ModelName: model, Source: source}
				digests[key] = item
			}
			if row.Type == LogTypeError {
				if err := addTelemetryCounters(telemetryDelta{&item.Errors, 1}); err != nil {
					return err
				}
			} else {
				tokens, err := checkedTelemetryTokenTotal(row.PromptTokens, row.CompletionTokens)
				if err != nil {
					return err
				}
				if err := addTelemetryCounters(telemetryDelta{&item.Requests, 1}, telemetryDelta{&item.Spend, row.AmountMicrousd.Int64()}, telemetryDelta{&item.Tokens, tokens}); err != nil {
					return err
				}
				if fact, ok := usagepkg.ParseFact(row.UsageFact); ok {
					cached, err := telemetryDimensionQuantity(fact, usagepkg.DimensionCacheRead)
					if err != nil {
						return err
					}
					if err := addTelemetryCounters(telemetryDelta{&item.Cache, cached}); err != nil {
						return err
					}
				}
				if official, ok := sessionOfficialPrice(row.LineItems, row.AmountMicrousd.Int64()); ok {
					if err := addTelemetryCounters(telemetryDelta{&item.Official, official}, telemetryDelta{&item.Compared, row.AmountMicrousd.Int64()}); err != nil {
						return err
					}
				}
			}
		}
		if row.Type == LogTypeConsume && strings.TrimSpace(row.ModelName) != "" {
			item := models[row.ModelName]
			if item == nil {
				item = &ModelRequestCounter{ModelName: row.ModelName, UpdatedAt: time.Now().UTC()}
				models[row.ModelName] = item
			}
			if err := addTelemetryCounters(telemetryDelta{&item.TotalRequests, 1}); err != nil {
				return err
			}
		}
	}
	if rows := sortedBatch(daily); len(rows) > 0 {
		if err := batchUpsert(tx, &rows, "dashboard_usage_daily_dimensions", []string{"day_start", "dimension_type", "dimension_id", "model_name"}, []string{"request_count", "error_count", "amount_microusd", "prompt_tokens", "completion_tokens"}, map[string]interface{}{"updated_at": time.Now().UTC()}); err != nil {
			return err
		}
	}
	if rows := sortedBatch(digests); len(rows) > 0 {
		if err := batchUpsert(tx, &rows, "digest_usage_hours", []string{"payer_user_id", "hour_start", "model_name", "source"}, []string{"requests", "errors", "spend", "tokens", "cache", "official", "compared"}, nil); err != nil {
			return err
		}
	}
	if rows := sortedBatch(models); len(rows) > 0 {
		return batchUpsert(tx, &rows, "model_request_counters", []string{"model_name"}, []string{"total_requests"}, map[string]interface{}{"updated_at": time.Now().UTC()})
	}
	return nil
}

func recordSessionBatchTx(tx *gorm.DB, logs []Log) error {
	sessions := map[string]*AgentSession{}
	for i := range logs {
		log := &logs[i]
		if log.UserId <= 0 || strings.TrimSpace(log.SessionID) == "" || (log.Type != LogTypeConsume && log.Type != LogTypeError) {
			continue
		}
		if err := validateTelemetryLogNumbers(log); err != nil {
			return err
		}
		key := fmt.Sprintf("%d|%s", log.UserId, log.SessionID)
		at := log.CreatedAt
		item := sessions[key]
		if item == nil {
			item = &AgentSession{UserID: log.UserId, SessionID: log.SessionID, ProjectID: log.ProjectID, TokenID: log.TokenId, TokenName: log.TokenName, Source: log.SessionSource, Client: log.ClientFramework, ClientVersion: log.ClientVersion, StartedAt: at, LastAt: at, StatsVersion: 1}
			sessions[key] = item
		}
		if err := addTelemetryCounters(telemetryDelta{&item.RequestCount, 1}, telemetryDelta{&item.PromptTokens, int64(log.PromptTokens)}, telemetryDelta{&item.CompletionTokens, int64(log.CompletionTokens)}, telemetryDelta{&item.AmountMicrousd, log.AmountMicrousd.Int64()}); err != nil {
			return err
		}
		if log.UpstreamCostMicrousd != nil {
			if err := addTelemetryCounters(telemetryDelta{&item.UpstreamCostMicrousd, *log.UpstreamCostMicrousd}); err != nil {
				return err
			}
		}
		if fact, ok := usagepkg.ParseFact(log.UsageFact); ok {
			cached, err := telemetryDimensionQuantity(fact, usagepkg.DimensionCacheRead)
			if err != nil {
				return err
			}
			if err := addTelemetryCounters(telemetryDelta{&item.CachedTokens, cached}); err != nil {
				return err
			}
		}
		if log.Type == LogTypeError {
			if err := addTelemetryCounters(telemetryDelta{&item.ErrorCount, 1}); err != nil {
				return err
			}
			if at > item.LastErrorAt {
				item.LastErrorAt = at
			}
		} else {
			if price, ok := sessionOfficialPrice(log.LineItems, log.AmountMicrousd.Int64()); ok {
				if err := addTelemetryCounters(telemetryDelta{&item.OfficialMicrousd, price}, telemetryDelta{&item.ComparedMicrousd, log.AmountMicrousd.Int64()}); err != nil {
					return err
				}
			}
		}
		if log.RequestKind == "compact" {
			if err := addTelemetryCounters(telemetryDelta{&item.CompactCount, 1}); err != nil {
				return err
			}
		}
		if log.RequestKind == "review" {
			if err := addTelemetryCounters(telemetryDelta{&item.ReviewCount, 1}); err != nil {
				return err
			}
		}
		if log.ThreadID != "" {
			if err := addTelemetryCounters(telemetryDelta{&item.SubagentRequests, 1}); err != nil {
				return err
			}
		}
		if at < item.StartedAt {
			item.StartedAt = at
		}
		if at > item.LastAt {
			item.LastAt = at
		}
		if item.FirstMessagePreview == "" {
			item.FirstMessagePreview = sessionPreview(log.SessionFirstMessage)
		}
	}
	if len(sessions) == 0 {
		return nil
	}
	rows := sortedBatch(sessions)
	extra := map[string]interface{}{"last_at": excludedExtreme("agent_sessions", "last_at", true), "started_at": excludedExtreme("agent_sessions", "started_at", false), "last_error_at": excludedExtreme("agent_sessions", "last_error_at", true), "first_message_preview": gorm.Expr("CASE WHEN agent_sessions.first_message_preview = '' THEN excluded.first_message_preview ELSE agent_sessions.first_message_preview END"), "client": gorm.Expr("CASE WHEN agent_sessions.client = '' THEN excluded.client ELSE agent_sessions.client END")}
	if err := batchUpsert(tx, &rows, "agent_sessions", []string{"user_id", "session_id"}, []string{"request_count", "error_count", "subagent_requests", "compact_count", "review_count", "prompt_tokens", "completion_tokens", "cached_tokens", "amount_microusd", "official_microusd", "compared_microusd", "upstream_cost_microusd"}, extra); err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID == 0 {
			return fmt.Errorf("session upsert did not return an id")
		}
		sessions[fmt.Sprintf("%d|%s", row.UserID, row.SessionID)].ID = row.ID
	}
	threadSessions := map[uint]bool{}
	threads := map[string]*AgentSessionThread{}
	models := map[string]*AgentSessionModel{}
	messages := map[string]*AgentSessionMessage{}
	for i := range logs {
		log := &logs[i]
		session := sessions[fmt.Sprintf("%d|%s", log.UserId, log.SessionID)]
		if session == nil {
			continue
		}
		if log.ThreadID != "" {
			threadSessions[session.ID] = true
		}
		key := fmt.Sprintf("%d|%s", session.ID, log.ThreadID)
		at := log.CreatedAt
		thread := threads[key]
		if thread == nil {
			kind := log.RequestKind
			if log.ThreadID == "" {
				kind = "main"
			}
			thread = &AgentSessionThread{SessionPK: session.ID, ThreadID: log.ThreadID, ParentThreadID: log.ParentThreadID, Kind: kind, StartedAt: at, LastAt: at}
			threads[key] = thread
		}
		if err := addTelemetryCounters(telemetryDelta{&thread.RequestCount, 1}, telemetryDelta{&thread.PromptTokens, int64(log.PromptTokens)}, telemetryDelta{&thread.CompletionTokens, int64(log.CompletionTokens)}, telemetryDelta{&thread.AmountMicrousd, log.AmountMicrousd.Int64()}); err != nil {
			return err
		}
		if log.Type == LogTypeError {
			if err := addTelemetryCounters(telemetryDelta{&thread.ErrorCount, 1}); err != nil {
				return err
			}
		}
		if log.RequestKind == "compact" {
			if err := addTelemetryCounters(telemetryDelta{&thread.CompactCount, 1}); err != nil {
				return err
			}
		}
		if at < thread.StartedAt {
			thread.StartedAt = at
		}
		if at > thread.LastAt {
			thread.LastAt = at
		}
		if log.Type == LogTypeConsume && strings.TrimSpace(log.ModelName) != "" {
			key := fmt.Sprintf("%d|%s", session.ID, log.ModelName)
			item := models[key]
			if item == nil {
				item = &AgentSessionModel{SessionPK: session.ID, ModelName: log.ModelName}
				models[key] = item
			}
			tokens, err := checkedTelemetryTokenTotal(log.PromptTokens, log.CompletionTokens)
			if err != nil {
				return err
			}
			if err := addTelemetryCounters(telemetryDelta{&item.RequestCount, 1}, telemetryDelta{&item.Tokens, tokens}, telemetryDelta{&item.AmountMicrousd, log.AmountMicrousd.Int64()}); err != nil {
				return err
			}
			if price, ok := sessionOfficialPrice(log.LineItems, log.AmountMicrousd.Int64()); ok {
				if err := addTelemetryCounters(telemetryDelta{&item.OfficialMicrousd, price}, telemetryDelta{&item.ComparedMicrousd, log.AmountMicrousd.Int64()}); err != nil {
					return err
				}
			}
			switch sessionLogLane(log) {
			case sessionLaneStable:
				if err := addTelemetryCounters(telemetryDelta{&item.StableRequests, 1}); err != nil {
					return err
				}
			case sessionLaneEco:
				if err := addTelemetryCounters(telemetryDelta{&item.EcoRequests, 1}); err != nil {
					return err
				}
			case sessionLaneFallback:
				if err := addTelemetryCounters(telemetryDelta{&item.FallbackRequests, 1}); err != nil {
					return err
				}
			}
		}
		if strings.TrimSpace(log.SessionFirstMessage) != "" {
			key := fmt.Sprint(session.ID)
			if messages[key] == nil {
				messages[key] = &AgentSessionMessage{SessionPK: session.ID, Content: log.SessionFirstMessage, CreatedAt: time.Unix(at, 0).UTC()}
			}
		}
	}
	threadRows := sortedBatch(threads)
	if err := batchUpsert(tx, &threadRows, "agent_session_threads", []string{"session_pk", "thread_id"}, []string{"request_count", "error_count", "compact_count", "prompt_tokens", "completion_tokens", "amount_microusd"}, map[string]interface{}{"last_at": excludedExtreme("agent_session_threads", "last_at", true), "started_at": excludedExtreme("agent_session_threads", "started_at", false)}); err != nil {
		return err
	}
	if len(threadSessions) > 0 {
		ids := make([]uint, 0, len(threadSessions))
		for id := range threadSessions {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err := tx.Model(&AgentSession{}).Where("id IN ?", ids).Update("thread_count", gorm.Expr("(SELECT COUNT(*) FROM agent_session_threads WHERE agent_session_threads.session_pk = agent_sessions.id AND thread_id <> '')")).Error; err != nil {
			return err
		}
	}
	if modelRows := sortedBatch(models); len(modelRows) > 0 {
		if err := batchUpsert(tx, &modelRows, "agent_session_models", []string{"session_pk", "model_name"}, []string{"request_count", "tokens", "amount_microusd", "official_microusd", "compared_microusd", "stable_requests", "eco_requests", "fallback_requests"}, nil); err != nil {
			return err
		}
	}
	if messageRows := sortedBatch(messages); len(messageRows) > 0 {
		return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&messageRows, 500).Error
	}
	return nil
}
