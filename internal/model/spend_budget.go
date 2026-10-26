package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SpendBudgetScopeToken = "token"
	SpendBudgetScopeUser  = "user"
	SpendBudgetScopeOrg   = "org"

	SpendBudgetPeriodDay   = "day"
	SpendBudgetPeriodMonth = "month"

	SpendBudgetEnforcementAlert = "alert"
	SpendBudgetEnforcementBlock = "block"

	SpendBudgetAlertThreshold = "threshold"
	SpendBudgetAlertExceeded  = "exceeded"
	SpendBudgetAlertBurst     = "burst"
	SpendBudgetAlertBlocked   = "blocked"

	// A reservation must outlive the request holding it, or a concurrent request
	// overshoots the Key, member and project caps. It must not outlive it by
	// much either: if a process dies between reserving and pre-consuming,
	// expiry is the only thing that ever releases the hold, and until then the
	// payer is charged for work that never happened.
	//
	// An ordinary relay request is bounded by its own connection, so an hour
	// covers any realistic streamed completion while capping what one crash can
	// freeze. A media job is different in kind: it is a persisted row a worker
	// polls, the longest admitted one is a video generation (a 15-minute
	// deferred start plus videoProviderMaxAttempts polls at the indeterminate
	// interval, roughly 15 hours), and every terminal path releases or finalizes
	// explicitly. FinalizeProjectSpendBudget tolerates an expired reservation —
	// charges dedupe on (budget, request, period) and project spends on their
	// event key — so a lost hold cannot double count either way.
	spendBudgetReservationTTL    = time.Hour
	spendBudgetJobReservationTTL = 24 * time.Hour
)

var ErrSpendBudgetExceeded = errors.New("spend budget exceeded")

