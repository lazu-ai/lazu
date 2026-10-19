package model

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentSession is one task an agent ran through the gateway: every request
// that carried the same session id for the same user. It is folded from the
// consume log as each request settles and is never purged, so a task stays
// visible after its request logs age out.
type AgentSession struct {
	ID        uint   `json:"-" gorm:"primaryKey"`
	PublicID  string `json:"id" gorm:"size:32;uniqueIndex"`
	UserID    int    `json:"-" gorm:"not null;uniqueIndex:idx_agent_session_key,priority:1;index:idx_agent_session_user_last,priority:1"`
	SessionID string `json:"session_id" gorm:"size:64;not null;uniqueIndex:idx_agent_session_key,priority:2"`
	ProjectID int    `json:"-" gorm:"not null;default:0;index"`
	TokenID   int    `json:"-" gorm:"not null;default:0"`
	TokenName string `json:"token_name" gorm:"size:64;default:''"`
	// Source is where SessionID came from ("claude-code", "codex", …, or
	// "token" for requests grouped by Key and day because the client sent
	// no id). Client / ClientVersion come from the User-Agent.
	Source        string `json:"source" gorm:"size:32;default:''"`
	Client        string `json:"client" gorm:"size:32;default:'';index"`
	ClientVersion string `json:"client_version" gorm:"size:32;default:''"`

	StartedAt int64 `json:"started_at" gorm:"not null;index"`
	LastAt    int64 `json:"last_at" gorm:"not null;index:idx_agent_session_user_last,priority:2"`

	RequestCount     int64 `json:"request_count" gorm:"not null;default:0"`
	ErrorCount       int64 `json:"error_count" gorm:"not null;default:0"`
	SubagentRequests int64 `json:"subagent_requests" gorm:"not null;default:0"`
	ThreadCount      int64 `json:"subagent_count" gorm:"not null;default:0"`
	CompactCount     int64 `json:"compact_count" gorm:"not null;default:0"`
	ReviewCount      int64 `json:"review_count" gorm:"not null;default:0"`

	PromptTokens     int64 `json:"prompt_tokens" gorm:"not null;default:0"`
	CompletionTokens int64 `json:"completion_tokens" gorm:"not null;default:0"`
	CachedTokens     int64 `json:"cached_tokens" gorm:"not null;default:0"`
	AmountMicrousd   int64 `json:"amount_microusd" gorm:"not null;default:0"`
	// OfficialMicrousd is what the requests that carry a usable vendor list
	// price would have cost at it; ComparedMicrousd is what those same
	// requests were charged. The saving is their difference, so requests
	// without a list price never dilute it.
	OfficialMicrousd int64 `json:"official_microusd" gorm:"not null;default:0"`
	ComparedMicrousd int64 `json:"compared_microusd" gorm:"not null;default:0"`
	// LastErrorAt is when the session's latest failed request was logged; a
	// session whose last request failed has LastErrorAt >= LastAt.
	LastErrorAt int64 `json:"last_error_at" gorm:"not null;default:0"`
	// StatsVersion marks rows whose official / lane / last-error figures
	// were folded in as requests settled (1), not left for the backfill (0).
	StatsVersion int `json:"-" gorm:"not null;default:0"`
	// Operator-only: never serialised to the customer.
	UpstreamCostMicrousd int64 `json:"-" gorm:"not null;default:0"`

	// FirstMessagePreview is the opening of the first message, for list
	// titles; the full text lives in agent_session_messages.
	FirstMessagePreview string `json:"title" gorm:"size:400;default:''"`
}

func (AgentSession) TableName() string { return "agent_sessions" }

func (s *AgentSession) BeforeCreate(tx *gorm.DB) error {
	if s.PublicID == "" {
		s.PublicID = common.GeneratePublicID("ses")
	}
	return nil
}

