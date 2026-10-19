package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// Drain first claims abandoned work. Storage.Apply may be repeated after a
// crash; XACK and backlog adjustment are one atomic Redis operation.
func (e *Engine) Drain(ctx context.Context, consumer string) (int, error) {
	if err := e.CheckContinuity(ctx); err != nil {
		return 0, err
	}
	err := e.Redis.XGroupCreateMkStream(ctx, Stream, Group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return 0, err
	}
	pending, err := e.Redis.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: Stream, Group: Group, Start: "-", End: "+", Count: 500, Idle: 5 * time.Second}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	messages := []redis.XMessage{}
	if len(pending) > 0 {
		ids := make([]string, len(pending))
		for i, p := range pending {
			ids[i] = p.ID
		}
		messages, err = e.Redis.XClaim(ctx, &redis.XClaimArgs{Stream: Stream, Group: Group, Consumer: consumer, MinIdle: 5 * time.Second, Messages: ids}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return 0, err
		}
	}
	if len(messages) == 0 {
		streams, err := e.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: consumer, Streams: []string{Stream, ">"}, Count: 500, Block: time.Second}).Result()
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		for _, s := range streams {
			messages = append(messages, s.Messages...)
		}
	}
	if len(messages) == 0 {
		return 0, nil
	}
	// A blocking stream read returns as soon as its first item arrives. Buffer
	// subsequent arrivals until the size or one-second flush deadline so the
	// SQL transaction is a real batch under sustained request traffic.
	flushAt := time.Now().Add(time.Second)
	for len(messages) < 500 {
		remaining := time.Until(flushAt)
		if remaining <= 0 {
			break
		}
		streams, readErr := e.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: consumer, Streams: []string{Stream, ">"}, Count: int64(500 - len(messages)), Block: remaining}).Result()
		if errors.Is(readErr, redis.Nil) {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
		for _, stream := range streams {
			messages = append(messages, stream.Messages...)
		}
	}
	events := make([]Event, 0, len(messages))
	for _, m := range messages {
		raw, ok := m.Values["event"].(string)
		if !ok {
			return 0, fmt.Errorf("ledger event %s has no payload", m.ID)
		}
		var event Event
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return 0, fmt.Errorf("ledger event %s: %w", m.ID, err)
		}
		event.StreamID = m.ID
		events = append(events, event)
	}
	if err := e.Storage.Apply(ctx, events); err != nil {
		successful := make([]Event, 0, len(events))
		for _, event := range events {
			if applyErr := e.Storage.Apply(ctx, []Event{event}); applyErr != nil {
				if deadErr := e.deadLetter(ctx, event); deadErr != nil {
					return 0, errors.Join(applyErr, deadErr)
				}
			} else {
				successful = append(successful, event)
			}
		}
		events = successful
	}
	args := []interface{}{Group, Prefix, e.Now().Add(10 * time.Minute).UnixMilli()}
	for _, event := range events {
		args = append(args, event.StreamID, event.OperationID, event.Amount)
	}
	result, err := e.evalRaw(ctx, ackScript, []string{Stream, Prefix + "exposure"}, args...)
	if err != nil {
		return 0, err
	}
	if result == "money_range" {
		return 0, fmt.Errorf("%w: invalid uncommitted exposure", ErrUnavailable)
	}
	return len(events), nil
}

