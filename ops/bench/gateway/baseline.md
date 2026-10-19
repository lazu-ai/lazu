# Gateway benchmark baseline

Historical local baseline: `6a29da9e7` / v0.7.160, measured on 2026-10-03.
PostgreSQL 30 connections, gateway pool 15, GOGC=50, GOMEMLIMIT=384MiB.
The local fake upstream sends its first output at 300ms, then 100 chunks at 10ms.
These numbers are evidence from the original investigation, not current results.

| Metric | Historical baseline | Acceptance target |
| --- | ---: | ---: |
| Extra first output, 1KB / 1 worker, P50 | 28ms | ≤5ms in full Redis mode |
| Extra first output, 100KB / 1 worker, P50 | 63ms | ≤15ms in full Redis mode |
| Extra first output, 600KB / 1 worker, P50 | 205ms | ≤50ms in full Redis mode |
| Extra first output, 600KB / 8 workers, P50 | 505ms | ≤120ms |
| Post-terminal tail, 1 worker, maximum | ~50ms | ≤15ms |
| Post-terminal tail, 50 workers, P95 | ~200ms | ≤50ms |
| SQL statements / request, API + consumer | ~85 | report API and total separately |
| SQL statements / request, API only | ~70 | ≤20 in PostgreSQL admission mode; 0 in Redis mode |
| Transactions / request | ~15 | ≤2 in PostgreSQL admission mode |
| Redis commands / request | ~35 | report |
| New upstream connections / request, 32 workers | ~94% | <5% after warm-up |
| Single key / ten keys in one project | not measured | 500 RPS for 60s, stable P95 windows |
| Consumer SQL / request | included above | report actual batch cost and occupancy separately |

The single-worker tail gate uses the maximum client terminal-event-to-EOF
duration, following the plan's A7.2 single-request limit. The approximate
historical tail figure does not establish a percentile; the earlier P50 label
in this tool was incorrect and does not narrow the acceptance requirement.

Consumer SQL includes both permanent financial writes and telemetry, plus the
jobs process's periodic queries. Its amortized cost depends on batch occupancy:
a sparse request still requires atomic writes to two stores and their aggregates.
The first tooling draft guessed an unconditional two-statement limit; that limit
does not appear in the approved plan's A13 or B12 acceptance tables and is not a
release gate. Retain and report the measured sparse cost as well as the capacity
cost; do not describe every background phase as using two or fewer statements.

`run.sh` creates `.local-results/gateway-bench/latest/results.md` and JSON
summaries. Keep before/after evidence in the PR, and update the measured baseline
only after the full local run and accounting/replay checks pass. Never infer a
production result from this local benchmark.
