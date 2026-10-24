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

	"github.com/lazu-ai/lazu/internal/common"
)

func TestCommand(t *testing.T) {
	cases := []struct {
		text, bot, name, args string
		ok                    bool
	}{
		{"/ask how do I pay?", "lazu_help_bot", "ask", "how do I pay?", true},
		{"/ask@lazu_help_bot  hi ", "lazu_help_bot", "ask", "hi", true},
		{"/ASK@Lazu_Help_Bot hi", "lazu_help_bot", "ask", "hi", true},
		{"/ask@other_bot hi", "lazu_help_bot", "", "", false},
		{"/start link_abc", "lazu_help_bot", "start", "link_abc", true},
		{"hello", "lazu_help_bot", "", "", false},
	}
	for _, tc := range cases {
		name, args, ok := Command(tc.text, tc.bot)
		if name != tc.name || args != tc.args || ok != tc.ok {
			t.Errorf("Command(%q) = %q, %q, %v", tc.text, name, args, ok)
		}
	}
}

func TestQuestionTriggers(t *testing.T) {
	me := botIdentity{ID: 999, Username: "lazu_help_bot"}
	user := &User{ID: 1}
	group := ChatInfo{ID: -100, Type: "supergroup"}
	cases := []struct {
		name string
		msg  IncomingMessage
		want string
		ok   bool
	}{
		{"private plain text", IncomingMessage{From: user, Chat: ChatInfo{ID: 1, Type: "private"}, Text: "how to top up"}, "how to top up", true},
		{"group without mention", IncomingMessage{From: user, Chat: group, Text: "how to top up"}, "", false},
		{"group mention", IncomingMessage{From: user, Chat: group, Text: "@Lazu_Help_Bot how to top up"}, "how to top up", true},
		{"group mention only", IncomingMessage{From: user, Chat: group, Text: "@lazu_help_bot"}, "", false},
		{"group reply to bot", IncomingMessage{From: user, Chat: group, Text: "and in yen?", ReplyTo: &IncomingMessage{From: &User{ID: 999}}}, "and in yen?", true},
		{"group reply to someone else", IncomingMessage{From: user, Chat: group, Text: "agreed", ReplyTo: &IncomingMessage{From: &User{ID: 5}}}, "", false},
		{"group /ask", IncomingMessage{From: user, Chat: group, Text: "/ask what is LZ-3101"}, "what is LZ-3101", true},
		{"other command in private", IncomingMessage{From: user, Chat: ChatInfo{ID: 1, Type: "private"}, Text: "/settings"}, "", false},
	}
	for _, tc := range cases {
		got, ok := question(&tc.msg, me)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: question = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMarkdownToHTML(t *testing.T) {
	md := "# Top up\nUse **Stripe** or see [docs](https://lazu.ai/docs/billing?a=1&b=2).\n- set `base_url` to `<api>`\n```bash\ncurl -H \"x: <y>\" https://api\n```\nDone & dusted."
	got := MarkdownToHTML(md)
	want := "<b>Top up</b>\nUse <b>Stripe</b> or see <a href=\"https://lazu.ai/docs/billing?a=1&amp;b=2\">docs</a>.\n• set <code>base_url</code> to <code>&lt;api&gt;</code>\n<pre>curl -H &#34;x: &lt;y&gt;&#34; https://api</pre>\nDone &amp; dusted."
	if got != want {
		t.Fatalf("MarkdownToHTML =\n%s\nwant\n%s", got, want)
	}
	if out := MarkdownToHTML("[x](javascript:alert(1)) <script>"); strings.Contains(out, "<a ") || strings.Contains(out, "<script>") {
		t.Fatalf("unsafe markup survived: %s", out)
	}
}

func TestTokensSplitWordsAndCJKBigrams(t *testing.T) {
	got := strings.Join(tokens("怎么充值 Stripe top-up, LZ-3101!"), "|")
	if got != "怎么|么充|充值|stripe|top-up|lz-3101" {
		t.Fatalf("tokens = %s", got)
	}
}

func TestRelevantPagesPrefersMatchingPage(t *testing.T) {
	full := strings.Repeat("filler line about nothing in particular\n", 900) +
		"\n---\n# Billing\nTop up the wallet with Stripe. 充值后余额立即可用。\n" +
		"\n---\n# Streaming\nSet stream true to receive server-sent events.\n" +
		"\n---\n# Errors\nLZ-3101 means the wallet is empty.\n"
	doc := newDocument("https://example.com/llms-full.txt", full)
	if len(doc.pages) < 3 {
		t.Fatalf("pages = %d", len(doc.pages))
	}
	pages := relevantPages([]document{doc}, "怎么充值？")
	if len(pages) == 0 || pages[0].page.title != "Billing" {
		t.Fatalf("relevantPages = %+v", pages)
	}
	pages = relevantPages([]document{doc}, "how does stream work")
	if len(pages) == 0 || pages[0].page.title != "Streaming" {
		t.Fatalf("relevantPages = %+v", pages)
	}
}

func TestCatalogRowsByNumberAndCode(t *testing.T) {
	rows := catalogRows("I keep getting lz-3101 and insufficient_quota")
	if len(rows) == 0 || !strings.HasPrefix(rows[0], "LZ-3101 ") {
		t.Fatalf("rows = %v", rows)
	}
	if rows := catalogRows("hello"); len(rows) != 0 {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

// answerFixture is a fake Bot API, a fake model and a fake docs server.
type answerFixture struct {
	bot *fakeBotAPI
	mu  sync.Mutex
	llm []map[string]any
}

func (f *answerFixture) llmCalls() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.llm...)
}

func newAnswerFixture(t *testing.T, reply string) *answerFixture {
	t.Helper()
	f := &answerFixture{bot: useFakeBotAPI(t, testToken, "")}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-answer-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"bad auth","lazu":"LZ-2001"}}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.llm = append(f.llm, body)
		f.mu.Unlock()
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(llm.Close)
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "# Example\n\n- Top up with Stripe in the wallet.\n")
	}))
	t.Cleanup(docs.Close)
	resetKnowledge()
	t.Cleanup(resetKnowledge)
	common.AppConfig.Telegram.Answer = common.TelegramAnswerConfig{
		Model: "test-model", APIKey: "sk-answer-test", BaseURL: llm.URL + "/v1",
		Chats: []string{"-1001"}, KnowledgeURLs: []string{docs.URL + "/llms.txt"},
	}
	return f
}

