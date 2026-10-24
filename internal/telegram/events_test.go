package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func useEventsChat(t *testing.T) *fakeBotAPI {
	t.Helper()
	fake := useFakeBotAPI(t, testToken, "")
	common.AppConfig.Telegram.EventsChat = "-1009"
	oldWindow := eventWindow
	eventWindow = 200 * time.Millisecond
	bursts.mu.Lock()
	bursts.windowEnd, bursts.sent, bursts.held = map[EventKind]time.Time{}, map[EventKind]int{}, map[EventKind]int{}
	bursts.mu.Unlock()
	localCounters.Lock()
	localCounters.v = map[string]int64{}
	localCounters.Unlock()
	t.Cleanup(func() { eventWindow = oldWindow })
	return fake
}

func TestEventsNeedAChat(t *testing.T) {
	fake := useFakeBotAPI(t, testToken, "-1001")
	PushEvent(EventSignup, "x")
	CountEvent(EventSignup, 0)
	if fake.count() != 0 {
		t.Fatal("events were sent without telegram.events_chat")
	}
}

func TestBurstIsMergedIntoOneLine(t *testing.T) {
	fake := useEventsChat(t)
	for i := 0; i < eventsPerMinute+3; i++ {
		PushEvent(EventSignup, "signup")
	}
	PushEvent(EventTopUp, "topup")
	if n := len(fake.methodCalls("sendMessage")); n != eventsPerMinute+1 {
		t.Fatalf("sent %d messages before the window ended, want %d signups and 1 top-up", n, eventsPerMinute)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(fake.methodCalls("sendMessage")) < eventsPerMinute+2 {
		time.Sleep(20 * time.Millisecond)
	}
	sent := fake.methodCalls("sendMessage")
	if len(sent) != eventsPerMinute+2 {
		t.Fatalf("held events were not reported: %d messages", len(sent))
	}
	last := sent[len(sent)-1]
	if last["chat_id"] != "-1009" || !strings.Contains(last["text"].(string), "另有 3 位新用户注册") {
		t.Fatalf("summary line = %v", last)
	}
}

func TestClaimEventOnce(t *testing.T) {
	useEventsChat(t)
	if !ClaimEvent("signup:7") || ClaimEvent("signup:7") {
		t.Fatal("ClaimEvent must succeed once per key")
	}
	if !ClaimEvent("signup:8") {
		t.Fatal("another key was refused")
	}
}

func TestDailySummaryPostsYesterdayOnceAfterNine(t *testing.T) {
	fake := useEventsChat(t)
	common.AppConfig.Telegram.EventsTimezone = "Asia/Shanghai"
	loc := EventsLocation()
	if loc.String() != "Asia/Shanghai" {
		t.Fatalf("location = %v", loc)
	}
	CountEvent(EventSignup, 0)
	CountEvent(EventSignup, 0)
	CountEvent(EventTopUp, 2050)
	today := time.Now().In(loc)
	tomorrowEarly := time.Date(today.Year(), today.Month(), today.Day()+1, 8, 0, 0, 0, loc)
	if postDailySummaryIfDue(tomorrowEarly) {
		t.Fatal("posted before 09:00")
	}
	tomorrowNine := time.Date(today.Year(), today.Month(), today.Day()+1, 9, 5, 0, 0, loc)
	if !postDailySummaryIfDue(tomorrowNine) || postDailySummaryIfDue(tomorrowNine.Add(time.Hour)) {
		t.Fatal("the summary must post exactly once after 09:00")
	}
	sent := fake.methodCalls("sendMessage")
	want := "新注册 2 人 · 充值 1 笔 · 合计 $20.50"
	if len(sent) != 1 || !strings.Contains(sent[0]["text"].(string), want) || !strings.Contains(sent[0]["text"].(string), today.Format("2006-01-02")) {
		t.Fatalf("summary = %v", sent)
	}
}

func TestDailySummaryOfAQuietDay(t *testing.T) {
	useEventsChat(t)
	got := DailySummary(context.Background(), "2026-01-02")
	if !strings.Contains(got, "新注册 0 人 · 充值 0 笔 · 合计 $0.00") {
		t.Fatalf("summary = %q", got)
	}
}
