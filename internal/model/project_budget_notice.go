package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// Budget notices tell people about a shared project's monthly budgets before
// and when they run out (brand/foundations/team-projects-2026-09.md, "Budget
// notices"). They are ink-tier mail: the news is the share already spent.
//
//   - A member hears about their own budget at the warning line and when it
//     is used up.
//   - Whoever may change that member's budget hears only when it is used up:
//     the owner and admins for an ordinary member, the owner alone for an
//     admin. A warning to managers would repeat once per member every month.
//   - The owner hears about the project's own monthly cap at both points.
//
// Each notice is one row in the project outbox, keyed by the budget, the
// period, the limit and the recipient, so it is sent once per month and again
// only after the limit changes.
const (
	projectBudgetWarn = "warn"
	projectBudgetOut  = "out"
)

func projectBudgetNoticeKey(kind string, budget SpendBudget, periodStart int64, recipientID int) string {
	return fmt.Sprintf("budget:%s:%d:%d:%d:%d", kind, budget.ID, periodStart, budget.LimitMicrousd.Int64(), recipientID)
}

// queueProjectBudgetNoticesSafeTx runs queueProjectBudgetNoticesTx inside a
// savepoint, so a failure undoes only the notice rows and leaves the caller's
// transaction usable (PostgreSQL aborts a whole transaction on any error).
func queueProjectBudgetNoticesSafeTx(tx *gorm.DB, budget SpendBudget, kind string, periodStart, periodEnd int64, spent billing.MicroUSD) error {
	if tx == nil || budget.ProjectID <= 0 {
		return nil
	}
	return tx.Transaction(func(nested *gorm.DB) error {
		return queueProjectBudgetNoticesTx(nested, budget, kind, periodStart, periodEnd, spent)
	})
}

// queueProjectBudgetNoticesTx queues the notices for one budget crossing one
// line. It runs inside the financial writer's transaction, which only reaches
// here on the rare batch that crosses a line, never on the request path.
func queueProjectBudgetNoticesTx(tx *gorm.DB, budget SpendBudget, kind string, periodStart, periodEnd int64, spent billing.MicroUSD) error {
	if tx == nil || budget.ProjectID <= 0 || !budget.Enabled || budget.LimitMicrousd <= 0 || budget.Period != SpendBudgetPeriodMonth {
		return nil
	}
	if budget.Scope != SpendBudgetScopeUser && budget.Scope != SpendBudgetScopeOrg {
		return nil // a Key's own cap has the KEY USAGE mail
	}
	var project Project
	if err := tx.Where("id = ?", budget.ProjectID).Limit(1).Find(&project).Error; err != nil || project.ID == 0 {
		return err
	}
	if project.UpgradedAt == nil {
		return nil // an unshared project has no members to warn
	}
	notice := projectBudgetMail{kind: kind, project: project, budget: budget, spent: spent, resetAt: time.Unix(periodEnd, 0)}
	if budget.Scope == SpendBudgetScopeOrg {
		notice.topSpend = projectTopSpendersTx(tx, project, periodStart, periodEnd, 3)
		return notice.queueTx(tx, project.OwnerUserID, periodStart, notice.projectCap)
	}
	var target ProjectMember
	if err := tx.Where("project_id = ? AND user_id = ?", project.ID, budget.UserID).Limit(1).Find(&target).Error; err != nil || target.ID == 0 {
		return err
	}
	if target.Status != ProjectMemberActive || target.Role == ProjectRoleOwner {
		return nil
	}
	notice.target = target
	notice.targetName = userMailNameTx(tx, target.UserID)
	if err := tx.Select("id", "public_id").Where("id = ?", target.UserID).Limit(1).Find(&notice.targetRef).Error; err != nil {
		return err
	}
	var request BudgetRequest
	if err := tx.Where("project_id = ? AND user_id = ? AND status = ?", project.ID, target.UserID, "pending").Order("id DESC").Limit(1).Find(&request).Error; err != nil {
		return err
	}
	if request.ID > 0 {
		notice.request = &request
	}
	if kind == projectBudgetWarn {
		return notice.queueTx(tx, target.UserID, periodStart, notice.memberOwn)
	}
	managerRoles := []string{ProjectRoleOwner}
	if target.Role == ProjectRoleMember {
		managerRoles = append(managerRoles, ProjectRoleAdmin)
	}
	var managers []ProjectMember
	if err := tx.Where("project_id = ? AND status = ? AND role IN ? AND user_id <> ?", project.ID, ProjectMemberActive, managerRoles, target.UserID).
		Order("id ASC").Find(&managers).Error; err != nil {
		return err
	}
	notice.ownerName = userMailNameTx(tx, project.OwnerUserID)
	if err := notice.queueTx(tx, target.UserID, periodStart, notice.memberOwn); err != nil {
		return err
	}
	for _, manager := range managers {
		if err := notice.queueTx(tx, manager.UserID, periodStart, notice.managerView); err != nil {
			return err
		}
	}
	return nil
}

