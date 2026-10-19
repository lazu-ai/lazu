package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
)

const LedgerBacklogAlertAmountOption = "LedgerBacklogAlertMicrousd"
const LedgerBacklogHardLimitOption = "LedgerBacklogHardLimitMicrousd"
const LedgerNegativeBalanceAlertOption = "LedgerNegativeBalanceAlertMicrousd"

// LedgerAlert is one finding of the ledger monitor or reconciler.
type LedgerAlert struct {
	// Key names what is wrong, such as budget:90. Delivery is throttled per key.
	Key string
	// Message is the raw diagnostic, logged when the alert is sent.
	Message string
	// Signature identifies the finding itself, such as a drift amount. A new
	// signature is sent at most once an hour; an unchanged one is repeated
	// once a day. Empty means Message.
	Signature string
	// Facts describe the finding for a person. They are looked up only when
	// the alert is about to be sent, so a suppressed repeat costs nothing.
	Facts func(context.Context) []LedgerAlertFact
}

type LedgerAlertFact struct {
	Label string
	Value string
	Mono  bool
}

// LedgerAlertNotice is what the operator notifier receives.
type LedgerAlertNotice struct {
	Key     string
	Message string
	Facts   []LedgerAlertFact
}

const (
	ledgerAlertChangedEvery   = time.Hour
	ledgerAlertUnchangedEvery = 24 * time.Hour
)

var ledgerAlertNotifier struct {
	sync.RWMutex
	send func(LedgerAlertNotice) error
}

type ledgerAlertClaim struct {
	until     time.Time
	signature string
	seenUntil time.Time
}

var ledgerAlertFallback struct {
	sync.Mutex
	until map[string]ledgerAlertClaim
}

func SetLedgerAlertNotifier(send func(LedgerAlertNotice) error) {
	ledgerAlertNotifier.Lock()
	defer ledgerAlertNotifier.Unlock()
	ledgerAlertNotifier.send = send
}

func ledgerOptionAmount(key string, fallback int64) int64 {
	n, err := strconv.ParseInt(GetOptionValue(key), 10, 64)
	if err != nil || n <= 0 || n > 9007199254740991 {
		return fallback
	}
	return n
}

func validateLedgerOption(key, value string) error {
	switch key {
	case LedgerBacklogAlertAmountOption, LedgerBacklogHardLimitOption, LedgerNegativeBalanceAlertOption:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 || n > 9007199254740991 {
			return errors.New("ledger threshold must be a positive integral microdollar amount")
		}
	}
	return nil
}

type LedgerHealth struct {
	Exposure         int64 `json:"exposure_microusd"`
	Uncommitted      int64 `json:"uncommitted"`
	OldestAgeSeconds int64 `json:"oldest_age_seconds"`
	Dead             int64 `json:"dead"`
	DeadAgeSeconds   int64 `json:"dead_age_seconds"`
	Reservations     int64 `json:"reservations"`
	SpoolExposure    int64 `json:"spool_exposure_microusd"`
	SpoolUnknown     int64 `json:"spool_unknown_exposure_files"`
	Quarantined      int64 `json:"quarantined_files"`
}

