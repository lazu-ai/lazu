package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// Business events (new accounts, completed top-ups) go to telegram.events_chat
// as they happen, plus a summary of the previous day at 09:00 in
// telegram.events_timezone. A burst (a wave of sign-ups) is not sent one by
// one: past a few per minute per kind, the rest are counted and reported in
// one line when the minute ends. Like ops alerts, events are a nudge; a
// failed send is logged and never fails the work that caused it.

// EventKind names one kind of business event.
type EventKind string

const (
	EventSignup EventKind = "signup"
	EventTopUp  EventKind = "topup"
)

// eventWindow is the burst window; tests shorten it.
var eventWindow = time.Minute

const (
	eventsPerMinute   = 5
	eventSendTimeout  = 10 * time.Second
	dailySummaryHour  = 9
	dailyCounterTTL   = 72 * time.Hour
	eventDedupeWindow = 7 * 24 * time.Hour
)

// EventsChat is the configured business-events chat, if any.
func EventsChat() (Chat, bool) { return ParseChat(common.AppConfig.Telegram.EventsChat) }

// EventsConfigured reports whether business events are sent at all.
func EventsConfigured() bool {
	_, ok := EventsChat()
	return Configured() && ok
}

// EventsLocation is the zone the daily summary follows.
func EventsLocation() *time.Location {
	if name := strings.TrimSpace(common.AppConfig.Telegram.EventsTimezone); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
		common.SysError("telegram: unknown events_timezone " + name + ", using UTC")
	}
	return time.UTC
}

// ClaimEvent reports whether an event with this key has not been reported
// yet, and marks it reported. Hooks that may fire more than once for the same
// thing (a sign-up seen on several session setups, a retried transaction)
// call it first.
func ClaimEvent(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return claim(ctx, "event:"+key, eventDedupeWindow)
}

type burst struct {
	mu        sync.Mutex
	windowEnd map[EventKind]time.Time
	sent      map[EventKind]int
	held      map[EventKind]int
}

var bursts = &burst{windowEnd: map[EventKind]time.Time{}, sent: map[EventKind]int{}, held: map[EventKind]int{}}

// eventNoun is how a held-back count is described in the summary line.
var eventNoun = map[EventKind]string{
	EventSignup: "位新用户注册",
	EventTopUp:  "笔充值到账",
}

// PushEvent sends one business event, or counts it toward the end-of-minute
// line when this kind has already used its messages for the minute.
func PushEvent(kind EventKind, html string) {
	if !EventsConfigured() {
		return
	}
	now := time.Now()
	bursts.mu.Lock()
	if end, ok := bursts.windowEnd[kind]; !ok || now.After(end) {
		bursts.windowEnd[kind] = now.Add(eventWindow)
		bursts.sent[kind] = 0
	}
	if bursts.sent[kind] >= eventsPerMinute {
		bursts.held[kind]++
		first := bursts.held[kind] == 1
		wait := time.Until(bursts.windowEnd[kind])
		bursts.mu.Unlock()
		if first {
			time.AfterFunc(wait, func() { flushHeld(kind) })
		}
		return
	}
	bursts.sent[kind]++
	bursts.mu.Unlock()
	sendEvent(html)
}

func flushHeld(kind EventKind) {
	bursts.mu.Lock()
	n := bursts.held[kind]
	bursts.held[kind] = 0
	bursts.mu.Unlock()
	if n > 0 {
		sendEvent(Escape(fmt.Sprintf("过去一分钟另有 %d %s，未逐条显示。", n, eventNoun[kind])))
	}
}

func sendEvent(html string) {
	chat, ok := EventsChat()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), eventSendTimeout)
	defer cancel()
	if _, err := Send(ctx, Message{Chat: chat, Text: html, DisableLinkCards: true}); err != nil {
		common.SysError("telegram: business event not delivered: " + err.Error())
	}
}

// --- daily counters ---

// CountEvent adds to the counters the daily summary reads: one event of this
// kind and, for top-ups, the paid amount in cents.
func CountEvent(kind EventKind, cents int64) {
	if !EventsConfigured() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	day := time.Now().In(EventsLocation()).Format("2006-01-02")
	addCounter(ctx, "daily:"+day+":"+string(kind), 1)
	if cents > 0 {
		addCounter(ctx, "daily:"+day+":"+string(kind)+":cents", cents)
	}
}

var localCounters = struct {
	sync.Mutex
	v map[string]int64
}{v: map[string]int64{}}

func addCounter(ctx context.Context, key string, n int64) {
	if redisReady() {
		full := keyPrefix + key
		if err := common.RDB.IncrBy(ctx, full, n).Err(); err == nil {
			_ = common.RDB.Expire(ctx, full, dailyCounterTTL).Err()
			return
		}
	}
	localCounters.Lock()
	localCounters.v[key] += n
	localCounters.Unlock()
}

func readCounter(ctx context.Context, key string) int64 {
	if redisReady() {
		if v, err := common.RDB.Get(ctx, keyPrefix+key).Result(); err == nil {
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
		return 0
	}
	localCounters.Lock()
	defer localCounters.Unlock()
	return localCounters.v[key]
}

// DailySummary is the summary line for one local day.
func DailySummary(ctx context.Context, day string) string {
	signups := readCounter(ctx, "daily:"+day+":"+string(EventSignup))
	topups := readCounter(ctx, "daily:"+day+":"+string(EventTopUp))
	cents := readCounter(ctx, "daily:"+day+":"+string(EventTopUp)+":cents")
	return fmt.Sprintf("<b>📊 %s 日报</b>\n新注册 %d 人 · 充值 %d 笔 · 合计 $%d.%02d",
		Escape(day), signups, topups, cents/100, cents%100)
}

func init() { common.RegisterBackgroundLoop("telegram-daily-summary", runDailySummary) }

// runDailySummary posts yesterday's summary once a day, after 09:00 in the
// events timezone. A Redis claim keeps replicas from posting it twice.
func runDailySummary() {
	for {
		time.Sleep(5 * time.Minute)
		if !EventsConfigured() {
			continue
		}
		postDailySummaryIfDue(time.Now())
	}
}

func postDailySummaryIfDue(now time.Time) bool {
	local := now.In(EventsLocation())
	if local.Hour() < dailySummaryHour {
		return false
	}
	day := local.AddDate(0, 0, -1).Format("2006-01-02")
	ctx, cancel := context.WithTimeout(context.Background(), eventSendTimeout)
	defer cancel()
	if !claim(ctx, "daily-summary:"+day, 48*time.Hour) {
		return false
	}
	sendEvent(DailySummary(ctx, day))
	return true
}
