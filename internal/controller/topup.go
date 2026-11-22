package controller

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

// stripeTopupEnabled is whether this deployment can open a card checkout:
// the three Stripe values Checkout needs are all present.
func stripeTopupEnabled() bool {
	return setting.StripeApiSecret != "" && setting.StripeWebhookSecret != "" && setting.StripePriceId != ""
}

func GetTopUpInfo(c *gin.Context) {
	payMethods := []map[string]string{}
	stripeEnabled := stripeTopupEnabled()
	if stripeEnabled {
		payMethods = append(payMethods, map[string]string{
			"name":   "Stripe",
			"type":   "stripe",
			"status": "available",
		})
	}

	data := gin.H{
		"enable_stripe_topup": stripeEnabled,
		"pay_methods":         payMethods,
		"stripe_min_topup":    getStripeMinTopup(),
		"stripe_max_topup":    getStripeMaxTopup(),
		"low_balance_mail":    lowBalanceMailState(c.GetInt("id")),
	}
	common.ApiSuccess(c, data)
}

// lowBalanceMailState tells the wallet whether the low-balance mail can be
// sent at all on this deployment, whether this account still wants it, and
// the balance it is sent under: the account's own threshold when it set
// one, otherwise the platform's.
func lowBalanceMailState(userID int) gin.H {
	cfg := email_setting.GetEmailSetting()
	threshold := billing.USD(cfg.LowBalanceThresholdUSD)
	on := true
	if user, err := model.GetUserById(userID, true); err == nil && user != nil {
		settings := user.GetSetting()
		if settings.QuotaWarningThreshold > 0 {
			threshold = billing.MicroUSD(settings.QuotaWarningThreshold)
		}
		on = !settings.LowBalanceOff
	}
	return gin.H{
		"available":          cfg.LowBalanceEnabled && threshold > 0,
		"on":                 on,
		"threshold_microusd": threshold,
	}
}

// tradeNo lock
var orderLocks sync.Map
var createLock sync.Mutex

// LockOrder locks a specific order by trade number
func LockOrder(tradeNo string) {
	lock, ok := orderLocks.Load(tradeNo)
	if !ok {
		createLock.Lock()
		defer createLock.Unlock()
		lock, ok = orderLocks.Load(tradeNo)
		if !ok {
			lock = new(sync.Mutex)
			orderLocks.Store(tradeNo, lock)
		}
	}
	lock.(*sync.Mutex).Lock()
}

// UnlockOrder releases the lock for a specific order
func UnlockOrder(tradeNo string) {
	lock, ok := orderLocks.Load(tradeNo)
	if ok {
		lock.(*sync.Mutex).Unlock()
	}
}

// maxTopUpRange bounds an explicit start/end query on the reader's own
// top-ups: the wallet asks for at most six months of records at a time.
const maxTopUpRange = 400 * 24 * time.Hour

func GetUserTopUps(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	// An explicit half-open [start, end) range replaces the default 30-day
	// window. Both bounds are required together.
	if rawStart, rawEnd := c.Query("start_timestamp"), c.Query("end_timestamp"); rawStart != "" || rawEnd != "" {
		start, startErr := strconv.ParseInt(rawStart, 10, 64)
		end, endErr := strconv.ParseInt(rawEnd, 10, 64)
		if startErr != nil || endErr != nil || start <= 0 || end <= start || time.Duration(end-start)*time.Second > maxTopUpRange {
			httperr.Invalid(c, "start_timestamp")
			return
		}
		topups, total, err := model.GetUserTopUpsBetween(userId, start, end, pageInfo)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		pageInfo.SetTotal(int(total))
		pageInfo.SetItems(topups)
		common.ApiSuccess(c, pageInfo)
		return
	}

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchUserTopUps(userId, keyword, pageInfo)
	} else {
		topups, total, err = model.GetUserTopUps(userId, pageInfo)
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

const maxWalletLedgerRange = 32 * 24 * time.Hour

// GetUserWalletLedger returns the signed-in user's authoritative daily wallet
// aggregates for a bounded half-open Unix timestamp range. Internal reference
// IDs, metadata, and individual request movements are intentionally omitted.
func GetUserWalletLedger(c *gin.Context) {
	startTimestamp, startErr := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, endErr := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	timezoneName := strings.TrimSpace(c.Query("timezone"))
	timezoneOffsetMinutes := 0
	var timezoneOffsetErr error
	if rawOffset := c.Query("timezone_offset_minutes"); timezoneName == "" && rawOffset != "" {
		timezoneOffsetMinutes, timezoneOffsetErr = strconv.Atoi(rawOffset)
	}
	var location *time.Location
	var timezoneErr error
	if timezoneName != "" {
		if len(timezoneName) > 64 {
			timezoneErr = errors.New("timezone name is too long")
		} else {
			location, timezoneErr = time.LoadLocation(timezoneName)
		}
	} else if timezoneOffsetErr == nil && timezoneOffsetMinutes >= -14*60 && timezoneOffsetMinutes <= 14*60 {
		// Older clients can still send a fixed offset. New Console clients send
		// an IANA zone so each movement uses the DST rule valid at its timestamp.
		location = time.FixedZone("wallet", -timezoneOffsetMinutes*60)
	}
	// Normalize query bounds to UTC before passing them to GORM. This keeps
	// SQLite's text-backed datetime comparisons consistent with PostgreSQL.
	start := time.Unix(startTimestamp, 0).UTC()
	end := time.Unix(endTimestamp, 0).UTC()
	if startErr != nil || endErr != nil || timezoneOffsetErr != nil || timezoneErr != nil || location == nil || startTimestamp <= 0 || endTimestamp <= 0 || !end.After(start) || end.Sub(start) > maxWalletLedgerRange {
		httperr.Code(c, types.ErrorCodeWalletLedgerRangeInvalid)
		return
	}

	payerID := c.GetInt("id")
	if c.Query("project_id") != "" {
		pc, ok := legacyProjectContext(c, "")
		if !ok {
			return
		}
		if !pc.Member.IsManager() {
			projectFailure(c, model.ErrProjectForbidden)
			return
		}
		payerID = pc.Project.OwnerUserID
	}
	ledger, err := model.GetUserWalletLedgerRange(payerID, start, end, location)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, ledger)
}

// GetAllTopUps returns all topup records for admins
func GetAllTopUps(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")
	owner := c.Query("owner")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" || owner != "" {
		topups, total, err = model.SearchAllTopUps(keyword, owner, pageInfo)
	} else {
		topups, total, err = model.GetAllTopUps(pageInfo)
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

type AdminCompleteTopupRequest struct {
	TradeNo string `json:"trade_no"`
}

// AdminCompleteTopUp allows admin to manually complete a pending topup
func AdminCompleteTopUp(c *gin.Context) {
	var req AdminCompleteTopupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TradeNo == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "trade_no"})
		return
	}

	// Order-level mutex to prevent concurrent completion
	LockOrder(req.TradeNo)
	defer UnlockOrder(req.TradeNo)

	userId, quotaAdded, err := model.ManualCompleteTopUp(req.TradeNo, common.GetRealClientIP(c))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	service.NotifyBalanceChange(userId, quotaAdded, service.BalanceReasonAdminTopUp)
	common.ApiSuccess(c, nil)
}