func ReadLedgerHealth(ctx context.Context) (LedgerHealth, error) {
	var health LedgerHealth
	raw, err := common.RDB.Eval(ctx, `
local clock=redis.call('TIME');local now=clock[1]*1000+math.floor(clock[2]/1000)
local oldest=redis.call('ZRANGE',KEYS[2],0,0,'WITHSCORES');local age=0
if #oldest>0 then local stamp=tonumber(oldest[2]);if stamp<1000000000000 then stamp=stamp*1000 end;age=math.max(0,math.floor((now-stamp)/1000)) end
local dead=redis.call('XRANGE',KEYS[3],'-','+','COUNT',1);local deadage=0
if #dead>0 then local id=dead[1][1];local ms=tonumber(string.match(id,'^(%d+)'));deadage=math.max(0,math.floor((now-ms)/1000)) end
return cjson.encode({exposure_microusd=redis.call('GET',KEYS[1]) or '0',uncommitted=redis.call('ZCARD',KEYS[2]),oldest_age_seconds=age,dead=redis.call('HLEN',KEYS[4]),dead_age_seconds=deadage,reservations=redis.call('HLEN',KEYS[5])})`, []string{ledger.Prefix + "exposure", ledger.Prefix + "uncommitted", ledger.DeadStream, ledger.Prefix + "dead_ids", ledger.Prefix + "reservations"}).Text()
	if err != nil {
		return health, err
	}
	// cjson rounds a numeric amount to fourteen significant digits. Keep the
	// internal wire value decimal while preserving the public integer field.
	wire := struct {
		*LedgerHealth
		Exposure json.Number `json:"exposure_microusd"`
	}{LedgerHealth: &health}
	if err = json.Unmarshal([]byte(raw), &wire); err != nil {
		return health, err
	}
	health.Exposure, err = wire.Exposure.Int64()
	if err == nil && health.Exposure < 0 {
		err = errors.New("negative ledger exposure")
	}
	if err == nil {
		if engine := GatewayLedger(); engine != nil && engine.SpoolDir != "" {
			usage, spoolErr := engine.SpoolUsage()
			if spoolErr != nil {
				return health, spoolErr
			}
			health.SpoolExposure, health.SpoolUnknown, health.Quarantined = usage.UnresolvedExposure, usage.UnknownExposureFiles, usage.QuarantinedFiles
			health.Exposure, err = checkedLedgerAdd(health.Exposure, health.SpoolExposure)
		}
	}
	return health, err
}

func notifyLedgerAlert(ctx context.Context, key, message string) {
	notifyLedgerFinding(ctx, LedgerAlert{Key: key, Message: message})
}

func notifyLedgerFinding(ctx context.Context, alert LedgerAlert) {
	ledgerAlertNotifier.RLock()
	send := ledgerAlertNotifier.send
	ledgerAlertNotifier.RUnlock()
	if send == nil {
		common.SysError("ledger alert " + alert.Key + ": " + alert.Message)
		return
	}
	signature := alert.Signature
	if signature == "" {
		signature = alert.Message
	}
	allowed, release := claimLedgerAlert(ctx, alert.Key, signature)
	if !allowed {
		return
	}
	common.SysError("ledger alert " + alert.Key + ": " + alert.Message)
	notice := LedgerAlertNotice{Key: alert.Key, Message: alert.Message}
	if alert.Facts != nil {
		notice.Facts = alert.Facts(ctx)
	}
	if err := send(notice); err != nil {
		common.SysError("ledger alert delivery: " + err.Error())
		release()
	}
}

// claimLedgerAlert decides whether a finding is sent now. The finding's
// signature is remembered for a day, so an unchanged drift mails once a day
// instead of every hour, while a changed one still mails within the hour.
func claimLedgerAlert(ctx context.Context, key, signature string) (bool, func()) {
	throttleKey, seenKey := ledger.Prefix+"alert:"+key, ledger.Prefix+"alert_seen:"+key
	seen, err := common.RDB.Get(ctx, seenKey).Result()
	if err == nil || errors.Is(err, redis.Nil) {
		if err == nil && seen == signature {
			return false, nil
		}
		allowed, setErr := common.RDB.SetNX(ctx, throttleKey, "1", ledgerAlertChangedEvery).Result()
		if setErr == nil {
			if !allowed {
				return false, nil
			}
			common.RDB.Set(ctx, seenKey, signature, ledgerAlertUnchangedEvery)
			return true, func() { common.RDB.Del(ctx, throttleKey, seenKey) }
		}
	}
	// AOF write failures can reject Redis writes themselves. Keep a
	// process-local claim so a durability alert still reaches the operator.
	ledgerAlertFallback.Lock()
	defer ledgerAlertFallback.Unlock()
	if ledgerAlertFallback.until == nil {
		ledgerAlertFallback.until = map[string]ledgerAlertClaim{}
	}
	now := time.Now()
	claim := ledgerAlertFallback.until[key]
	if (claim.signature == signature && now.Before(claim.seenUntil)) || now.Before(claim.until) {
		return false, nil
	}
	ledgerAlertFallback.until[key] = ledgerAlertClaim{until: now.Add(ledgerAlertChangedEvery), signature: signature, seenUntil: now.Add(ledgerAlertUnchangedEvery)}
	return true, func() {
		ledgerAlertFallback.Lock()
		delete(ledgerAlertFallback.until, key)
		ledgerAlertFallback.Unlock()
	}
}

