package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestRecordSessionActivityFoldsRequestsThreadsAndErrorsIntoOneSession(t *testing.T) {
	db := withPricingTestDB(t, "agent_session_record")
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionThread{}, &AgentSessionModel{}, &AgentSessionMessage{}); err != nil {
		t.Fatalf("migrate session tables: %v", err)
	}
	previousLogDB := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previousLogDB })
	owner := User{Username: "session-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	base := Log{UserId: owner.Id, TokenId: 9, TokenName: "laptop", SessionID: "sess-abc", SessionSource: "claude-code",
		ClientFramework: "claude-code", ClientVersion: "2.1.0"}
	main := base
	main.Type, main.RequestID, main.CreatedAt = LogTypeConsume, "req-main", 1_000
	main.ModelName, main.PromptTokens, main.CompletionTokens, main.AmountMicrousd = "claude-sonnet", 100, 20, billing.MicroUSD(500)
	main.SessionFirstMessage = "  Fix the   flaky login test\nand explain why  "
	subagent := base
	subagent.Type, subagent.RequestID, subagent.CreatedAt = LogTypeConsume, "req-sub", 1_060
	subagent.ModelName, subagent.PromptTokens, subagent.CompletionTokens, subagent.AmountMicrousd = "claude-haiku", 40, 10, billing.MicroUSD(50)
	subagent.ThreadID, subagent.ParentThreadID, subagent.RequestKind = "agent-1", "", "subagent"
	failed := base
	failed.Type, failed.RequestID, failed.CreatedAt = LogTypeError, "req-err", 1_120
	failed.ModelName, failed.Content = "claude-sonnet", "upstream overloaded\nstack detail"

	for _, step := range []struct {
		log    Log
		failed bool
	}{{main, false}, {subagent, false}, {failed, true}} {
		entry := step.log
		if err := recordSessionActivity(&entry, step.failed); err != nil {
			t.Fatalf("record %s: %v", entry.RequestID, err)
		}
		// The detail view reads the request rows back from the log.
		if err := telemetryDB.Create(&entry).Error; err != nil {
			t.Fatalf("seed log %s: %v", entry.RequestID, err)
		}
	}

	var sessions []AgentSession
	if err := db.Find(&sessions).Error; err != nil {
		t.Fatalf("load sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.RequestCount != 3 || s.ErrorCount != 1 || s.SubagentRequests != 1 || s.ThreadCount != 1 {
		t.Fatalf("counters: requests=%d errors=%d subagent_requests=%d threads=%d", s.RequestCount, s.ErrorCount, s.SubagentRequests, s.ThreadCount)
	}
	if s.StartedAt != 1_000 || s.LastAt != 1_120 || s.AmountMicrousd != 550 || s.PromptTokens != 140 || s.CompletionTokens != 30 {
		t.Fatalf("totals: %+v", s)
	}
	if s.FirstMessagePreview != "Fix the flaky login test and explain why" || s.Client != "claude-code" || s.Source != "claude-code" {
		t.Fatalf("identity/preview: %+v", s)
	}
	if s.LastErrorAt != 1_120 || s.StatsVersion != 1 {
		t.Fatalf("last error / stats version: %+v", s)
	}

	items, total, summary, err := ListAgentSessions(AgentSessionQuery{UserID: owner.Id, Num: 20})
	if err != nil {
		t.Fatalf("ListAgentSessions: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].PublicID != s.PublicID || items[0].Tokens != 170 {
		t.Fatalf("list: total=%d items=%+v", total, items)
	}
	var modelRequests int64
	for _, m := range items[0].Models {
		modelRequests += m.RequestCount
	}
	if len(items[0].Models) != 2 || modelRequests != 2 {
		t.Fatalf("failed requests must not count toward model use: %+v", items[0].Models)
	}
	if summary.Sessions != 1 || summary.Subagents != 1 || summary.RequestCount != 3 || summary.FailedSessions != 1 ||
		summary.MedianDuration == nil || *summary.MedianDuration != 120 {
		t.Fatalf("summary: %+v", summary)
	}
	if others, otherTotal, _, err := ListAgentSessions(AgentSessionQuery{UserID: owner.Id + 1, Num: 20}); err != nil || otherTotal != 0 || len(others) != 0 {
		t.Fatalf("another account saw this session: total=%d err=%v", otherTotal, err)
	}

	detail, err := GetAgentSessionDetailPage(owner.Id, s.PublicID, 0, 0)
	if err != nil {
		t.Fatalf("GetAgentSessionDetail: %v", err)
	}
	if detail.FirstMessage != main.SessionFirstMessage {
		t.Fatalf("first message = %q", detail.FirstMessage)
	}
	if len(detail.Threads) != 2 || detail.Threads[0].Kind != "main" || detail.Threads[0].RequestCount != 2 || detail.Threads[0].ErrorCount != 1 ||
		detail.Threads[1].ThreadID != "agent-1" || detail.Threads[1].Kind != "subagent" || detail.Threads[1].RequestCount != 1 {
		t.Fatalf("threads: %+v", detail.Threads)
	}
	if !detail.RequestsRetained || !detail.RequestsComplete || len(detail.Requests) != 3 {
		t.Fatalf("requests: retained=%v %+v", detail.RequestsRetained, detail.Requests)
	}
	if r := detail.Requests[0]; r.RequestID != "req-main" || r.Kind != "main" || r.Failed {
		t.Fatalf("first request: %+v", r)
	}
	if r := detail.Requests[2]; r.RequestID != "req-err" || !r.Failed || r.Error != "upstream overloaded" {
		t.Fatalf("failed request: %+v", r)
	}
	if _, err := GetAgentSessionDetailPage(owner.Id+1, s.PublicID, 0, 0); err == nil {
		t.Fatal("another account opened this session")
	}
	adminView, err := GetAgentSessionDetailPage(0, s.PublicID, 0, 0)
	if err != nil || adminView.Username != owner.Username {
		t.Fatalf("admin read: %+v %v", adminView, err)
	}
}

func TestRecordSessionActivityIgnoresRequestsWithoutSession(t *testing.T) {
	db := withPricingTestDB(t, "agent_session_ignore")
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionThread{}, &AgentSessionModel{}, &AgentSessionMessage{}); err != nil {
		t.Fatalf("migrate session tables: %v", err)
	}
	RecordSessionActivity(&Log{UserId: 1, SessionID: "  "}, false)
	RecordSessionActivity(&Log{UserId: 0, SessionID: "s"}, false)
	RecordSessionActivity(nil, false)
	var count int64
	if err := db.Model(&AgentSession{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("sessions=%d err=%v, want none", count, err)
	}
}

func quoteJSON(t *testing.T, official int64, dimensions ...string) string {
	t.Helper()
	quote := billing.QuoteResult{}
	for _, dimension := range dimensions {
		quote.LineItems = append(quote.LineItems, billing.LineItem{Dimension: dimension})
	}
	if official > 0 {
		quote.Official = &billing.OfficialComparison{TotalMicrousd: billing.MicroUSD(official)}
	}
	raw, err := json.Marshal(quote)
	if err != nil {
		t.Fatalf("marshal quote: %v", err)
	}
	return string(raw)
}

func TestSessionOfficialPriceMatchesTheReceiptGate(t *testing.T) {
	cases := []struct {
		name      string
		lineItems string
		charged   int64
		want      int64
		ok        bool
	}{
		{"fair comparison", quoteJSON(t, 1_000, "input"), 400, 1_000, true},
		{"no official price", quoteJSON(t, 0, "input"), 400, 0, false},
		{"minimum charge", quoteJSON(t, 1_000, "minimum_charge"), 400, 0, false},
		{"nothing charged", quoteJSON(t, 1_000, "input"), 0, 0, false},
		{"catalogue in the wrong unit", quoteJSON(t, 20_001, "input"), 1_000, 0, false},
		{"exactly 20x", quoteJSON(t, 20_000, "input"), 1_000, 20_000, true},
		{"no receipt", "", 400, 0, false},
	}
	for _, c := range cases {
		got, ok := sessionOfficialPrice(c.lineItems, c.charged)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestSessionLogLane(t *testing.T) {
	cases := []struct {
		group, other, want string
	}{
		{"cheap", "", sessionLaneEco},
		{"direct", "", sessionLaneStable},
		{"Direct", `{"lane_requested":"direct"}`, sessionLaneStable},
		{"direct", `{"lane_requested":"cheap"}`, sessionLaneFallback},
		{"default", "", ""},
	}
	for _, c := range cases {
		if got := sessionLogLane(&Log{Group: c.group, Other: c.other}); got != c.want {
			t.Errorf("group=%q other=%q: got %q, want %q", c.group, c.other, got, c.want)
		}
	}
}

func TestAgentSessionOfficialLanesAndStatus(t *testing.T) {
	db := withPricingTestDB(t, "agent_session_official")
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionThread{}, &AgentSessionModel{}, &AgentSessionMessage{}); err != nil {
		t.Fatalf("migrate session tables: %v", err)
	}
	previousLogDB := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previousLogDB })

	base := Log{UserId: 7, TokenName: "laptop", SessionID: "sess-x", SessionSource: "codex", ModelName: "gpt-x"}
	logs := []struct {
		log    Log
		failed bool
	}{}
	add := func(mutate func(*Log), failed bool) {
		entry := base
		mutate(&entry)
		logs = append(logs, struct {
			log    Log
			failed bool
		}{entry, failed})
	}
	add(func(l *Log) {
		l.Type, l.CreatedAt, l.AmountMicrousd, l.Group = LogTypeConsume, 100, billing.MicroUSD(400), "cheap"
		l.LineItems = quoteJSON(t, 1_000, "input")
	}, false)
	add(func(l *Log) { l.Type, l.CreatedAt = LogTypeError, 150 }, true)
	add(func(l *Log) {
		l.Type, l.CreatedAt, l.AmountMicrousd, l.Group = LogTypeConsume, 200, billing.MicroUSD(600), "direct"
		l.Other = `{"lane_requested":"cheap"}`
	}, false)
	for _, step := range logs {
		entry := step.log
		if err := recordSessionActivity(&entry, step.failed); err != nil {
			t.Fatalf("record: %v", err)
		}
		if err := db.Create(&entry).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}
	// Requests without a session id are grouped by Key under source "token";
	// they are not agent sessions and stay out of the list.
	token := Log{UserId: 7, SessionID: "token-9-20260930", SessionSource: "token", Type: LogTypeConsume, CreatedAt: 120,
		ModelName: "gpt-x", AmountMicrousd: billing.MicroUSD(999)}
	if err := recordSessionActivity(&token, false); err != nil {
		t.Fatalf("record token group: %v", err)
	}

	items, total, summary, err := ListAgentSessions(AgentSessionQuery{UserID: 7, Num: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].SessionID != "sess-x" {
		t.Fatalf("token-grouped requests leaked into the list: total=%d %+v", total, items)
	}
	s := items[0]
	if s.OfficialMicrousd != 1_000 || s.ComparedMicrousd != 400 || s.AmountMicrousd != 1_000 {
		t.Fatalf("official comparison: %+v", s.AgentSession)
	}
	if s.LastErrorAt != 150 || s.LastAt != 200 {
		t.Fatalf("a session that failed and then carried on is not failed: %+v", s.AgentSession)
	}
	if summary.FailedSessions != 0 || summary.AmountMicrousd != 1_000 || summary.OfficialMicrousd != 1_000 {
		t.Fatalf("summary: %+v", summary)
	}
	if len(s.Models) != 1 || s.Models[0].EcoRequests != 1 || s.Models[0].FallbackRequests != 1 || s.Models[0].StableRequests != 0 {
		t.Fatalf("lanes: %+v", s.Models)
	}
	if _, failedTotal, _, _ := ListAgentSessions(AgentSessionQuery{UserID: 7, Num: 20, OnlyFailed: true}); failedTotal != 0 {
		t.Fatalf("only-failed matched a session that recovered")
	}

	detail, err := GetAgentSessionDetailPage(7, s.PublicID, 0, 0)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if r := detail.Requests[0]; r.Lane != sessionLaneEco || r.OfficialMicrousd != 1_000 {
		t.Fatalf("first request: %+v", r)
	}
	if r := detail.Requests[2]; r.Lane != sessionLaneFallback || r.OfficialMicrousd != 0 {
		t.Fatalf("last request: %+v", r)
	}

	// A session recorded before v16 is refolded from its logs.
	if err := db.Model(&AgentSession{}).Where("id = ?", s.ID).Updates(map[string]any{
		"official_microusd": 0, "compared_microusd": 0, "last_error_at": 0, "stats_version": 0}).Error; err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := db.Model(&AgentSessionModel{}).Where("session_pk = ?", s.ID).Updates(map[string]any{
		"official_microusd": 0, "compared_microusd": 0, "eco_requests": 0, "fallback_requests": 0}).Error; err != nil {
		t.Fatalf("reset models: %v", err)
	}
	if err := backfillAgentSessionStats(db, db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var refolded AgentSession
	if err := db.First(&refolded, s.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if refolded.OfficialMicrousd != 1_000 || refolded.ComparedMicrousd != 400 || refolded.LastErrorAt != 150 || refolded.StatsVersion != 1 {
		t.Fatalf("backfill: %+v", refolded)
	}
	var model AgentSessionModel
	if err := db.Where("session_pk = ?", s.ID).First(&model).Error; err != nil {
		t.Fatalf("reload model: %v", err)
	}
	if model.EcoRequests != 1 || model.FallbackRequests != 1 || model.OfficialMicrousd != 1_000 {
		t.Fatalf("backfill model: %+v", model)
	}
}

func TestAgentSessionRequestPaginationAndRetentionCoverage(t *testing.T) {
	db := withPricingTestDB(t, "session_pagination")
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionThread{}, &AgentSessionModel{}, &AgentSessionMessage{}); err != nil {
		t.Fatal(err)
	}
	previous := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previous })
	session := AgentSession{PublicID: "pagination", UserID: 42, SessionID: "same-time", StartedAt: 100, LastAt: 100, RequestCount: 2001}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	logs := make([]Log, 2001)
	for i := range logs {
		logs[i] = Log{UserId: 42, SessionID: session.SessionID, CreatedAt: 100, Type: LogTypeConsume, RequestID: fmt.Sprintf("request-%d", i)}
	}
	if err := db.CreateInBatches(logs, 100).Error; err != nil {
		t.Fatal(err)
	}
	// Rows beyond the cursor in other accounts or sessions must stay excluded.
	foreign := []Log{{UserId: 43, SessionID: session.SessionID, CreatedAt: 100, Type: LogTypeConsume, RequestID: "other-user"}, {UserId: 42, SessionID: "another-session", CreatedAt: 100, Type: LogTypeConsume, RequestID: "other-session"}}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	first, err := GetAgentSessionDetailPage(42, session.PublicID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Requests) != 2000 || first.RequestsAvailable != 2001 || first.RequestsComplete || first.NextRequestCursor == "" {
		t.Fatalf("unexpected coverage: %+v", first)
	}
	var at int64
	var id int
	if _, err := fmt.Sscanf(first.NextRequestCursor, "%d:%d", &at, &id); err != nil {
		t.Fatal(err)
	}
	last, err := GetAgentSessionDetailPage(42, session.PublicID, at, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Requests) != 1 || last.Requests[0].RequestID != "request-2000" || last.NextRequestCursor != "" {
		t.Fatalf("last page: %+v", last)
	}
	if _, err := GetAgentSessionDetailPage(43, session.PublicID, at, id); err == nil {
		t.Fatal("another account read a later page")
	}
	if err := db.Delete(&Log{}, logs[0].Id).Error; err != nil {
		t.Fatal(err)
	}
	partial, err := GetAgentSessionDetailPage(42, session.PublicID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if partial.RequestsComplete || partial.NextRequestCursor != "" || partial.RequestsAvailable != 2000 {
		t.Fatal("retained subset reported as complete")
	}

}
