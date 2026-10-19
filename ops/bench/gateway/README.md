# Gateway local benchmark

Run `./ops/bench/gateway/run.sh` from any working directory. Requires Go,
Python 3, Docker and the PostgreSQL/Redis images (downloaded when missing).
The script builds this checkout, starts only loopback fixture services, creates
synthetic users, channels and keys, measures direct/upstream and gateway timings,
then removes exactly the processes and containers it created. It refuses remote
fixture URLs and does not read production configuration.
After schema migration, each empty fixture runs the explicit `ledger-bootstrap`
migration command before API/seed startup. It uses the normal financial
continuity guard; the benchmark never bypasses recovery admission.

Outputs overwrite `.local-results/gateway-bench/latest/`. Fixture credentials
stay there with mode 0600, are never printed, and must never be committed.
Checkout status, production Go/tool source hashes (including untracked files),
and binary hashes accompany the measurements so a dirty checkout is reviewable.

For a dedicated Linux test server (the selected new host has 8 vCPUs and about 16GB),
keep every service on loopback and use only synthetic users and credentials.
Do not use the production compose or production configuration. The same script
works on macOS and Linux; its writable Go cache defaults to
`.local-results/gateway-bench/.go-cache/`, with `GOCACHE` still overridable.
An optional `LAZU_BENCH_OUT_DIR` selects an absolute or repo-relative output
directory **inside** `.local-results/gateway-bench/`. Escaping paths and symlinks
that escape that tree are rejected. A portable Python advisory lock rejects overlapping runs against
the same output directory before any artifacts are overwritten. Different output
directories still require distinct fixture ports; run sequentially for timing.

```sh
LAZU_BENCH_OUT_DIR=.local-results/gateway-bench/server-0ms ./ops/bench/gateway/run.sh
LAZU_BENCH_OUT_DIR=.local-results/gateway-bench/server-35ms LAZU_BENCH_PG_RTT_MS=35 ./ops/bench/gateway/run.sh
```

For the non-streaming Decisions request path, opt in explicitly:

```sh
LAZU_BENCH_MODE=decisions LAZU_BENCH_DURATION=3s \
  LAZU_BENCH_OUT_DIR=.local-results/gateway-bench/decisions ./ops/bench/gateway/run.sh
```

This runs six 1KB cases: legacy Jev (`state` / keyed questions) and native
OpenAI (`gpt-6-luna`, `input` / ordered questions), each with 1, 8 and 32
workers. Both providers are loopback mocks, with real fixture authentication,
financial admission, asynchronous consumers and channel capacity admission
(10000 RPS / 1000 in-flight). Answers and provider usage are validated, followed
by the paused-writer zero-SQL check, commit visibility and complete accounting.
It skips the unrelated streaming stress and latency gates; the default
Chat/Responses matrix and its gates are unchanged. `ttft_*` fields for this
non-streaming mode mean complete-response latency, not SSE first-token time.
Quantile differences are unpaired. This demonstrates request-path work, not
real decision-provider capacity or support in an older Lazu build.

Only a Unix-socket Docker endpoint is accepted, including the selected Docker
context; remote `DOCKER_HOST`/context endpoints are rejected before connecting.
The validated endpoint is pinned for all fixture operations.
`environment-manifest.json` records OS/architecture, CPU/memory, Docker server
version/resources, the actual PG/Redis image IDs/content digests, container limits
and effective PG/Redis settings. It omits environment variables, usernames,
hostnames and endpoint paths. Existing resource limits and numeric gates remain
unchanged; the manifest records an uncapped container as uncapped. Server hardware
evidence does not turn the synthetic loopback fixture into production acceptance.
The historical schema-21 `6a29da9e7` target uses `legacy_run.sh` with supplied
old binaries and the same current mock/client/delay binaries. Its different
migration, seed and metric contracts are described below.

Private overrides: `LAZU_BENCH_PG_PORT`, `LAZU_BENCH_REDIS_PORT`,
`LAZU_BENCH_UPSTREAM_PORT`, `LAZU_BENCH_GATEWAY_PORT`, `LAZU_BENCH_DURATION`
(default 8s per case).
`LAZU_BENCH_PG_RTT_MS=35` adds 35 ms propagation RTT to both database
connections after migration/seeding, through a bounded loopback TCP fixture.
`LAZU_BENCH_PG_DELAY_PORT` changes its listener port (default 15474).
The default run also offers 500 requests/second for 60 seconds, first against a
single key and then ten keys in one project. `LAZU_BENCH_STRESS_ENABLED=false`
skips these two cases during a quick investigation; that run does not cover the
full throughput target. Fifteen-second P95 windows make drift visible.
The request client uses absolute arrival times instead of a ticker that may drop
ticks under Linux scheduling. JSON records `offered_requests`, actual
`started_requests` / `dispatched_requests`, `missed_arrivals` and schedule-lag
percentiles. Arrivals are never sent after the load window; actual request count,
not configured RPS, determines throughput acceptance. TTFT starts immediately
before the HTTP call and does not include scheduler delay, which stays visible
as a separate metric.
The synthetic fixture disables host overload rejection so unrelated desktop
activity cannot reject benchmark requests; financial, permission and rate
admission still run normally. Run without concurrent builds for stable timing.
The jobs fixture runs `control_plane_snapshot,settlement_outbox_reconciler`
scheduler tasks, including the actual B6 SQL publisher and one-minute Redis
snapshot refresh. Financial and telemetry stream engines run continuously
outside that scheduler filter. This local selection excludes unrelated external
API/email jobs; production keeps the default full scheduler.
Use distinct ports for simultaneous runs. Source `run.sh` uses no existing stack,
volume or database and leaves the current application/IDE untouched.