type SpendBudget struct {
	ProjectID int `json:"-" gorm:"index;not null;default:0"`

	ID                  uint             `json:"id" gorm:"primaryKey"`
	Scope               string           `json:"scope" gorm:"size:16;index:idx_spend_budget_scope;not null"`
	UserID              int              `json:"user_id" gorm:"index"`
	TokenID             int              `json:"token_id" gorm:"index"`
	Period              string           `json:"period" gorm:"size:16;not null;default:'month'"`
	LimitMicrousd       billing.MicroUSD `json:"limit_microusd" gorm:"type:bigint;not null"`
	WarningThresholdPct int              `json:"warning_threshold_pct" gorm:"not null;default:80"`
	BurstMultiplier     float64          `json:"burst_multiplier" gorm:"type:decimal(8,2);not null;default:0"`
	Enforcement         string           `json:"enforcement" gorm:"size:16;not null;default:'block'"`
	Enabled             bool             `json:"enabled" gorm:"not null;default:true"`
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

type SpendBudgetAlert struct {
	ProjectID   int `json:"-" gorm:"index;not null;default:0"`
	PayerUserID int `json:"-" gorm:"not null;default:0"`
	ActorUserID int `json:"-" gorm:"not null;default:0"`

	ID            uint             `json:"id" gorm:"primaryKey"`
	BudgetID      uint             `json:"budget_id" gorm:"index"`
	Scope         string           `json:"scope" gorm:"size:16;index"`
	UserID        int              `json:"user_id" gorm:"index"`
	TokenID       int              `json:"token_id" gorm:"index"`
	SessionID     string           `json:"session_id" gorm:"size:64;index"`
	AgentID       string           `json:"agent_id" gorm:"size:64;index"`
	RequestID     string           `json:"request_id" gorm:"size:64;index"`
	AlertType     string           `json:"alert_type" gorm:"size:32;index"`
	ThresholdPct  int              `json:"threshold_pct"`
	Period        string           `json:"period" gorm:"size:16"`
	PeriodStart   int64            `json:"period_start" gorm:"index"`
	PeriodEnd     int64            `json:"period_end" gorm:"index"`
	SpentMicrousd billing.MicroUSD `json:"spent_microusd" gorm:"type:bigint"`
	LimitMicrousd billing.MicroUSD `json:"limit_microusd" gorm:"type:bigint"`
	DeltaMicrousd billing.MicroUSD `json:"delta_microusd" gorm:"type:bigint"`
	Message       string           `json:"message" gorm:"type:text"`
	CreatedAt     time.Time        `json:"created_at" gorm:"index"`
}

type SpendBudgetReservation struct {
	ProjectID   int `json:"-" gorm:"index;not null;default:0"`
	PayerUserID int `json:"-" gorm:"not null;default:0"`
	ActorUserID int `json:"-" gorm:"not null;default:0"`

	ID               uint             `json:"id" gorm:"primaryKey"`
	BudgetID         uint             `json:"budget_id" gorm:"index;uniqueIndex:idx_spend_budget_reservation_request,priority:1"`
	Scope            string           `json:"scope" gorm:"size:16;index"`
	UserID           int              `json:"user_id" gorm:"index"`
	TokenID          int              `json:"token_id" gorm:"index"`
	SessionID        string           `json:"session_id" gorm:"size:64;index"`
	AgentID          string           `json:"agent_id" gorm:"size:64;index"`
	RequestID        string           `json:"request_id" gorm:"size:64;index;uniqueIndex:idx_spend_budget_reservation_request,priority:3"`
	Period           string           `json:"period" gorm:"size:16"`
	PeriodStart      int64            `json:"period_start" gorm:"index;uniqueIndex:idx_spend_budget_reservation_request,priority:2"`
	PeriodEnd        int64            `json:"period_end" gorm:"index"`
	ReservedMicrousd billing.MicroUSD `json:"reserved_microusd" gorm:"type:bigint"`
	LimitMicrousd    billing.MicroUSD `json:"limit_microusd" gorm:"type:bigint"`
	ExpiresAt        int64            `json:"expires_at" gorm:"index"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type SpendBudgetCharge struct {
	LedgerOperationID string `json:"-" gorm:"size:191;not null;default:'';index"`
	ProjectID         int    `json:"-" gorm:"index;not null;default:0"`
	PayerUserID       int    `json:"-" gorm:"not null;default:0"`
	ActorUserID       int    `json:"-" gorm:"not null;default:0"`

	ID             uint             `json:"id" gorm:"primaryKey"`
	BudgetID       uint             `json:"budget_id" gorm:"index"`
	Scope          string           `json:"scope" gorm:"size:16;index"`
	UserID         int              `json:"user_id" gorm:"index"`
	TokenID        int              `json:"token_id" gorm:"index"`
	SessionID      string           `json:"session_id" gorm:"size:64;index"`
	AgentID        string           `json:"agent_id" gorm:"size:64;index"`
	RequestID      string           `json:"request_id" gorm:"size:64;index"`
	Period         string           `json:"period" gorm:"size:16"`
	PeriodStart    int64            `json:"period_start" gorm:"index"`
	PeriodEnd      int64            `json:"period_end" gorm:"index"`
	AmountMicrousd billing.MicroUSD `json:"amount_microusd" gorm:"type:bigint"`
	CreatedAt      time.Time        `json:"created_at" gorm:"index"`
}

type SpendBudgetChargeInput struct {
	ProjectID     int
	PayerUserID   int
	ActorUserID   int
	UserID        int
	TokenID       int
	SessionID     string
	AgentID       string
	RequestID     string
	DeltaMicrousd billing.MicroUSD
	OccurredAt    time.Time
	// LongRunning marks a persisted job whose work outlives any one connection,
	// so its reservation gets the job TTL rather than the relay one.
	LongRunning bool
}

type SpendBudgetBlockError struct {
	Budget SpendBudget
	Alert  SpendBudgetAlert
}

func (e SpendBudgetBlockError) Error() string {
	return fmt.Sprintf("spend budget exceeded: scope=%s budget_id=%d spent=%d limit=%d", e.Budget.Scope, e.Budget.ID, e.Alert.SpentMicrousd, e.Alert.LimitMicrousd)
}

func (e SpendBudgetBlockError) Unwrap() error {
	return ErrSpendBudgetExceeded
}

func (b *SpendBudget) Normalize() {
	b.Scope = normalizeSpendBudgetScope(b.Scope)
	b.Period = normalizeSpendBudgetPeriod(b.Period)
	b.Enforcement = normalizeSpendBudgetEnforcement(b.Enforcement)
	if b.WarningThresholdPct <= 0 || b.WarningThresholdPct >= 100 {
		b.WarningThresholdPct = 80
	}
	if b.LimitMicrousd < 0 {
		b.LimitMicrousd = 0
	}
	if b.Scope == SpendBudgetScopeOrg {
		b.UserID = 0
		b.TokenID = 0
	}
	if b.Scope == SpendBudgetScopeUser {
		b.TokenID = 0
	}
}

func (b *SpendBudget) BeforeSave(*gorm.DB) error {
	b.Normalize()
	if b.Scope == SpendBudgetScopeUser && b.UserID <= 0 {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "user_id", "reason": "a user budget needs user_id"})
	}
	if b.Scope == SpendBudgetScopeToken && b.TokenID <= 0 {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "token_id", "reason": "a key budget needs token_id"})
	}
	if b.LimitMicrousd < 0 || (b.ProjectID == 0 && b.LimitMicrousd == 0) {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "limit_microusd", "reason": "limit_microusd must be positive"})
	}
	return nil
}

func ListSpendBudgets(limit int) ([]SpendBudget, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []SpendBudget
	err := CoreDB().Order("updated_at DESC, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func ListSpendBudgetsPage(limit int, offset int) ([]SpendBudget, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := CoreDB().Model(&SpendBudget{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []SpendBudget
	err := CoreDB().Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func GetSpendBudget(id uint) (*SpendBudget, error) {
	var row SpendBudget
	err := CoreDB().First(&row, id).Error
	return &row, err
}

func SaveSpendBudget(row SpendBudget) (*SpendBudget, error) {
	row.Normalize()
	if row.ID > 0 {
		var existing SpendBudget
		if err := CoreDB().First(&existing, row.ID).Error; err != nil {
			return nil, err
		}
		row.CreatedAt = existing.CreatedAt
	}
	if err := CoreDB().Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func DeleteSpendBudget(id uint) error {
	return CoreDB().Delete(&SpendBudget{}, id).Error
}

func ListSpendBudgetAlerts(limit int) ([]SpendBudgetAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []SpendBudgetAlert
	err := CoreDB().Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func CheckSpendBudgetBeforeCharge(input SpendBudgetChargeInput) error {
	input = normalizeSpendBudgetChargeInput(input)
	if input.DeltaMicrousd <= 0 || CoreDB() == nil {
		return nil
	}
	if !spendBudgetTablesReady(CoreDB(), true, true, true) {
		return nil
	}
	var blockErr error
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		return checkSpendBudgetBeforeChargeTx(tx, input, &blockErr)
	})
	if err != nil {
		return err
	}
	return blockErr
}

func checkSpendBudgetBeforeChargeTx(tx *gorm.DB, input SpendBudgetChargeInput, blockErr *error) error {
	if tx == nil || input.DeltaMicrousd <= 0 {
		return nil
	}
	if input.ProjectID > 0 {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", input.ProjectID).Find(&project).Error; err != nil {
			return err
		}
	}
	if err := deleteExpiredSpendBudgetReservations(tx, input.OccurredAt.Unix()); err != nil {
		return err
	}
	input = normalizeSpendBudgetChargeInput(input)
	budgets, err := applicableProjectSpendBudgetsWithDB(tx, input, true)
	if err != nil {
		return err
	}
	type reservationPlan struct {
		budget      SpendBudget
		periodStart int64
		periodEnd   int64
	}
	plans := make([]reservationPlan, 0, len(budgets))
	for _, budget := range budgets {
		if budget.Enforcement != SpendBudgetEnforcementBlock {
			continue
		}
		periodStart, periodEnd := spendBudgetPeriodWindow(budget.Period, input.OccurredAt)
		spent, err := sumSpendMicrousdForBudgetWithDB(tx, budget, periodStart, periodEnd)
		if err != nil {
			return err
		}
		reserved, err := sumReservedSpendMicrousdForBudget(tx, budget, periodStart, periodEnd, input.RequestID)
		if err != nil {
			return err
		}
		projected := spent + reserved + input.DeltaMicrousd
		if projected > budget.LimitMicrousd {
			alert := spendBudgetAlertFromInput(budget, input, SpendBudgetAlertBlocked, 100, periodStart, periodEnd, projected)
			alert.Message = "Budget would be exceeded; request blocked before upstream call"
			if err := tx.Create(&alert).Error; err != nil {
				return err
			}
			if blockErr != nil {
				*blockErr = SpendBudgetBlockError{Budget: budget, Alert: alert}
			}
			return nil
		}
		plans = append(plans, reservationPlan{budget: budget, periodStart: periodStart, periodEnd: periodEnd})
	}
	if input.ProjectID > 0 {
		start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, input.OccurredAt)
		if err := reserveSpendBudgetCharge(tx, SpendBudget{Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodMonth}, input, start, end); err != nil {
			return err
		}
	}
	for _, plan := range plans {
		if err := reserveSpendBudgetCharge(tx, plan.budget, input, plan.periodStart, plan.periodEnd); err != nil {
			return err
		}
	}
	return nil
}

func RecordSpendBudgetCharge(input SpendBudgetChargeInput) error {
	input = normalizeSpendBudgetChargeInput(input)
	if input.DeltaMicrousd <= 0 || CoreDB() == nil {
		return nil
	}
	if !spendBudgetTablesReady(CoreDB(), true, false, true) {
		return nil
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		return recordSpendBudgetChargeTx(tx, input)
	})
}

// ErrSpendBudgetChargeOutsideLedger refuses a budget charge written straight
// to SQL while the Redis ledger is on. With the ledger on, budget spend lives
// in its hot counters and reaches SQL only through settlement events; a row
// written here exists in SQL alone, so admission never sees it and the
// reconciler reports the budget as drifted until its period ends.
var ErrSpendBudgetChargeOutsideLedger = errors.New("spend budget charge must settle through the gateway ledger")

func recordSpendBudgetChargeTx(tx *gorm.DB, input SpendBudgetChargeInput) error {
	if tx == nil || input.DeltaMicrousd <= 0 {
		return nil
	}
	if GatewayLedgerEnabled() {
		return ErrSpendBudgetChargeOutsideLedger
	}
	input = normalizeSpendBudgetChargeInput(input)
	if input.ProjectID > 0 {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", input.ProjectID).Find(&project).Error; err != nil {
			return err
		}
	}

	budgets, err := applicableProjectSpendBudgetsWithDB(tx, input, true)
	if err != nil {
		return err
	}
	for _, budget := range budgets {
		periodStart, periodEnd := spendBudgetPeriodWindow(budget.Period, input.OccurredAt)
		spentBefore, previousSpend, err := sumSpendBudgetAlertPeriodsWithDB(tx, budget, periodStart, periodEnd)
		if err != nil {
			return err
		}
		created, err := createSpendBudgetChargeOnce(tx, budget, input, periodStart, periodEnd)
		if err != nil {
			return err
		}
		if !created {
			continue
		}
		spentAfter := spentBefore + input.DeltaMicrousd
		if err := recordSpendBudgetThresholdAlerts(tx, budget, input, periodStart, periodEnd, spentBefore, spentAfter); err != nil {
			return err
		}
		if err := recordSpendBudgetBurstAlert(tx, budget, input, periodStart, periodEnd, spentAfter, previousSpend); err != nil {
			return err
		}
	}
	return recordProjectSpendTx(tx, input)
}

func applicableProjectSpendBudgetsWithDB(tx *gorm.DB, input SpendBudgetChargeInput, lock bool) ([]SpendBudget, error) {
	var budgets []SpendBudget
	if tx == nil {
		return budgets, nil
	}
	query := tx.Where("enabled = ?", true)
	if input.ProjectID > 0 {
		// Global platform caps always apply. A request without a resolved actor
		// must not match a user budget, including the payer's.
		scopes := tx.Where("scope = ? AND project_id IN ?", SpendBudgetScopeOrg, []int{0, input.ProjectID})
		if input.ActorUserID > 0 {
			scopes = scopes.Or("scope = ? AND project_id = ? AND user_id = ?", SpendBudgetScopeUser, input.ProjectID, input.ActorUserID)
		}
		if input.TokenID > 0 {
			scopes = scopes.Or("scope = ? AND project_id = ? AND token_id = ?", SpendBudgetScopeToken, input.ProjectID, input.TokenID)
		}
		query = query.Where(scopes)
	} else {
		query = query.Where("project_id = 0").Where(tx.Where("scope = ?", SpendBudgetScopeOrg).Or("scope = ? AND user_id = ?", SpendBudgetScopeUser, input.UserID).Or("scope = ? AND token_id = ?", SpendBudgetScopeToken, input.TokenID))
	}
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	// Consistent lock order prevents concurrent requests from taking overlapping
	// global/project/user budget locks in opposite orders.
	if err := query.Order("id ASC").Find(&budgets).Error; err != nil {
		return nil, err
	}
	if input.ProjectID > 0 {
		rank := func(b SpendBudget) int {
			if b.ProjectID == 0 {
				return 0
			}
			switch b.Scope {
			case SpendBudgetScopeToken:
				return 1
			case SpendBudgetScopeUser:
				return 2
			default:
				return 3
			}
		}
		sort.SliceStable(budgets, func(i, j int) bool { return rank(budgets[i]) < rank(budgets[j]) })
	}
	return budgets, nil
}

func recordSpendBudgetThresholdAlerts(tx *gorm.DB, budget SpendBudget, input SpendBudgetChargeInput, periodStart int64, periodEnd int64, spentBefore billing.MicroUSD, spentAfter billing.MicroUSD) error {
	thresholds := []struct {
		kind string
		pct  int
	}{
		{kind: SpendBudgetAlertThreshold, pct: budget.WarningThresholdPct},
		{kind: SpendBudgetAlertExceeded, pct: 100},
	}
	for _, threshold := range thresholds {
		limitAtThreshold := budget.LimitMicrousd * billing.MicroUSD(threshold.pct) / 100
		if spentBefore >= limitAtThreshold || spentAfter < limitAtThreshold {
			continue
		}
		alert := spendBudgetAlertFromInput(budget, input, threshold.kind, threshold.pct, periodStart, periodEnd, spentAfter)
		alert.Message = fmt.Sprintf("Spend reached %d%% of budget", threshold.pct)
		if err := createSpendBudgetAlertOnce(tx, alert); err != nil {
			return err
		}
		kind := projectBudgetWarn
		if threshold.kind == SpendBudgetAlertExceeded {
			kind = projectBudgetOut
		}
		// A notice is best effort: a mail that cannot be built or queued must
		// never roll back the settlement batch that crossed the line.
		if err := queueProjectBudgetNoticesSafeTx(tx, budget, kind, periodStart, periodEnd, spentAfter); err != nil {
			common.SysLog("project budget notice: " + err.Error())
		}
	}
	return nil
}

func recordSpendBudgetBurstAlert(tx *gorm.DB, budget SpendBudget, input SpendBudgetChargeInput, periodStart int64, periodEnd int64, spentAfter billing.MicroUSD, previousSpend billing.MicroUSD) error {
	if budget.BurstMultiplier <= 0 {
		return nil
	}
	if previousSpend <= 0 {
		return nil
	}
	if float64(spentAfter) < float64(previousSpend)*budget.BurstMultiplier {
		return nil
	}
	alert := spendBudgetAlertFromInput(budget, input, SpendBudgetAlertBurst, 0, periodStart, periodEnd, spentAfter)
	alert.Message = fmt.Sprintf("Spend is %.2fx previous period", float64(spentAfter)/float64(previousSpend))
	return createSpendBudgetAlertOnce(tx, alert)
}

func createSpendBudgetChargeOnce(tx *gorm.DB, budget SpendBudget, input SpendBudgetChargeInput, periodStart int64, periodEnd int64) (bool, error) {
	if tx == nil {
		return false, errors.New("database is not initialized")
	}
	if input.RequestID != "" {
		var count int64
		if err := tx.Model(&SpendBudgetCharge{}).
			Where("budget_id = ? AND request_id = ? AND period_start = ?", budget.ID, input.RequestID, periodStart).
			Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return false, nil
		}
	}
	row := SpendBudgetCharge{
		ProjectID:      input.ProjectID,
		PayerUserID:    input.PayerUserID,
		ActorUserID:    input.ActorUserID,
		BudgetID:       budget.ID,
		Scope:          budget.Scope,
		UserID:         input.UserID,
		TokenID:        input.TokenID,
		SessionID:      input.SessionID,
		AgentID:        input.AgentID,
		RequestID:      input.RequestID,
		Period:         budget.Period,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		AmountMicrousd: input.DeltaMicrousd,
		CreatedAt:      input.OccurredAt,
	}
	if err := tx.Create(&row).Error; err != nil {
		return false, err
	}
	return true, nil
}

func sumSpendBudgetAlertPeriodsWithDB(tx *gorm.DB, budget SpendBudget, periodStart int64, periodEnd int64) (billing.MicroUSD, billing.MicroUSD, error) {
	if budget.BurstMultiplier <= 0 || periodEnd <= periodStart {
		currentSpend, err := sumSpendMicrousdForBudgetWithDB(tx, budget, periodStart, periodEnd)
		return currentSpend, 0, err
	}
	return sumSpendMicrousdForBudgetPairWithDB(tx, budget, periodStart, periodEnd)
}

func createSpendBudgetAlertOnce(tx *gorm.DB, alert SpendBudgetAlert) error {
	if tx == nil {
		tx = CoreDB()
	}
	if tx == nil {
		return errors.New("database is not initialized")
	}
	var count int64
	err := tx.Model(&SpendBudgetAlert{}).
		Where("budget_id = ? AND alert_type = ? AND threshold_pct = ? AND period_start = ?", alert.BudgetID, alert.AlertType, alert.ThresholdPct, alert.PeriodStart).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return tx.Create(&alert).Error
}

func sumSpendMicrousdForBudgetWithDB(db *gorm.DB, budget SpendBudget, periodStart int64, periodEnd int64) (billing.MicroUSD, error) {
	if budget.ProjectID > 0 {
		return sumProjectSpendTx(db, budget, periodStart, periodEnd)
	}
	var sum billing.MicroUSD
	tx, err := scopedSpendBudgetChargeQuery(db, budget, periodStart, periodEnd)
	if err != nil {
		return 0, err
	}
	// Permanent operations cover scoped consumption even before this budget
	// existed. Modern per-budget charge rows describe the original definition
	// and must not be added a second time when a definition is created/changed.
	if db.Migrator().HasTable(&RedisLedgerOperation{}) {
		tx = tx.Where("ledger_operation_id = ? OR ledger_operation_id IS NULL", "")
		var current billing.MicroUSD
		financial := db.Model(&RedisLedgerOperation{}).Where("kind IN ? AND created_at >= ? AND created_at < ?", []string{"settle", "refund"}, time.Unix(periodStart, 0).UTC(), time.Unix(periodEnd, 0).UTC())
		if budget.Scope == SpendBudgetScopeUser {
			financial = financial.Where("actor_id = ?", budget.UserID)
		}
		if budget.Scope == SpendBudgetScopeToken {
			financial = financial.Where("token_id = ?", budget.TokenID)
		}
		if err := financial.Select("COALESCE(SUM(amount),0)").Scan(&current).Error; err != nil {
			return 0, err
		}
		if err := tx.Select("COALESCE(SUM(amount_microusd),0)").Scan(&sum).Error; err != nil {
			return 0, err
		}
		return sum + current, nil
	}
	err = tx.Select("COALESCE(SUM(amount_microusd), 0)").Scan(&sum).Error
	return sum, err
}

func sumSpendMicrousdForBudgetPairWithDB(db *gorm.DB, budget SpendBudget, periodStart int64, periodEnd int64) (billing.MicroUSD, billing.MicroUSD, error) {
	if budget.ProjectID > 0 || db != nil && db.Migrator().HasTable(&RedisLedgerOperation{}) {
		current, err := sumSpendMicrousdForBudgetWithDB(db, budget, periodStart, periodEnd)
		if err != nil {
			return 0, 0, err
		}
		previous, err := sumSpendMicrousdForBudgetWithDB(db, budget, periodStart-(periodEnd-periodStart), periodStart)
		return current, previous, err
	}
	duration := periodEnd - periodStart
	if duration <= 0 {
		currentSpend, err := sumSpendMicrousdForBudgetWithDB(db, budget, periodStart, periodEnd)
		return currentSpend, 0, err
	}
	previousStart := periodStart - duration
	var sums struct {
		CurrentMicrousd  billing.MicroUSD `gorm:"column:current_microusd"`
		PreviousMicrousd billing.MicroUSD `gorm:"column:previous_microusd"`
	}
	tx, err := scopedSpendBudgetChargeQuery(db, budget, previousStart, periodEnd)
	if err != nil {
		return 0, 0, err
	}
	err = tx.Select(
		"COALESCE(SUM(CASE WHEN created_at >= ? AND created_at < ? THEN amount_microusd ELSE 0 END), 0) AS current_microusd, COALESCE(SUM(CASE WHEN created_at >= ? AND created_at < ? THEN amount_microusd ELSE 0 END), 0) AS previous_microusd",
		time.Unix(periodStart, 0).UTC(),
		time.Unix(periodEnd, 0).UTC(),
		time.Unix(previousStart, 0).UTC(),
		time.Unix(periodStart, 0).UTC(),
	).Scan(&sums).Error
	return sums.CurrentMicrousd, sums.PreviousMicrousd, err
}

func scopedSpendBudgetChargeQuery(db *gorm.DB, budget SpendBudget, periodStart int64, periodEnd int64) (*gorm.DB, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	tx := db.Model(&SpendBudgetCharge{}).
		Where("budget_id = ?", budget.ID).
		Where("created_at >= ? AND created_at < ?", time.Unix(periodStart, 0).UTC(), time.Unix(periodEnd, 0).UTC())
	switch budget.Scope {
	case SpendBudgetScopeToken:
		tx = tx.Where("token_id = ?", budget.TokenID)
	case SpendBudgetScopeUser:
		tx = tx.Where("user_id = ?", budget.UserID)
	case SpendBudgetScopeOrg:
	default:
		return nil, fmt.Errorf("unsupported spend budget scope %q", budget.Scope)
	}
	return tx, nil
}

func sumReservedSpendMicrousdForBudget(tx *gorm.DB, budget SpendBudget, periodStart int64, periodEnd int64, excludeRequestID string) (billing.MicroUSD, error) {
	var sum billing.MicroUSD
	if tx == nil {
		return 0, nil
	}
	query := tx.Model(&SpendBudgetReservation{}).
		Where("budget_id = ? AND period_start = ? AND period_end = ?", budget.ID, periodStart, periodEnd)
	if budget.ProjectID > 0 {
		query = tx.Model(&SpendBudgetReservation{}).Where("project_id = ? AND created_at >= ? AND created_at < ?", budget.ProjectID, time.Unix(periodStart, 0).UTC(), time.Unix(periodEnd, 0).UTC()).
			Where("budget_id = 0 OR (budget_id = ? AND NOT EXISTS (SELECT 1 FROM spend_budget_reservations sr WHERE sr.budget_id = 0 AND sr.project_id = spend_budget_reservations.project_id AND sr.request_id = spend_budget_reservations.request_id))", budget.ID)
		if budget.Scope == SpendBudgetScopeUser {
			query = query.Where("actor_user_id = ?", budget.UserID)
		}
		if budget.Scope == SpendBudgetScopeToken {
			query = query.Where("token_id = ?", budget.TokenID)
		}
	}
	if excludeRequestID != "" {
		query = query.Where("request_id <> ?", excludeRequestID)
	}
	err := query.Select("COALESCE(SUM(reserved_microusd), 0)").Scan(&sum).Error
	return sum, err
}

func reserveSpendBudgetCharge(tx *gorm.DB, budget SpendBudget, input SpendBudgetChargeInput, periodStart int64, periodEnd int64) error {
	if tx == nil || input.RequestID == "" || input.DeltaMicrousd <= 0 {
		return nil
	}
	if err := tx.Where("budget_id = ? AND period_start = ? AND request_id = ?", budget.ID, periodStart, input.RequestID).
		Delete(&SpendBudgetReservation{}).Error; err != nil {
		return err
	}
	row := SpendBudgetReservation{
		ProjectID:        input.ProjectID,
		PayerUserID:      input.PayerUserID,
		ActorUserID:      input.ActorUserID,
		BudgetID:         budget.ID,
		Scope:            budget.Scope,
		UserID:           input.UserID,
		TokenID:          input.TokenID,
		SessionID:        input.SessionID,
		AgentID:          input.AgentID,
		RequestID:        input.RequestID,
		Period:           budget.Period,
		PeriodStart:      periodStart,
		PeriodEnd:        periodEnd,
		ReservedMicrousd: input.DeltaMicrousd,
		LimitMicrousd:    budget.LimitMicrousd,
		ExpiresAt:        input.OccurredAt.Add(spendBudgetReservationLifetime(input)).Unix(),
		CreatedAt:        input.OccurredAt,
	}
	return tx.Create(&row).Error
}

// spendBudgetReservationLifetime picks how long a hold may outlive the request
// that took it. See the TTL constants for why the two differ.
func spendBudgetReservationLifetime(input SpendBudgetChargeInput) time.Duration {
	if input.LongRunning {
		return spendBudgetJobReservationTTL
	}
	return spendBudgetReservationTTL
}

func deleteExpiredSpendBudgetReservations(tx *gorm.DB, nowUnix int64) error {
	if tx == nil || nowUnix <= 0 {
		return nil
	}
	return tx.Where("expires_at > 0 AND expires_at <= ?", nowUnix).Delete(&SpendBudgetReservation{}).Error
}

func ReleaseSpendBudgetReservation(input SpendBudgetChargeInput) error {
	input = normalizeSpendBudgetChargeInput(input)
	if CoreDB() == nil || input.RequestID == "" {
		return nil
	}
	if !spendBudgetTablesReady(CoreDB(), false, true, false) {
		return nil
	}
	tx := CoreDB().Where("request_id = ?", input.RequestID)
	if input.ProjectID > 0 {
		tx = tx.Where("project_id = ?", input.ProjectID)
	}
	if input.UserID > 0 {
		tx = tx.Where("user_id = ?", input.UserID)
	}
	if input.TokenID > 0 {
		tx = tx.Where("token_id = ?", input.TokenID)
	}
	return tx.Delete(&SpendBudgetReservation{}).Error
}

func spendBudgetTablesReady(db *gorm.DB, needBudget bool, needReservation bool, needCharge bool) bool {
	if db == nil {
		return false
	}
	migrator := db.Migrator()
	if needBudget && !migrator.HasTable(&SpendBudget{}) {
		return false
	}
	if needReservation && !migrator.HasTable(&SpendBudgetReservation{}) {
		return false
	}
	if needCharge && !migrator.HasTable(&SpendBudgetCharge{}) {
		return false
	}
	return true
}

func spendBudgetAlertFromInput(budget SpendBudget, input SpendBudgetChargeInput, alertType string, thresholdPct int, periodStart int64, periodEnd int64, spent billing.MicroUSD) SpendBudgetAlert {
	return SpendBudgetAlert{
		ProjectID:     input.ProjectID,
		PayerUserID:   input.PayerUserID,
		ActorUserID:   input.ActorUserID,
		BudgetID:      budget.ID,
		Scope:         budget.Scope,
		UserID:        input.UserID,
		TokenID:       input.TokenID,
		SessionID:     input.SessionID,
		AgentID:       input.AgentID,
		RequestID:     input.RequestID,
		AlertType:     alertType,
		ThresholdPct:  thresholdPct,
		Period:        budget.Period,
		PeriodStart:   periodStart,
		PeriodEnd:     periodEnd,
		SpentMicrousd: spent,
		LimitMicrousd: budget.LimitMicrousd,
		DeltaMicrousd: input.DeltaMicrousd,
	}
}

func normalizeSpendBudgetChargeInput(input SpendBudgetChargeInput) SpendBudgetChargeInput {
	if input.ProjectID > 0 {
		input.UserID = input.ActorUserID
	} else {
		input.ActorUserID = input.UserID
		if input.PayerUserID == 0 {
			input.PayerUserID = input.UserID
		}
	}

	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	input.SessionID = truncateSpendBudgetString(input.SessionID, 64)
	input.AgentID = truncateSpendBudgetString(input.AgentID, 64)
	input.RequestID = truncateSpendBudgetString(input.RequestID, 64)
	return input
}

func normalizeSpendBudgetScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case SpendBudgetScopeToken:
		return SpendBudgetScopeToken
	case SpendBudgetScopeUser:
		return SpendBudgetScopeUser
	case SpendBudgetScopeOrg, "":
		return SpendBudgetScopeOrg
	default:
		return SpendBudgetScopeOrg
	}
}

func normalizeSpendBudgetPeriod(period string) string {
	switch strings.ToLower(strings.TrimSpace(period)) {
	case SpendBudgetPeriodDay:
		return SpendBudgetPeriodDay
	case SpendBudgetPeriodMonth, "":
		return SpendBudgetPeriodMonth
	default:
		return SpendBudgetPeriodMonth
	}
}

func normalizeSpendBudgetEnforcement(enforcement string) string {
	switch strings.ToLower(strings.TrimSpace(enforcement)) {
	case SpendBudgetEnforcementAlert:
		return SpendBudgetEnforcementAlert
	case SpendBudgetEnforcementBlock, "":
		return SpendBudgetEnforcementBlock
	default:
		return SpendBudgetEnforcementBlock
	}
}

func spendBudgetPeriodWindow(period string, now time.Time) (int64, int64) {
	now = now.UTC()
	switch normalizeSpendBudgetPeriod(period) {
	case SpendBudgetPeriodDay:
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start.Unix(), start.AddDate(0, 0, 1).Unix()
	default:
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start.Unix(), start.AddDate(0, 1, 0).Unix()
	}
}

func truncateSpendBudgetString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// FinalizeProjectSpendBudget commits the charge and reservation release
// together. Replays use the original accounting timestamp and request ID.
func FinalizeProjectSpendBudget(input SpendBudgetChargeInput) error {
	input = normalizeSpendBudgetChargeInput(input)
	if input.ProjectID <= 0 {
		return nil
	}
	if CoreDB() == nil {
		return errors.New("database is not initialized")
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := recordSpendBudgetChargeTx(tx, input); err != nil {
			return err
		}
		if input.RequestID == "" {
			return nil
		}
		return releaseSpendBudgetReservationTx(tx, input)
	})
}

// releaseSpendBudgetReservationTx drops the holds a charge has just replaced,
// in the charge's own transaction. Doing it afterwards leaves a settled job's
// hold standing for its whole lifetime whenever the process dies or the
// best-effort cleanup fails, and the cap then counts the job twice.
func releaseSpendBudgetReservationTx(tx *gorm.DB, input SpendBudgetChargeInput) error {
	if tx == nil || input.RequestID == "" || input.ProjectID <= 0 {
		return nil
	}
	query := tx.Where("request_id = ? AND project_id = ? AND user_id = ?", input.RequestID, input.ProjectID, input.ActorUserID)
	if input.TokenID > 0 {
		query = query.Where("token_id = ?", input.TokenID)
	}
	return query.Delete(&SpendBudgetReservation{}).Error
}