type projectBudgetMail struct {
	kind       string
	project    Project
	budget     SpendBudget
	spent      billing.MicroUSD
	resetAt    time.Time
	target     ProjectMember
	targetName string
	// targetRef is the member's public id, for the edit link.
	targetRef User
	ownerName string
	topSpend  string
	request   *BudgetRequest
}

type projectBudgetRender func(lang string, recipient User) (string, common.Mail)

func (n projectBudgetMail) queueTx(tx *gorm.DB, recipientID int, periodStart int64, render projectBudgetRender) error {
	if recipientID <= 0 {
		return nil
	}
	var recipient User
	if err := tx.Select("id", "public_id", "email", "setting").Where("id = ?", recipientID).Limit(1).Find(&recipient).Error; err != nil {
		return err
	}
	if recipient.Id == 0 || strings.TrimSpace(recipient.Email) == "" {
		return nil // nowhere to send it; the console still shows the state
	}
	lang := common.MailLang(recipient.GetSetting().Language)
	subject, mail := render(lang, recipient)
	mail.Lang, mail.Tier = lang, common.MailInk
	body, err := common.RenderMail(mail)
	if err != nil {
		return err
	}
	key := projectBudgetNoticeKey(n.kind, n.budget, periodStart, recipientID)
	return QueueProjectEmailTx(tx, n.project.ID, recipientID, key, ProjectEmail{Receiver: recipient.Email, Subject: subject, HTML: body})
}

func (n projectBudgetMail) share() (int, string) {
	pct := 0
	if n.budget.LimitMicrousd > 0 {
		pct = int(n.spent.Int64() * 100 / n.budget.LimitMicrousd.Int64())
	}
	if n.kind == projectBudgetOut && pct < 100 {
		// Admission refuses a request that would cross the cap, so a used-up
		// budget can sit a few cents short of it. It is still used up.
		pct = 100
	}
	return pct, fmt.Sprintf("%d%%", pct)
}

func (n projectBudgetMail) left() billing.MicroUSD {
	if n.spent >= n.budget.LimitMicrousd {
		return 0
	}
	return n.budget.LimitMicrousd - n.spent
}

func (n projectBudgetMail) usageURL(reason string) string {
	link := projectConsoleURL("", "/console/usage", n.project) + "&tab=logs"
	if reason != "" {
		link += "&result=rejected&reason=" + reason
	}
	return link
}

// memberOwn is the member's own notice, at the warning line or used up.
func (n projectBudgetMail) memberOwn(lang string, _ User) (string, common.Mail) {
	p := n.project.Name
	_, share := n.share()
	reset := projectMailTime(n.resetAt)
	var subject, message string
	if n.kind == projectBudgetWarn {
		subject = common.MailText(lang, "你在 "+p+" 的本月预算已用 "+share, "You've used "+share+" of your monthly budget in "+p)
		message = common.MailText(lang,
			"你在 "+p+" 的本月预算已用掉 "+share+"。用完后你在这个项目里的 Key 会暂停，"+reset+" 重置。",
			"You've used "+share+" of your monthly budget in "+p+". When it runs out, your Keys in this project pause until "+reset+".")
	} else {
		subject = common.MailText(lang, "你在 "+p+" 的本月预算用完了", "Your monthly budget in "+p+" is used up")
		message = common.MailText(lang,
			"你在 "+p+" 的本月预算用完了。你在这个项目里的 Key 收到的新请求会被拒绝，直到 "+reset+" 重置或预算被提高。你自己的默认项目不受影响。",
			"Your monthly budget in "+p+" is used up. New requests from your Keys in this project are refused until "+reset+" or until the budget is raised. Your own default project is not affected.")
	}
	facts := []common.MailFact{
		{Label: common.MailText(lang, "本月已用", "Used this month"), Value: common.MailUSD(n.spent.Dollars())},
		{Label: common.MailText(lang, "月预算", "Monthly budget"), Value: common.MailUSD(n.budget.LimitMicrousd.Dollars())},
	}
	if n.kind == projectBudgetWarn {
		facts = append(facts, common.MailFact{Label: common.MailText(lang, "剩余", "Left"), Value: common.MailUSD(n.left().Dollars())})
	}
	facts = append(facts, common.MailFact{Label: common.MailText(lang, "重置", "Resets"), Value: reset})
	if n.kind == projectBudgetOut && n.ownerName != "" {
		facts = append(facts, common.MailFact{Label: common.MailText(lang, "已通知", "Also told"), Value: common.MailText(lang, n.ownerName+"（项目所有者）", n.ownerName+" (project owner)")})
	}
	ask := common.MailText(lang, "申请提高预算", "Ask for a higher budget")
	if n.request != nil {
		ask = common.MailText(lang, "查看我的申请", "View my request")
	}
	return subject, common.Mail{
		Eyebrow:   "MEMBER BUDGET",
		Preheader: common.MailText(lang, "已用 ", "") + common.MailUSD(n.spent.Dollars()) + " / " + common.MailUSD(n.budget.LimitMicrousd.Dollars()),
		Hero:      share, Message: message,
		Blocks: []common.MailBlock{
			{Facts: facts},
			{Primary: &common.MailAction{Label: ask, URL: projectConsoleURL("", "/console", n.project)},
				Secondary: &common.MailAction{Label: common.MailText(lang, "查看用量", "View usage"), URL: n.usageURL("")}},
		},
	}
}