const ackScript = decimalLua + `
local remaining=decimal(redis.call('GET',KEYS[2]) or '0');local seen={}
for n=4,#ARGV,3 do
 local id=ARGV[n];local op=ARGV[n+1]
 if not seen[op] and redis.call('ZSCORE',ARGV[2]..'uncommitted',op) and #redis.call('XPENDING',KEYS[1],ARGV[1],id,id,1)>0 then
  seen[op]=true;remaining=checkedAdd(remaining,neg(abs(ARGV[n+2])))
  if not remaining or compare(remaining,'0')<0 then return 'money_range' end
 end
end
for n=4,#ARGV,3 do
 local id=ARGV[n];local op=ARGV[n+1];local amount=ARGV[n+2]
 if redis.call('XACK',KEYS[1],ARGV[1],id)>0 then
  if redis.call('ZREM',ARGV[2]..'uncommitted',op)>0 then redis.call('INCRBY',KEYS[2],neg(abs(amount))) end
  redis.call('ZADD',ARGV[2]..'journal_gc',ARGV[3],op)
  redis.call('ZADD',ARGV[2]..'stream_gc',ARGV[3],KEYS[1]..'|'..id)
  redis.call('EXPIRE',ARGV[2]..'request:'..op,172800)
 end
end
return 1
`

func (e *Engine) RetryCredits(ctx context.Context) error {
	rows, err := e.Storage.PendingCredits(ctx, 500)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := e.Credit(ctx, row); err != nil {
			if !errors.Is(err, ErrRebuilding) {
				return err
			}
			missing, lookupErr := e.Redis.Exists(ctx, wallet(row.PayerID)).Result()
			if lookupErr != nil {
				return lookupErr
			}
			if missing == 0 {
				if err := e.Initialize(ctx, Input{RequestID: fmt.Sprintf("credit-init:%d", row.ID), PayerID: row.PayerID}); err != nil && !errors.Is(err, ErrRebuilding) {
					return err
				}
			}
		}
	}
	return nil
}

func (e *Engine) Reap(ctx context.Context) error {
	ids, err := e.Redis.ZRangeByScore(ctx, Prefix+"expiry", &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(e.Now().UnixMilli(), 10), Count: 500}).Result()
	if err != nil {
		return err
	}
	for _, id := range ids {
		raw, err := e.Redis.HGet(ctx, request(id), "input").Result()
		if errors.Is(err, redis.Nil) {
			raw, err = e.Redis.HGet(ctx, Prefix+"reservations", id).Result()
			if errors.Is(err, redis.Nil) {
				continue
			}
		}
		if err != nil {
			return err
		}
		var input Input
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			return err
		}
		// Release is conditional on the same expiry that was observed here. A
		// simultaneous heartbeat must not release a live reservation.
		owner := fmt.Sprintf("reap:%s:%d", id, e.Now().UnixNano())
		locked, err := e.Redis.SetNX(ctx, fence(input.PayerID), owner, 10*time.Second).Result()
		if err != nil {
			return err
		}
		if !locked {
			continue
		}
		result, evalErr := e.evalRaw(ctx, reapScript, []string{fence(input.PayerID), Prefix + "expiry", request(id), wallet(input.PayerID), token(input.TokenID)}, owner, id, e.Now().UnixMilli(), Prefix)
		if evalErr != nil {
			return evalErr
		}
		if result == "money_range" {
			return fmt.Errorf("%w: reservation release exceeds counter range", ErrUnavailable)
		}
		if result == "cold" {
			input.Generations = nil
			input.WalletGeneration = ""
			input.TokenGeneration = ""
			if err := e.Initialize(ctx, input); err != nil {
				return err
			}
		}
	}
	return nil
}

