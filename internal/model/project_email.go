package model

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// Team mail is paper-tier (brand/foundations/emails.md) and written in the
// recipient's console language.

func projectMailBudget(lang string, budget *billing.MicroUSD) string {
	if budget == nil {
		return common.MailText(lang, "不限", "No limit")
	}
	return common.MailUSD(budget.Dollars())
}

func projectMailRole(lang, role string) string {
	switch role {
	case ProjectRoleOwner:
		return common.MailText(lang, "所有者", "Owner")
	case ProjectRoleAdmin:
		return common.MailText(lang, "管理员", "Admin")
	}
	return common.MailText(lang, "成员", "Member")
}

func projectMailStatus(lang, status string) string {
	switch status {
	case ProjectMemberActive:
		return common.MailText(lang, "正常", "Active")
	case ProjectMemberSuspended:
		return common.MailText(lang, "已暂停", "Suspended")
	case ProjectMemberRemoved:
		return common.MailText(lang, "已离开", "Removed")
	}
	return status
}

func userMailNameTx(tx *gorm.DB, userID int) string {
	if userID <= 0 {
		return ""
	}
	var user User
	if err := tx.Select("username", "display_name").First(&user, userID).Error; err != nil {
		return ""
	}
	if name := strings.TrimSpace(user.DisplayName); name != "" {
		return name
	}
	return user.Username
}

func projectMailTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func projectConsoleURL(baseURL, path string, project Project) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = strings.TrimSuffix(common.MailSiteURL(""), "/")
	}
	return base + path + "?project_id=" + url.QueryEscape(project.PublicID)
}

func renderProjectMail(m common.Mail) (string, error) {
	m.Tier = common.MailPaper
	return common.RenderMail(m)
}

