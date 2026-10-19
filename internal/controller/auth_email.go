package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/internal/types"
)

// authEmailBaseURL resolves the origin users should be sent back to. The
// console can live on a different host than the API, so the frontend origin
// wins and ServerAddress is only the fallback.
func authEmailBaseURL() string {
	if origin := oidcFrontendOrigin(); origin != "" {
		return origin
	}
	return strings.TrimRight(system_setting.ServerAddress, "/")
}

// authEmailAPIBaseURL resolves the public backend origin independently from
// the console origin. Deployments commonly serve the two on different hosts,
// and the OpenAI-compatible endpoint must always point at the API host.
func authEmailAPIBaseURL() string {
	if base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/"); base != "" {
		return base
	}
	if base := common.SiteAPIOrigin(); base != "" {
		return base
	}
	return common.SiteWebOrigin()
}

// requestMailLang is the language of the person behind this request. The
// console sends its interface language as Accept-Language.
func requestMailLang(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return common.MailDefaultLang
	}
	return common.MailLang(c.GetHeader("Accept-Language"))
}

// userMailLang prefers the language the account last used in the console and
// falls back to the request's.
func userMailLang(user *model.User, c *gin.Context) string {
	if user != nil {
		if lang := user.GetSetting().Language; lang != "" {
			return common.MailLang(lang)
		}
	}
	return requestMailLang(c)
}

type mailLangContextKey struct{}

// withMailLang carries the request's mail language into account resolution
// that only receives a context (OIDC).
func withMailLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, mailLangContextKey{}, lang)
}

type mailSurfaceContextKey struct{}

// withMailSurface carries the surface the browser signs up through (the
// lz_surface cookie) into account resolution that only receives a context,
// so the welcome mail can open the right product.
func withMailSurface(ctx context.Context, surface string) context.Context {
	return context.WithValue(ctx, mailSurfaceContextKey{}, surface)
}

func mailSurfaceFromContext(ctx context.Context) string {
	if ctx != nil {
		if surface, ok := ctx.Value(mailSurfaceContextKey{}).(string); ok {
			return model.NormalizeUserHomeSurface(surface)
		}
	}
	return ""
}

func mailLangFromContext(ctx context.Context) string {
	if ctx != nil {
		if lang, ok := ctx.Value(mailLangContextKey{}).(string); ok {
			return common.MailLang(lang)
		}
	}
	return common.MailDefaultLang
}

// renderAuthMail renders identity mail on the paper tier. A render failure is
// a programming error in a fixed template; it degrades to the plain lead so
// the reader still gets the code or link.
func renderAuthMail(m common.Mail) string {
	m.Tier = common.MailPaper
	body, err := common.RenderMail(m)
	if err != nil {
		common.SysError("render auth email: " + err.Error())
		return "<p>" + m.Title + "</p>"
	}
	return body
}

func authVerificationMail(lang, code string) (string, string) {
	name := common.SystemName
	minutes := strconv.Itoa(common.VerificationValidMinutes)
	subject := common.MailText(lang, name+" 验证码 "+code, "Your "+name+" verification code: "+code)
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "VERIFY EMAIL",
		Preheader: common.MailText(lang, "验证码 "+minutes+" 分钟内有效", "The code expires in "+minutes+" minutes"),
		Title:     common.MailText(lang, "验证你的邮箱", "Verify your email"),
		Lead:      common.MailText(lang, "输入下面的验证码，完成 "+name+" 邮箱验证。", "Enter this code to verify your email for "+name+"."),
		Blocks: []common.MailBlock{
			{Code: code},
			{Muted: true, Text: common.MailText(lang,
				"验证码 "+minutes+" 分钟内有效。不是你本人操作的话，忽略这封邮件即可。",
				"The code expires in "+minutes+" minutes. If this wasn't you, ignore this email.")},
		},
	})
}

func authLoginCodeMail(lang, code string) (string, string) {
	name := common.SystemName
	minutes := strconv.Itoa(common.VerificationValidMinutes)
	subject := common.MailText(lang, name+" 登录验证码 "+code, "Your "+name+" sign-in code: "+code)
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "SIGN IN",
		Preheader: common.MailText(lang, "验证码 "+minutes+" 分钟内有效", "The code expires in "+minutes+" minutes"),
		Title:     common.MailText(lang, "登录 "+name, "Sign in to "+name),
		Lead: common.MailText(lang,
			"输入下面的验证码登录。这个邮箱还没有账号的话，会同时为你创建一个。",
			"Enter this code to sign in. If this email has no account yet, one is created for you."),
		Blocks: []common.MailBlock{
			{Code: code},
			{Muted: true, Text: common.MailText(lang,
				"验证码 "+minutes+" 分钟内有效。不是你本人操作的话，忽略这封邮件即可，你的账号不会有任何变化。",
				"The code expires in "+minutes+" minutes. If this wasn't you, ignore this email. Nothing about your account changes.")},
		},
	})
}