The table reports TTFT overhead as gateway P50/P95 minus the direct fixture's
P50/P95 for the same body/concurrency. Tail is terminal SSE event to EOF. SQL and
Redis counters include background activity during the case, normalized by
successful gateway requests. Separate fixture SQL roles attribute statements
to the API and consumer, excluding setup and instrumentation. The fake upstream counts fresh
connections only; it supports HTTP/1.1, so the separate transport TLS test proves
HTTP/2 negotiation. Warm-up runs before the measured 32-worker connection case.
Redis columns distinguish required request-path client dispatches, all API
dispatches (including asynchronous diagnostics), and all server commands
(including Lua internals and jobs). A pipeline counts as one client dispatch;
automatic network retries are not separately counted. Stable warm requests
without retries can compare the required column with the three-round-trip goal.
The telemetry writer groups up to 500 request IDs, with additional limits of
4000 physical messages and 16 MiB per processing batch, and a one-second
collection window. Financial receipts, complete relay facts and same-batch
diagnostics share one transaction. A single oversized telemetry message follows
the observable retry/dead-letter path; permanent Core financial facts remain.
Consumer SQL/request includes both stores and periodic jobs, so report sparse
batch costs separately from the amortized cost at 500 RPS.
`cold-warm.json` retains every serial payer initialization and warm-up result,
including status codes and exact SQL/Redis counters; any non-200 warm request
fails the run. Each short matrix case uses fresh credentials for the same ten
payers and primes them before measuring, with the normal 60-second identity
TTL. The 60-second stress crosses that TTL and includes cold-refresh SQL;
it must be distinguished from the completely warm eight-second matrix.
The JSON for each phase keeps exact statement totals and normalized API SQL
shapes, categorized as identity refresh, budget/cold admission or other. The
stress table shows both the integer SQL count and six decimal places per
request so a small TTL refresh cannot be rounded into a fictitious zero.
`timing-profiles.json` records preparation, upstream first-byte and completion
percentiles and maxima from those exact normal-case request IDs.
`timing-facts.json` retains their individual request IDs and timing fields;
the client sample files include TTFT, terminal-event-to-EOF tail, and EOF time.
When private hot-path metrics are enabled, additional bounded timers attribute
billing, consume logging, settlement encoding/Eval, capture enqueue, routing
completion and outer middleware completion. The completion clock includes
access-logger writes before the HTTP handler returns; client EOF remains the
acceptance measure and also includes transport/framework completion.
Each synthetic matrix case repeats one prompt. The bounded pure-text shadow
memo therefore hits after the first exact count; its effect must not be
generalized to unique production prompts, whose misses still count full text.
Media inputs bypass this memo, and every request retains its own comparison
and accounting facts.
For additional cache-miss experiments, the client accepts `-unique-prompt=true`:
it appends a fresh per-run nonce and per-request identity inside the actual prompt.
The default is false and retains the historical request bytes. Unique experiments
need their own result directories and direct baselines; they do not replace the
default matrix or make its original acceptance flags apply automatically.
`acceptance.json` exposes each numeric check separately; a completed run is
not a claim that all targets passed. Review stress-window drift and compare
the same-source 0/35ms runs before accepting database-latency independence.
Compare both runtime source and binary manifests; the source map includes
embedded Lua, templates and assets. Successful fixture streams must contain all
100 content events, a complete terminal SSE event and an error-free EOF. Every
measured gateway success must also have its unique request-ID sample. Missing
or disabled Redis instrumentation aborts; the first cold baseline may retain an
unseen request counter as null until its first instrumented request completes.