// AgentSessionThread is the main conversation or one sub-agent of a session.
// ThreadID is empty for the main thread.
type AgentSessionThread struct {
	ID               uint   `json:"-" gorm:"primaryKey"`
	SessionPK        uint   `json:"-" gorm:"not null;uniqueIndex:idx_agent_session_thread,priority:1"`
	ThreadID         string `json:"thread_id" gorm:"size:64;not null;default:'';uniqueIndex:idx_agent_session_thread,priority:2"`
	ParentThreadID   string `json:"parent_thread_id" gorm:"size:64;default:''"`
	Kind             string `json:"kind" gorm:"size:16;default:''"`
	RequestCount     int64  `json:"request_count" gorm:"not null;default:0"`
	ErrorCount       int64  `json:"error_count" gorm:"not null;default:0"`
	CompactCount     int64  `json:"compact_count" gorm:"not null;default:0"`
	PromptTokens     int64  `json:"prompt_tokens" gorm:"not null;default:0"`
	CompletionTokens int64  `json:"completion_tokens" gorm:"not null;default:0"`
	AmountMicrousd   int64  `json:"amount_microusd" gorm:"not null;default:0"`
	StartedAt        int64  `json:"started_at" gorm:"not null"`
	LastAt           int64  `json:"last_at" gorm:"not null"`
}

func (AgentSessionThread) TableName() string { return "agent_session_threads" }

// AgentSessionModel is a session's use of one model, with how many of its
// requests ran on each lane.
type AgentSessionModel struct {
	ID               uint   `json:"-" gorm:"primaryKey"`
	SessionPK        uint   `json:"-" gorm:"not null;uniqueIndex:idx_agent_session_model,priority:1"`
	ModelName        string `json:"model_name" gorm:"size:128;not null;uniqueIndex:idx_agent_session_model,priority:2"`
	RequestCount     int64  `json:"request_count" gorm:"not null;default:0"`
	Tokens           int64  `json:"tokens" gorm:"not null;default:0"`
	AmountMicrousd   int64  `json:"amount_microusd" gorm:"not null;default:0"`
	OfficialMicrousd int64  `json:"official_microusd" gorm:"not null;default:0"`
	ComparedMicrousd int64  `json:"compared_microusd" gorm:"not null;default:0"`
	StableRequests   int64  `json:"stable_requests" gorm:"not null;default:0"`
	EcoRequests      int64  `json:"eco_requests" gorm:"not null;default:0"`
	FallbackRequests int64  `json:"fallback_requests" gorm:"not null;default:0"`
}

func (AgentSessionModel) TableName() string { return "agent_session_models" }

// AgentSessionMessage is the full first message a person typed in a
// session. Visible only to that person and platform admins.
type AgentSessionMessage struct {
	SessionPK uint      `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Content   string    `json:"content" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
}

func (AgentSessionMessage) TableName() string { return "agent_session_messages" }

const sessionPreviewRunes = 120

func sessionPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= sessionPreviewRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:sessionPreviewRunes]) + "…"
}

// Lanes a request ran on, as the 请求 table names them.
const (
	sessionLaneStable   = "stable"
	sessionLaneEco      = "eco"
	sessionLaneFallback = "fallback"
)

// sessionLogLane reads the lane a settled request ran on: the agent lane's
// group is projected as eco; the direct group is the stable lane, or a fallback when the
// Key had asked for another lane. Anything else ran on no customer lane.
func sessionLogLane(log *Log) string {
	switch strings.ToLower(strings.TrimSpace(log.Group)) {
	case "cheap":
		return sessionLaneEco
	case "direct":
		var other struct {
			LaneRequested string `json:"lane_requested"`
		}
		if log.Other != "" {
			_ = json.Unmarshal([]byte(log.Other), &other)
		}
		if requested := strings.TrimSpace(other.LaneRequested); requested != "" && requested != "direct" {
			return sessionLaneFallback
		}
		return sessionLaneStable
	}
	return ""
}

// sessionOfficialPrice is the vendor list price recorded on a request's
// receipt, when it is a fair comparison: the same gate the 请求 receipt uses
// before it prints 官方价 (no minimum charge, both sides positive, and not
// more than 20× the charge, which is a catalogue price in the wrong unit).
func sessionOfficialPrice(lineItems string, charged int64) (int64, bool) {
	if lineItems == "" || charged <= 0 {
		return 0, false
	}
	var quote billing.QuoteResult
	if err := json.Unmarshal([]byte(lineItems), &quote); err != nil || quote.Official == nil {
		return 0, false
	}
	for _, item := range quote.LineItems {
		if item.Dimension == "minimum_charge" {
			return 0, false
		}
	}
	official := quote.Official.TotalMicrousd.Int64()
	if official <= 0 || (charged <= math.MaxInt64/20 && official > charged*20) {
		return 0, false
	}
	return official, true
}

func addExpr(column string, value int64) clause.Expr {
	return gorm.Expr(column+" + ?", value)
}