// ledgerAlertUSD renders microUSD exactly, as $0.824522.
func ledgerAlertUSD(micro int64) string {
	sign := ""
	if micro < 0 {
		sign, micro = "-", -micro
	}
	return fmt.Sprintf("%s$%d.%06d", sign, micro/1_000_000, micro%1_000_000)
}

// ledgerAlertDrift renders expected minus hot with its sign: how much the
// database holds that the hot counter does not.
func ledgerAlertDrift(hot, expected int64) string {
	drift := expected - hot
	if drift > 0 {
		return "+" + ledgerAlertUSD(drift)
	}
	return ledgerAlertUSD(drift)
}

func ledgerAlertAccount(ctx context.Context, userID int) string {
	var user User
	if userID <= 0 || CoreDB().WithContext(ctx).Unscoped().Select("id", "username").First(&user, userID).Error != nil {
		return fmt.Sprintf("#%d", userID)
	}
	return fmt.Sprintf("%s (#%d)", user.Username, userID)
}

func ledgerAlertKey(ctx context.Context, tokenID int) string {
	var token Token
	if tokenID <= 0 || CoreDB().WithContext(ctx).Unscoped().Select("id", "name").First(&token, tokenID).Error != nil {
		return fmt.Sprintf("#%d", tokenID)
	}
	return fmt.Sprintf("%s (#%d)", token.Name, tokenID)
}

func ledgerAlertProject(ctx context.Context, projectID int) string {
	var project Project
	if projectID <= 0 || CoreDB().WithContext(ctx).Select("id", "name").First(&project, projectID).Error != nil {
		return fmt.Sprintf("#%d", projectID)
	}
	return fmt.Sprintf("%s (#%d)", project.Name, projectID)
}

func ledgerBudgetAlertFacts(ctx context.Context, budget ledger.Budget, spent, expectedSpent, reserved, expectedReserved int64) []LedgerAlertFact {
	facts := []LedgerAlertFact{{Label: "预算", Value: fmt.Sprintf("#%d · %s · %s 起（UTC）", budget.ID, budget.Period, time.Unix(budget.Start, 0).UTC().Format("2006-01-02"))}}
	switch budget.Scope {
	case SpendBudgetScopeToken:
		facts = append(facts, LedgerAlertFact{Label: "Key", Value: ledgerAlertKey(ctx, budget.TokenID)})
	case SpendBudgetScopeUser:
		facts = append(facts, LedgerAlertFact{Label: "成员", Value: ledgerAlertAccount(ctx, budget.UserID)})
	}
	if budget.ProjectID > 0 {
		facts = append(facts, LedgerAlertFact{Label: "项目", Value: ledgerAlertProject(ctx, budget.ProjectID)})
	}
	facts = append(facts,
		LedgerAlertFact{Label: "Redis 已花", Value: ledgerAlertUSD(spent), Mono: true},
		LedgerAlertFact{Label: "数据库已花", Value: ledgerAlertUSD(expectedSpent), Mono: true},
		LedgerAlertFact{Label: "差额（数据库 − Redis）", Value: ledgerAlertDrift(spent, expectedSpent), Mono: true},
	)
	if reserved != expectedReserved {
		facts = append(facts,
			LedgerAlertFact{Label: "Redis 预留", Value: ledgerAlertUSD(reserved), Mono: true},
			LedgerAlertFact{Label: "数据库预留", Value: ledgerAlertUSD(expectedReserved), Mono: true},
		)
	}
	// Rows without a ledger operation were written to SQL directly; they are
	// the usual reason the database holds spend the hot counter never saw.
	var direct struct {
		Count  int64
		Amount int64
	}
	if CoreDB().WithContext(ctx).Model(&SpendBudgetCharge{}).
		Where("budget_id = ? AND created_at >= ? AND created_at < ?", budget.ID, time.Unix(budget.Start, 0).UTC(), time.Unix(budget.End, 0).UTC()).
		Where("ledger_operation_id = ? OR ledger_operation_id IS NULL", "").
		Select("COUNT(*) AS count, COALESCE(SUM(amount_microusd),0) AS amount").Scan(&direct).Error == nil && direct.Count > 0 {
		facts = append(facts, LedgerAlertFact{Label: "未经账本写入的 SQL 记录", Value: fmt.Sprintf("%d 行 · %s", direct.Count, ledgerAlertUSD(direct.Amount)), Mono: true})
	}
	return facts
}

