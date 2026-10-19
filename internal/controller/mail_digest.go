package controller

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func mailOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func mailError(c *gin.Context, status int, code string) {
	httperr.Write(c, types.NewErrorWithStatusCode(nil, types.ErrorCode(code), status))
}

// ----- public: one-click opt-out and the digest chart ---------------------------

var mailOptOutPage = template.Must(template.New("optout").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow"><title>{{.Title}} · Lazu</title>
<style>
body{margin:0;background:#f2f2ef;font:15px/1.6 -apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Arial,sans-serif;color:#22242a}
main{max-width:480px;margin:12vh auto 0;padding:40px 40px 44px;background:#fdfdfc;border:1px solid #e5e5e3;border-radius:16px}
h1{margin:0 0 12px;font-size:22px;font-weight:600}p{margin:0 0 24px;color:#5f646d}
button{height:44px;padding:0 24px;border:0;border-radius:10px;background:#202329;color:#fbfaf7;font:500 15px/44px inherit;cursor:pointer}
a{color:#4a66bd;text-decoration:none}.foot{margin-top:28px;font-size:13px;color:#696d75}
@media (prefers-color-scheme:dark){body{background:#171718;color:#f1f1f2}main{background:#232325;border-color:#414145}p,.foot{color:#a4a4a7}button{background:#f1f1f2;color:#1a1a1b}a{color:#9fb6f7}}
@media (max-width:560px){main{margin:16px;padding:28px}}
</style></head><body><main>
<h1>{{.Title}}</h1><p>{{.Body}}</p>
{{if .Confirm}}<form method="post" action="{{.Action}}"><button type="submit">{{.Button}}</button></form>{{end}}
<div class="foot"><a href="{{.SettingsURL}}">{{.SettingsLabel}}</a></div>
</main></body></html>`))

type mailOptOutView struct {
	Lang, Title, Body, Button, Action, SettingsURL, SettingsLabel string
	Confirm                                                       bool
}

func mailOptOutName(kind, lang string) string {
	switch kind {
	case service.MailOptOutWeekly:
		return common.MailText(lang, "周报", "the weekly usage digest")
	}
	return common.MailText(lang, "产品更新邮件", "product update mail")
}

func renderMailOptOut(c *gin.Context, status int, view mailOptOutView) {
	view.SettingsURL = common.MailSiteURL("/console/personal")
	view.SettingsLabel = common.MailText(view.Lang, "打开通知设置 →", "Open notification settings →")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = mailOptOutPage.Execute(c.Writer, view)
}

func mailOptOutLang(userID int) string {
	if user, err := model.GetUserById(userID, true); err == nil {
		return common.MailLang(user.GetSetting().Language)
	}
	return common.MailDefaultLang
}

// MailUnsubscribePage shows what the link switches off. It changes nothing:
// mail scanners open every link, so only the button's POST applies it.
func MailUnsubscribePage(c *gin.Context) {
	token := c.Query("t")
	userID, kind, ok := service.ParseMailOptOutToken(token)
	if !ok {
		renderMailOptOut(c, http.StatusBadRequest, mailOptOutView{Lang: common.MailLang(c.GetHeader("Accept-Language")),
			Title: common.MailText(common.MailLang(c.GetHeader("Accept-Language")), "链接无效", "This link is not valid"),
			Body:  common.MailText(common.MailLang(c.GetHeader("Accept-Language")), "可以在控制台的通知设置里关闭邮件。", "You can turn mail off in the console's notification settings.")})
		return
	}
	lang := mailOptOutLang(userID)
	name := mailOptOutName(kind, lang)
	renderMailOptOut(c, http.StatusOK, mailOptOutView{Lang: lang, Confirm: true, Action: "/api/mail/unsubscribe?t=" + token,
		Title:  common.MailText(lang, "关闭"+name+"？", "Turn off "+name+"?"),
		Body:   common.MailText(lang, "关闭后不再收到"+name+"，其他邮件照常。随时可以在通知设置里重新打开。", "You will stop getting "+name+"; other mail is unaffected. You can turn it back on in notification settings."),
		Button: common.MailText(lang, "关闭", "Turn off")})
}

// MailUnsubscribeApply switches the mail off. Mail clients' own one-click
// button (RFC 8058) posts here too.
func MailUnsubscribeApply(c *gin.Context) {
	userID, kind, ok := service.ParseMailOptOutToken(c.Query("t"))
	if !ok {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := service.ApplyMailOptOut(userID, kind, true); err != nil {
		common.SysError("apply mail opt-out: " + err.Error())
		c.Status(http.StatusInternalServerError)
		return
	}
	lang := mailOptOutLang(userID)
	name := mailOptOutName(kind, lang)
	renderMailOptOut(c, http.StatusOK, mailOptOutView{Lang: lang,
		Title: common.MailText(lang, "已关闭"+name, "Turned off "+name),
		Body:  common.MailText(lang, "之后不会再收到"+name+"。想重新打开，去通知设置。", "You won't get "+name+" any more. To turn it back on, visit notification settings.")})
}

// MailDigestChart serves the chart image of an archived digest.
func MailDigestChart(c *gin.Context) {
	id, ok := service.ParseDigestChartToken(c.Param("file"))
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	png, err := service.DigestChartPNG(id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, "image/png", png)
}

// ----- the reader's own settings -------------------------------------------------

type updateMailPrefsRequest struct {
	WeeklyDigest   *bool `json:"weekly_digest"`
	ProductUpdates *bool `json:"product_updates"`
}

func UpdateSelfMailPrefs(c *gin.Context) {
	var req updateMailPrefsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	values := map[string]any{}
	if req.WeeklyDigest != nil {
		values["weekly_digest_off"] = !*req.WeeklyDigest
	}
	if req.ProductUpdates != nil {
		values["product_updates_off"] = !*req.ProductUpdates
	}
	if err := model.SetUserSettingValues(c.GetInt("id"), values); err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, nil)
}

type updateTimezoneRequest struct {
	Timezone string `json:"timezone"`
	// Auto marks the console's own detection: it only fills an empty value
	// and never overrides a zone the reader chose.
	Auto bool `json:"auto"`
}

func UpdateSelfTimezone(c *gin.Context) {
	var req updateTimezoneRequest
	if err := c.ShouldBindJSON(&req); err != nil || !service.ValidTimezone(req.Timezone) {
		httperr.Invalid(c, "timezone")
		return
	}
	values := map[string]any{"timezone": strings.TrimSpace(req.Timezone)}
	var err error
	if req.Auto {
		err = model.FillUserSettingValues(c.GetInt("id"), values)
	} else {
		err = model.SetUserSettingValues(c.GetInt("id"), values)
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, nil)
}

func digestKindParam(c *gin.Context) (string, bool) {
	switch kind := c.DefaultQuery("kind", service.DigestWeekly); kind {
	case service.DigestWeekly, service.DigestMonthly:
		return kind, true
	}
	httperr.Invalid(c, "kind")
	return "", false
}

// GetSelfDigestPreview renders the reader's most recent digest.
func GetSelfDigestPreview(c *gin.Context) {
	kind, ok := digestKindParam(c)
	if !ok {
		return
	}
	subject, html, err := service.RenderDigestPreview(c.GetInt("id"), kind, time.Now(), "", false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, gin.H{"subject": subject, "html": html})
}

// ----- operators ------------------------------------------------------------------

type adminMailRow struct {
	model.MailDelivery
	Username string `json:"username"`
}

func AdminListMailDeliveries(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	page = max(page, 1)
	size = min(max(size, 1), 100)
	rows, total, err := model.ListMailDeliveries(model.MailDeliveryQuery{
		Kind: c.Query("kind"), Status: c.Query("status"), Search: c.Query("q"), Period: c.Query("period"),
		Offset: (page - 1) * size, Limit: size,
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, gin.H{"items": attachMailUsernames(rows), "total": total, "page": page, "page_size": size})
}

func attachMailUsernames(rows []model.MailDelivery) []adminMailRow {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.UserID > 0 {
			ids = append(ids, row.UserID)
		}
	}
	names := map[int]string{}
	if len(ids) > 0 {
		var users []struct {
			Id       int
			Username string
		}
		model.CoreDB().
			Model(&model.User{}).Select("id, username").Where("id IN ?", ids).Scan(&users)
		for _, u := range users {
			names[u.Id] = u.Username
		}
	}
	out := make([]adminMailRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminMailRow{MailDelivery: row, Username: names[row.UserID]})
	}
	return out
}

func mailDeliveryParam(c *gin.Context) (*model.MailDelivery, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httperr.Invalid(c, "id")
		return nil, false
	}
	row, err := model.GetMailDelivery(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		mailError(c, http.StatusNotFound, "mail_not_found")
		return nil, false
	}
	if err != nil {
		httperr.Write(c, err)
		return nil, false
	}
	return row, true
}

// AdminGetMailDelivery returns one archived mail with its body. Opening a
// body is written to the system log: it is a customer's mail.
func AdminGetMailDelivery(c *gin.Context) {
	row, ok := mailDeliveryParam(c)
	if !ok {
		return
	}
	html, err := row.HTML()
	if err != nil {
		html = ""
	}
	snapshot, err := row.SnapshotJSON()
	if err != nil {
		snapshot = ""
	}
	if html != "" {
		common.SysLog("mail archive: admin " + strconv.Itoa(c.GetInt("id")) + " opened mail " + strconv.FormatUint(row.ID, 10))
	}
	rows := attachMailUsernames([]model.MailDelivery{*row})
	mailOK(c, gin.H{"mail": rows[0], "html": html, "snapshot": snapshot})
}

func AdminResendMailDelivery(c *gin.Context) {
	row, ok := mailDeliveryParam(c)
	if !ok {
		return
	}
	if err := service.ResendArchivedMail(row.ID, ""); err != nil {
		mailError(c, http.StatusBadRequest, "mail_resend_failed")
		return
	}
	mailOK(c, nil)
}

func adminEmail(c *gin.Context) (string, bool) {
	user, err := model.GetUserById(c.GetInt("id"), true)
	if err != nil || strings.TrimSpace(user.Email) == "" {
		mailError(c, http.StatusBadRequest, "admin_email_missing")
		return "", false
	}
	return user.Email, true
}

func AdminCopyMailDelivery(c *gin.Context) {
	row, ok := mailDeliveryParam(c)
	if !ok {
		return
	}
	email, ok := adminEmail(c)
	if !ok {
		return
	}
	if err := service.ResendArchivedMail(row.ID, email); err != nil {
		mailError(c, http.StatusBadRequest, "mail_resend_failed")
		return
	}
	mailOK(c, gin.H{"receiver": email})
}

func AdminRegenerateMailDelivery(c *gin.Context) {
	row, ok := mailDeliveryParam(c)
	if !ok {
		return
	}
	if err := service.RegenerateDigest(row.ID, time.Now()); err != nil {
		mailError(c, http.StatusBadRequest, "mail_regenerate_failed")
		return
	}
	mailOK(c, nil)
}

// AdminGetDigestOverview is the 用量回顾 tab: the switch, when the next
// digests go out, who they would reach, and past periods.
func AdminGetDigestOverview(c *gin.Context) {
	now := time.Now()
	candidates, err := model.ListDigestCandidates()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var weeklyOff int
	for _, cand := range candidates {
		setting := (&model.User{Setting: cand.Setting}).GetSetting()
		if setting.WeeklyDigestOff {
			weeklyOff++
		}
	}
	type runRow struct {
		Kind   string `json:"kind"`
		Period string `json:"period"`
		Status string `json:"status"`
		Error  string `json:"reason"`
		Count  int64  `json:"count"`
		First  int64  `json:"first_at"`
	}
	var runs []runRow
	model.CoreDB().
		Model(&model.MailDelivery{}).
		Select("kind, period, status, error, COUNT(*) AS count, MIN(created_at) AS first").
		Where("kind IN ? AND ref_key IS NOT NULL AND created_at > ?", []string{common.MailKindWeeklyDigest, common.MailKindMonthlyDigest}, now.AddDate(0, -4, 0).Unix()).
		Group("kind, period, status, error").Scan(&runs)
	for i := range runs {
		if runs[i].Status != model.MailStatusSkipped {
			runs[i].Error = ""
		}
	}
	next := func(kind string, zone string) int64 {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return 0
		}
		local := now.In(loc)
		day := digestNextSendDay(kind, local)
		return day.Add(9*time.Hour + 30*time.Minute).Unix()
	}
	mailOK(c, gin.H{
		"enabled":    common.UsageDigestEnabled,
		"settings":   service.DigestSettingsNow(),
		"candidates": len(candidates),
		"weekly_off": weeklyOff,
		"next": gin.H{
			"weekly":  gin.H{"asia_shanghai": next(service.DigestWeekly, "Asia/Shanghai"), "america_los_angeles": next(service.DigestWeekly, "America/Los_Angeles")},
			"monthly": gin.H{"asia_shanghai": next(service.DigestMonthly, "Asia/Shanghai"), "america_los_angeles": next(service.DigestMonthly, "America/Los_Angeles")},
		},
		"runs": runs,
	})
}

func digestNextSendDay(kind string, local time.Time) time.Time {
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	sendAt := func(d time.Time) time.Time { return d.Add(9*time.Hour + 30*time.Minute) }
	if kind == service.DigestMonthly {
		first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
		if local.Before(sendAt(first)) {
			return first
		}
		return first.AddDate(0, 1, 0)
	}
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	if local.Before(sendAt(monday)) {
		return monday
	}
	return monday.AddDate(0, 0, 7)
}

type setDigestRequest struct {
	Enabled  *bool                   `json:"enabled"`
	Settings *service.DigestSettings `json:"settings"`
}

// AdminSetDigest switches the digests on or off and saves their thresholds.
func AdminSetDigest(c *gin.Context) {
	var req setDigestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if req.Settings != nil {
		if err := service.SaveDigestSettings(*req.Settings); err != nil {
			mailError(c, http.StatusBadRequest, "digest_settings_invalid")
			return
		}
	}
	if req.Enabled != nil {
		if err := model.UpdateOption("UsageDigestEnabled", strconv.FormatBool(*req.Enabled)); err != nil {
			httperr.Write(c, err)
			return
		}
	}
	mailOK(c, gin.H{"enabled": common.UsageDigestEnabled, "settings": service.DigestSettingsNow()})
}

func adminDigestUser(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("user"))
	if raw == "" {
		return c.GetInt("id"), true
	}
	if id, err := strconv.Atoi(raw); err == nil && id > 0 {
		return id, true
	}
	var user model.User
	if err := model.CoreDB().Select("id").Where("email = ? OR username = ?", raw, raw).First(&user).Error; err != nil {
		mailError(c, http.StatusNotFound, "user_not_found")
		return 0, false
	}
	return user.Id, true
}

func AdminPreviewDigest(c *gin.Context) {
	kind, ok := digestKindParam(c)
	if !ok {
		return
	}
	userID, ok := adminDigestUser(c)
	if !ok {
		return
	}
	lang := c.Query("lang")
	if lang != "" {
		lang = common.MailLang(lang)
	}
	subject, html, err := service.RenderDigestPreview(userID, kind, time.Now(), lang, c.Query("upcoming") == "1")
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, gin.H{"user_id": userID, "subject": subject, "html": html})
}

func AdminCopyDigest(c *gin.Context) {
	kind, ok := digestKindParam(c)
	if !ok {
		return
	}
	userID, ok := adminDigestUser(c)
	if !ok {
		return
	}
	email, ok := adminEmail(c)
	if !ok {
		return
	}
	if err := service.SendDigestCopy(userID, kind, email, time.Now()); err != nil {
		mailError(c, http.StatusBadRequest, "mail_send_failed")
		return
	}
	mailOK(c, gin.H{"receiver": email})
}

// AdminGenerateDigestDrafts files this period's digests as drafts in the
// archive. Nothing is sent.
func AdminGenerateDigestDrafts(c *gin.Context) {
	kind, ok := digestKindParam(c)
	if !ok {
		return
	}
	made, err := service.GenerateDigestDrafts(kind, time.Now())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, gin.H{"drafts": made})
}

// AdminListDigestDrafts lists every reader's upcoming digest with the
// figures so far. Nothing is sent or archived.
func AdminListDigestDrafts(c *gin.Context) {
	kind, ok := digestKindParam(c)
	if !ok {
		return
	}
	drafts, err := service.DigestDrafts(kind, time.Now())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mailOK(c, gin.H{"items": drafts})
}
