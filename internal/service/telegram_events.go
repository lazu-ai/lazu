package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/telegram"
	"gorm.io/gorm"
)

// New accounts and completed top-ups, reported to telegram.events_chat.
//
// A top-up's completion hook runs inside the transaction, before the order
// is marked successful and before later hooks settle the bonus. So the hook
// only remembers the order; once the transaction has had time to commit,
// the order is read back and reported only if it really succeeded, with its
// final amounts.

var topUpNoticeDelays = []time.Duration{3 * time.Second, 15 * time.Second, time.Minute}

func init() {
	model.RegisterTopUpCompletingHook(func(_ *gorm.DB, topUp *model.TopUp) error {
		if topUp != nil && topUp.Id > 0 && telegram.EventsConfigured() {
			id := topUp.Id
			common.SafeGo("telegram-topup-notice", func() { reportTopUpWhenCommitted(id, topUpNoticeDelays) })
		}
		return nil
	})
}

func reportTopUpWhenCommitted(id int, delays []time.Duration) {
	for _, delay := range delays {
		time.Sleep(delay)
		var order model.TopUp
		if err := model.CoreDB().Where("id = ?", id).Limit(1).Find(&order).Error; err != nil || order.Id == 0 {
			continue
		}
		if order.Status != common.TopUpStatusSuccess {
			continue
		}
		ReportTopUp(&order)
		return
	}
}

// ReportTopUp posts a completed top-up once.
func ReportTopUp(order *model.TopUp) {
	if order == nil || order.Status != common.TopUpStatusSuccess || !telegram.ClaimEvent("topup:"+strconv.Itoa(order.Id)) {
		return
	}
	payer := order.UserId
	if order.PaidByUserID > 0 {
		payer = order.PaidByUserID
	}
	who := fmt.Sprintf("用户 ID %d", payer)
	if user, err := model.GetUserById(payer, false); err == nil && user != nil {
		who = fmt.Sprintf("%s（ID %d）", user.Username, user.Id)
	}
	paid := order.PaidAmount
	parts := []string{fmt.Sprintf("💰 <b>充值到账 $%.2f</b>", paid), telegram.Escape(who)}
	if credited := order.CreditedMicrousd.Dollars(); credited > 0 && math.Abs(credited-paid) >= 0.005 {
		parts = append(parts, telegram.Escape(fmt.Sprintf("到账 $%.2f", credited)))
	}
	if method := strings.TrimSpace(order.PaymentMethod); method != "" {
		parts = append(parts, telegram.Escape(method))
	}
	if order.ProjectID > 0 {
		parts = append(parts, telegram.Escape(fmt.Sprintf("团队项目 #%d", order.ProjectID)))
	}
	if order.AutoReload {
		parts = append(parts, "自动充值")
	} else if firstTopUp(order) {
		parts = append(parts, "首次充值")
	}
	telegram.CountEvent(telegram.EventTopUp, int64(math.Round(paid*100)))
	telegram.PushEvent(telegram.EventTopUp, strings.Join(parts, " · "))
}

// firstTopUp reports whether this is the account's only successful top-up.
func firstTopUp(order *model.TopUp) bool {
	var n int64
	if err := model.CoreDB().Model(&model.TopUp{}).Where("user_id = ? AND status = ?", order.UserId, common.TopUpStatusSuccess).Count(&n).Error; err != nil {
		return false
	}
	return n == 1
}

// ReportSignup posts a new account once, however many session setups see it
// as fresh.
func ReportSignup(user *model.User) {
	if user == nil || user.Id <= 0 || !telegram.EventsConfigured() || !telegram.ClaimEvent("signup:"+strconv.Itoa(user.Id)) {
		return
	}
	parts := []string{fmt.Sprintf("🆕 <b>新用户</b> %s", telegram.Escape(fmt.Sprintf("%s（ID %d）", user.Username, user.Id)))}
	if method := signupMethod(user); method != "" {
		parts = append(parts, telegram.Escape(method))
	}
	if email := MaskEmail(user.Email); email != "" {
		parts = append(parts, telegram.Escape(email))
	}
	if surface := strings.TrimSpace(user.HomeSurface); surface != "" {
		parts = append(parts, telegram.Escape("从 "+surface+" 注册"))
	}
	telegram.CountEvent(telegram.EventSignup, 0)
	telegram.PushEvent(telegram.EventSignup, strings.Join(parts, " · "))
}

var providerLabels = map[string]string{"google": "Google 登录", "github": "GitHub 登录", "oidc": "企业登录"}

func signupMethod(user *model.User) string {
	providers, err := model.GetUserIdentityProviders([]int{user.Id})
	if err == nil {
		for _, p := range providers[user.Id] {
			if label, ok := providerLabels[p]; ok {
				return label
			}
		}
	}
	if !user.NeedsPassword {
		return "邮箱密码注册"
	}
	if strings.TrimSpace(user.Email) != "" {
		return "邮箱验证码注册"
	}
	return ""
}

// MaskEmail keeps the first character of the local part and the domain:
// a***@example.com.
func MaskEmail(email string) string {
	email = strings.TrimSpace(email)
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return ""
	}
	first := []rune(local)[0]
	return string(first) + "***@" + domain
}
