// Package ledger owns prepaid admission and durable, replayable settlement.
// Money uses integral microdollars. Redis is the admission authority; Storage
// commits the permanent financial record before a stream entry is acknowledged.
package ledger

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
)

const Prefix = "lazu:ledger:"
const Stream = Prefix + "events"
const Group = "ledger-writers"
const ContinuityKey = Prefix + "continuity"
const safeInteger int64 = 9007199254740991
const RateLimitsContextKey = "gateway_ledger_rate_limits"

var ErrUnavailable = errors.New("ledger unavailable")
var ErrRebuilding = errors.New("payer ledger rebuilding")

// ErrAdmissionPaused is the operator-wide admission pause (the maintenance key
// set for a deploy quiesce, recovery or backup). It is not a per-payer state:
// every payer is refused until the operator clears it, which can take minutes.
var ErrAdmissionPaused = errors.New("ledger admission paused for maintenance")
var ErrStale = errors.New("identity snapshot stale")
var ErrContinuity = fmt.Errorf("%w: financial continuity is missing or changed; explicit recovery required", ErrUnavailable)

type Rejection struct {
	Code              string
	BudgetID          uint
	RateDecision      string
	RateLimit         int64
	RetryAfterSeconds int64
	WindowMinutes     int
}

func (e *Rejection) Error() string { return e.Code }

type Generation struct {
	Key        string `json:"key"`
	PendingKey string `json:"pending_key"`
	Value      string `json:"value"`
}
type Budget struct {
	ProjectID int    `json:"project_id"`
	Scope     string `json:"scope"`
	UserID    int    `json:"user_id"`
	TokenID   int    `json:"token_id"`
	Period    string `json:"period"`
	ID        uint   `json:"id"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	Limit     int64  `json:"limit,string"`
	Block     bool   `json:"block"`
	Spent     int64  `json:"spent,string"`
}

func (b Budget) Key() string {
	return fmt.Sprintf("%sbudget:%d:%d:%d:%d:%s:%d:%d", Prefix, b.ID, b.Start, b.End, b.ProjectID, b.Scope, b.UserID, b.TokenID)
}

// RateLimit is one fixed-window admission bucket. OnSuccess buckets are
// checked before admission but incremented only by successful settlement.
type RateLimit struct {
	Key           string `json:"key"`
	Decision      string `json:"decision"`
	Limit         int64  `json:"limit,string"`
	Cost          int64  `json:"cost,string"`
	WindowSeconds int64  `json:"window_seconds"`
	OnSuccess     bool   `json:"on_success"`
}

type Input struct {
	SessionID          string `json:"session_id,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
	ReferenceRequestID string `json:"reference_request_id,omitempty"`
	ParentRequestID    string `json:"parent_request_id,omitempty"`
	WalletGeneration   string `json:"wallet_generation"`
	TokenGeneration    string `json:"token_generation"`
	RequestID          string `json:"request_id"`
	PayerID            int    `json:"payer_id"`
	ActorID            int    `json:"actor_id"`
	ProjectID          int    `json:"project_id"`
	TokenID            int    `json:"token_id"`
	Unlimited          bool   `json:"unlimited"`
	// Only an explicitly priced free model may admit a zero balance.
	AllowZeroBalance bool `json:"allow_zero_balance,omitempty"`
	// ClampToRemaining admits a worst-case estimate against a positive Key
	// remainder or wallet balance smaller than the estimate, holding only what
	// remains. Settlement records the actual charge, which may overdraw by at
	// most this one request. A zero or negative remainder still rejects.
	ClampToRemaining bool         `json:"clamp_to_remaining,omitempty"`
	Amount           int64        `json:"amount,string"`
	PricingVersion   int64        `json:"pricing_version,string"`
	Model            string       `json:"model"`
	OccurredAt       time.Time    `json:"occurred_at"`
	BudgetTimestamp  int64        `json:"budget_timestamp"`
	ExpiresAt        int64        `json:"expires_at"`
	Budgets          []Budget     `json:"budgets"`
	Generations      []Generation `json:"generations"`
	Rates            []RateLimit  `json:"rates"`
	RatePending      bool         `json:"rate_pending,omitempty"`
}
type Event struct {
	Input
	TelemetryEnvelope string `json:"telemetry_envelope,omitempty"`
	OperationID       string `json:"operation_id"`
	Kind              string `json:"kind"`
	StreamID          string `json:"-"`
}
type Snapshot struct {
	Balance            int64
	CreditSeq          int64
	TokenRemaining     map[int]int64
	Budgets            map[string]int64
	BudgetReserved     map[string]int64
	ReservationBudgets map[string][]string
}
type Credit struct {
	ID       uint
	PayerID  int
	Sequence int64
	Amount   int64
}