The run waits for financial and telemetry consumers to drain and writes
`accounting.json`: pending/lag, exposure, reservations, uncommitted settlements,
dead letters and spool files must be zero. Each synthetic SQL wallet balance
must equal its Redis balance and wallet-ledger sum; operation and ledger-entry
counts must agree. Any discrepancy fails the run before containers are removed.
After a drain timeout, it still extracts wallet balances, commit visibility,
timing coverage and SQL shapes before exiting with failed acceptance. A balanced
financial ledger cannot hide a telemetry backlog; each outcome remains explicit.
Missing timing rows or any failed numeric gate also cause a nonzero exit.
The final case pauses the owned jobs process, which contains both writers,
and verifies successful requests produce zero new API or jobs SQL statements;
the process resumes in `finally` before checking the drained accounts.
`settlement-visibility.json` compares sampled EOF with exact Core
`redis_ledger_operations` (`kind=settle`) commit timestamps
(`pg_xact_commit_timestamp(xmin)`, fixture-only `track_commit_timestamp=on`).
It does not measure Telemetry logs, dashboards, or full Console first visibility.
Before/after host/PostgreSQL clock bounds must be within 100 ms; the lowest offset
gives a conservative upper commit-latency bound. Raw clock samples remain in JSON.
The paused-writer case is excluded from P99; P99 is not an all-request guarantee.

Run the default matrix and then the following for the delayed database comparison:

```sh
LAZU_BENCH_PG_RTT_MS=35 ./ops/bench/gateway/run.sh
```

Preserve the first run's result files when comparing
the two runs; outputs otherwise overwrite. Each run uses a fresh, unique spool
directory so settlements from an earlier disposable fixture cannot replay into
the next fixture.

This is a manual release check; it is deliberately not added to CI. A passing
local table does not replace replay, recovery, reconciliation or backup-environment
migration validation.

The historical Mac `final-awake` 0/35ms pair passed 10/11 and 10/11 numeric flags,
respectively. New scanner/client changes require fresh dedicated-server evidence;
all flags, actual failures and provenance are in [validation.md](validation.md).
Settlement visibility covers Core settlement commits only, not Telemetry or full
Console first visibility.

## Historical schema-21 comparison

`legacy_run.sh` requires `LAZU_BENCH_LEGACY_BIN_DIR` containing gateway/migrate
built from exact commit `6a29da9e75ab76af38d270b9be58151c94c3e63d`, plus a
`build-source.txt` containing that SHA. It records this caller attestation and
binary hashes; it does not independently rebuild or prove the attestation.
`LAZU_BENCH_TOOLS_BIN_DIR` supplies the identical `mock`, `bench` and `pgdelay`
executables from the modern comparison run. Both runners create fresh synthetic
fixtures; the old API retains its original in-process event consumer.

The old API binds all interfaces. On the isolated host, first restrict inbound
traffic to the selected legacy gateway port to loopback for both IPv4 and IPv6,
then attest it with `LAZU_BENCH_LEGACY_BIND_ISOLATED=true`. The runner refuses
to start without that attestation; it cannot verify an external host firewall.
Default legacy fixture ports are PostgreSQL 15483, Redis 16393, mock 10082,
gateway 10110 and delay 15484. None use existing data stores.

```sh
LAZU_BENCH_LEGACY_BIND_ISOLATED=true \
LAZU_BENCH_LEGACY_BIN_DIR=.local-results/gateway-bench/legacy21-bin \
LAZU_BENCH_TOOLS_BIN_DIR=.local-results/gateway-bench/server-0ms \
LAZU_BENCH_OUT_DIR=.local-results/gateway-bench/legacy21-0ms \
./ops/bench/gateway/legacy_run.sh
```

Run sequentially, wait for owned ports to release, and add `LAZU_BENCH_PG_RTT_MS=35`
with a new output directory for the delayed comparison. The default twelve-case
matrix excludes stress; `LAZU_BENCH_STRESS_ENABLED=true` explicitly adds the two
500 RPS cases. `LAZU_BENCH_LEGACY_CASES` can select entries such as
`responses:1:1,responses:600:8` for diagnosis. Old SQL pools default to 15 versus
modern 6 per store/role; database max_connections remains 30 and this configuration
difference must accompany the numbers.

Legacy SQL is total API-process I/O including its consumer and periodic work;
Redis counts are server commands. Required Redis dispatches, API-only SQL,
separate-worker I/O, modern permanent financial ledger and Core commit visibility
are unavailable. They are recorded as null / unsupported, never zero or PASS.
`comparison_complete` requires complete successful HTTP streams. Old Core wallet
equality and old queue drain are checked independently. Script exit 0 denotes a
completed observation; `release_acceptance` is null and does not claim modern
eleven-gate acceptance.

最终独立8核/16GB优化版同机与35ms对照已按原门槛11/11通过；完整12格、压力发出数/状态、账务、同源哈希和短样本限制见[validation.md](validation.md)。原Mac10/11与Linux初轮发压不足的历史失败保留。更高目标负载与API参数对照作为单独观察，不改变原验收定义。

额外四组unique压力参数实验与单独CPU采样均已完成。pool64 / GOGC100 / GOMEMLIMIT1GiB在这台共享八核fixture中改善高压结果，但实际发出量仍未达到1000/2000RPS；生产默认参数未变。完整HTTP失败、未发arrival、采样CPU/RSS、账务与归因限制见验证报告，不能用runner exit0代替容量验收。