func privateMessage(text string) Update {
	return Update{UpdateID: 1, Message: &IncomingMessage{
		MessageID: 10, From: &User{ID: 42, LanguageCode: "zh-hans"}, Chat: ChatInfo{ID: 42, Type: "private"}, Text: text,
	}}
}

func TestPrivateQuestionIsAnsweredFromTheReference(t *testing.T) {
	f := newAnswerFixture(t, "在 **钱包** 里用 Stripe 充值。")
	Dispatch(context.Background(), privateMessage("怎么充值？"))

	calls := f.llmCalls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d", len(calls))
	}
	msgs := calls[0]["messages"].([]any)
	system := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "Top up with Stripe in the wallet.") || !strings.Contains(system, "cannot see anyone's account") {
		t.Fatalf("system prompt lacks the reference or the account rule:\n%s", system)
	}
	if user := msgs[len(msgs)-1].(map[string]any)["content"]; user != "怎么充值？" || calls[0]["model"] != "test-model" {
		t.Fatalf("unexpected request: %v", calls[0])
	}
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 1 || sent[0]["text"] != "在 <b>钱包</b> 里用 Stripe 充值。" || sent[0]["chat_id"] != "42" {
		t.Fatalf("sendMessage = %v", sent)
	}
	if rp, _ := sent[0]["reply_parameters"].(map[string]any); rp["message_id"] != float64(10) {
		t.Fatalf("reply_parameters = %v", sent[0]["reply_parameters"])
	}
	if len(f.bot.methodCalls("sendChatAction")) != 1 {
		t.Fatal("no typing indicator")
	}
}

