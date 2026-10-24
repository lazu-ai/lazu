package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

const testToken = "123456:TEST-token-value" // secret-scan: allow -- fake token for the httptest Bot API

type fakeBotAPI struct {
	mu       sync.Mutex
	calls    []map[string]any
	paths    []string
	fail     bool
	response string
	// respond, when set, answers by method name; empty falls through.
	respond func(method string, body map[string]any) string
}

// methodCalls returns the bodies of calls to one Bot API method.
func (f *fakeBotAPI) methodCalls(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for i, p := range f.paths {
		if strings.HasSuffix(p, "/"+method) {
			out = append(out, f.calls[i])
		}
	}
	return out
}

func (f *fakeBotAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeBotAPI) last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

// useFakeBotAPI points the client at a fake Bot API and configures a bot
// and an ops chat; everything is restored when the test ends.
func useFakeBotAPI(t *testing.T, token, opsChat string) *fakeBotAPI {
	t.Helper()
	fake := &fakeBotAPI{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		fake.mu.Lock()
		fake.calls = append(fake.calls, body)
		fake.paths = append(fake.paths, r.URL.Path)
		fail, response, respond := fake.fail, fake.response, fake.respond
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if respond != nil {
			if out := respond(method, body); out != "" {
				_, _ = io.WriteString(w, out)
				return
			}
		}
		switch {
		case method == "getMe":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":999,"is_bot":true,"username":"lazu_help_bot"}}`)
		case response != "":
			_, _ = io.WriteString(w, response)
		case fail:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
		}
	}))
	t.Cleanup(server.Close)

	oldBase, oldCfg, oldRedis := apiBase, common.AppConfig.Telegram, common.RedisEnabled
	apiBase = server.URL
	common.AppConfig.Telegram.BotToken = token
	common.AppConfig.Telegram.OpsChat = opsChat
	common.RedisEnabled = false
	resetLocalState()
	resetIdentity()
	t.Cleanup(func() {
		apiBase = oldBase
		common.AppConfig.Telegram = oldCfg
		common.RedisEnabled = oldRedis
		resetLocalState()
		resetIdentity()
	})
	return fake
}

func TestParseChat(t *testing.T) {
	cases := []struct {
		raw    string
		ok     bool
		id     string
		thread int64
	}{
		{"-1001234567890", true, "-1001234567890", 0},
		{" -1001234567890:42 ", true, "-1001234567890", 42},
		{"@lazu_status", true, "@lazu_status", 0},
		{"", false, "", 0},
		{"group", false, "", 0},
		{"-100:abc", false, "", 0},
		{"-100:0", false, "", 0},
		{":5", false, "", 0},
	}
	for _, tc := range cases {
		chat, ok := ParseChat(tc.raw)
		if ok != tc.ok || chat.ID != tc.id || chat.ThreadID != tc.thread {
			t.Errorf("ParseChat(%q) = %+v, %v; want %q/%d, %v", tc.raw, chat, ok, tc.id, tc.thread, tc.ok)
		}
	}
}

func TestPushOpsAlertSendsEachSubjectOncePerHour(t *testing.T) {
	fake := useFakeBotAPI(t, testToken, "-1001:9")

	PushOpsAlert(common.OpsAlert{Subject: "[Lazu ops] disk 91%", Text: "used: 91%"})
	PushOpsAlert(common.OpsAlert{Subject: "[Lazu ops] disk 91%", Text: "used: 92%"})
	if got := fake.count(); got != 1 {
		t.Fatalf("same subject twice within an hour sent %d messages, want 1", got)
	}
	body := fake.last()
	if body["chat_id"] != "-1001" || body["message_thread_id"] != float64(9) || body["parse_mode"] != "HTML" {
		t.Fatalf("unexpected sendMessage body: %v", body)
	}
	if text, _ := body["text"].(string); !strings.HasPrefix(text, "<b>[Lazu ops] disk 91%</b>") || !strings.Contains(text, "used: 91%") {
		t.Fatalf("unexpected text: %q", text)
	}
	if fake.paths[0] != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("unexpected path %q", fake.paths[0])
	}

	PushOpsAlert(common.OpsAlert{Subject: "[Lazu ops] error rate"})
	if got := fake.count(); got != 2 {
		t.Fatalf("a different subject was not sent: %d messages", got)
	}
}