// Jobs checks exposure every ten seconds and reconciles every minute. A
// mismatch is observable and never repaired by overwriting authoritative money.
func MonitorGatewayLedger(ctx context.Context) error {
	// This runs in the independent jobs observer, never in admission.
	memory, err := common.RDB.Info(ctx, "memory").Result()
	if err != nil {
		return err
	}
	persistence, err := common.RDB.Info(ctx, "persistence").Result()
	if err != nil {
		return err
	}
	for key, message := range redisDurabilityAlerts(memory, persistence) {
		notifyLedgerAlert(ctx, key, message)
	}
	health, err := ReadLedgerHealth(ctx)
	if err != nil {
		return err
	}
	if health.OldestAgeSeconds > 60 || health.Exposure > ledgerOptionAmount(LedgerBacklogAlertAmountOption, 10_000_000) {
		notifyLedgerAlert(ctx, "backlog", fmt.Sprintf("uncommitted=%d exposure_microusd=%d oldest_age_seconds=%d", health.Uncommitted, health.Exposure, health.OldestAgeSeconds))
	}
	if health.Dead > 0 && health.DeadAgeSeconds > 24*60*60 {
		notifyLedgerAlert(ctx, "dead-letter-24h", fmt.Sprintf("dead=%d oldest_age_seconds=%d", health.Dead, health.DeadAgeSeconds))
	}
	if health.Quarantined > 0 || health.SpoolUnknown > 0 {
		notifyLedgerAlert(ctx, "spool-quarantine", fmt.Sprintf("quarantined_files=%d unresolved_spool_exposure_microusd=%d unknown_exposure_files=%d; unresolved evidence still prevents financial recovery", health.Quarantined, health.SpoolExposure, health.SpoolUnknown))
	}
	allowed, err := common.RDB.SetNX(ctx, ledger.Prefix+"reconciliation_tick", "1", time.Minute).Result()
	if err != nil || !allowed {
		return err
	}
	return ReconcileGatewayLedger(ctx)
}