const reapScript = budgetLua + `
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
if redis.call('EXISTS',ARGV[4]..'budget_fence')==1 then redis.call('DEL',KEYS[1]);return 0 end
local expiry=redis.call('ZSCORE',KEYS[2],ARGV[2])
if redis.call('EXISTS',KEYS[3])==0 then local saved=redis.call('HGET',ARGV[4]..'reservations',ARGV[2]);if saved then redis.call('HSET',KEYS[3],'input',saved,'state','reserved') end end
if not expiry or tonumber(expiry)>tonumber(ARGV[3]) or redis.call('HGET',KEYS[3],'state')~='reserved' then redis.call('DEL',KEYS[1]);return 0 end
local i=canonicalInput(cjson.decode(redis.call('HGET',KEYS[3],'input')))
if redis.call('EXISTS',KEYS[4])==0 or (not i.unlimited and i.token_id>0 and redis.call('EXISTS',KEYS[5])==0) then redis.call('DEL',KEYS[1]);return 'cold' end
for _,b in ipairs(i.budgets or {}) do if redis.call('EXISTS',budgetKey(ARGV[4],b))==0 then redis.call('DEL',KEYS[1]);return 'cold' end end
local touched={};for _,b in ipairs(i.budgets or {}) do touched[budgetKey(ARGV[4],b)]=true end
for _,key in ipairs(redis.call('SMEMBERS',ARGV[4]..'reservation_budgets:'..ARGV[2])) do touched[key]=true end
if not checkedAdd(redis.call('HGET',KEYS[4],'avail'),i.amount) then redis.call('DEL',KEYS[1]);return 'money_range' end
if not i.unlimited and i.token_id>0 and not checkedAdd(redis.call('GET',KEYS[5]),i.amount) then redis.call('DEL',KEYS[1]);return 'money_range' end
for key,_ in pairs(touched) do if redis.call('EXISTS',key)==1 and not checkedAdd(redis.call('HGET',key,'reserved') or '0',neg(i.amount)) then redis.call('DEL',KEYS[1]);return 'money_range' end end
redis.call('HDEL',ARGV[4]..'reservations',i.request_id)
redis.call('HINCRBY',KEYS[4],'avail',i.amount)
redis.call('HINCRBY',KEYS[4],'revision',1)
if not i.unlimited and i.token_id>0 then redis.call('INCRBY',KEYS[5],i.amount) end
for key,_ in pairs(touched) do if redis.call('EXISTS',key)==1 then redis.call('HINCRBY',key,'reserved',neg(i.amount));redis.call('HINCRBY',key,'revision',1) end end
redis.call('DEL',ARGV[4]..'reservation_budgets:'..ARGV[2])
redis.call('HSET',KEYS[3],'state','released');redis.call('EXPIRE',KEYS[3],172800);redis.call('ZREM',KEYS[2],ARGV[2]);redis.call('DEL',KEYS[1]);return 1
`