// managerView tells an owner or admin that a member's budget is used up.
func (n projectBudgetMail) managerView(lang string, _ User) (string, common.Mail) {
	p, who := n.project.Name, n.targetName
	_, share := n.share()
	subject := common.MailText(lang, who+" 在 "+p+" 的本月预算用完了", who+"'s monthly budget in "+p+" is used up")
	message := common.MailText(lang,
		who+" 在 "+p+" 的本月预算用完了，"+who+" 的 Key 收到的新请求正在被拒绝。提高 "+who+" 的预算即可恢复，立即生效。",
		who+"'s monthly budget in "+p+" is used up, so new requests from "+who+"'s Keys are being refused. Raising the budget restores them at once.")
	editURL := projectConsoleURL("", "/console/project/members", n.project)
	if n.targetRef.PublicID != "" {
		editURL += "&edit=" + n.targetRef.PublicID
	}
	primary := common.MailText(lang, "调整 "+who+" 的预算", "Change "+who+"'s budget")
	if n.request != nil {
		amount := common.MailUSD(n.request.RequestedMicrousd.Dollars())
		primary = common.MailText(lang, "处理 "+who+" 的申请（"+amount+"）", "Review "+who+"'s request ("+amount+")")
		message += common.MailText(lang, " "+who+" 还有一条预算申请等你处理。", " "+who+" also has a budget request waiting for you.")
	}
	return subject, common.Mail{
		Eyebrow:   "MEMBER BUDGET",
		Preheader: common.MailUSD(n.spent.Dollars()) + " / " + common.MailUSD(n.budget.LimitMicrousd.Dollars()) + common.MailText(lang, "，新请求已暂停", ", new requests paused"),
		Hero:      share, Message: message,
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: common.MailText(lang, "成员", "Member"), Value: who},
				{Label: common.MailText(lang, "本月已用", "Used this month"), Value: common.MailUSD(n.spent.Dollars()) + " / " + common.MailUSD(n.budget.LimitMicrousd.Dollars())},
				{Label: common.MailText(lang, "重置", "Resets"), Value: projectMailTime(n.resetAt)},
			}},
			{Primary: &common.MailAction{Label: primary, URL: editURL},
				Secondary: &common.MailAction{Label: common.MailText(lang, "查看被拒的请求", "View refused requests"), URL: n.usageURL(string(types.ErrorCodeMemberBudgetExceeded))}},
		},
	}
}