func queueProjectInviteTx(tx *gorm.DB, project Project, invite ProjectInvite, raw, baseURL string) error {
	var inviter User
	if err := tx.Select("id", "username", "display_name", "email", "setting").First(&inviter, invite.InvitedBy).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// The invitee may have no account yet; the invitation reads in the
	// inviter's language.
	lang := common.MailLang(inviter.GetSetting().Language)
	name := strings.TrimSpace(inviter.DisplayName)
	if name == "" {
		name = inviter.Username
	}
	role := projectMailRole(lang, invite.Role)
	lead := common.MailText(lang,
		"邀请你以"+role+"身份加入。加入后用项目的钱包调用模型，费用由项目所有者支付。",
		"You're invited to join as "+strings.ToLower(role)+". Requests you make are paid from the project's wallet by its owner.")
	subject := common.MailText(lang, "邀请你加入 "+project.Name, "You're invited to "+project.Name)
	if name != "" {
		lead = common.MailText(lang,
			name+" 邀请你以"+role+"身份加入。加入后用项目的钱包调用模型，费用由项目所有者支付。",
			name+" invited you to join as "+strings.ToLower(role)+". Requests you make are paid from the project's wallet by its owner.")
		subject = common.MailText(lang, name+" 邀请你加入 "+project.Name, name+" invited you to "+project.Name)
	}
	deadline := time.Unix(invite.EffectiveExpiresAt(), 0)
	expires := projectMailTime(deadline)
	link := strings.TrimRight(baseURL, "/") + "/invite/" + raw
	noAccount := common.MailText(lang,
		"没有 Lazu 账户也能接受：用这个邮箱收一个验证码，账户会自动建好。",
		"Don't have a Lazu account? Get a code at this address and we'll create one.")
	body, err := renderProjectMail(common.Mail{
		Lang: lang, Eyebrow: "INVITATION",
		Preheader: common.MailText(lang, "有效至 "+expires, "Valid until "+expires),
		Title:     common.MailText(lang, "加入 "+project.Name, "Join "+project.Name),
		Lead:      lead,
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: common.MailText(lang, "受邀邮箱", "Invited email"), Value: invite.Email, Mono: true},
				{Label: common.MailText(lang, "角色", "Role"), Value: role},
				{Label: common.MailText(lang, "月预算", "Monthly budget"), Value: projectMailBudget(lang, invite.BudgetMicrousd)},
				{Label: common.MailText(lang, "有效至", "Valid until"), Value: expires},
			}},
			{Primary: &common.MailAction{Label: common.MailText(lang, "接受邀请", "Accept invitation"), URL: link}},
			{Muted: true, Text: noAccount + common.MailText(lang,
				"不认识邀请人的话，忽略这封邮件即可。",
				" If you don't know the sender, ignore this email.")},
		},
	})
	if err != nil {
		return err
	}
	if err := QueueProjectEmailTx(tx, project.ID, 0, projectInviteMailKey(invite.TokenDigest), ProjectEmail{Receiver: invite.Email, Subject: subject, HTML: body}); err != nil {
		return err
	}

	// A day before the link expires, remind the invitee once.
	remindAt := deadline.Add(-projectInviteReminderLead)
	if remindAt.After(time.Now()) {
		reminder := common.MailText(lang, name+" 的邀请明天过期", "Your invitation from "+name+" expires tomorrow")
		if name == "" {
			reminder = common.MailText(lang, "加入 "+project.Name+" 的邀请明天过期", "Your invitation to "+project.Name+" expires tomorrow")
		}
		lead := common.MailText(lang,
			"你还没有接受加入 "+project.Name+" 的邀请。过期后需要对方重新发送。",
			"You haven't accepted the invitation to "+project.Name+" yet. Once it expires, it has to be sent again.")
		if name != "" {
			lead = common.MailText(lang,
				name+" 邀请你加入 "+project.Name+"，你还没有接受。过期后需要 "+name+" 重新发送。",
				name+" invited you to "+project.Name+" and you haven't accepted yet. Once it expires, "+name+" has to send it again.")
		}
		body, err := renderProjectMail(common.Mail{
			Lang: lang, Eyebrow: "INVITATION",
			Preheader: common.MailText(lang, "加入 "+project.Name+"，"+expires+" 前有效", "Join "+project.Name+" before "+expires),
			Title:     common.MailText(lang, "邀请明天过期", "Your invitation expires tomorrow"),
			Lead:      lead,
			Blocks: []common.MailBlock{
				{Facts: []common.MailFact{
					{Label: common.MailText(lang, "受邀邮箱", "Invited email"), Value: invite.Email, Mono: true},
					{Label: common.MailText(lang, "有效至", "Valid until"), Value: expires},
				}},
				{Primary: &common.MailAction{Label: common.MailText(lang, "接受邀请", "Accept invitation"), URL: link}},
				{Muted: true, Text: noAccount},
			},
		})
		if err != nil {
			return err
		}
		if err := queueProjectEmailAtTx(tx, project.ID, 0, projectInviteReminderKey(invite.TokenDigest), ProjectEmail{Receiver: invite.Email, Subject: reminder, HTML: body}, remindAt.Unix()); err != nil {
			return err
		}
	}

	// When it expires unanswered, tell whoever sent it, once.
	if inviter.Id > 0 && strings.TrimSpace(inviter.Email) != "" {
		issued := invite.CreatedAt
		if invite.ResentAt != nil && *invite.ResentAt > issued {
			issued = *invite.ResentAt
		}
		subject := common.MailText(lang, invite.Email+" 没有接受邀请", invite.Email+" didn't accept your invitation")
		body, err := renderProjectMail(common.Mail{
			Lang: lang, Eyebrow: "PROJECT UPDATE",
			Preheader: common.MailText(lang, project.Name+" 的邀请已过期，可以重新发送", "The invitation to "+project.Name+" expired; you can send it again"),
			Title:     common.MailText(lang, "邀请已过期，对方没有接受", "Your invitation expired unanswered"),
			Lead: common.MailText(lang,
				"你发给 "+invite.Email+" 的邀请三天内没有被接受，链接已经失效。还想邀请的话重新发送一次即可；也可以先确认对方常用哪个邮箱。",
				"The invitation you sent to "+invite.Email+" wasn't accepted within three days, so the link no longer works. Send it again if you still want them in, or check which address they use."),
			Blocks: []common.MailBlock{
				{Facts: []common.MailFact{
					{Label: common.MailText(lang, "项目", "Project"), Value: project.Name},
					{Label: common.MailText(lang, "受邀邮箱", "Invited email"), Value: invite.Email, Mono: true},
					{Label: common.MailText(lang, "发出 / 过期", "Sent / expired"), Value: projectMailTime(time.Unix(issued, 0)) + " → " + expires},
				}},
				{Primary: &common.MailAction{Label: common.MailText(lang, "重新发送", "Send it again"), URL: projectConsoleURL(baseURL, "/console/project/members", project) + "&invite=" + url.QueryEscape(invite.PublicID)}},
			},
		})
		if err != nil {
			return err
		}
		if err := queueProjectEmailAtTx(tx, project.ID, inviter.Id, projectInviteExpiredKey(invite.TokenDigest), ProjectEmail{Receiver: inviter.Email, Subject: subject, HTML: body}, deadline.Unix()); err != nil {
			return err
		}
	}
	return nil
}

