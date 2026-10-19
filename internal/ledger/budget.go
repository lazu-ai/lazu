package ledger

// Matches keeps hot budget rebuilds consistent with the permanent scoped
// spend aggregate, including charges admitted before a budget was created.
func (b Budget) Matches(input Input) bool {
	if b.ProjectID > 0 && b.ProjectID != input.ProjectID {
		return false
	}
	if b.UserID > 0 && b.UserID != input.ActorID {
		return false
	}
	if b.TokenID > 0 && b.TokenID != input.TokenID {
		return false
	}
	at := input.OccurredAt.Unix()
	return at >= b.Start && at < b.End
}

const budgetLua = decimalLua + `
local function budgetKey(p,b)
 return p..'budget:'..b.id..':'..b.start..':'..b['end']..':'..(b.project_id or 0)..':'..(b.scope or '')..':'..(b.user_id or 0)..':'..(b.token_id or 0)
end
local function budgetIndex(p,b)
 local subject=0;if b.scope=='user' then subject=b.user_id elseif b.scope=='token' then subject=b.token_id end
 return p..'budget_index:'..(b.project_id or 0)..':'..(b.scope or '')..':'..(subject or 0)
end
local function retainBudget(key,b)
 -- A reconstructed immutable period may already be older than normal cache
 -- retention. Keep it for a bounded retry window instead of deleting it in
 -- the same initializer that the delayed settlement/refund depends on.
 local now=tonumber(redis.call('TIME')[1])
 redis.call('EXPIREAT',key,math.max(b['end']+604800,now+604800))
end
local function activeBudgetKeys(p,e)
 local result={};local seen={};local at=tonumber(e.budget_timestamp or 0)
 for _,project in ipairs({0,e.project_id}) do
  for _,scope in ipairs({'org','user','token'}) do
   local subject=0;if scope=='user' then subject=e.actor_id elseif scope=='token' then subject=e.token_id end
   local index=p..'budget_index:'..project..':'..scope..':'..subject
   for _,key in ipairs(redis.call('SMEMBERS',index)) do
    local definition=redis.call('HGET',key,'definition')
    if not definition then redis.call('SREM',index,key) else
     local b=canonicalBudget(cjson.decode(definition))
     if not seen[key] and at>=b.start and at<b['end'] and (b.project_id==0 or b.project_id==e.project_id) and (b.user_id==0 or b.user_id==e.actor_id) and (b.token_id==0 or b.token_id==e.token_id) then seen[key]=true;result[#result+1]=key end
    end
   end
  end
 end
 return result
end
`