func TestGroupAnswersOnlyInListedChatsAndWhenAddressed(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	msg := func(chat int64, text string) Update {
		return Update{UpdateID: 2, Message: &IncomingMessage{
			MessageID: 11, MessageThreadID: 7, IsTopicMessage: true, From: &User{ID: 43},
			Chat: ChatInfo{ID: chat, Type: "supergroup"}, Text: text,
		}}
	}
	Dispatch(context.Background(), msg(-2002, "@lazu_help_bot how to pay"))
	Dispatch(context.Background(), msg(-1001, "how to pay"))
	if n := len(f.llmCalls()); n != 0 {
		t.Fatalf("unlisted group or unaddressed message reached the model %d times", n)
	}
	Dispatch(context.Background(), msg(-1001, "@lazu_help_bot how to pay"))
	sent := f.bot.methodCalls("sendMessage")
	if len(f.llmCalls()) != 1 || len(sent) != 1 || sent[0]["message_thread_id"] != float64(7) {
		t.Fatalf("listed group: model=%d sent=%v", len(f.llmCalls()), sent)
	}
}

// linkedAs makes LinkedAccount answer linked for the test.
func linkedAs(t *testing.T, linked bool) {
	t.Helper()
	old := LinkedAccount
	LinkedAccount = func(context.Context, int64) bool { return linked }
	t.Cleanup(func() { LinkedAccount = old })
}

func TestPerUserLimitWarnsOnceThenStaysQuiet(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	linkedAs(t, true)
	for i := 0; i < userWindowLimit+3; i++ {
		Dispatch(context.Background(), privateMessage("q"))
	}
	if n := len(f.llmCalls()); n != userWindowLimit {
		t.Fatalf("model calls = %d, want %d", n, userWindowLimit)
	}
	if n := len(f.bot.methodCalls("sendMessage")); n != userWindowLimit+1 {
		t.Fatalf("messages = %d, want %d answers and one warning", n, userWindowLimit)
	}
}

func TestAPIKeyInAQuestionIsNeverSentToTheModel(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	Dispatch(context.Background(), privateMessage("my key sk-abcdefghijklmnopqrstuvwx fails"))
	if n := len(f.llmCalls()); n != 0 {
		t.Fatalf("a leaked key reached the model")
	}
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 1 || !strings.Contains(sent[0]["text"].(string), "API Key") {
		t.Fatalf("sendMessage = %v", sent)
	}
	if len(f.bot.methodCalls("deleteMessage")) != 1 {
		t.Fatal("the message with the key was not deleted")
	}
}

func TestUnparsableHTMLIsResentAsPlainText(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	first := true
	f.bot.respond = func(method string, body map[string]any) string {
		if method == "sendMessage" && first {
			first = false
			return `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: unsupported start tag"}`
		}
		return ""
	}
	reply(context.Background(), privateMessage("x").Message, "<b>a &lt; b</b>")
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 2 || sent[1]["text"] != "a &lt; b" {
		t.Fatalf("sendMessage = %v", sent)
	}
}

func TestStartReplyNeedsNoModel(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	common.AppConfig.Telegram.Answer.Model = ""
	Dispatch(context.Background(), privateMessage("/start"))
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 1 || !strings.Contains(sent[0]["text"].(string), "文档") || len(f.llmCalls()) != 0 {
		t.Fatalf("sendMessage = %v", sent)
	}
}

func TestModelErrorRepliesWithFallback(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	common.AppConfig.Telegram.Answer.APIKey = "not-the-key-the-fake-model-expects"
	Dispatch(context.Background(), privateMessage("怎么充值？"))
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 1 || sent[0]["text"] != Escape(phrases[phraseFailed]["zh"]) {
		t.Fatalf("sendMessage = %v", sent)
	}
}