func authPasswordResetMail(lang, link string) (string, string) {
	name := common.SystemName
	minutes := strconv.Itoa(common.VerificationValidMinutes)
	subject := common.MailText(lang, "重置你的 "+name+" 密码", "Reset your "+name+" password")
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "RESET PASSWORD",
		Preheader: common.MailText(lang, "链接 "+minutes+" 分钟内有效", "The link expires in "+minutes+" minutes"),
		Title:     common.MailText(lang, "重置密码", "Reset your password"),
		Lead:      common.MailText(lang, "有人为这个邮箱申请了重置密码。点下面的按钮设置新密码。", "Someone asked to reset the password for this email. Use the button to set a new one."),
		Blocks: []common.MailBlock{
			{Primary: &common.MailAction{Label: common.MailText(lang, "设置新密码", "Set a new password"), URL: link}},
			{Muted: true, Text: common.MailText(lang,
				"链接 "+minutes+" 分钟内有效，只能使用一次。不是你本人操作的话，忽略这封邮件即可，密码不会改变。",
				"The link works once and expires in "+minutes+" minutes. If this wasn't you, ignore this email and your password stays the same.")},
		},
	})
}

func authPasswordNotUsedMail(lang, providerLabel string) (string, string) {
	name := common.SystemName
	provider := providerLabel
	if provider == "" {
		provider = common.MailText(lang, "第三方账号", "a linked account")
	}
	subject := common.MailText(lang, "你的 "+name+" 账号用 "+provider+" 登录", "Your "+name+" account signs in with "+provider)
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "SIGN IN",
		Preheader: common.MailText(lang, "这个账号没有设置密码", "This account has no password"),
		Title:     common.MailText(lang, "你的账号不用密码登录", "Your account has no password"),
		Lead: common.MailText(lang,
			"这个邮箱的 "+name+" 账号是通过 "+provider+" 创建的，没有设置密码，所以没法重置。直接用 "+provider+" 登录就行。",
			"The "+name+" account for this email was created with "+provider+" and has no password to reset. Sign in with "+provider+" instead."),
		Blocks: []common.MailBlock{
			{Primary: &common.MailAction{Label: common.MailText(lang, "去登录", "Sign in"), URL: authEmailBaseURL() + "/login"}},
			{Muted: true, Text: common.MailText(lang, "登录后可以在 个人设置 → 安全 里补一个密码。", "After signing in you can add a password under Settings → Security.")},
		},
	})
}

func authAccountExistsMail(lang string) (string, string) {
	name := common.SystemName
	base := authEmailBaseURL()
	subject := common.MailText(lang, "这个邮箱已经有 "+name+" 账号了", "This email already has a "+name+" account")
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "SIGN IN",
		Preheader: common.MailText(lang, "直接登录，或者重置密码", "Sign in, or reset your password"),
		Title:     common.MailText(lang, "这个邮箱已经注册过", "You already have an account"),
		Lead: common.MailText(lang,
			"有人用这个邮箱申请注册，但它已经有一个 "+name+" 账号了。直接登录就行；忘了密码的话可以重置。",
			"Someone tried to sign up with this email, but it already has a "+name+" account. Sign in, or reset your password if you forgot it."),
		Blocks: []common.MailBlock{
			{Primary: &common.MailAction{Label: common.MailText(lang, "去登录", "Sign in"), URL: base + "/login"},
				Secondary: &common.MailAction{Label: common.MailText(lang, "忘记密码", "Forgot password"), URL: base + "/reset"}},
			{Muted: true, Text: common.MailText(lang, "不是你本人操作的话，忽略这封邮件即可。", "If this wasn't you, ignore this email.")},
		},
	})
}

func authRegistrationClosedMail(lang string) (string, string) {
	name := common.SystemName
	subject := common.MailText(lang, name+" 暂未开放注册", name+" isn't open for sign-up yet")
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "SIGN UP",
		Preheader: common.MailText(lang, "收到团队邀请后即可加入", "You can join once a team invites you"),
		Title:     common.MailText(lang, name+" 暂未开放注册", name+" isn't open for sign-up yet"),
		Lead: common.MailText(lang,
			"你用这个邮箱申请了验证码，但 "+name+" 目前只接受受邀用户。收到团队邀请后，用邀请里的链接加入即可。",
			"You asked for a code with this email, but "+name+" only accepts invited people for now. When a team invites you, join from the link in the invitation."),
		Blocks: []common.MailBlock{
			{Muted: true, Text: common.MailText(lang, "不是你本人操作的话，忽略这封邮件即可。", "If this wasn't you, ignore this email.")},
		},
	})
}

