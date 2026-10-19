import json, urllib.request, urllib.error, subprocess, sys, os, urllib.parse
B=os.environ.get('LAZU_BENCH_GATEWAY_URL','http://127.0.0.1:10070')
if urllib.parse.urlparse(B).hostname not in ('127.0.0.1','localhost','::1'): raise SystemExit('fixture setup only accepts loopback gateway URLs')
def raw(path, body=None, method=None, headers=None):
    h={'Content-Type':'application/json'}; h.update(headers or {})
    req=urllib.request.Request(B+path, data=json.dumps(body).encode() if body is not None else None, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            d=r.read(); return r.status, (json.loads(d) if d else {}), r.headers
    except urllib.error.HTTPError as e:
        d=e.read()
        try: return e.code, json.loads(d), e.headers
        except Exception: return e.code, {'raw':d[:300].decode(errors='replace')}, e.headers
class S:
    def __init__(s, u, p):
        st,b,h=raw('/api/user/login',{'username':u,'password':p})
        assert st==200 and b.get('success'), (st,b)
        s.h={'Cookie':h.get('Set-Cookie').split(';')[0],'X-Lazu-User':str(b['data']['id'])}; s.id=b['data']['id']
    def api(s,path,body=None,method=None):
        st,b,_=raw(path,body,method,s.h)
        if st!=200 or b.get('success') is False: raise RuntimeError(f'{path} {st} {json.dumps(b)[:400]}')
        return b.get('data',b)
def sql(q): return subprocess.check_output(['docker','exec',os.environ['LAZU_BENCH_PG_CONTAINER'],'psql','-U','lazu','-Atc',q],text=True).strip()
root=S(os.environ['ROOT_USERNAME'],os.environ['ROOT_PASSWORD'])
for k,v in {'performance_setting.monitor_enabled':'false','fetch_setting.allow_private_ip':'true','fetch_setting.allowed_ports':json.dumps(['80','443',str(urllib.parse.urlparse(os.environ.get('LAZU_BENCH_UPSTREAM_URL','http://127.0.0.1:10062')).port)])}.items():
    root.api('/api/option/',{'key':k,'value':v},'PUT')
vend=root.api('/api/vendors/',{'name':'MockAI','status':1,'description':'perf'})
row={'type':1,'name':'Mock OpenAI','models':'gpt-mock','group':'direct','source_type':'official','status':1,'priority':30,'weight':10,'auto_ban':0,'base_url':os.environ.get('LAZU_BENCH_UPSTREAM_URL','http://127.0.0.1:10062'),'setting':'{}','settings':json.dumps({'observe_new_routes':False,'protocol_dialect':'openai','capabilities':{'supports_responses':True}}),'key':'mock-key'}
root.api('/api/channel/',{'mode':'single','channel':row})
root.api('/api/models/',{'model_name':'gpt-mock','vendor_id':vend.get('internal_id') if isinstance(vend,dict) else None,'description':'perf','status':1,'lifecycle_status':'active','name_rule':0,'source':'manual','sync_official':0,'official_input_price':1,'official_output_price':2,'modalities':{'input':['text'],'output':['text']}})
root.api('/api/pricing/sell_prices',{'model_name':'gpt-mock','channel_group':'direct','input_per_mtok':1,'output_per_mtok':2,'active':True,'source':'manual'},'PUT')
if os.environ.get('LAZU_BENCH_MODE', 'streaming') == 'decisions':
    root.api('/api/option/', {'key': 'DecisionsEnabled', 'value': 'true'}, 'PUT')
    for name, dialect in (('jev-1.13', 'typesafe'), ('gpt-6-luna-decisions', 'openai')):
        channel=dict(row, name=f'Mock {dialect} Decisions', models=name,
                     setting=json.dumps({'decision_dialect':dialect,'max_rps':10000,'max_in_flight':1000}))
        root.api('/api/channel/', {'mode':'single','channel':channel})
        root.api('/api/models/', {'model_name':name,'vendor_id':vend.get('internal_id') if isinstance(vend,dict) else None,'description':'synthetic decision fixture','status':1,'lifecycle_status':'active','name_rule':0,'source':'manual','sync_official':0,'official_input_price':1,'official_output_price':0,'modalities':{'input':['text'],'output':['text']}})
        root.api('/api/pricing/sell_prices', {'model_name':name,'channel_group':'direct','input_per_mtok':1,'output_per_mtok':0,'active':True,'source':'manual'}, 'PUT')
keys=[]
same_project=[]
case_keys=[[] for _ in range(12)]
for i in range(10):
    u=f'perf{i}'; p='perf-user-pass-123'
    root.api('/api/user/',{'username':u,'password':p,'display_name':u})
    uid=sql(f"select id from users where username='{u}'")
    sql(f"WITH prior AS (SELECT id,quota FROM users WHERE id={uid}), updated AS (UPDATE users SET quota=100000000000,credit_seq=credit_seq+1 WHERE id={uid} RETURNING id) INSERT INTO wallet_ledgers (user_id,delta_microusd,kind,ref_type,ref_id,pricing_version,metadata,created_at) SELECT id,100000000000-quota,'top_up','benchmark_fixture','synthetic_initial_credit',0,'{{}}',NOW() FROM prior")
    s=S(u,p)
    t=s.api('/api/token/',{'name':'perf','expired_time':-1,'unlimited_quota':True,'remain_quota':0})
    keys.append(t['key'] if isinstance(t,dict) and 'key' in t else t)
    # Each short measured case uses previously unused credentials with the
    # ordinary 60-second identity TTL. Warm them before measurement; no
    # fixture TTL extension or permission/ledger guard bypass is involved.
    for case in range(12):
        extra=s.api('/api/token/',{'name':f'perf-case-{case}','expired_time':-1,'unlimited_quota':True,'remain_quota':0})
        case_keys[case].append(extra['key'])
    if i==0:
        same_project.append(keys[-1])
        for j in range(9):
            extra=s.api('/api/token/',{'name':f'perf-shared-{j}','expired_time':-1,'unlimited_quota':True,'remain_quota':0})
            same_project.append(extra['key'])
fd=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,'w') as f: f.write(','.join(keys))
for name,value in [('single-key.txt',keys[0]),('same-project-keys.txt',','.join(same_project))]:
    path=os.path.join(os.path.dirname(sys.argv[1]),name)
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
    with os.fdopen(fd,'w') as f: f.write(value)
path=os.path.join(os.path.dirname(sys.argv[1]),'case-keys.json')
fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,'w') as f: json.dump(case_keys,f)
print('synthetic fixture users:',len(keys),'same-project keys:',len(same_project))