func ReconcileGatewayLedger(ctx context.Context) error {
	engine := GatewayLedger()
	if engine == nil {
		return nil
	}
	var cursor uint64
	for {
		keys, next, err := common.RDB.Scan(ctx, cursor, ledger.Prefix+"wallet:*", 500).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			payerID, err := strconv.Atoi(strings.TrimPrefix(key, ledger.Prefix+"wallet:"))
			if err != nil {
				return err
			}
			if err := reconcileLedgerPayer(ctx, engine, payerID); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return reconcileLedgerBudgets(ctx, engine)
}

func reconcileLedgerPayer(ctx context.Context, engine *ledger.Engine, payerID int) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hot, err := common.RDB.HGetAll(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, payerID)).Result()
	if err != nil || len(hot) == 0 {
		return err
	}
	available, err := strconv.ParseInt(hot["avail"], 10, 64)
	if err != nil {
		return err
	}
	seq, err := strconv.ParseInt(hot["credit_seq"], 10, 64)
	if err != nil {
		return err
	}
	expected, err := engine.Storage.Snapshot(ctx, ledger.Input{PayerID: payerID, Unlimited: true})
	if err != nil {
		return err
	}
	var notYetSynced int64
	// The original open/closed sequence range is empty when the hot sequence
	// has caught up (or is ahead). Preserve that zero result without a SQL trip.
	if expected.CreditSeq > seq {
		if err := CoreDB().WithContext(ctx).Model(&LedgerCreditSync{}).Where("user_id = ? AND sequence > ? AND sequence <= ?", payerID, seq, expected.CreditSeq).Select("COALESCE(SUM(amount),0)").Scan(&notYetSynced).Error; err != nil {
			return err
		}
	}
	expected.Balance, err = checkedLedgerSubtract(expected.Balance, notYetSynced)
	if err != nil {
		return err
	}
	after, err := common.RDB.HGetAll(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, payerID)).Result()
	if err != nil {
		return err
	}
	if len(after) == 0 || hot["revision"] != after["revision"] {
		return nil
	}
	if hot["generation"] != "" {
		gens, err := readIdentityGenerations(ctx, []string{fmt.Sprintf("user:%d", payerID), "policy"}, false)
		if errors.Is(err, ErrIdentityStale) || errors.Is(err, ErrIdentityUpdating) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(gens) != 2 || hot["generation"] != gens[0].Value+":"+gens[1].Value {
			return nil
		}
	}
	if available != expected.Balance {
		hot, want := available, expected.Balance
		notifyLedgerFinding(ctx, LedgerAlert{
			Key:       fmt.Sprintf("balance:%d", payerID),
			Message:   fmt.Sprintf("payer_id=%d hot_microusd=%d expected_microusd=%d credit_seq=%d", payerID, available, expected.Balance, seq),
			Signature: fmt.Sprintf("drift=%d", want-hot),
			Facts: func(ctx context.Context) []LedgerAlertFact {
				return []LedgerAlertFact{
					{Label: "账户", Value: ledgerAlertAccount(ctx, payerID)},
					{Label: "Redis 可用余额", Value: ledgerAlertUSD(hot), Mono: true},
					{Label: "数据库预期余额", Value: ledgerAlertUSD(want), Mono: true},
					{Label: "差额（数据库 − Redis）", Value: ledgerAlertDrift(hot, want), Mono: true},
				}
			},
		})
	}
	if available < -ledgerOptionAmount(LedgerNegativeBalanceAlertOption, 5_000_000) {
		hot := available
		notifyLedgerFinding(ctx, LedgerAlert{
			Key:     fmt.Sprintf("negative:%d", payerID),
			Message: fmt.Sprintf("payer_id=%d available_microusd=%d", payerID, available),
			Facts: func(ctx context.Context) []LedgerAlertFact {
				return []LedgerAlertFact{
					{Label: "账户", Value: ledgerAlertAccount(ctx, payerID)},
					{Label: "可用余额", Value: ledgerAlertUSD(hot), Mono: true},
				}
			},
		})
	}
	return reconcileLedgerKeys(ctx, engine, payerID)
}

