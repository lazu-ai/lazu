package model

import (
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The pre-batch consume-log writers and the session activity they recorded.
// Production writes consume logs through persistRelayResultBatch and records
// session activity in recordSessionBatchTx; these remain only as seed helpers
// for the tests that call them.

// From agent_session.go.
func addExpr(column string, value int64) clause.Expr {
	return gorm.Expr(column+" + ?", value)
}

// From agent_session.go.
func lesserExpr(table string, column string, value int64) clause.Expr {
	return gorm.Expr("CASE WHEN "+table+"."+column+" < ? THEN "+table+"."+column+" ELSE ? END", value, value)
}

// From agent_session.go.
// RecordSessionActivity folds one settled (or failed) request into its
// session. It runs where the consume / error log is written, which already
// happens exactly once per request.
func RecordSessionActivity(log *Log, failed bool) {
	if log == nil || TelemetryDB() == nil || log.UserId <= 0 || strings.TrimSpace(log.SessionID) == "" {
		return
	}
	if err := recordSessionActivity(log, failed); err != nil {
		common.SysLog("failed to record session activity: " + err.Error())
	}
}

// From agent_session.go.
func recordSessionActivity(log *Log, failed bool) error {
	return TelemetryDB().Transaction(func(tx *gorm.DB) error { return recordSessionActivityTx(tx, log, failed) })
}

// From agent_session.go.
func recordSessionActivityTx(tx *gorm.DB, log *Log, failed bool) error {
	if log == nil || log.UserId <= 0 || strings.TrimSpace(log.SessionID) == "" {
		return nil
	}
	if err := validateTelemetryLogNumbers(log); err != nil {
		return err
	}
	tokens, err := checkedTelemetryTokenTotal(log.PromptTokens, log.CompletionTokens)
	if err != nil {
		return err
	}
	at := log.CreatedAt
	if at <= 0 {
		at = time.Now().Unix()
	}
	var cached int64
	prompt, completion := int64(log.PromptTokens), int64(log.CompletionTokens)
	if fact, ok := usagepkg.ParseFact(log.UsageFact); ok {
		cached, err = telemetryDimensionQuantity(fact, usagepkg.DimensionCacheRead)
		if err != nil {
			return err
		}
	}
	amount := log.AmountMicrousd.Int64()
	var upstream int64
	if log.UpstreamCostMicrousd != nil {
		upstream = *log.UpstreamCostMicrousd
	}
	var errors, compact, review, subagentRequests, lastErrorAt int64
	if failed {
		errors = 1
		lastErrorAt = at
	}
	var official, compared int64
	if !failed {
		if price, ok := sessionOfficialPrice(log.LineItems, amount); ok {
			official, compared = price, amount
		}
	}
	switch log.RequestKind {
	case "compact":
		compact = 1
	case "review":
		review = 1
	}
	if log.ThreadID != "" {
		subagentRequests = 1
	}

	session := AgentSession{
		UserID: log.UserId, SessionID: log.SessionID, ProjectID: log.ProjectID,
		TokenID: log.TokenId, TokenName: log.TokenName,
		Source: log.SessionSource, Client: log.ClientFramework, ClientVersion: log.ClientVersion,
		StartedAt: at, LastAt: at,
		RequestCount: 1, ErrorCount: errors, SubagentRequests: subagentRequests,
		CompactCount: compact, ReviewCount: review,
		PromptTokens: prompt, CompletionTokens: completion, CachedTokens: cached,
		AmountMicrousd: amount, UpstreamCostMicrousd: upstream,
		OfficialMicrousd: official, ComparedMicrousd: compared, LastErrorAt: lastErrorAt,
		StatsVersion:        1,
		FirstMessagePreview: sessionPreview(log.SessionFirstMessage),
	}
	const table = "agent_sessions"
	updates := map[string]any{
		"request_count":          addExpr(table+".request_count", 1),
		"error_count":            addExpr(table+".error_count", errors),
		"subagent_requests":      addExpr(table+".subagent_requests", subagentRequests),
		"compact_count":          addExpr(table+".compact_count", compact),
		"review_count":           addExpr(table+".review_count", review),
		"prompt_tokens":          addExpr(table+".prompt_tokens", prompt),
		"completion_tokens":      addExpr(table+".completion_tokens", completion),
		"cached_tokens":          addExpr(table+".cached_tokens", cached),
		"amount_microusd":        addExpr(table+".amount_microusd", amount),
		"official_microusd":      addExpr(table+".official_microusd", official),
		"compared_microusd":      addExpr(table+".compared_microusd", compared),
		"last_error_at":          greaterExpr(table, "last_error_at", lastErrorAt),
		"upstream_cost_microusd": addExpr(table+".upstream_cost_microusd", upstream),
		"last_at":                greaterExpr(table, "last_at", at),
		"started_at":             lesserExpr(table, "started_at", at),
	}
	if session.FirstMessagePreview != "" {
		updates["first_message_preview"] = gorm.Expr("CASE WHEN "+table+".first_message_preview = '' THEN ? ELSE "+table+".first_message_preview END", session.FirstMessagePreview)
	}
	if session.Client != "" {
		updates["client"] = gorm.Expr("CASE WHEN "+table+".client = '' THEN ? ELSE "+table+".client END", session.Client)
		updates["client_version"] = session.ClientVersion
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "session_id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&session).Error; err != nil {
		return err
	}
	if session.ID == 0 {
		if err := tx.Model(&AgentSession{}).Select("id").
			Where("user_id = ? AND session_id = ?", log.UserId, log.SessionID).
			Scan(&session.ID).Error; err != nil || session.ID == 0 {
			return err
		}
	}

	// Thread row: the main conversation (empty id) or one sub-agent.
	thread := AgentSessionThread{SessionPK: session.ID, ThreadID: log.ThreadID, ParentThreadID: log.ParentThreadID,
		Kind: log.RequestKind, StartedAt: at, LastAt: at}
	if thread.ThreadID == "" {
		thread.Kind = "main"
	}
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&thread)
	if created.Error != nil {
		return created.Error
	}
	if created.RowsAffected == 1 && thread.ThreadID != "" {
		if err := tx.Model(&AgentSession{}).Where("id = ?", session.ID).
			Update("thread_count", gorm.Expr("thread_count + 1")).Error; err != nil {
			return err
		}
	}
	const threads = "agent_session_threads"
	if err := tx.Model(&AgentSessionThread{}).
		Where("session_pk = ? AND thread_id = ?", session.ID, log.ThreadID).
		Updates(map[string]any{
			"request_count":     addExpr("request_count", 1),
			"error_count":       addExpr("error_count", errors),
			"compact_count":     addExpr("compact_count", compact),
			"prompt_tokens":     addExpr("prompt_tokens", prompt),
			"completion_tokens": addExpr("completion_tokens", completion),
			"amount_microusd":   addExpr("amount_microusd", amount),
			"last_at":           greaterExpr(threads, "last_at", at),
			"started_at":        lesserExpr(threads, "started_at", at),
		}).Error; err != nil {
		return err
	}

	if !failed && strings.TrimSpace(log.ModelName) != "" {
		model := AgentSessionModel{SessionPK: session.ID, ModelName: log.ModelName,
			RequestCount: 1, Tokens: tokens, AmountMicrousd: amount,
			OfficialMicrousd: official, ComparedMicrousd: compared}
		switch sessionLogLane(log) {
		case sessionLaneStable:
			model.StableRequests = 1
		case sessionLaneEco:
			model.EcoRequests = 1
		case sessionLaneFallback:
			model.FallbackRequests = 1
		}
		const models = "agent_session_models"
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_pk"}, {Name: "model_name"}},
			DoUpdates: clause.Assignments(map[string]any{
				"request_count":     addExpr(models+".request_count", 1),
				"tokens":            addExpr(models+".tokens", tokens),
				"amount_microusd":   addExpr(models+".amount_microusd", amount),
				"official_microusd": addExpr(models+".official_microusd", official),
				"compared_microusd": addExpr(models+".compared_microusd", compared),
				"stable_requests":   addExpr(models+".stable_requests", model.StableRequests),
				"eco_requests":      addExpr(models+".eco_requests", model.EcoRequests),
				"fallback_requests": addExpr(models+".fallback_requests", model.FallbackRequests),
			}),
		}).Create(&model).Error; err != nil {
			return err
		}
	}

	if strings.TrimSpace(log.SessionFirstMessage) != "" {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentSessionMessage{
			SessionPK: session.ID, Content: log.SessionFirstMessage, CreatedAt: time.Unix(at, 0).UTC(),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// From log.go.
func createConsumeLogsBatch(logs []Log) error {
	events := make([]relayResultEvent, 0, len(logs))
	for i := range logs {
		ensureConsumeLogPublicID(&logs[i])
		event := newRelayConsumeLogEvent(&logs[i])
		events = append(events, relayResultEvent{ConsumeLog: &event})
	}
	return persistRelayResultBatch(events)
}

// From log.go.
func consumeLogSideEffects(log *Log, async bool) {
	if log == nil {
		return
	}
	if log.Type == LogTypeConsume && log.RequestID != "" {
		if err := ReleaseSpendBudgetReservation(SpendBudgetChargeInput{
			ProjectID: log.ProjectID, PayerUserID: log.PayerUserID, ActorUserID: log.UserId,
			UserID:    log.UserId,
			TokenID:   log.TokenId,
			RequestID: log.RequestID,
		}); err != nil {
			common.SysLog("failed to release spend budget reservation: " + err.Error())
		}
		StampUserRequestActivity(log.UserId, log.CreatedAt)
		RecordSessionActivity(log, false)
	}
	if common.DataExportEnabled {
		fn := func() {
			createdAt := log.CreatedAt
			if createdAt == 0 {
				createdAt = common.GetTimestamp()
			}
			LogUsageData(log.UserId, log.Username, log.ModelName, log.AmountMicrousd, createdAt, log.PromptTokens+log.CompletionTokens)
		}
		if async {
			common.SafeGo("usage-data-log", fn)
		} else {
			fn()
		}
	}
	if log.ModelName != "" {
		modelName := log.ModelName
		fn := func() {
			if err := IncrementModelRequests(modelName); err != nil {
				common.SysLog("failed to increment model requests: " + err.Error())
			}
		}
		if async {
			common.SafeGo("model-request-counter", fn)
		} else {
			fn()
		}
	}
}