// The three mails of one invitation link share its digest, so revoking,
// resending, accepting or dissolving cancels all of them together.
const projectInviteReminderLead = 24 * time.Hour

func projectInviteMailKey(digest string) string     { return "invite:" + digest }
func projectInviteReminderKey(digest string) string { return "invite:reminder:" + digest }
func projectInviteExpiredKey(digest string) string  { return "invite:expired:" + digest }

func queueProjectJoinedTx(tx *gorm.DB, project Project, member ProjectMember, invite ProjectInvite, baseURL string) error {
	var user User
	if err := tx.Select("email", "setting").First(&user, member.UserID).Error; err != nil {
		return err
	}
	lang := common.MailLang(user.GetSetting().Language)
	role := projectMailRole(lang, member.Role)
	budget := projectMailBudget(lang, invite.BudgetMicrousd)
	subject := common.MailText(lang, "你已加入 "+project.Name, "You joined "+project.Name)
	body, err := renderProjectMail(common.Mail{
		Lang: lang, Eyebrow: "JOINED",
		Preheader: common.MailText(lang, "角色："+role+" · 月预算 "+budget, "Role: "+role+" · Monthly budget "+budget),
		Title:     subject,
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: common.MailText(lang, "角色", "Role"), Value: role},
				{Label: common.MailText(lang, "月预算", "Monthly budget"), Value: budget},
				{Label: common.MailText(lang, "付款方", "Paid by"), Value: common.MailText(lang, "项目所有者", "Project owner")},
			}},
			{Primary: &common.MailAction{Label: common.MailText(lang, "打开项目", "Open project"), URL: projectConsoleURL(baseURL, "/console/token", project)}},
		},
	})
	if err != nil {
		return err
	}
	return QueueProjectEmailTx(tx, project.ID, member.UserID, "accepted:"+invite.PublicID, ProjectEmail{Receiver: user.Email, Subject: subject, HTML: body})
}

// Member notices: one template, one subject and set of facts per event.
const (
	noticeMembership    = "membership"
	noticeRestored      = "restored"
	noticeBudget        = "budget"
	noticeRemoved       = "removed"
	noticeDissolved     = "dissolved"
	noticeBudgetRequest = "budget_request"
	noticeKeyRotated    = "key_rotated"
	noticeKeyRevealed   = "key_revealed"
)

type projectNotice struct {
	Kind    string
	ActorID int
	Budget  *billing.MicroUSD
	// Budget requests: approved or rejected, and the handler's note.
	Action string
	Note   string
	// Key events.
	TokenName string
}

