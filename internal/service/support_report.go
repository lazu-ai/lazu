package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// Support reports: the console's 报告问题 sends what the reader saw (the
// error number and request id from the toast, the page) with their own words
// to the support address. The server adds what it knows for certain: who
// sent it, and the request itself when the id belongs to them. The mail
// carries Reply-To, so answering it reaches the reader. There is no ticket
// table; the support inbox is the queue.

const (
	supportReportMaxRunes   = 4000
	supportReportPerHour    = 5
	supportLookupWindowDays = 30
)

var (
	ErrSupportUnavailable  = errors.New("no support address is configured")
	ErrSupportRateLimited  = errors.New("too many support reports")
	requestIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_\-]{6,64}$`)
	lzPattern              = regexp.MustCompile(`^LZ-\d{4}$`)
	supportPagePattern     = regexp.MustCompile(`^/[^\s]{0,300}$`)
	supportReportCountsMem = struct {
		sync.Mutex
		window map[int]time.Time
		count  map[int]int
	}{window: map[int]time.Time{}, count: map[int]int{}}
)

// SupportReport is what the reader sends.
type SupportReport struct {
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Error     string `json:"error"` // LZ-####
	Page      string `json:"page"`
}

// SupportReportInvalid names the field a report was refused for.
type SupportReportInvalid struct{ Reason string }

func (e *SupportReportInvalid) Error() string { return "support report invalid: " + e.Reason }

// Validate trims a report and checks its fields.
func (r *SupportReport) Validate() error {
	r.Message = strings.TrimSpace(r.Message)
	r.RequestID = strings.TrimSpace(r.RequestID)
	r.Error = strings.ToUpper(strings.TrimSpace(r.Error))
	r.Page = strings.TrimSpace(r.Page)
	n := utf8.RuneCountInString(r.Message)
	switch {
	case n < 2 || n > supportReportMaxRunes:
		return &SupportReportInvalid{"message must be 2 to 4000 characters"}
	case r.RequestID != "" && !requestIDPattern.MatchString(r.RequestID):
		return &SupportReportInvalid{"request_id is not a request id"}
	case r.Error != "" && !lzPattern.MatchString(r.Error):
		return &SupportReportInvalid{"error must look like LZ-1234"}
	case r.Page != "" && !supportPagePattern.MatchString(r.Page):
		return &SupportReportInvalid{"page must be a path"}
	}
	return nil
}

// supportFamily is the triage tag for an error number: who can act on it.
func supportFamily(lz string) string {
	if len(lz) != 7 {
		return "其他"
	}
	switch {
	case strings.HasPrefix(lz, "LZ-1"):
		return "请求"
	case strings.HasPrefix(lz, "LZ-22"):
		return "登录"
	case strings.HasPrefix(lz, "LZ-2"):
		return "密钥与权限"
	case strings.HasPrefix(lz, "LZ-3"):
		return "钱包"
	case strings.HasPrefix(lz, "LZ-4"):
		return "模型与线路"
	case strings.HasPrefix(lz, "LZ-5"):
		return "限流"
	case strings.HasPrefix(lz, "LZ-6"):
		return "上游"
	case strings.HasPrefix(lz, "LZ-7"):
		return "控制台"
	case strings.HasPrefix(lz, "LZ-9"):
		return "内部"
	}
	return "其他"
}

// allowSupportReport counts reports per account per hour.
func allowSupportReport(ctx context.Context, userID int) bool {
	if common.RedisEnabled && common.RDB != nil {
		key := fmt.Sprintf("lazu:support:report:%d:%s", userID, time.Now().UTC().Format("2006010215"))
		n, err := common.RDB.Incr(ctx, key).Result()
		if err == nil {
			if n == 1 {
				_ = common.RDB.Expire(ctx, key, time.Hour).Err()
			}
			return n <= supportReportPerHour
		}
	}
	m := &supportReportCountsMem
	m.Lock()
	defer m.Unlock()
	if until, ok := m.window[userID]; !ok || time.Now().After(until) {
		m.window[userID], m.count[userID] = time.Now().Add(time.Hour), 0
	}
	m.count[userID]++
	return m.count[userID] <= supportReportPerHour
}

// supportRequestFacts looks up the reported request when it belongs to the
// reader, within the last month (the logs are partitioned by time).
func supportRequestFacts(userID int, requestID string) []common.MailFact {
	if requestID == "" || model.TelemetryDB() == nil {
		return nil
	}
	var row model.Log
	since := time.Now().Add(-supportLookupWindowDays * 24 * time.Hour).Unix()
	err := model.TelemetryDB().Where("request_id = ? AND user_id = ? AND created_at >= ?", requestID, userID, since).
		Order("created_at desc").Limit(1).Find(&row).Error
	if err != nil {
		return []common.MailFact{{Label: "请求记录", Value: "暂时读不到"}}
	}
	if row.RequestID == "" {
		return []common.MailFact{{Label: "请求记录", Value: "30 天内没有这个账户的这条请求"}}
	}
	facts := []common.MailFact{
		{Label: "请求时间（UTC）", Value: time.Unix(row.CreatedAt, 0).UTC().Format("2006-01-02 15:04:05")},
		{Label: "模型", Value: row.ModelName, Mono: true},
	}
	if row.Type == model.LogTypeError {
		facts = append(facts, common.MailFact{Label: "失败原因", Value: truncateRunes(strings.TrimSpace(row.Content), 300)})
	}
	return facts
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// SubmitSupportReport mails a report to the support address.
func SubmitSupportReport(ctx context.Context, user *model.User, report SupportReport, userAgent string) error {
	if err := report.Validate(); err != nil {
		return err
	}
	receiver := common.SiteSupportEmail()
	if receiver == "" || !common.EmailTransportConfigured() {
		return ErrSupportUnavailable
	}
	if !allowSupportReport(ctx, user.Id) {
		return ErrSupportRateLimited
	}

	facts := []common.MailFact{
		{Label: "账户", Value: fmt.Sprintf("%s（ID %d）", user.Username, user.Id)},
		{Label: "邮箱", Value: user.Email},
	}
	subjectParts := []string{"[Lazu support]"}
	if report.Error != "" {
		subjectParts = append(subjectParts, supportFamily(report.Error), report.Error)
		fact := common.MailFact{Label: "错误编号", Value: report.Error, Mono: true}
		for code, spec := range types.Catalog {
			if spec.ID == report.Error {
				fact.Value = report.Error + " · " + string(code)
				break
			}
		}
		facts = append(facts, fact)
	}
	subjectParts = append(subjectParts, user.Username)
	if report.RequestID != "" {
		facts = append(facts, common.MailFact{Label: "请求 ID", Value: report.RequestID, Mono: true})
		facts = append(facts, supportRequestFacts(user.Id, report.RequestID)...)
	}
	if report.Page != "" {
		facts = append(facts, common.MailFact{Label: "页面", Value: report.Page, Mono: true})
	}
	if ua := strings.TrimSpace(userAgent); ua != "" {
		facts = append(facts, common.MailFact{Label: "浏览器", Value: truncateRunes(ua, 160)})
	}

	mail := common.Mail{
		Tier: common.MailOps, Source: "SUPPORT", Severity: "NOTICE",
		Title:     "控制台问题报告",
		Preheader: truncateRunes(report.Message, 80),
		Blocks: []common.MailBlock{
			{Text: report.Message},
			{Facts: facts},
			{Muted: true, Text: "直接回复这封邮件会发给报告人。"},
		},
	}
	body, err := common.RenderMail(mail)
	if err != nil {
		return err
	}
	meta := common.MailMeta{Kind: common.MailKindOther, UserID: user.Id, ReplyTo: strings.TrimSpace(user.Email)}
	return common.SendMail(meta, strings.Join(subjectParts, " "), receiver, body)
}