func greaterExpr(table string, column string, value int64) clause.Expr {
	return gorm.Expr("CASE WHEN "+table+"."+column+" > ? THEN "+table+"."+column+" ELSE ? END", value, value)
}

func lesserExpr(table string, column string, value int64) clause.Expr {
	return gorm.Expr("CASE WHEN "+table+"."+column+" < ? THEN "+table+"."+column+" ELSE ? END", value, value)
}

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

func recordSessionActivity(log *Log, failed bool) error {
	return TelemetryDB().Transaction(func(tx *gorm.DB) error { return recordSessionActivityTx(tx, log, failed) })
}

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

// AgentSessionQuery filters the session list. UserID 0 lists every user
// (admins only); a positive UserID lists one account.
type AgentSessionQuery struct {
	UserID     int
	Start      int64
	End        int64
	Client     string
	TokenName  string
	OnlyFailed bool
	Keyword    string
	StartIdx   int
	Num        int
}

type AgentSessionListItem struct {
	AgentSession
	// UserID and Username are filled for operator reads only.
	UserID   int                 `json:"user_id,omitempty"`
	Username string              `json:"username,omitempty"`
	Models   []AgentSessionModel `json:"models"`
	Tokens   int64               `json:"tokens"`
}

// AgentSessionListSummary is the strip above the list: every agent session
// the filter matches. Requests that carried no session id are not agent
// sessions and live only in the request log.
type AgentSessionListSummary struct {
	Tokens           int64  `json:"tokens"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	RequestCount     int64  `json:"request_count"`
	AmountMicrousd   int64  `json:"amount_microusd"`
	OfficialMicrousd int64  `json:"official_microusd"`
	ComparedMicrousd int64  `json:"compared_microusd"`
	Sessions         int64  `json:"sessions"`
	Subagents        int64  `json:"subagents"`
	FailedSessions   int64  `json:"failed_sessions"`
	MedianAmount     *int64 `json:"median_amount_microusd"`
	MedianDuration   *int64 `json:"median_duration_seconds"`
	MedianRequests   *int64 `json:"median_requests"`
}

func agentSessionBaseQuery(q AgentSessionQuery) *gorm.DB {
	tx := TelemetryDB().Model(&AgentSession{}).Where("agent_sessions.source <> ?", "token")
	if q.UserID > 0 {
		tx = tx.Where("agent_sessions.user_id = ?", q.UserID)
	}
	if q.Start > 0 {
		tx = tx.Where("agent_sessions.last_at >= ?", q.Start)
	}
	if q.End > 0 {
		tx = tx.Where("agent_sessions.started_at < ?", q.End)
	}
	if client := strings.TrimSpace(q.Client); client != "" {
		tx = tx.Where("agent_sessions.client = ?", client)
	}
	if token := strings.TrimSpace(q.TokenName); token != "" {
		tx = tx.Where("agent_sessions.token_name = ?", token)
	}
	if q.OnlyFailed {
		tx = tx.Where("agent_sessions.error_count > 0 AND agent_sessions.last_error_at >= agent_sessions.last_at")
	}
	if keyword := strings.TrimSpace(q.Keyword); keyword != "" {
		like := "%" + strings.ReplaceAll(strings.ReplaceAll(keyword, "%", "\\%"), "_", "\\_") + "%"
		tx = tx.Where("(agent_sessions.first_message_preview LIKE ? OR agent_sessions.session_id LIKE ? OR agent_sessions.public_id = ?)", like, like, keyword)
	}
	return tx
}

// ListAgentSessions returns one page of sessions, newest activity first,
// plus the summary of everything matching the filter.
func ListAgentSessions(q AgentSessionQuery) ([]AgentSessionListItem, int64, AgentSessionListSummary, error) {
	var summary AgentSessionListSummary
	var total int64
	if err := agentSessionBaseQuery(q).Count(&total).Error; err != nil {
		return nil, 0, summary, err
	}
	var rows []AgentSession
	if err := agentSessionBaseQuery(q).Order("agent_sessions.last_at DESC").
		Offset(q.StartIdx).Limit(q.Num).Find(&rows).Error; err != nil {
		return nil, 0, summary, err
	}

	// Aggregate in the database; never materialize every matching session.
	columns := []string{"prompt_tokens", "completion_tokens", "cached_tokens", "request_count", "amount_microusd", "official_microusd", "compared_microusd"}
	sums := []string{"COUNT(*) AS sessions", "COALESCE(SUM(thread_count),0) AS subagents", "COALESCE(SUM(CASE WHEN error_count > 0 AND last_error_at >= last_at THEN 1 ELSE 0 END),0) AS failed_sessions"}
	for _, column := range columns {
		sums = append(sums, "COALESCE(SUM("+column+"),0) AS "+column)
	}
	if err := agentSessionBaseQuery(q).Select(strings.Join(sums, ", ")).Scan(&summary).Error; err != nil {
		return nil, 0, summary, err
	}
	summary.Tokens = summary.PromptTokens + summary.CompletionTokens
	if total > 0 {
		targets := []**int64{&summary.MedianAmount, &summary.MedianDuration, &summary.MedianRequests}
		for i, expression := range []string{"amount_microusd", "last_at - started_at", "request_count"} {
			var values []struct{ Value int64 }
			if err := agentSessionBaseQuery(q).Select(expression + " AS value").Order(expression + " ASC").Offset(int((total - 1) / 2)).Limit(int(2 - total%2)).Scan(&values).Error; err != nil {
				return nil, 0, summary, err
			}
			numbers := make([]int64, len(values))
			for j, v := range values {
				numbers[j] = v.Value
			}
			if len(numbers) > 0 {
				value := median(numbers)
				*targets[i] = &value
			}
		}
	}

	items := make([]AgentSessionListItem, len(rows))
	ids := make([]uint, len(rows))
	userIDs := make([]int, 0, len(rows))
	for i, row := range rows {
		items[i] = AgentSessionListItem{AgentSession: row, Tokens: row.PromptTokens + row.CompletionTokens, Models: []AgentSessionModel{}}
		ids[i] = row.ID
		userIDs = append(userIDs, row.UserID)
	}
	if len(ids) > 0 {
		var models []AgentSessionModel
		if err := TelemetryDB().Where("session_pk IN ?", ids).Order("request_count DESC").Find(&models).Error; err != nil {
			return nil, 0, summary, err
		}
		byPK := map[uint][]AgentSessionModel{}
		for _, m := range models {
			byPK[m.SessionPK] = append(byPK[m.SessionPK], m)
		}
		for i := range items {
			items[i].Models = append(items[i].Models, byPK[items[i].ID]...)
		}
		if q.UserID == 0 {
			var users []User
			if err := CoreDB().Unscoped().Select("id", "username").Where("id IN ?", userIDs).Find(&users).Error; err == nil {
				names := map[int]string{}
				for _, u := range users {
					names[u.Id] = u.Username
				}
				for i := range items {
					items[i].UserID = items[i].AgentSession.UserID
					items[i].Username = names[items[i].AgentSession.UserID]
				}
			}
		}
	}
	return items, total, summary, nil
}

type AgentSessionRequest struct {
	RequestID string `json:"request_id"`
	CreatedAt int64  `json:"created_at"`
	ThreadID  string `json:"thread_id"`
	Kind      string `json:"kind"`
	ModelName string `json:"model_name"`
	// Lane is stable / eco / fallback, or empty when the request never
	// reached a lane. OfficialMicrousd is its fair list-price comparison, 0
	// when there is none.
	Lane             string `json:"lane,omitempty"`
	OfficialMicrousd int64  `json:"official_microusd,omitempty"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CachedTokens     int    `json:"cached_tokens"`
	AmountMicrousd   int64  `json:"amount_microusd"`
	DurationSeconds  int    `json:"duration_seconds"`
	Failed           bool   `json:"failed"`
	Error            string `json:"error,omitempty"`
}