func TestPushOpsAlertRespectsMute(t *testing.T) {
	fake := useFakeBotAPI(t, testToken, "-1001")
	ctx := context.Background()

	until, err := MuteOps(ctx, time.Hour)
	if err != nil || !until.After(time.Now()) {
		t.Fatalf("MuteOps: %v, %v", until, err)
	}
	if got := OpsMutedUntil(ctx); !got.Equal(until) {
		t.Fatalf("OpsMutedUntil = %v, want %v", got, until)
	}
	PushOpsAlert(common.OpsAlert{Subject: "muted"})
	if got := fake.count(); got != 0 {
		t.Fatalf("muted alert sent %d messages", got)
	}

	if _, err := MuteOps(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if !OpsMutedUntil(ctx).IsZero() {
		t.Fatal("mute was not lifted")
	}
	PushOpsAlert(common.OpsAlert{Subject: "muted"})
	if got := fake.count(); got != 1 {
		t.Fatalf("alert after unmute sent %d messages, want 1 (a muted alert must not use up its hour)", got)
	}
}

func TestPushOpsAlertWithoutConfigMakesNoRequest(t *testing.T) {
	fake := useFakeBotAPI(t, "", "-1001")
	PushOpsAlert(common.OpsAlert{Subject: "no token"})

	common.AppConfig.Telegram.BotToken = testToken
	common.AppConfig.Telegram.OpsChat = ""
	PushOpsAlert(common.OpsAlert{Subject: "no chat"})

	common.AppConfig.Telegram.OpsChat = "not-a-chat"
	PushOpsAlert(common.OpsAlert{Subject: "bad chat"})

	if got := fake.count(); got != 0 {
		t.Fatalf("unconfigured Telegram made %d requests", got)
	}
}

func TestFailedAlertIsRetriedNextTime(t *testing.T) {
	fake := useFakeBotAPI(t, testToken, "-1001")
	fake.fail = true
	PushOpsAlert(common.OpsAlert{Subject: "flaky"})
	fake.mu.Lock()
	fake.fail = false
	fake.mu.Unlock()
	PushOpsAlert(common.OpsAlert{Subject: "flaky"})
	if got := fake.count(); got != 2 {
		t.Fatalf("a failed send kept its subject quiet: %d attempts, want 2", got)
	}
}

func TestSendOpsTestReportsTelegramError(t *testing.T) {
	fake := useFakeBotAPI(t, testToken, "-1001")
	fake.fail = true
	err := SendOpsTest(context.Background(), "test")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Description != "Bad Request: chat not found" || apiErr.Code != 400 {
		t.Fatalf("SendOpsTest error = %#v", err)
	}

	common.AppConfig.Telegram.OpsChat = ""
	if err := SendOpsTest(context.Background(), "test"); err != ErrNoOpsChat {
		t.Fatalf("missing chat: %v", err)
	}
	common.AppConfig.Telegram.BotToken = ""
	if err := SendOpsTest(context.Background(), "test"); err != ErrNotConfigured {
		t.Fatalf("missing token: %v", err)
	}
}

func TestTransportErrorsNeverCarryTheToken(t *testing.T) {
	useFakeBotAPI(t, testToken, "-1001")
	// Nothing listens on port 1.
	apiBase = "http://127.0.0.1:1"
	_, err := Send(context.Background(), Message{Chat: Chat{ID: "-1001"}, Text: "x"})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "TEST-token") {
		t.Fatalf("error leaks the bot token: %v", err)
	}
}

func TestNonJSONReplyIsAnError(t *testing.T) {
	fake := useFakeBotAPI(t, testToken, "-1001")
	fake.response = "<html>bad gateway</html>"
	if _, err := Send(context.Background(), Message{Chat: Chat{ID: "-1001"}, Text: "x"}); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("non-JSON reply: %v", err)
	}
}

func TestFormatOpsAlertEscapesAndTruncates(t *testing.T) {
	got := FormatOpsAlert(common.OpsAlert{Subject: "a <b> & c", Text: "x < y"})
	if got != "<b>a &lt;b&gt; &amp; c</b>\n\nx &lt; y" {
		t.Fatalf("FormatOpsAlert = %q", got)
	}
	long := FormatOpsAlert(common.OpsAlert{Subject: "s", Text: strings.Repeat("字", 10000)})
	if n := len([]rune(long)); n > MaxMessageRunes {
		t.Fatalf("message has %d runes, over Telegram's limit", n)
	}
}