// projectCap tells the owner about the project's own monthly cap.
func (n projectBudgetMail) projectCap(lang string, _ User) (string, common.Mail) {
	p := n.project.Name
	_, share := n.share()
	reset := projectMailTime(n.resetAt)
	subject := common.MailText(lang, p+" 本月已用掉上限的 "+share, p+" has used "+share+" of its monthly cap")
	message := common.MailText(lang,
		p+" 本月已用掉项目月上限的 "+share+"。到上限后所有成员的新请求都会被拒绝，"+reset+" 重置。",
		p+" has used "+share+" of its monthly cap. At the cap, new requests from every member are refused until "+reset+".")
	if n.kind == projectBudgetOut {
		subject = common.MailText(lang, p+" 本月上限用完了", p+" has reached its monthly cap")
		message = common.MailText(lang,
			p+" 本月的项目上限用完了，所有成员的新请求正在被拒绝，直到 "+reset+" 重置或上限被提高。",
			p+" has reached its monthly cap. New requests from every member are refused until "+reset+" or until the cap is raised.")
	}
	facts := []common.MailFact{
		{Label: common.MailText(lang, "本月已用", "Used this month"), Value: common.MailUSD(n.spent.Dollars())},
		{Label: common.MailText(lang, "项目月上限", "Monthly cap"), Value: common.MailUSD(n.budget.LimitMicrousd.Dollars())},
	}
	if n.topSpend != "" {
		facts = append(facts, common.MailFact{Label: common.MailText(lang, "用得最多", "Top spenders"), Value: n.topSpend})
	}
	facts = append(facts, common.MailFact{Label: common.MailText(lang, "重置", "Resets"), Value: reset})
	return subject, common.Mail{
		Eyebrow:   "PROJECT BUDGET",
		Preheader: common.MailText(lang, "已用 ", "") + common.MailUSD(n.spent.Dollars()) + " / " + common.MailUSD(n.budget.LimitMicrousd.Dollars()),
		Hero:      share, Message: message,
		Blocks: []common.MailBlock{
			{Facts: facts},
			{Primary: &common.MailAction{Label: common.MailText(lang, "调整项目上限", "Change the cap"), URL: projectConsoleURL("", "/console/project/settings", n.project)},
				Secondary: &common.MailAction{Label: common.MailText(lang, "查看成员用量", "View member spend"), URL: projectConsoleURL("", "/console/project/members", n.project)}},
		},
	}
}

// projectTopSpendersTx names the members who spent most in the period, for
// the owner deciding between a higher cap and a word with someone. It is best
// effort: a failed read leaves the line out.
func projectTopSpendersTx(tx *gorm.DB, project Project, periodStart, periodEnd int64, limit int) string {
	var rows []struct {
		ActorUserID    int
		AmountMicrousd billing.MicroUSD
	}
	if err := tx.Model(&ProjectSpend{}).Where("project_id = ? AND occurred_at >= ? AND occurred_at < ?", project.ID, time.Unix(periodStart, 0).UTC(), time.Unix(periodEnd, 0).UTC()).
		Select("actor_user_id, COALESCE(SUM(amount_microusd),0) AS amount_microusd").Group("actor_user_id").
		Order("amount_microusd DESC").Limit(limit).Scan(&rows).Error; err != nil {
		return ""
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.AmountMicrousd <= 0 {
			continue
		}
		if name := userMailNameTx(tx, row.ActorUserID); name != "" {
			parts = append(parts, name+" "+common.MailUSD(row.AmountMicrousd.Dollars()))
		}
	}
	return strings.Join(parts, " · ")
}

// A refused request is the other way a budget shows it is used up: admission
// turns away the request that would cross the cap, so spending can stop just
// short of it and never cross the line the financial writer watches. The
// telemetry writer hands each committed batch here after it commits.
var projectBudgetRejectionSeen = struct {
	sync.Mutex
	keys map[string]time.Time
}{keys: map[string]time.Time{}}

// A refusal storm costs one lookup per member every few minutes. The window
// is short, not monthly, so a budget raised and used up again in the same
// month is still noticed; the outbox key keeps the notice itself once-only.
const projectBudgetRejectionRecheck = 10 * time.Minute

func noteProjectBudgetRejections(committed []Log) {
	type candidate struct {
		projectID, userID int
		reason            string
	}
	var found []candidate
	now := time.Now()
	projectBudgetRejectionSeen.Lock()
	for i := range committed {
		row := &committed[i]
		if row.Type != LogTypeError || row.ProjectID <= 0 {
			continue
		}
		reason := row.RejectionReason
		if reason != string(types.ErrorCodeMemberBudgetExceeded) && reason != string(types.ErrorCodeProjectBudgetExceeded) {
			continue
		}
		userID := row.UserId
		if reason == string(types.ErrorCodeProjectBudgetExceeded) {
			userID = 0
		}
		// The outbox key already makes the notice once-only; this only spares
		// the database a lookup for every refused request in a storm.
		key := fmt.Sprintf("%d:%d:%s", row.ProjectID, userID, reason)
		if at, seen := projectBudgetRejectionSeen.keys[key]; seen && now.Sub(at) < projectBudgetRejectionRecheck {
			continue
		}
		if len(projectBudgetRejectionSeen.keys) > 10000 {
			projectBudgetRejectionSeen.keys = map[string]time.Time{}
		}
		projectBudgetRejectionSeen.keys[key] = now
		found = append(found, candidate{row.ProjectID, userID, reason})
	}
	projectBudgetRejectionSeen.Unlock()
	if len(found) == 0 || CoreDB() == nil {
		return
	}
	common.SafeGo("project-budget-rejection-notice", func() {
		for _, c := range found {
			if err := queueProjectBudgetUsedUp(c.projectID, c.userID, time.Now()); err != nil {
				common.SysLog("project budget notice: " + err.Error())
			}
		}
	})
}