func (e *Engine) Run(ctx context.Context, consumer string, report func(error)) {
	if e.Observer != nil {
		// The observer and delivery loop can report concurrently. Preserve the
		// original callback's serial invocation contract.
		originalReport := report
		var reportMu sync.Mutex
		report = func(err error) {
			reportMu.Lock()
			defer reportMu.Unlock()
			originalReport(err)
		}
	}
	stopObserver := e.startObserver(ctx, report, 10*time.Second)
	defer stopObserver()
	maintenance := time.NewTicker(10 * time.Second)
	defer maintenance.Stop()
	for ctx.Err() == nil {
		select {
		case <-maintenance.C:
			if e.Maintenance != nil {
				if err := e.Maintenance(ctx); err != nil {
					report(err)
				}
			}
			if err := e.ReplaySpool(ctx); err != nil {
				report(err)
			}
			if err := e.RetryDead(ctx, consumer); err != nil {
				report(err)
			}
			if err := e.Collect(ctx); err != nil {
				report(err)
			}
			if err := e.RetryCredits(ctx); err != nil {
				report(err)
			}
			if err := e.Reap(ctx); err != nil {
				report(err)
			}
		default:
		}
		if _, err := e.Drain(ctx, consumer); err != nil && ctx.Err() == nil {
			report(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

// One goroutine invokes the observer serially; ticker notifications coalesce
// while a scan is running. Cancellation stops future scans, and the caller
// joins the active callback before closing its financial resources.
func (e *Engine) startObserver(ctx context.Context, report func(error), interval time.Duration) func() {
	observer := e.Observer
	if observer == nil {
		return func() {}
	}
	observerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-observerCtx.Done():
				return
			case <-ticker.C:
				if observerCtx.Err() != nil {
					return
				}
				if err := observer(observerCtx); err != nil && observerCtx.Err() == nil {
					report(err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

const DeadStream = Prefix + "dead"
const DeadGroup = Group + "-dead"

func (e *Engine) deadLetter(ctx context.Context, event Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = e.evalRaw(ctx, `
 if redis.call('ZSCORE',ARGV[5]..'uncommitted',ARGV[2]) and redis.call('HSETNX',KEYS[2],ARGV[2],'pending')==1 then redis.call('XADD',KEYS[3],'*','event',ARGV[3]) end
 redis.call('XACK',KEYS[1],ARGV[1],ARGV[4]);redis.call('ZADD',ARGV[5]..'stream_gc',ARGV[6],KEYS[1]..'|'..ARGV[4]);return 1
 `, []string{Stream, Prefix + "dead_ids", DeadStream}, Group, event.OperationID, string(raw), event.StreamID, Prefix, e.Now().Add(10*time.Minute).UnixMilli())
	return err
}

// Failed operations remain financial exposure until this persistent stream
// commits them. Moving a message never advances a financial watermark.
func (e *Engine) RetryDead(ctx context.Context, consumer string) error {
	if err := e.CheckContinuity(ctx); err != nil {
		return err
	}
	if err := e.Redis.XGroupCreateMkStream(ctx, DeadStream, DeadGroup, "0").Err(); err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	pending, err := e.Redis.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: DeadStream, Group: DeadGroup, Start: "-", End: "+", Count: 500, Idle: 5 * time.Second}).Result()
	if err != nil {
		return err
	}
	messages := []redis.XMessage{}
	if len(pending) > 0 {
		ids := make([]string, len(pending))
		for i, p := range pending {
			ids[i] = p.ID
		}
		messages, err = e.Redis.XClaim(ctx, &redis.XClaimArgs{Stream: DeadStream, Group: DeadGroup, Consumer: consumer, MinIdle: 5 * time.Second, Messages: ids}).Result()
		if err != nil {
			return err
		}
	}
	if len(messages) == 0 {
		streams, err := e.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: DeadGroup, Consumer: consumer, Streams: []string{DeadStream, ">"}, Count: 500, Block: time.Millisecond}).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, s := range streams {
			messages = append(messages, s.Messages...)
		}
	}
	var failures []error
	for _, m := range messages {
		raw, ok := m.Values["event"].(string)
		if !ok {
			return errors.New("dead letter has no financial payload")
		}
		var event Event
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return err
		}
		if err := e.Storage.Apply(ctx, []Event{event}); err != nil {
			failures = append(failures, err)
			continue
		}
		args := []interface{}{DeadGroup, Prefix, e.Now().Add(10 * time.Minute).UnixMilli(), m.ID, event.OperationID, event.Amount}
		result, err := e.evalRaw(ctx, ackScript, []string{DeadStream, Prefix + "exposure"}, args...)
		if err != nil {
			return err
		}
		if result == "money_range" {
			return fmt.Errorf("%w: invalid uncommitted exposure", ErrUnavailable)
		}
		if err := e.Redis.HDel(ctx, Prefix+"dead_ids", event.OperationID).Err(); err != nil {
			return err
		}
	}
	return errors.Join(failures...)
}
func (e *Engine) Collect(ctx context.Context) error {
	_, err := e.evalRaw(ctx, `
 local ids=redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',ARGV[1],'LIMIT',0,500)
 for _,id in ipairs(ids) do redis.call('HDEL',KEYS[2],id);redis.call('ZREM',KEYS[1],id) end
 local streams=redis.call('ZRANGEBYSCORE',KEYS[3],'-inf',ARGV[1],'LIMIT',0,500)
 for _,entry in ipairs(streams) do local mark=string.find(entry,'|',1,true);if mark then redis.call('XDEL',string.sub(entry,1,mark-1),string.sub(entry,mark+1)) end;redis.call('ZREM',KEYS[3],entry) end
 return 1`, []string{Prefix + "journal_gc", Prefix + "journal", Prefix + "stream_gc"}, e.Now().UnixMilli())
	return err
}
