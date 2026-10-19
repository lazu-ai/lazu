package ledger

import (
	"context"
	"fmt"
	"strconv"
)

// ReadBalances is a display-only snapshot of initialized available wallets.
// The continuity check and reads run atomically. It never initializes wallets,
// applies credits, changes holds or consults permanent storage. Missing or
// malformed wallets are absent from the result so callers retain their already
// loaded committed balance. Available balances already reflect active reservations.
func (e *Engine) ReadBalances(ctx context.Context, payerIDs []int) (map[int]int64, error) {
	result := make(map[int]int64, len(payerIDs))
	if len(payerIDs) == 0 {
		return result, nil
	}
	if e == nil || e.Redis == nil || e.Continuity == "" {
		return nil, ErrContinuity
	}
	ids := make([]int, 0, len(payerIDs))
	seen := make(map[int]bool, len(payerIDs))
	for _, id := range payerIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	const batchSize = 256
	for offset := 0; offset < len(ids); offset += batchSize {
		batch := ids[offset:min(offset+batchSize, len(ids))]
		keys := make([]string, 0, len(batch)*2)
		for _, id := range batch {
			keys = append(keys, wallet(id), fence(id))
		}
		raw, err := e.evalRaw(ctx, readBalancesScript, keys)
		if err != nil {
			return nil, err
		}
		if raw == "rebuilding" {
			return nil, ErrRebuilding
		}
		values, ok := raw.([]interface{})
		if !ok || len(values) != len(batch)*2 {
			return nil, fmt.Errorf("%w: invalid wallet display snapshot", ErrUnavailable)
		}
		for i, id := range batch {
			text, exists := values[i*2].(string)
			sequence, initialized := values[i*2+1].(string)
			if !exists || !initialized {
				continue
			}
			amount, err := strconv.ParseInt(text, 10, 64)
			creditSequence, sequenceErr := strconv.ParseInt(sequence, 10, 64)
			if err == nil && strconv.FormatInt(amount, 10) == text && sequenceErr == nil && creditSequence >= 0 && strconv.FormatInt(creditSequence, 10) == sequence {
				result[id] = amount
			}
		}
	}
	return result, nil
}

const readBalancesScript = `
if redis.call('EXISTS','lazu:ledger:maintenance')==1 then return 'rebuilding' end
local result={}
for i=1,#KEYS,2 do
 local value=false
 local sequence=false
 if redis.call('EXISTS',KEYS[i+1])==0 and redis.call('HEXISTS',KEYS[i],'generation')==1 then
  value=redis.call('HGET',KEYS[i],'avail') or false
  sequence=redis.call('HGET',KEYS[i],'credit_seq') or false
 end
 result[#result+1]=value
 result[#result+1]=sequence
end
return result`

// ReadTokenRemaining reads initialized limited-Key counters for display only.
// Callers select limited Keys from their already authorized Core page. Missing
// or malformed counters retain Core fallback; this never initializes a Key.
func (e *Engine) ReadTokenRemaining(ctx context.Context, tokenIDs []int) (map[int]int64, error) {
	result := make(map[int]int64, len(tokenIDs))
	if len(tokenIDs) == 0 {
		return result, nil
	}
	if e == nil || e.Redis == nil || e.Continuity == "" {
		return nil, ErrContinuity
	}
	ids := make([]int, 0, len(tokenIDs))
	seen := make(map[int]bool, len(tokenIDs))
	for _, id := range tokenIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	const batchSize = 256
	for offset := 0; offset < len(ids); offset += batchSize {
		batch := ids[offset:min(offset+batchSize, len(ids))]
		keys := make([]string, 0, len(batch)*2)
		for _, id := range batch {
			keys = append(keys, token(id), fmt.Sprintf("%stoken_gen:%d", Prefix, id))
		}
		raw, err := e.evalRaw(ctx, readTokenRemainingScript, keys)
		if err != nil {
			return nil, err
		}
		if raw == "rebuilding" {
			return nil, ErrRebuilding
		}
		values, ok := raw.([]interface{})
		if !ok || len(values) != len(batch) {
			return nil, fmt.Errorf("%w: invalid Key display snapshot", ErrUnavailable)
		}
		for i, id := range batch {
			text, exists := values[i].(string)
			if !exists {
				continue
			}
			amount, err := strconv.ParseInt(text, 10, 64)
			if err == nil && strconv.FormatInt(amount, 10) == text {
				result[id] = amount
			}
		}
	}
	return result, nil
}

const readTokenRemainingScript = `
if redis.call('EXISTS','lazu:ledger:maintenance')==1 then return 'rebuilding' end
local result={}
for i=1,#KEYS,2 do
 local value=false
 if redis.call('EXISTS',KEYS[i+1])==1 then value=redis.call('GET',KEYS[i]) or false end
 result[#result+1]=value
end
return result`