func reconcileLedgerKeys(ctx context.Context, engine *ledger.Engine, payerID int) error {
	var ids []int
	if err := CoreDB().WithContext(ctx).Unscoped().Model(&Token{}).Joins("LEFT JOIN projects ON projects.id = tokens.project_id").Where("tokens.unlimited_quota = ? AND ((tokens.project_id > 0 AND projects.owner_user_id = ?) OR (tokens.project_id = 0 AND tokens.user_id = ?))", false, payerID, payerID).Pluck("tokens.id", &ids).Error; err != nil {
		return err
	}
	for _, tokenID := range ids {
		key := fmt.Sprintf("%stoken:%d", ledger.Prefix, tokenID)
		baseline, err := common.RDB.Get(ctx, fmt.Sprintf("%stoken_gen:%d", ledger.Prefix, tokenID)).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return err
		}
		gens, err := readIdentityGenerations(ctx, []string{fmt.Sprintf("token:%d", tokenID), "policy"}, false)
		if errors.Is(err, ErrIdentityStale) || errors.Is(err, ErrIdentityUpdating) {
			continue
		}
		if err != nil {
			return err
		}
		if len(gens) != 2 || baseline != gens[0].Value+":"+gens[1].Value {
			continue
		}
		before, err := common.RDB.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return err
		}
		walletBefore, err := common.RDB.HGet(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, payerID), "revision").Result()
		if err != nil {
			return err
		}
		expected, err := engine.Storage.Snapshot(ctx, ledger.Input{PayerID: payerID, TokenID: tokenID})
		if err != nil {
			return err
		}
		after, err := common.RDB.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return err
		}
		walletAfter, err := common.RDB.HGet(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, payerID), "revision").Result()
		if err != nil {
			return err
		}
		if before != after || walletBefore != walletAfter {
			continue
		}
		baselineAfter, err := common.RDB.Get(ctx, fmt.Sprintf("%stoken_gen:%d", ledger.Prefix, tokenID)).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return err
		}
		if baseline != baselineAfter {
			continue
		}
		if err := CheckIdentityGenerations(ctx, gens); errors.Is(err, ErrIdentityStale) || errors.Is(err, ErrIdentityUpdating) {
			continue
		} else if err != nil {
			return err
		}
		remaining, err := strconv.ParseInt(after, 10, 64)
		if err != nil {
			return err
		}
		if want := expected.TokenRemaining[tokenID]; remaining != want {
			hot, keyID := remaining, tokenID
			notifyLedgerFinding(ctx, LedgerAlert{
				Key:       fmt.Sprintf("key:%d", tokenID),
				Message:   fmt.Sprintf("token_id=%d hot_remaining_microusd=%d expected_remaining_microusd=%d", tokenID, remaining, want),
				Signature: fmt.Sprintf("drift=%d", want-hot),
				Facts: func(ctx context.Context) []LedgerAlertFact {
					return []LedgerAlertFact{
						{Label: "Key", Value: ledgerAlertKey(ctx, keyID)},
						{Label: "付费账户", Value: ledgerAlertAccount(ctx, payerID)},
						{Label: "Redis 剩余额度", Value: ledgerAlertUSD(hot), Mono: true},
						{Label: "数据库预期额度", Value: ledgerAlertUSD(want), Mono: true},
						{Label: "差额（数据库 − Redis）", Value: ledgerAlertDrift(hot, want), Mono: true},
					}
				},
			})
		}
	}
	return nil
}

func reconcileLedgerBudgets(ctx context.Context, engine *ledger.Engine) error {
	var definitions []SpendBudget
	if err := CoreDB().WithContext(ctx).Where("enabled = ?", true).Find(&definitions).Error; err != nil {
		return err
	}
	for _, budget := range LedgerBudgets(definitions, time.Now().UTC()) {
		before, err := common.RDB.HGetAll(ctx, budget.Key()).Result()
		if err != nil {
			return err
		}
		if len(before) == 0 {
			continue
		}
		// Storage reads a consistent SQL snapshot plus retained financial facts.
		// Revision detects concurrent reservations, refunds and settlements, so
		// shared budgets require no admission pause for this diagnostic read.
		expected, err := engine.Storage.Snapshot(ctx, ledger.Input{PayerID: 0, Unlimited: true, Budgets: []ledger.Budget{budget}})
		if err != nil {
			return err
		}
		after, err := common.RDB.HGetAll(ctx, budget.Key()).Result()
		if err != nil {
			return err
		}
		if before["revision"] != after["revision"] || len(after) == 0 {
			continue
		}
		spent, err := strconv.ParseInt(after["spent"], 10, 64)
		if err != nil {
			return err
		}
		reserved, err := strconv.ParseInt(after["reserved"], 10, 64)
		if err != nil {
			return err
		}
		expectedSpent, expectedReserved := expected.Budgets[budget.Key()], expected.BudgetReserved[budget.Key()]
		if spent != expectedSpent || reserved != expectedReserved {
			found, hotSpent, hotReserved := budget, spent, reserved
			notifyLedgerFinding(ctx, LedgerAlert{
				Key:       fmt.Sprintf("budget:%d", budget.ID),
				Message:   fmt.Sprintf("budget_id=%d hot_spent=%d expected_spent=%d hot_reserved=%d expected_reserved=%d", budget.ID, spent, expectedSpent, reserved, expectedReserved),
				Signature: fmt.Sprintf("period=%d spent_drift=%d reserved_drift=%d", budget.Start, expectedSpent-spent, expectedReserved-reserved),
				Facts: func(ctx context.Context) []LedgerAlertFact {
					return ledgerBudgetAlertFacts(ctx, found, hotSpent, expectedSpent, hotReserved, expectedReserved)
				},
			})
		}
	}
	return nil
}