func TestAcceptRejectsNonUpdates(t *testing.T) {
	if err := Accept([]byte(`{"hello":1}`)); err == nil {
		t.Fatal("a body without update_id was accepted")
	}
	if err := Accept([]byte(`not json`)); err == nil {
		t.Fatal("non-JSON was accepted")
	}
}

func TestAnswerNeverCarriesForeignLinks(t *testing.T) {
	f := newAnswerFixture(t, "Pay at [this page](https://evil.example.org/pay) or https://evil.example.org/x now.")
	Dispatch(context.Background(), privateMessage("怎么充值？"))
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 1 {
		t.Fatalf("sendMessage = %v", sent)
	}
	if text := sent[0]["text"].(string); strings.Contains(text, "evil.example.org") || !strings.Contains(text, "this page") {
		t.Fatalf("foreign link reached the chat: %q", text)
	}
}

func TestUnlinkedPrivateChatGetsFiveADay(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	for i := 0; i < defaultUnlinkedPrivateDaily+3; i++ {
		Dispatch(context.Background(), privateMessage("q"))
	}
	if n := len(f.llmCalls()); n != defaultUnlinkedPrivateDaily {
		t.Fatalf("model calls = %d, want %d", n, defaultUnlinkedPrivateDaily)
	}
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != defaultUnlinkedPrivateDaily+1 {
		t.Fatalf("messages = %d, want %d answers and one notice", len(sent), defaultUnlinkedPrivateDaily)
	}
	if text := sent[len(sent)-1]["text"].(string); !strings.Contains(text, "每天可以问 5 次") || !strings.Contains(text, "绑定 Telegram") {
		t.Fatalf("notice = %q", text)
	}
}

func TestLinkingLiftsThePrivateChatCap(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	linkedAs(t, true)
	for i := 0; i < defaultUnlinkedPrivateDaily+1; i++ {
		Dispatch(context.Background(), privateMessage("q"))
	}
	if n := len(f.llmCalls()); n != defaultUnlinkedPrivateDaily+1 {
		t.Fatalf("a linked account was held to the unlinked cap: %d answers", n)
	}
}

func TestPerUserDailyCapCountsGroupsAndPrivateTogether(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	linkedAs(t, true)
	common.AppConfig.Telegram.Answer.PerUserDaily = 3
	group := func() Update {
		return Update{UpdateID: 9, Message: &IncomingMessage{MessageID: 12, From: &User{ID: 42, LanguageCode: "zh-hans"},
			Chat: ChatInfo{ID: -1001, Type: "supergroup"}, Text: "@lazu_help_bot 你好"}}
	}
	Dispatch(context.Background(), group())
	Dispatch(context.Background(), privateMessage("q"))
	Dispatch(context.Background(), group())
	Dispatch(context.Background(), privateMessage("q"))
	Dispatch(context.Background(), group())
	if n := len(f.llmCalls()); n != 3 {
		t.Fatalf("model calls = %d, want 3", n)
	}
	sent := f.bot.methodCalls("sendMessage")
	if len(sent) != 4 || !strings.Contains(sent[3]["text"].(string), "今天的提问次数已经用完") {
		t.Fatalf("messages = %d, last = %v", len(sent), sent[len(sent)-1]["text"])
	}
}

func TestSiteDailyCapNoticeOncePerPerson(t *testing.T) {
	f := newAnswerFixture(t, "ok")
	linkedAs(t, true)
	common.AppConfig.Telegram.Answer.DailyLimit = 1
	Dispatch(context.Background(), privateMessage("q"))
	Dispatch(context.Background(), privateMessage("q"))
	Dispatch(context.Background(), privateMessage("q"))
	if n := len(f.llmCalls()); n != 1 {
		t.Fatalf("model calls = %d, want 1", n)
	}
	if n := len(f.bot.methodCalls("sendMessage")); n != 2 {
		t.Fatalf("messages = %d, want one answer and one notice", n)
	}
}
