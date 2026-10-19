package ledger

import (
	"context"
	"encoding/json"
	"errors"
)

// SplitReservation transfers part of an accepted hold to a child request.
// Wallet/key/budget totals stay unchanged throughout a two-stage operation.
func (e *Engine) SplitReservation(ctx context.Context, parent Input, childID string, amount int64) (Input, Input, error) {
	if childID == "" || childID == parent.RequestID || amount < 0 || amount > parent.Amount {
		return Input{}, Input{}, errors.New("invalid reservation split")
	}
	child := parent
	child.RequestID, child.Amount, child.Rates = childID, amount, []RateLimit{}
	child.ParentRequestID = parent.RequestID
	child.RatePending = true
	parent.Amount -= amount
	encoded, err := json.Marshal(child)
	if err != nil {
		return Input{}, Input{}, err
	}
	result, err := e.eval(ctx, splitScript, []string{request(parent.RequestID), request(childID), fence(parent.PayerID), Prefix + "budget_fence"}, string(encoded), Prefix, parent.RequestID)
	if err != nil {
		return Input{}, Input{}, err
	}
	if len(result) == 0 || result[0] != '{' {
		return Input{}, Input{}, &Rejection{Code: result}
	}
	var transferred struct {
		Parent Input `json:"parent"`
		Child  Input `json:"child"`
	}
	if err := json.Unmarshal([]byte(result), &transferred); err != nil {
		return Input{}, Input{}, err
	}
	return transferred.Parent, transferred.Child, nil
}

const splitScript = decimalLua + `
local child=canonicalInput(cjson.decode(ARGV[1]));local p=ARGV[2]
if redis.call('EXISTS',KEYS[3])==1 or redis.call('EXISTS',KEYS[4])==1 then return 'rebuilding' end
if redis.call('EXISTS',KEYS[1])==0 then local saved=redis.call('HGET',p..'reservations',ARGV[3]);if saved then redis.call('HSET',KEYS[1],'input',saved,'state','reserved') end end
if redis.call('EXISTS',KEYS[2])==0 then
 local saved=redis.call('HGET',p..'reservations',child.request_id)
 if saved then
  local prior=cjson.decode(saved);if prior.parent_request_id~=ARGV[3] then return 'identity_conflict' end
  redis.call('HSET',KEYS[2],'input',saved,'state','reserved','parent_id',ARGV[3],'rate_recorded',prior.rate_pending and '0' or '1')
 end
end
if redis.call('HGET',KEYS[2],'parent_id')==ARGV[3] then
 if redis.call('HGET',KEYS[2],'state')~='reserved' then return 'terminal' end
 local existing=canonicalInput(cjson.decode(redis.call('HGET',KEYS[2],'input')))
 if existing.amount~=child.amount then return 'identity_conflict' end
 return cjson.encode({parent=canonicalInput(cjson.decode(redis.call('HGET',KEYS[1],'input'))),child=existing})
end
if redis.call('EXISTS',KEYS[2])==1 then return 'identity_conflict' end
if redis.call('HGET',KEYS[1],'state')~='reserved' then return 'terminal' end
local parent=canonicalInput(cjson.decode(redis.call('HGET',KEYS[1],'input')))
if parent.payer_id~=child.payer_id or parent.token_id~=child.token_id or compare(parent.amount,child.amount)<0 then return 'identity_conflict' end
parent.amount=add(parent.amount,neg(child.amount))
child.expires_at=parent.expires_at;child.budget_timestamp=parent.budget_timestamp
local encodedChild=cjson.encode(child)
redis.call('HSET',KEYS[1],'input',cjson.encode(parent),'split_child',child.request_id)
redis.call('HSET',KEYS[2],'input',encodedChild,'state','reserved','parent_id',ARGV[3],'rate_recorded','0')
redis.call('HSET',p..'reservations',parent.request_id,cjson.encode(parent),child.request_id,encodedChild)
for _,key in ipairs(redis.call('SMEMBERS',p..'reservation_budgets:'..parent.request_id)) do redis.call('SADD',p..'reservation_budgets:'..child.request_id,key) end
redis.call('ZADD',p..'expiry',child.expires_at,child.request_id)
return cjson.encode({parent=parent,child=child})
`