func authWelcomeMail(lang, displayName, providerLabel string) (string, string) {
	name := common.SystemName
	apiBase := authEmailAPIBaseURL() + "/v1"
	base := authEmailBaseURL()
	title := common.MailText(lang, "欢迎来到 "+name, "Welcome to "+name)
	if displayName = strings.TrimSpace(displayName); displayName != "" {
		title = common.MailText(lang, "欢迎来到 "+name+"，"+displayName, "Welcome to "+name+", "+displayName)
	}
	hint := common.MailText(lang, "把 API Key 当作密码保管，泄露后可以在控制台随时吊销。", "Keep your API Key like a password. You can revoke it from the console at any time.")
	if providerLabel != "" {
		provider := providerLabel
		if providerLabel == emailAuthProviderLabel() {
			provider = common.MailText(lang, "邮箱验证码", "an email code")
		}
		hint = common.MailText(lang,
			"你通过 "+provider+" 注册。建议再设一个密码作为备用登录方式：个人设置 → 安全。",
			"You signed up with "+provider+". Add a password as a backup way to sign in under Settings → Security.")
	}
	snippet := "curl " + apiBase + "/chat/completions \\\n" +
		"  -H \"Authorization: Bearer $LAZU_API_KEY\" \\\n" +
		"  -H \"Content-Type: application/json\" \\\n" +
		"  -d '{\"model\": \"gpt-6-luna\",\n       \"messages\": [{\"role\": \"user\", \"content\": \"hi\"}]}'"
	subject := common.MailText(lang, "欢迎来到 "+name+"，三步发出第一个请求", "Welcome to "+name+": your first request in three steps")
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "WELCOME",
		Preheader: common.MailText(lang, "三步发出第一个请求", "Your first request in three steps"),
		Title:     title,
		Lead: common.MailText(lang,
			"一个 API Key，就能调用 OpenAI、Anthropic、Google、DeepSeek 等模型。三步发出第一个请求：",
			"One API Key reaches models from OpenAI, Anthropic, Google, DeepSeek and more. Your first request takes three steps:"),
		Blocks: []common.MailBlock{
			{Steps: []common.MailStep{
				{Text: common.MailText(lang, "在控制台创建一把 API Key", "Create an API Key in the console")},
				{Text: common.MailText(lang, "把 Base URL 换成", "Point your Base URL at"), Mono: apiBase},
				{Text: common.MailText(lang, "用原来的 SDK 发请求，模型名照写", "Send requests with your existing SDK and model names")},
			}},
			{Snippet: snippet},
			{Primary: &common.MailAction{Label: common.MailText(lang, "打开控制台", "Open console"), URL: base + "/console"},
				Secondary: &common.MailAction{Label: common.MailText(lang, "阅读文档", "Read the docs"), URL: base + "/docs"}},
			{Muted: true, Text: hint},
		},
	})
}

func authPasswordChangedMail(lang, account string, changedAt time.Time, source string) (string, string) {
	name := common.SystemName
	subject := common.MailText(lang, "你的 "+name+" 密码已修改", "Your "+name+" password was changed")
	return subject, renderAuthMail(common.Mail{
		Lang: lang, Eyebrow: "SECURITY",
		Preheader: common.MailText(lang, "不是你本人？立即重置", "Not you? Reset it now"),
		Title:     common.MailText(lang, "你的密码刚刚被修改", "Your password was just changed"),
		Lead:      common.MailText(lang, "如果是你本人操作，不用做任何事。", "If this was you, there is nothing to do."),
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: common.MailText(lang, "账号", "Account"), Value: account, Mono: true},
				{Label: common.MailText(lang, "时间", "Time"), Value: changedAt.Format("2006-01-02 15:04 MST")},
				{Label: common.MailText(lang, "来源", "From"), Value: source},
			}},
			{Text: common.MailText(lang, "不是你本人？", "Not you?"),
				LinkLabel: common.MailText(lang, "立即重置密码", "Reset your password"), LinkURL: authEmailBaseURL() + "/reset",
				TextAfter: common.MailText(lang, " 重置后其他设备会全部退出登录。", " Resetting signs out every other device.")},
		},
	})
}

// sendAuthEmailAsync delivers identity emails off the request path. These are
// notifications: a delivery failure must never fail the action that caused it.
var authEmailDeliveryWaitGroup sync.WaitGroup
var deliverAuthEmail = func(subject string, receiver string, content string) error {
	return common.SendMail(common.MailMeta{Kind: common.MailKindAuth, Sensitive: true}, subject, receiver, content)
}