// queueProjectBudgetUsedUp queues the used-up notices for a member's budget
// (userID > 0) or for the project cap (userID 0).
func queueProjectBudgetUsedUp(projectID, userID int, now time.Time) error {
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		scope := SpendBudgetScopeOrg
		if userID > 0 {
			scope = SpendBudgetScopeUser
		}
		var budget SpendBudget
		err := tx.Where("project_id = ? AND scope = ? AND user_id = ? AND token_id = 0 AND period = ? AND enabled = ?", projectID, scope, userID, SpendBudgetPeriodMonth, true).
			Order("id ASC").First(&budget).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		start, end := spendBudgetPeriodWindow(budget.Period, now)
		spent, err := sumSpendMicrousdForBudgetWithDB(tx, budget, start, end)
		if err != nil {
			return err
		}
		return queueProjectBudgetNoticesTx(tx, budget, projectBudgetOut, start, end, spent)
	})
}

// ProjectNearBudget is a member past their budget's warning line this month.
type ProjectNearBudget struct {
	UserID         int
	SpentMicrousd  billing.MicroUSD
	BudgetMicrousd billing.MicroUSD
	WarningPct     int
}

// ReadProjectNearBudget lists active members whose month-to-date spend has
// reached their budget's warning line, most used first. It reads the same
// budget rows and spend facts the members page reads.
func ReadProjectNearBudget(project Project, now time.Time) ([]ProjectNearBudget, error) {
	var budgets []SpendBudget
	if err := CoreDB().Where("project_id = ? AND scope = ? AND token_id = 0 AND period = ? AND enabled = ? AND limit_microusd > 0", project.ID, SpendBudgetScopeUser, SpendBudgetPeriodMonth, true).
		Order("id ASC").Find(&budgets).Error; err != nil {
		return nil, err
	}
	if len(budgets) == 0 {
		return []ProjectNearBudget{}, nil
	}
	var active []ProjectMember
	if err := CoreDB().Where("project_id = ? AND status = ? AND role <> ?", project.ID, ProjectMemberActive, ProjectRoleOwner).Find(&active).Error; err != nil {
		return nil, err
	}
	isActive := make(map[int]bool, len(active))
	for _, member := range active {
		isActive[member.UserID] = true
	}
	byUser := map[int]SpendBudget{}
	ids := map[int]struct{}{}
	for _, budget := range budgets {
		if _, seen := byUser[budget.UserID]; !seen && isActive[budget.UserID] {
			byUser[budget.UserID] = budget
			ids[budget.UserID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return []ProjectNearBudget{}, nil
	}
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var amounts []struct {
		ActorUserID    int
		AmountMicrousd billing.MicroUSD
	}
	if err := CoreDB().Model(&ProjectSpend{}).Where("project_id = ? AND actor_user_id IN ? AND occurred_at >= ? AND occurred_at < ?",
		project.ID, consoleIDKeys(ids), monthStart, time.Unix(now.Unix()+1, 0).UTC()).
		Select("actor_user_id, COALESCE(SUM(amount_microusd),0) AS amount_microusd").Group("actor_user_id").Scan(&amounts).Error; err != nil {
		return nil, err
	}
	result := []ProjectNearBudget{}
	for _, row := range amounts {
		budget := byUser[row.ActorUserID]
		warning := budget.WarningThresholdPct
		if warning <= 0 || warning > 100 {
			warning = 100
		}
		if row.AmountMicrousd.Int64()*100 < budget.LimitMicrousd.Int64()*int64(warning) {
			continue
		}
		result = append(result, ProjectNearBudget{UserID: row.ActorUserID, SpentMicrousd: row.AmountMicrousd, BudgetMicrousd: budget.LimitMicrousd, WarningPct: budget.WarningThresholdPct})
	}
	sort.Slice(result, func(i, j int) bool {
		left := float64(result[i].SpentMicrousd) / float64(result[i].BudgetMicrousd)
		right := float64(result[j].SpentMicrousd) / float64(result[j].BudgetMicrousd)
		if left != right {
			return left > right
		}
		return result[i].UserID < result[j].UserID
	})
	return result, nil
}