type AgentSessionDetail struct {
	Session      AgentSession         `json:"session"`
	Username     string               `json:"username,omitempty"`
	FirstMessage string               `json:"first_message"`
	Threads      []AgentSessionThread `json:"threads"`
	Models       []AgentSessionModel  `json:"models"`
	// Requests are the session's request log rows, oldest first. They are
	// absent once the log has aged out; the totals above remain.
	Requests          []AgentSessionRequest `json:"requests"`
	RequestsRetained  bool                  `json:"requests_retained"`
	RequestsAvailable int64                 `json:"requests_available"`
	RequestsComplete  bool                  `json:"requests_complete"`
	NextRequestCursor string                `json:"next_request_cursor,omitempty"`
}

const agentSessionRequestLimit = 2000

// GetAgentSessionDetail loads one session by public id. userID 0 is an
// admin read; otherwise the session must belong to userID.
func GetAgentSessionDetail(userID int, publicID string) (*AgentSessionDetail, error) {
	return GetAgentSessionDetailPage(userID, publicID, 0, 0)
}

// The cursor orders by completion time and log ID; equal timestamps never skip
// rows. Every page repeats the ownership check before accessing the log TelemetryDB().
func GetAgentSessionDetailPage(userID int, publicID string, afterTime int64, afterID int) (*AgentSessionDetail, error) {
	var session AgentSession
	tx := TelemetryDB().Where("public_id = ?", strings.TrimSpace(publicID))
	if userID > 0 {
		tx = tx.Where("user_id = ?", userID)
	}
	if err := tx.First(&session).Error; err != nil {
		return nil, err
	}
	detail := &AgentSessionDetail{Session: session, Threads: []AgentSessionThread{}, Models: []AgentSessionModel{}, Requests: []AgentSessionRequest{}}
	if userID == 0 {
		var owner User
		if err := CoreDB().Unscoped().Select("username").Where("id = ?", session.UserID).First(&owner).Error; err == nil {
			detail.Username = owner.Username
		}
	}
	var message AgentSessionMessage
	if err := TelemetryDB().Where("session_pk = ?", session.ID).Limit(1).Find(&message).Error; err != nil {
		return nil, err
	}
	detail.FirstMessage = message.Content
	if err := TelemetryDB().Where("session_pk = ?", session.ID).Order("started_at ASC").Find(&detail.Threads).Error; err != nil {
		return nil, err
	}
	if err := TelemetryDB().Where("session_pk = ?", session.ID).Order("amount_microusd DESC").Find(&detail.Models).Error; err != nil {
		return nil, err
	}
	if TelemetryDB() != nil {
		var logs []Log
		base := TelemetryDB().Model(&Log{}).Where("user_id = ? AND session_id = ? AND type IN ?", session.UserID, session.SessionID,
			[]int{LogTypeConsume, LogTypeError}).Where("created_at >= ? AND created_at <= ?", session.StartedAt, session.LastAt)
		if err := base.Count(&detail.RequestsAvailable).Error; err != nil {
			return nil, err
		}
		detail.RequestsRetained = detail.RequestsAvailable > 0

		if afterID > 0 {
			base = base.Where("(created_at > ? OR (created_at = ? AND id > ?))", afterTime, afterTime, afterID)
		}
		if err := base.Order("created_at ASC, id ASC").Limit(agentSessionRequestLimit + 1).Find(&logs).Error; err != nil {
			return nil, err
		}
		if len(logs) > agentSessionRequestLimit {
			logs = logs[:agentSessionRequestLimit]
			last := logs[len(logs)-1]
			detail.NextRequestCursor = fmt.Sprintf("%d:%d", last.CreatedAt, last.Id)
		}
		for _, log := range logs {
			request := AgentSessionRequest{
				RequestID: log.RequestID, CreatedAt: log.CreatedAt, ThreadID: log.ThreadID, Kind: log.RequestKind,
				ModelName: log.ModelName, PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens,
				AmountMicrousd: log.AmountMicrousd.Int64(), DurationSeconds: log.UseTime,
				Failed: log.Type == LogTypeError,
			}
			if request.Kind == "" {
				request.Kind = "main"
			}
			if fact, ok := usagepkg.ParseFact(log.UsageFact); ok {
				request.CachedTokens = dimensionQuantity(fact, usagepkg.DimensionCacheRead)
			}
			if request.Failed {
				request.Error = sessionErrorLabel(log.Content)
			} else {
				request.Lane = sessionLogLane(&log)
				if official, ok := sessionOfficialPrice(log.LineItems, request.AmountMicrousd); ok {
					request.OfficialMicrousd = official
				}
			}
			detail.Requests = append(detail.Requests, request)
		}
		detail.RequestsComplete = afterID == 0 && detail.NextRequestCursor == "" && int64(len(detail.Requests)) == session.RequestCount
	}
	return detail, nil
}

// sessionErrorLabel keeps the first line of an error log's content, which is
// the readable reason; the rest is diagnostic detail.
func sessionErrorLabel(content string) string {
	line := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	if utf8.RuneCountInString(line) > 120 {
		line = string([]rune(line)[:120]) + "…"
	}
	return line
}