// Snapshot is used only under a payer fence. Apply must atomically deduplicate
// by financial operation identity and update balances and all budget records.
type Storage interface {
	Snapshot(context.Context, Input) (Snapshot, error)
	Settled(context.Context, string, string) (bool, error)
	Apply(context.Context, []Event) error
	PendingCredits(context.Context, int) ([]Credit, error)
	CreditApplied(context.Context, uint) error
}
type Engine struct {
	// Continuity is loaded from the permanent core record at startup. It is
	// immutable for this process; recovery fences every earlier process.
	Continuity         string
	AdmissionCheck     func() error
	SpoolFailure       func(error)
	SpoolQuarantine    func(QuarantinedOperation)
	TelemetryStream    string
	TelemetryMaxLength int64
	TelemetryEncode    func(Event) (string, error)
	Maintenance        func(context.Context) error
	// Observer performs read-only health/reconciliation work independently of
	// financial delivery. It must honor cancellation before Run can exit.
	Observer       func(context.Context) error
	SpoolDir       string
	Redis          *redis.Client
	Storage        Storage
	HardLimit      int64
	HardLimitValue func() int64
	Now            func() time.Time
	scripts        sync.Map // guardedScriptKey -> *redis.Script
}

type guardedScriptKey struct {
	continuity string
	source     string
}

func New(client *redis.Client, storage Storage) *Engine {
	return &Engine{Redis: client, Storage: storage, HardLimit: 100_000_000, Now: time.Now}
}
func wallet(id int) string     { return fmt.Sprintf("%swallet:%d", Prefix, id) }
func token(id int) string      { return fmt.Sprintf("%stoken:%d", Prefix, id) }
func fence(id int) string      { return fmt.Sprintf("%sfence:%d", Prefix, id) }
func request(id string) string { return Prefix + "request:" + id }

func validate(input Input) error {
	if input.PayerID <= 0 || input.RequestID == "" || input.Amount < 0 || input.Amount > safeInteger {
		return errors.New("invalid ledger input")
	}
	for _, b := range input.Budgets {
		if b.Limit < 0 || b.Limit > safeInteger {
			return errors.New("invalid budget")
		}
	}
	for _, rate := range input.Rates {
		if rate.Key == "" || rate.Decision == "" || rate.Limit <= 0 || rate.Limit > safeInteger || rate.Cost < 0 || rate.Cost > safeInteger || rate.WindowSeconds <= 0 || rate.WindowSeconds > 86400 {
			return errors.New("invalid rate bucket")
		}
	}
	return nil
}
func normalize(input Input) Input {
	if !input.OccurredAt.IsZero() {
		input.BudgetTimestamp = input.OccurredAt.Unix()
	}
	if input.Budgets == nil {
		input.Budgets = []Budget{}
	}
	if input.Generations == nil {
		input.Generations = []Generation{}
	}
	if input.Rates == nil {
		input.Rates = []RateLimit{}
	}
	return input
}
func (e *Engine) eval(ctx context.Context, script string, keys []string, args ...interface{}) (string, error) {
	result, err := e.evalRaw(ctx, script, keys, args...)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(result), nil
}
func (e *Engine) evalRaw(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	if e.Redis == nil {
		return "", ErrUnavailable
	}
	if e.Continuity == "" {
		return nil, ErrContinuity
	}
	// A literal prefix preserves every script's key/argument indexes, including
	// the variable-size acknowledgement batch. The check and money mutation
	// execute in the same Lua invocation and add no warm-path round trip.
	key := guardedScriptKey{continuity: e.Continuity, source: script}
	cached, found := e.scripts.Load(key)
	if !found {
		guarded := "if redis.call('GET'," + strconv.Quote(ContinuityKey) + ")~=" + strconv.Quote(key.continuity) + " then return 'continuity' end\n" + script
		cached, _ = e.scripts.LoadOrStore(key, redis.NewScript(guarded))
	}
	// Run uses one EVALSHA dispatch when warm, with the standard NOSCRIPT
	// fallback after startup or SCRIPT FLUSH. The cached script includes this
	// engine's continuity guard, which still executes atomically with mutation.
	result, err := cached.(*redis.Script).Run(ctx, e.Redis, keys, args...).Result()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if result == "continuity" {
		return nil, ErrContinuity
	}
	return result, nil
}