func enqueueAuthEmailJob(name string, job func()) {
	if job == nil {
		return
	}
	authEmailDeliveryWaitGroup.Add(1)
	common.SafeGo(name, func() {
		defer authEmailDeliveryWaitGroup.Done()
		job()
	})
}

func sendAuthEmail(subject string, receiver string, content string) {
	receiver = strings.TrimSpace(receiver)
	if receiver == "" {
		return
	}
	if err := deliverAuthEmail(subject, receiver, content); err != nil {
		common.SysLog(fmt.Sprintf("failed to send auth email to %s: %v", receiver, err))
	}
}

func sendAuthEmailAsync(subject string, receiver string, content string) {
	receiver = strings.TrimSpace(receiver)
	if receiver == "" {
		return
	}
	enqueueAuthEmailJob("auth-email-delivery", func() {
		sendAuthEmail(subject, receiver, content)
	})
}

// WaitForAuthEmailDeliveries joins every accepted authentication-email job.
// The API shutdown path calls this only after http.Server.Shutdown has drained
// handlers, so no new request can race a fresh WaitGroup.Add after the wait
// begins. The context bounds rollout shutdowns when an email provider stalls.
func WaitForAuthEmailDeliveries(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		authEmailDeliveryWaitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForAuthEmailDeliveries keeps test cleanup concise.
func waitForAuthEmailDeliveries() {
	_ = WaitForAuthEmailDeliveries(context.Background())
}

// SendWelcomeEmail notifies a freshly created account. providerLabel is empty
// for password registration and names the provider for social sign-up.
// surface is the product the browser signed up through; the welcome mail is
// sent before the session records it on the account.
func SendWelcomeEmail(user *model.User, providerLabel string, lang string, surface string) {
	if user == nil || strings.TrimSpace(user.Email) == "" {
		return
	}
	displayName := strings.TrimSpace(user.DisplayName)
	if displayName == "" {
		displayName = user.Username
	}
	subject, body := authWelcomeMail(lang, displayName, providerLabel)
	if welcome, ok := surfaceWelcomeMails[model.NormalizeUserHomeSurface(surface)]; ok {
		subject, body = welcome(lang, displayName)
	}
	sendAuthEmailAsync(subject, user.Email, body)
}

// sendPasswordChangedEmail tells the owner that credentials changed, so a
// compromised mailbox surfaces immediately instead of silently.
func sendPasswordChangedEmail(c *gin.Context, email string, account string, lang string) {
	if strings.TrimSpace(email) == "" {
		return
	}
	source := common.MailText(lang, "未知来源", "Unknown")
	if c != nil && c.Request != nil {
		userAgent := strings.TrimSpace(c.Request.UserAgent())
		clientIP := strings.TrimSpace(common.GetRealClientIP(c))
		switch {
		case userAgent != "" && clientIP != "":
			source = fmt.Sprintf("%s · %s", truncateRunes(userAgent, 60), clientIP)
		case clientIP != "":
			source = clientIP
		case userAgent != "":
			source = truncateRunes(userAgent, 60)
		}
	}
	subject, body := authPasswordChangedMail(lang, account, time.Now(), source)
	sendAuthEmailAsync(subject, email, body)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// authMailPreviews renders every identity mail with sample data.
func authMailPreviews(lang string) map[string]string {
	pick := func(_ string, body string) string { return body }
	previews := map[string]string{
		"verification":        pick(authVerificationMail(lang, "482913")),
		"login_code":          pick(authLoginCodeMail(lang, "482913")),
		"welcome":             pick(authWelcomeMail(lang, "Grace", "GitHub")),
		"password_reset":      pick(authPasswordResetMail(lang, authEmailBaseURL()+"/user/reset?email=user%40example.com&token=rk_7f2c1d")),
		"password_changed":    pick(authPasswordChangedMail(lang, "grace", time.Now(), "Chrome · macOS · 101.80.0.1")),
		"password_not_used":   pick(authPasswordNotUsedMail(lang, "GitHub")),
		"account_exists":      pick(authAccountExistsMail(lang)),
		"registration_closed": pick(authRegistrationClosedMail(lang)),
	}
	for surface, welcome := range surfaceWelcomeMails {
		previews["welcome_"+surface] = pick(welcome(lang, "Grace"))
	}
	return previews
}

// PreviewAuthEmail renders an identity email with sample data so the templates
// can be reviewed in a browser without sending mail. Root only. ?lang=en shows
// the English version.
func PreviewAuthEmail(c *gin.Context) {
	content, ok := authMailPreviews(common.MailLang(c.DefaultQuery("lang", "zh")))[c.Param("template")]
	if !ok {
		httperr.Code(c, types.ErrorCodeResourceNotFound, types.D{"resource": "email template"})
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(content))
}
