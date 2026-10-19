# Gateway optimization comparison

Reviewed official documentation and source on 2026-10-04. This is an implementation comparison, not an independently reproduced ranking of gateways.

| Project | Verified approach | Lazu assessment |
| --- | --- | --- |
| [LiteLLM request architecture](https://docs.litellm.ai/docs/proxy/architecture) and [production guidance](https://docs.litellm.ai/docs/proxy/prod) | Cache-first virtual-key authorization; post-response logging; periodic batched spend writes; bounded database pools. | Lazu already has strict 60-second identity validity, snapshots and batched consumers. Its accepted financial facts must first reach Redis or fsynced spool; moving them into an ordinary in-memory task queue is insufficient. |
| [Bifrost provider source](https://github.com/maximhq/bifrost/blob/dev/core/providers/openai/openai.go) and [shared stream utilities](https://github.com/maximhq/bifrost/blob/dev/core/providers/utils/utils.go) | Pooled request/response objects; fasthttp connection reuse; Sonic decoding; large-body passthrough paths. | Adopt measured allocation reductions. Preserve Lazu's existing HTTP/2, proxy, cancellation and protocol behavior when considering a transport or JSON-library replacement. Current isolated profiling does not justify either replacement. |

[LiteLLM's auth-cache documentation](https://docs.litellm.ai/docs/proxy/caching_redis) describes a default 60-second key-cache TTL, an optional shared Redis tier and a capacity setting. Increasing TTL is not part of this optimization: Lazu retains the selected behavior of returning 503 when expired authority cannot be revalidated in SQL.

The [Bifrost README](https://github.com/maximhq/bifrost#performance) reports 11 microseconds of overhead at 5,000 RPS in its own benchmarks. Lazu's acceptance measures the difference in client-observed time to the first complete text-content SSE event. These figures do not establish a relative speed without the same protocol, workload, enabled governance and financial behavior. The [upstream benchmark repository](https://github.com/maximhq/bifrost-benchmarking) also supplies unique prompts and mock-provider tooling; its methodology is useful, but its published numbers are not Lazu measurements.

## Narrow change with measured evidence

Lazu's scanner now reuses its fixed 64KB initial buffer and one callback channel/timer per stream. The buffer is cleared and returned only when the reader goroutine actually exits; oversized scanner growth remains request-owned. Callback timeout, cancellation, ping serialization, event order and protocol-terminal handling are retained.

An identical local in-memory probe before/after this change measured:

| Probe | Before | After |
| --- | --- | --- |
| Initial metadata + first content + terminal | 45.0–45.4 us; 80.3KB; 126 allocations | 34.7–34.8 us; 14.6–14.8KB; 120 allocations |
| 100 content events | 705–709 us; 186.2KB; 1909 allocations | 643–659 us; 84.4–84.6KB; 1507 allocations |

These probes contain no network, authorization or Redis wait and cannot replace the 5ms end-to-end gate. Source/probe manifests and raw profiles are under `.local-results/gateway-optimization/request-probe/`. Helper and OpenAI adapter race suites, including concurrent large-frame ordering and retained callback strings, passed after the change; the full Go suite and vet also passed.

## Dedicated server experiment

The user provided an independent 8-core/16GB Ubuntu server. Experiments use newly generated fixture credentials, fresh PostgreSQL and Redis instances, loopback-only data-store ports, fixed Go 1.25.13 and actual host/image/resource manifests. No production database or environment is copied. The first comparison uses same-host PostgreSQL, followed by a synthetic 35ms PostgreSQL RTT; that delay is not a real managed-database measurement.

Old `6a29da9e7` uses schema21 and requires its own setup/migration adapter. It shares the new mock/client and first-content definition. New ledger, dispatch and timing metrics missing from the old version are reported as unavailable, not zero or passing. Old SQL is attributed to the API process including its native background consumer, so comparison must use compatible total-I/O populations.

The complete baseline pair passed all 11 original numeric checks at 0ms and 35ms, with 814 runtime/tool hashes and six binary hashes identical between runs. Responses 1KB/c1 added 1.540/1.483ms to median first text output; 600KB/c1 added 29.236/28.045ms. This snapshot is a baseline: it predates the following decoder and identity-refresh changes. The 35ms stress maxima were still 3137/2859ms, coinciding with 189/199 duplicate cold identity reads across the 60-second boundary.

Two additional narrow changes retain protocol and authorization behavior:

- Responses common text arrays use a strictly guarded typed decode. Unknown fields, duplicates, nulls, media and tool-item shapes keep the original decoder; outer metadata and native bytes are retained. On the same 614529-byte local fixture, decode fell from 9.912–10.229ms to 4.950–4.972ms and allocations from 73 to 34. Full Go, textgen/relay race, 47 shape comparisons and six native-byte cases passed. Final source additionally rejects unsupported type/role before typed parsing; the original fallback remains, with a safe JSON eligibility scan that adds one large-string copy to unsupported inputs (600KB output_text 10.06–10.09ms baseline versus 10.47–10.54ms final component observation). This is component evidence; server results must be measured separately.
- Each API process merges simultaneous cold refreshes of the same credential digest. Warm authorization adds no dispatch. The coordinator admits at most 1024 active digests, shares serialized projections, and gives each waiter a fresh independent decode and current-time/generation check. Cancellation and the existing SQL deadline remain bounded; validity stays anchored to query start plus 60 seconds. Native PostgreSQL/Redis race tests showed one JOIN and one budget query for 32 concurrent cold or expired requests. This does not merge work across separate API nodes.

Unique-prompt baseline probes also completed, but target 1000/2000 RPS loads sent only 609/509 RPS and the latter included 417 overload responses. Client schedule lag and shared-host CPU contention were material; the API used Redis pool 10 and a 384MiB Go memory target. Financial balances reconciled, while one telemetry dead letter prevented complete drain. These higher-load experiments are observations, not passing capacity gates. Detailed populations, failures and limits are recorded in [validation.md](validation.md).

The final log-target correction suppresses orphan post diagnostics on early authorization rejects and endpoints that never created a log. Active PostgreSQL media carriers retain temporary timing, while expiry/replay still cannot resurrect details. Native PostgreSQL/Redis race and final whole-Go/vet checks passed; this correction does not establish the historical dead letter’s type.

Final dedicated-server optimized0/35ms both passed all 11 unchanged checks, with 816 source/tool and six binary hashes identical between runs. Responses600KB/c1 overhead was19.377/17.015ms; warm API SQL0/requiredRedis3. In the35ms stress, identity JOINs fell189/199→1/10 and first-content MAX3137/2859→616/757ms. Same-host Responses100/600KB/c1 overhead fell37.3%/33.7%; Chat600KB/c8 moved upward19.3%, retained without an unproved cause. Eight-second cohorts remain short observations. The complete table and financial/drain proof are in [validation.md](validation.md).

## Configuration observations and remaining work

Four additional ordinary load observations retained the same final binaries and unique prompt generator. API-only Redis pools 10/32/64 with GOGC50 and a 384MiB runtime target sent 519.10/455.15/491.25 RPS against the 2000-RPS target. Pools32/64 removed overload responses in these observations, but did not improve throughput. Pool64 with GOGC100 and a 1GiB target sent878.50RPS, all200; successful TTFT P95 fell from5486ms at the default fixture configuration to2692ms. All four balances and queues reconciled, including zero telemetry dead letters; the default fixture’s950503 responses remain failures.

At fixed pool64, increasing GC and memory targets together reduced sampled API CPU maxima638.6→481.5% while raising sampled RSS534.9→971.2MiB. These are one-second sample maxima, with100% representing one core. The combined setting is a candidate for further evaluation, not an adopted production default or proof of the contribution of either individual parameter. Every load missed arrivals; client/mock/worker/data stores share eight cores, so sustained independent gateway capacity has not been isolated.

A separate instrumented run captured two ten-second API CPU profiles wholly within the measured load phases. Syscall6 accounted for22.34/22.01% flat samples, JSON object decoding15.21/12.67% cumulative, and JSON validation4.80/4.56% cumulative. Cumulative percentages overlap; the function-only summary does not identify particular syscalls, sockets or JSON call sites. This suggests examining call stacks, stream flush frequency and repeated decoding before attempting transport/JSON-library replacement. Bifrost’s pooling and parsing approach remains useful prior art, not proof of a safe drop-in speedup. Preserve first-content, cancellation, event-order and native-byte behavior in any subsequent experiment. Profiling-run throughput is excluded from the ordinary four-setting comparison.