func queueProjectMemberNoticeTx(tx *gorm.DB, project Project, target ProjectMember, notice projectNotice, eventKey string) error {
	var user User
	if err := tx.Select("id", "email", "setting").First(&user, target.UserID).Error; err != nil {
		return err
	}
	lang := common.MailLang(user.GetSetting().Language)
	subject, body, err := renderProjectNotice(tx, lang, project, target, notice, time.Now())
	if err != nil {
		return err
	}
	return QueueProjectEmailTx(tx, project.ID, target.UserID, eventKey, ProjectEmail{Receiver: user.Email, Subject: subject, HTML: body})
}

func renderProjectNotice(tx *gorm.DB, lang string, project Project, target ProjectMember, notice projectNotice, now time.Time) (string, string, error) {
	p := project.Name
	actor := userMailNameTx(tx, notice.ActorID)
	if notice.ActorID == target.UserID {
		actor = ""
	}
	projectFact := common.MailFact{Label: common.MailText(lang, "项目", "Project"), Value: p}
	timeFact := common.MailFact{Label: common.MailText(lang, "时间", "Time"), Value: projectMailTime(now)}
	byFact := func(zh, en string) []common.MailFact {
		if actor == "" {
			return nil
		}
		return []common.MailFact{{Label: common.MailText(lang, zh, en), Value: actor}}
	}
	openProject := common.MailBlock{Primary: &common.MailAction{Label: common.MailText(lang, "打开项目", "Open project"), URL: projectConsoleURL("", "/console/token", project)}}
	m := common.Mail{Lang: lang, Eyebrow: "PROJECT UPDATE"}
	var subject string

	switch notice.Kind {
	case noticeBudget:
		budget := projectMailBudget(lang, notice.Budget)
		subject = common.MailText(lang, p+" 调整了你的月预算", p+" changed your monthly budget")
		m.Preheader = common.MailText(lang, "新的月预算 "+budget, "New monthly budget: "+budget)
		m.Title = common.MailText(lang, "你的月预算调整了", "Your monthly budget changed")
		m.Lead = common.MailText(lang, "新的月预算立即生效。", "The new budget applies right away.")
		facts := append([]common.MailFact{projectFact, {Label: common.MailText(lang, "月预算", "Monthly budget"), Value: budget}}, byFact("操作人", "Changed by")...)
		m.Blocks = []common.MailBlock{{Facts: facts}, openProject}
	case noticeRemoved:
		subject = common.MailText(lang, "你已离开 "+p, "You left "+p)
		m.Preheader = common.MailText(lang, "项目下的 API Key 已停用", "Your API Keys in this project are off")
		m.Title = subject
		m.Lead = common.MailText(lang,
			"你在这个项目下的 API Key 已经停用。项目之外的账户不受影响。",
			"Your API Keys in this project have stopped working. Nothing outside the project changes.")
		if actor != "" {
			m.Title = common.MailText(lang, actor+" 把你移出了 "+p, actor+" removed you from "+p)
		}
		m.Blocks = []common.MailBlock{
			{Facts: []common.MailFact{projectFact, timeFact}},
			{Muted: true, Text: common.MailText(lang, "觉得是误操作的话，请联系项目管理员。", "If this looks like a mistake, contact the project's admin.")},
		}
	case noticeRestored:
		subject = common.MailText(lang, "你已回到 "+p, "You're back in "+p)
		m.Preheader = common.MailText(lang, "你的成员身份已恢复", "Your membership was restored")
		m.Title = subject
		m.Lead = common.MailText(lang, "之前的移出已被撤销，你的成员身份和 API Key 都已恢复。", "Your removal was undone. Your membership and API Keys are back.")
		m.Blocks = []common.MailBlock{{Facts: []common.MailFact{projectFact, {Label: common.MailText(lang, "角色", "Role"), Value: projectMailRole(lang, target.Role)}}}, openProject}
	case noticeDissolved:
		subject = common.MailText(lang, p+" 已解散", p+" was closed")
		m.Preheader = common.MailText(lang, "项目共享的 API Key 已停用", "The project's API Keys are off")
		m.Title = subject
		m.Lead = common.MailText(lang, "项目所有者解散了这个项目，项目下的 API Key 已经停用。", "The owner closed this project. Its API Keys have stopped working.")
		m.Blocks = []common.MailBlock{{Facts: append([]common.MailFact{projectFact, timeFact}, byFact("操作人", "Closed by")...)}}
	case noticeBudgetRequest:
		approved := notice.Action == "approved"
		if approved {
			subject = common.MailText(lang, "你的预算申请已通过", "Your budget request was approved")
			m.Lead = common.MailText(lang, "新的月预算立即生效。", "The new budget applies right away.")
		} else {
			subject = common.MailText(lang, "你的预算申请没有通过", "Your budget request was declined")
			m.Lead = common.MailText(lang, "月预算保持不变。", "Your monthly budget stays the same.")
		}
		m.Preheader, m.Title = p, subject
		facts := []common.MailFact{projectFact}
		if approved && notice.Budget != nil {
			facts = append(facts, common.MailFact{Label: common.MailText(lang, "月预算", "Monthly budget"), Value: projectMailBudget(lang, notice.Budget)})
		}
		facts = append(facts, byFact("处理人", "Handled by")...)
		if note := strings.TrimSpace(notice.Note); note != "" {
			facts = append(facts, common.MailFact{Label: common.MailText(lang, "说明", "Note"), Value: note})
		}
		m.Blocks = []common.MailBlock{{Facts: facts}, openProject}
	case noticeKeyRotated, noticeKeyRevealed:
		keysLink := common.MailBlock{Primary: &common.MailAction{Label: common.MailText(lang, "查看 API Keys", "View API Keys"), URL: projectConsoleURL("", "/console/token", project)}}
		m.Eyebrow = "SECURITY"
		facts := append([]common.MailFact{projectFact, {Label: "Key", Value: notice.TokenName, Mono: true}}, byFact("操作人", "By")...)
		facts = append(facts, timeFact)
		if notice.Kind == noticeKeyRotated {
			subject = common.MailText(lang, "你的 API Key 被轮换了", "Your API Key was rotated")
			m.Preheader = p + " · " + notice.TokenName
			m.Title = subject
			m.Lead = common.MailText(lang,
				"项目管理员轮换了你名下的一把 Key：旧 Key 到期后停用，请换用新 Key。",
				"A project admin rotated one of your Keys. The old Key stops at its expiry; switch to the new one.")
		} else {
			subject = common.MailText(lang, "管理员查看了你的 API Key", "An admin viewed your API Key")
			m.Preheader = p + " · " + notice.TokenName
			m.Title = subject
			m.Lead = common.MailText(lang,
				"项目管理员查看了你名下一把 Key 的完整内容。这是项目管理员的正常权限，这封邮件只是让你知情。",
				"A project admin viewed the full value of one of your Keys. Admins may do this; this email keeps you informed.")
		}
		m.Blocks = []common.MailBlock{{Facts: facts}, keysLink}
	default:
		subject = common.MailText(lang, "你在 "+p+" 的成员身份有变动", "Your membership in "+p+" changed")
		m.Preheader = projectMailRole(lang, target.Role) + " · " + projectMailStatus(lang, target.Status)
		m.Title = common.MailText(lang, "你的成员身份有变动", "Your membership changed")
		facts := append([]common.MailFact{
			projectFact,
			{Label: common.MailText(lang, "角色", "Role"), Value: projectMailRole(lang, target.Role)},
			{Label: common.MailText(lang, "状态", "Status"), Value: projectMailStatus(lang, target.Status)},
		}, byFact("操作人", "Changed by")...)
		m.Blocks = []common.MailBlock{{Facts: facts}, openProject}
	}
	body, err := renderProjectMail(m)
	return subject, body, err
}

func projectNoticeTokenName(token Token) string {
	if name := strings.TrimSpace(token.Name); name != "" {
		return name
	}
	return token.DisplayKey()
}