func (e *Engine) CheckContinuity(ctx context.Context) error {
	if e.Redis == nil || e.Continuity == "" {
		return ErrContinuity
	}
	value, err := e.Redis.Get(ctx, ContinuityKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrContinuity
		}
		return fmt.Errorf("%w: %w", ErrContinuity, err)
	}
	if value != e.Continuity {
		return ErrContinuity
	}
	return nil
}

// Initialize freezes this payer's ledger while reading its committed snapshot.
// SETNX alone is insufficient: a refill could otherwise overwrite a credit or
// settlement that ran after the database snapshot was read.
func (e *Engine) Initialize(ctx context.Context, input Input) error {
	if err := e.CheckContinuity(ctx); err != nil {
		return err
	}
	input = normalize(input)
	owner := strconv.FormatInt(e.Now().UnixNano(), 36) + ":" + input.RequestID
	budgetOwner := "budget:" + owner
	budgetLocked, err := e.Redis.SetNX(ctx, Prefix+"budget_fence", budgetOwner, 30*time.Second).Result()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if !budgetLocked {
		return ErrRebuilding
	}
	defer e.Redis.Eval(context.Background(), `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{Prefix + "budget_fence"}, budgetOwner)
	locked, err := e.Redis.SetNX(ctx, fence(input.PayerID), owner, 30*time.Second).Result()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if !locked {
		return ErrRebuilding
	}
	defer e.Redis.Eval(context.Background(), `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{fence(input.PayerID)}, owner)
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snap, err := e.Storage.Snapshot(readCtx, input)
	if err != nil {
		return err
	}
	if snap.TokenRemaining == nil {
		snap.TokenRemaining = map[int]int64{}
	}
	if snap.BudgetReserved == nil {
		snap.BudgetReserved = map[string]int64{}
	}
	if snap.Budgets == nil {
		snap.Budgets = map[string]int64{}
	}
	if snap.ReservationBudgets == nil {
		snap.ReservationBudgets = map[string][]string{}
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, err := e.eval(ctx, initializeScript, []string{fence(input.PayerID), wallet(input.PayerID)}, owner, string(data), string(encoded), Prefix, budgetOwner)
	if err != nil {
		return err
	}
	if result == "stale" {
		return ErrStale
	}
	if result != "ok" {
		return ErrRebuilding
	}
	return nil
}

func (e *Engine) Reserve(ctx context.Context, input Input) error {
	_, err := e.ReserveAdmitted(ctx, input)
	return err
}

// ReserveAdmitted reserves input and returns the amount Redis now holds. It
// differs from input.Amount only when ClampToRemaining admitted a smaller hold.
func (e *Engine) ReserveAdmitted(ctx context.Context, input Input) (int64, error) {
	if e.AdmissionCheck != nil {
		if err := e.AdmissionCheck(); err != nil {
			return 0, err
		}
	}
	input = normalize(input)
	if err := validate(input); err != nil {
		return 0, err
	}
	if input.ExpiresAt == 0 {
		input.ExpiresAt = e.Now().Add(45 * time.Minute).UnixMilli()
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = e.Now().UTC()
	}
	input.BudgetTimestamp = input.OccurredAt.Unix()
	data, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	hardLimit := e.HardLimit
	if e.HardLimitValue != nil {
		hardLimit = e.HardLimitValue()
	}
	// Only an observed cold/fenced result enters this bounded retry. An ordinary
	// warm admission still uses exactly one Lua invocation.
	retryUntil := time.Now().Add(500 * time.Millisecond)
	for attempt := 0; attempt < 64; attempt++ {
		result, err := e.eval(ctx, reserveScript, []string{wallet(input.PayerID), token(input.TokenID), request(input.RequestID), fence(input.PayerID), Prefix + "exposure", Prefix + "expiry"}, data, Prefix, e.Now().UnixMilli(), hardLimit)
		if err != nil {
			return 0, err
		}
		switch result {
		case "ok":
			return input.Amount, nil
		case "cold":
			if err := e.Initialize(ctx, input); err != nil {
				if errors.Is(err, ErrRebuilding) && time.Now().Before(retryUntil) {
					if err := waitColdAdmission(ctx); err != nil {
						return 0, err
					}
					continue
				}
				return 0, err
			}
			continue
		case "rebuilding":
			if time.Now().Before(retryUntil) {
				if err := waitColdAdmission(ctx); err != nil {
					return 0, err
				}
				continue
			}
			return 0, ErrRebuilding
		case "maintenance":
			return 0, ErrAdmissionPaused
		case "stale":
			return 0, ErrStale
		default:
			if admitted, ok := strings.CutPrefix(result, "ok|"); ok {
				value, err := strconv.ParseInt(admitted, 10, 64)
				if err != nil || value <= 0 || value > input.Amount {
					return 0, fmt.Errorf("invalid admitted reservation %q", result)
				}
				return value, nil
			}
			if parts := strings.Split(result, "|"); len(parts) == 5 && parts[0] == "rate" {
				limit, _ := strconv.ParseInt(parts[2], 10, 64)
				retry, _ := strconv.ParseInt(parts[3], 10, 64)
				window, _ := strconv.Atoi(parts[4])
				return 0, &Rejection{Code: "rate", RateDecision: parts[1], RateLimit: limit, RetryAfterSeconds: retry, WindowMinutes: window}
			}
			id, _ := strconv.ParseUint(result, 10, 64)
			if id > 0 {
				return 0, &Rejection{Code: "budget", BudgetID: uint(id)}
			}
			return 0, &Rejection{Code: result}
		}
	}
	return 0, ErrUnavailable
}

func waitColdAdmission(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *Engine) Settle(ctx context.Context, input Input, actual int64) error {
	if actual < 0 || actual > safeInteger {
		return errors.New("invalid settlement")
	}
	return e.finish(ctx, input, actual, "settle")
}
func (e *Engine) SettleWithTelemetry(ctx context.Context, input Input, actual int64, envelope string) error {
	if actual < 0 || actual > safeInteger {
		return errors.New("invalid settlement")
	}
	return e.finishWithTelemetry(ctx, input, actual, "settle", envelope)
}
func (e *Engine) Release(ctx context.Context, input Input) error {
	return e.finish(ctx, input, 0, "release")
}
func (e *Engine) Refund(ctx context.Context, input Input, amount int64, operationID string) error {
	if amount <= 0 || amount > safeInteger || operationID == "" {
		return errors.New("invalid refund")
	}
	input.RequestID = operationID
	return e.finish(ctx, input, -amount, "refund")
}
func (e *Engine) finish(ctx context.Context, input Input, amount int64, kind string) error {
	return e.finishWithTelemetry(ctx, input, amount, kind, "")
}
func (e *Engine) finishWithTelemetry(ctx context.Context, input Input, amount int64, kind string, envelope string) error {
	input = normalize(input)
	if input.PayerID <= 0 || input.RequestID == "" {
		return errors.New("missing settlement identity")
	}
	event := Event{Input: input, OperationID: input.RequestID, Kind: kind}
	event.Amount = amount
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	telemetry := ""
	if e.TelemetryEncode != nil && e.TelemetryStream != "" {
		telemetry, err = e.TelemetryEncode(event)
		if err != nil {
			return err
		}
	}
	allowUnknown := false
	telemetryMaxLength := e.TelemetryMaxLength
	if telemetryMaxLength < 1 {
		telemetryMaxLength = 100000
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := e.eval(ctx, finishScript, []string{wallet(input.PayerID), token(input.TokenID), request(input.RequestID), fence(input.PayerID), Stream, Prefix + "expiry", Prefix + "exposure"}, string(encoded), Prefix, amount, kind, map[bool]string{false: "0", true: "1"}[allowUnknown], e.TelemetryStream, telemetry, envelope, telemetryMaxLength)
		if err != nil {
			return err
		}
		switch result {
		case "ok":
			return nil
		case "rebuilding":
			return ErrRebuilding
		case "unknown":
			done, err := e.Storage.Settled(ctx, input.RequestID, kind)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			allowUnknown = true
			fallthrough
		case "cold":
			coldInput := input
			coldInput.Generations = nil
			coldInput.WalletGeneration = ""
			coldInput.TokenGeneration = ""
			if err := e.Initialize(ctx, coldInput); err != nil {
				return err
			}
		default:
			return &Rejection{Code: result}
		}
	}
	return ErrUnavailable
}

func (e *Engine) Extend(ctx context.Context, id string, expiry time.Time) error {
	_, err := e.eval(ctx, `if redis.call('HGET',KEYS[1],'state')=='reserved' then redis.call('ZADD',KEYS[2],ARGV[1],ARGV[2]); return 1 end return 0`, []string{request(id), Prefix + "expiry"}, expiry.UnixMilli(), id)
	return err
}
func (e *Engine) Credit(ctx context.Context, c Credit) error {
	result, err := e.eval(ctx, creditScript, []string{wallet(c.PayerID), fence(c.PayerID)}, c.Sequence, c.Amount)
	if err != nil {
		return err
	}
	if result != "ok" {
		return ErrRebuilding
	}
	return e.Storage.CreditApplied(ctx, c.ID)
}

const initializeScript = budgetLua + `
if redis.call('GET',KEYS[1])~=ARGV[1] or redis.call('GET',ARGV[4]..'budget_fence')~=ARGV[5] then return 'rebuilding' end
local s=cjson.decode(ARGV[2]); local i=canonicalInput(cjson.decode(ARGV[3])); local p=ARGV[4]
s.Balance=decimal(s.Balance);s.CreditSeq=decimal(s.CreditSeq)
if not checkedAdd(s.Balance,'0') or not checkedAdd(s.CreditSeq,'0') then return 'money_range' end
for id,value in pairs(s.TokenRemaining or {}) do if not checkedAdd(value,'0') then return 'money_range' end end
for key,value in pairs(s.Budgets or {}) do if not checkedAdd(value,'0') then return 'money_range' end end
for key,value in pairs(s.BudgetReserved or {}) do if not checkedAdd(value,'0') then return 'money_range' end end
-- Existing hot state remains authoritative. This is a cold initializer, not
-- an unconditional replacement of a concurrent request's accounting.
for _,g in ipairs(i.generations or {}) do
 if redis.call('GET',g.key)~=g.value then return 'stale' end
 local now=redis.call('TIME');local ms=now[1]*1000+math.floor(now[2]/1000);if redis.call('ZCOUNT',g.pending_key,ms,'+inf')>0 then return 'stale' end
end
if redis.call('EXISTS',KEYS[2])==0 or (i.wallet_generation~='' and redis.call('HGET',KEYS[2],'generation')~=i.wallet_generation) then redis.call('HSET',KEYS[2],'avail',s.Balance,'credit_seq',s.CreditSeq,'generation',i.wallet_generation);redis.call('HINCRBY',KEYS[2],'revision',1) end
for id,value in pairs(s.TokenRemaining or {}) do
 if redis.call('EXISTS',p..'token:'..id)==0 or (i.token_generation~='' and redis.call('GET',p..'token_gen:'..id)~=i.token_generation) then redis.call('SET',p..'token:'..id,decimal(value));redis.call('SET',p..'token_gen:'..id,i.token_generation) end -- secret-scan: allow -- Redis counter and identity generation names, no credential literal
end
for _,b in ipairs(i.budgets or {}) do
 local key=budgetKey(p,b)
 if redis.call('EXISTS',key)==0 then
  redis.call('HSET',key,'spent',decimal((s.Budgets or {})[key] or '0'),'reserved',decimal((s.BudgetReserved or {})[key] or '0'),'revision',1,'definition',cjson.encode(b))
  retainBudget(key,b)
  redis.call('SADD',budgetIndex(p,b),key)
  for _,id in ipairs((s.ReservationBudgets or {})[key] or {}) do redis.call('SADD',p..'reservation_budgets:'..id,key) end
 end
end
redis.call('DEL',KEYS[1]); return 'ok'
`
const reserveScript = budgetLua + `
local i=canonicalInput(cjson.decode(ARGV[1])); local p=ARGV[2]; local clock=redis.call('TIME');local now=clock[1]*1000+math.floor(clock[2]/1000);local amount=i.amount
if redis.call('EXISTS',p..'maintenance')==1 then return 'maintenance' end
if redis.call('EXISTS',KEYS[4])==1 or redis.call('EXISTS',p..'budget_fence')==1 then return 'rebuilding' end
local adopting=false
local state=redis.call('HGET',KEYS[3],'state');if not state then local held=redis.call('HGET',p..'reservations',i.request_id);if held then local saved=cjson.decode(held);redis.call('HSET',KEYS[3],'state','reserved','input',held,'rate_recorded',saved.rate_pending and '0' or '1');state='reserved' end end; if state then
 if not redis.call('HGET',KEYS[3],'input') then return 'terminal' end
 local original=canonicalInput(cjson.decode(redis.call('HGET',KEYS[3],'input')));if original.payer_id~=i.payer_id or original.token_id~=i.token_id or (original.allow_zero_balance or false)~=(i.allow_zero_balance or false) then return 'identity_conflict' end
 if state=='reserved' and original.amount==i.amount then if redis.call('HGET',KEYS[3],'rate_recorded')=='0' then adopting=true else return 'ok' end else return 'terminal' end
end
for _,g in ipairs(i.generations or {}) do
 if redis.call('GET',g.key)~=g.value then return 'stale' end
 if redis.call('ZCOUNT',g.pending_key,now,'+inf')>0 then return 'rebuilding' end
end
if redis.call('EXISTS',KEYS[1])==0 or (i.wallet_generation~='' and redis.call('HGET',KEYS[1],'generation')~=i.wallet_generation) then return 'cold' end
if not i.unlimited and i.token_id>0 and (redis.call('EXISTS',KEYS[2])==0 or (i.token_generation~='' and redis.call('GET',p..'token_gen:'..i.token_id)~=i.token_generation)) then return 'cold' end -- secret-scan: allow -- Runtime numeric token ID and identity generation, no credential literal
for _,b in ipairs(i.budgets or {}) do local key=budgetKey(p,b);if redis.call('EXISTS',key)==0 then return 'cold' end end
local rates={}
for _,r in ipairs(i.rates or {}) do
 local duration=r.window_seconds*1000;local bucket=math.floor(now/duration);local ending=(bucket+1)*duration
 local key=p..'rate:'..r.key..':'..bucket;local count=decimal(redis.call('GET',key) or '0')
 local next=checkedAdd(count,r.cost);if not next then return 'money_range' end
 if compare(next,r.limit)>0 then return 'rate|'..r.decision..'|'..r.limit..'|'..math.max(1,math.ceil((ending-now)/1000))..'|'..math.max(1,math.floor(r.window_seconds/60)) end
 rates[#rates+1]={key=key,cost=r.cost,expiry=ending,success=r.on_success}
end
if adopting then
 for _,r in ipairs(rates) do if not r.success then redis.call('INCRBY',r.key,r.cost);redis.call('PEXPIREAT',r.key,r.expiry) end end
 local original=canonicalInput(cjson.decode(redis.call('HGET',KEYS[3],'input')));original.rates=i.rates;original.model=i.model;original.pricing_version=i.pricing_version;original.rate_pending=false
 redis.call('HSET',KEYS[3],'input',cjson.encode(original),'rate_recorded','1');redis.call('HSET',p..'reservations',i.request_id,cjson.encode(original));return 'ok'
end
if compare(redis.call('GET',KEYS[5]) or '0',ARGV[4])>=0 then return 'backlog' end
local available=decimal(redis.call('HGET',KEYS[1],'avail'))
-- A worst-case estimate above a positive remainder holds only what remains;
-- settlement still records the actual charge. Zero or negative still rejects.
local clamped=false
if i.clamp_to_remaining and compare(amount,'0')>0 then
 if not i.unlimited and i.token_id>0 then local left=decimal(redis.call('GET',KEYS[2]));if compare(left,'0')>0 and compare(left,amount)<0 then amount=left;clamped=true end end
 if compare(available,'0')>0 and compare(available,amount)<0 then amount=available;clamped=true end
end
local stored=ARGV[1];if clamped then i.amount=amount;stored=cjson.encode(i) end
if not i.unlimited and i.token_id>0 and compare(redis.call('GET',KEYS[2]),amount)<0 then return 'key' end
for _,b in ipairs(i.budgets or {}) do
 local key=budgetKey(p,b)
 local spent=decimal(redis.call('HGET',key,'spent') or '0');local reserved=decimal(redis.call('HGET',key,'reserved') or '0')
 if not checkedAdd(reserved,amount) then return 'money_range' end
 if b.block and compare(add(add(spent,reserved),amount),b.limit)>0 then return tostring(b.id) end
end
if compare(available,amount)<0 or (amount=='0' and available=='0' and not i.allow_zero_balance) then return 'balance' end
if not checkedAdd(redis.call('HGET',KEYS[1],'avail'),neg(amount)) then return 'money_range' end
if not i.unlimited and i.token_id>0 and not checkedAdd(redis.call('GET',KEYS[2]),neg(amount)) then return 'money_range' end
for _,r in ipairs(rates) do if not r.success then redis.call('INCRBY',r.key,r.cost);redis.call('PEXPIREAT',r.key,r.expiry) end end
redis.call('HINCRBY',KEYS[1],'avail',neg(amount))
redis.call('HINCRBY',KEYS[1],'revision',1)
if not i.unlimited and i.token_id>0 then redis.call('DECRBY',KEYS[2],amount) end
for _,b in ipairs(i.budgets or {}) do local key=budgetKey(p,b);redis.call('HINCRBY',key,'reserved',amount);redis.call('HINCRBY',key,'revision',1);redis.call('SADD',p..'reservation_budgets:'..i.request_id,key);retainBudget(key,b) end
redis.call('HSET',KEYS[3],'state','reserved','input',stored,'rate_recorded','1');redis.call('HSET',p..'reservations',i.request_id,stored)
redis.call('ZADD',KEYS[6],i.expires_at,i.request_id)
if clamped then return 'ok|'..amount end
return 'ok'
`
const finishScript = budgetLua + `
local e=canonicalInput(cjson.decode(ARGV[1]));local p=ARGV[2];local actual=decimal(ARGV[3]);local kind=ARGV[4]
if redis.call('EXISTS',KEYS[4])==1 or redis.call('EXISTS',p..'budget_fence')==1 then return 'rebuilding' end
local state=redis.call('HGET',KEYS[3],'state')
if state and not redis.call('HGET',KEYS[3],'input') then if kind=='release' then return 'ok' end;state=nil end
if not state then local held=redis.call('HGET',p..'reservations',e.request_id);if held then redis.call('HSET',KEYS[3],'state','reserved','input',held);state='reserved' end end
if not state and kind~='release' then
 local journal=redis.call('HGET',p..'journal',e.operation_id)
 if journal then local prior=cjson.decode(journal);if prior.payer_id~=e.payer_id or prior.token_id~=e.token_id or prior.kind~=kind then return 'identity_conflict' end;redis.call('HSET',KEYS[3],'state','settled','input',journal);return 'ok' end
 if ARGV[5]~='1' then return 'unknown' end
end
if state then local original=canonicalInput(cjson.decode(redis.call('HGET',KEYS[3],'input')));if original.payer_id~=e.payer_id or original.token_id~=e.token_id then return 'identity_conflict' end end
if state=='settled' or (kind=='release' and state~='reserved') then return 'ok' end
if redis.call('EXISTS',KEYS[1])==0 then return 'cold' end
local reserved='0'
if state=='reserved' then local i=canonicalInput(cjson.decode(redis.call('HGET',KEYS[3],'input')));reserved=i.amount;e.budgets=i.budgets;e.unlimited=i.unlimited;e.rates=i.rates;e.allow_zero_balance=i.allow_zero_balance end
if not e.unlimited and e.token_id>0 and redis.call('EXISTS',KEYS[2])==0 then return 'cold' end
for _,b in ipairs(e.budgets or {}) do local key=budgetKey(p,b);if redis.call('EXISTS',key)==0 then return 'cold' end end
local delta=add(reserved,neg(actual))
if not checkedAdd(redis.call('HGET',KEYS[1],'avail'),delta) then return 'money_range' end
if not e.unlimited and e.token_id>0 and not checkedAdd(redis.call('GET',KEYS[2]),delta) then return 'money_range' end
local touched={};local heldByKey={}
for _,b in ipairs(e.budgets or {}) do touched[budgetKey(p,b)]=true end
for _,key in ipairs(activeBudgetKeys(p,e)) do touched[key]=true end
for key,_ in pairs(touched) do
 if redis.call('EXISTS',key)==1 then
  local held='0';if reserved~='0' and redis.call('SISMEMBER',p..'reservation_budgets:'..e.request_id,key)==1 then held=reserved end
  heldByKey[key]=held
  if not checkedAdd(redis.call('HGET',key,'reserved') or '0',neg(held)) or not checkedAdd(redis.call('HGET',key,'spent') or '0',actual) then return 'money_range' end
 end
end
if kind~='release' and not checkedAdd(redis.call('GET',KEYS[7]) or '0',abs(actual)) then return 'money_range' end
local successes={}
if kind=='settle' then
 local clock=redis.call('TIME');local ms=clock[1]*1000+math.floor(clock[2]/1000)
 for _,r in ipairs(e.rates or {}) do if r.on_success then
  local duration=r.window_seconds*1000;local bucket=math.floor(ms/duration);local key=p..'rate:'..r.key..':'..bucket
  if not checkedAdd(redis.call('GET',key) or '0',r.cost) then return 'money_range' end
  successes[#successes+1]={key=key,cost=r.cost,expiry=(bucket+1)*duration}
 end end
end
-- Lua does not roll back writes on runtime errors. Validate every destination
-- used after money changes, including stream ID exhaustion and bookkeeping.
local function keyType(key,wanted)
 local got=redis.call('TYPE',key).ok
 return got=='none' or got==wanted
end
local function writableStream(key)
 if not keyType(key,'stream') then return false end
 if redis.call('EXISTS',key)==1 then
  local info=redis.call('XINFO','STREAM',key)
  for n=1,#info,2 do
   if info[n]=='last-generated-id' and string.match(info[n+1],'^18446744073709551615%-') then return false end
  end
 end
 return true
end
if not keyType(KEYS[6],'zset') or not keyType(p..'reservations','hash') then return redis.error_reply('invalid ledger bookkeeping type') end
if not checkedAdd(redis.call('HGET',KEYS[1],'revision') or '0','1') then return 'money_range' end
for key,_ in pairs(heldByKey) do
 if not checkedAdd(redis.call('HGET',key,'revision') or '0','1') then return 'money_range' end
end
if kind~='release' then
 if not writableStream(KEYS[5]) or not keyType(p..'journal','hash') or not keyType(p..'uncommitted','zset') then return redis.error_reply('invalid ledger event destination') end
end
if ARGV[6]~='' and (ARGV[7]~='' or (ARGV[8] and ARGV[8]~='')) then
 if not writableStream(ARGV[6]) or not keyType(ARGV[6]..':dropped','string') then return redis.error_reply('invalid telemetry destination') end
 local dropped=redis.call('GET',ARGV[6]..':dropped') or '0'
 local count=redis.call('EXISTS',ARGV[6])==1 and redis.call('XLEN',ARGV[6]) or 0
 if not checkedAdd(dropped,tostring(count+2)) then return redis.error_reply('invalid telemetry drop counter') end
end
-- All integer values and accumulated counter ranges are checked before the
-- first money/rate mutation. Redis Lua errors do not roll earlier writes back.
redis.call('HINCRBY',KEYS[1],'avail',delta)
redis.call('HINCRBY',KEYS[1],'revision',1)
if not e.unlimited and e.token_id>0 then redis.call('INCRBY',KEYS[2],delta) end
for key,held in pairs(heldByKey) do
 redis.call('HINCRBY',key,'reserved',neg(held));redis.call('HINCRBY',key,'spent',actual);redis.call('HINCRBY',key,'revision',1)
end
redis.call('DEL',p..'reservation_budgets:'..e.request_id)
for _,r in ipairs(successes) do redis.call('INCRBY',r.key,r.cost);redis.call('PEXPIREAT',r.key,r.expiry) end
if kind~='release' then redis.call('XADD',KEYS[5],'*','event',cjson.encode(e));redis.call('HSET',p..'journal',e.operation_id,cjson.encode(e));redis.call('INCRBY',KEYS[7],abs(actual));redis.call('ZADD',p..'uncommitted',redis.call('TIME')[1],e.operation_id) end
if ARGV[6]~='' and ARGV[7]~='' then
 local before=redis.call('XLEN',ARGV[6]);redis.call('XADD',ARGV[6],'MAXLEN','=',ARGV[9],'*','type','financial_settlement','payload',ARGV[7]);local after=redis.call('XLEN',ARGV[6]);local dropped=before+1-after;if dropped>0 then redis.call('INCRBY',ARGV[6]..':dropped',dropped) end
end
if ARGV[6]~='' and ARGV[8] and ARGV[8]~='' then
 local before=redis.call('XLEN',ARGV[6]);redis.call('XADD',ARGV[6],'MAXLEN','=',ARGV[9],'*','type','relay_result','payload',ARGV[8]);local after=redis.call('XLEN',ARGV[6]);local dropped=before+1-after;if dropped>0 then redis.call('INCRBY',ARGV[6]..':dropped',dropped) end
end
redis.call('HDEL',p..'reservations',e.request_id)
redis.call('HSET',KEYS[3],'state',kind=='release' and 'released' or 'settled','input',cjson.encode(e))
-- Pending financial operations never lose their dedupe state. A committed
-- operation gets its retention TTL in the acknowledgement script.
if kind=='release' then redis.call('EXPIRE',KEYS[3],172800) end
redis.call('ZREM',KEYS[6],e.request_id)
return 'ok'
`
const creditScript = decimalLua + `
if redis.call('EXISTS',KEYS[2])==1 or redis.call('EXISTS',KEYS[1])==0 then return 'rebuilding' end
local seq=decimal(redis.call('HGET',KEYS[1],'credit_seq') or '0');local wanted=decimal(ARGV[1])
if compare(wanted,seq)<=0 then return 'ok' end
if wanted~=add(seq,'1') then return 'rebuilding' end
if not checkedAdd(redis.call('HGET',KEYS[1],'avail'),ARGV[2]) then return 'money_range' end
redis.call('HINCRBY',KEYS[1],'avail',ARGV[2]);redis.call('HSET',KEYS[1],'credit_seq',wanted);redis.call('HINCRBY',KEYS[1],'revision',1);return 'ok'
`
