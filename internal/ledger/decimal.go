package ledger

// Redis counters accept signed 64-bit decimal arguments. Lua's JSON encoder
// only emits 14 significant digits, and passing a Lua number to an integer
// command can use scientific notation. Keep money as decimal strings in JSON
// and perform counter arithmetic/comparisons without a floating-point roundtrip.
const decimalLua = `
local function decimal(v)
 if type(v)=='number' then
  if v~=math.floor(v) or math.abs(v)>9007199254740991 then error('invalid legacy integral money') end
  v=string.format('%.0f',v)
 end
 if type(v)~='string' or not string.match(v,'^%-?%d+$') then error('invalid decimal money') end
 local negative=string.sub(v,1,1)=='-';if negative then v=string.sub(v,2) end
 v=string.gsub(v,'^0+','');if v=='' then return '0' end
 if negative then return '-'..v end;return v
end
local function neg(v)
 v=decimal(v);if v=='0' then return v end;if string.sub(v,1,1)=='-' then return string.sub(v,2) end;return '-'..v
end
local function abs(v) v=decimal(v);if string.sub(v,1,1)=='-' then return string.sub(v,2) end;return v end
local function compareAbs(a,b)
 if #a~=#b then if #a<#b then return -1 else return 1 end end
 if a==b then return 0 end;if a<b then return -1 end;return 1
end
local function compare(a,b)
 a=decimal(a);b=decimal(b);local an=string.sub(a,1,1)=='-';local bn=string.sub(b,1,1)=='-'
 if an~=bn then if an then return -1 else return 1 end end
 local result=compareAbs(abs(a),abs(b));if an then return -result end;return result
end
local function addAbs(a,b)
 local result={};local carry=0;local ai=#a;local bi=#b
 while ai>0 or bi>0 or carry>0 do
  local x=0;local y=0;if ai>0 then x=string.byte(a,ai)-48;ai=ai-1 end;if bi>0 then y=string.byte(b,bi)-48;bi=bi-1 end
  local value=x+y+carry;carry=math.floor(value/10);table.insert(result,1,string.char(48+value%10))
 end;return table.concat(result)
end
local function subtractAbs(a,b)
 local result={};local borrow=0;local bi=#b
 for ai=#a,1,-1 do
  local x=string.byte(a,ai)-48-borrow;local y=0;if bi>0 then y=string.byte(b,bi)-48;bi=bi-1 end
  if x<y then x=x+10;borrow=1 else borrow=0 end;table.insert(result,1,string.char(48+x-y))
 end;return decimal(table.concat(result))
end
local function add(a,b)
 a=decimal(a);b=decimal(b);local an=string.sub(a,1,1)=='-';local bn=string.sub(b,1,1)=='-';local aa=abs(a);local ba=abs(b)
 if an==bn then local result=addAbs(aa,ba);if an then return neg(result) end;return result end
 local order=compareAbs(aa,ba);if order==0 then return '0' end
 if order>0 then local result=subtractAbs(aa,ba);if an then return neg(result) end;return result end
 local result=subtractAbs(ba,aa);if bn then return neg(result) end;return result
end
local function checkedAdd(a,b)
 local result=add(a,b)
 if compare(result,'9223372036854775807')>0 or compare(result,'-9223372036854775808')<0 then return nil end
 return result
end
local function canonicalBudget(b)
 b.limit=decimal(b.limit or '0');b.spent=decimal(b.spent or '0');return b
end
local function canonicalInput(i)
 i.amount=decimal(i.amount)
 for _,b in ipairs(i.budgets or {}) do canonicalBudget(b) end
 for _,r in ipairs(i.rates or {}) do r.limit=decimal(r.limit);r.cost=decimal(r.cost) end
 return i
end
`
