import { normalizeLaneSlug } from './sellPrices';
import type { LaneKey, LanePrice, SellPriceRow } from './sellPrices';
import type {
  ModelOpsHealth,
  ModelOpsLane,
  ModelOpsPoolEntry,
  ModelOpsSourceType,
} from '../../hooks/models/useModelOpsQuery';

/** Shared read-only derivations for the 模型运营 page. Everything here reduces a
 *  real backend field to something displayable — none of it invents values. */

export type HealthTone = 'bad' | 'idle' | 'ok' | 'warn';

/** Thresholds mirror internal/controller/observability.go (0.99 degraded,
 *  0.95 outage) so the console and /api/admin/health cannot disagree. */
export const HEALTH_DEGRADED = 0.99;
export const HEALTH_OUTAGE = 0.95;

export const healthTone = (
  health: ModelOpsHealth | undefined,
  channelStatus?: number,
): HealthTone => {
  if (channelStatus !== undefined && channelStatus !== 1) return 'bad';
  if (!health || health.request_count <= 0 || health.success_rate === null) {
    return 'idle';
  }
  if (health.success_rate >= HEALTH_DEGRADED) return 'ok';
  if (health.success_rate >= HEALTH_OUTAGE) return 'warn';
  return 'bad';
};

export const bucketTone = (successRate: number | null): HealthTone => {
  if (successRate === null) return 'idle';
  if (successRate >= HEALTH_DEGRADED) return 'ok';
  if (successRate >= HEALTH_OUTAGE) return 'warn';
  return 'bad';
};

export const laneToKey = (lane: ModelOpsLane): LaneKey =>
  lane === 'cheap' ? 'eco' : 'stable';

export const keyToLane = (key: LaneKey): ModelOpsLane =>
  key === 'eco' ? 'cheap' : 'direct';

export const LANE_ORDER: ModelOpsLane[] = ['direct', 'cheap'];

/** Display order. 未分类 leads because it is the state that needs action. */
export const SOURCE_TYPES: ModelOpsSourceType[] = [
  'unclassified',
  'official',
  'cloud',
  'aggregator',
  'reverse',
];

export const formatUsd = (value: number | null | undefined, digits = 2) => {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return '—';
  }
  return `$${value.toFixed(digits)}`;
};

/** Price-per-Mtok values span four orders of magnitude ($0.14 → $75.00); a fixed
 *  2dp would render cheap models as $0.00. */
export const formatPerMtok = (value: number | null | undefined) => {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return '—';
  }
  if (value === 0) return '0';
  return value < 1 ? value.toFixed(4).replace(/0+$/, '') : value.toFixed(2);
};

export const formatLatency = (ms: number | null | undefined) => {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms <= 0) {
    return '—';
  }
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`;
};

export const formatRate = (rate: number | null | undefined) => {
  if (rate === null || rate === undefined || !Number.isFinite(rate)) return '—';
  return `${(rate * 100).toFixed(rate >= 0.999 ? 2 : 1)}%`;
};

/** A lane is on sale when an active sell-price row carries any price at all. */
export const laneHasAnyPrice = (lane: LanePrice | undefined) =>
  Boolean(
    lane &&
    (lane.input_per_million != null ||
      lane.output_per_million != null ||
      lane.per_call != null ||
      lane.per_second != null),
  );

/** Roll a pool up into one health summary. Rates are weighted by request count,
 *  because a source with three requests must not drag a busy pool's number. */
export const aggregatePoolHealth = (
  entries: ModelOpsPoolEntry[] | undefined,
): ModelOpsHealth => {
  const empty: ModelOpsHealth = {
    avg_latency_ms: null,
    error_count: 0,
    last_outcome_at: null,
    request_count: 0,
    success_rate: null,
  };
  if (!entries?.length) return empty;
  let requests = 0;
  let errors = 0;
  let latencyWeighted = 0;
  entries.forEach((entry) => {
    requests += entry.health?.request_count || 0;
    errors += entry.health?.error_count || 0;
    if (entry.health?.avg_latency_ms && entry.health.request_count > 0) {
      latencyWeighted +=
        entry.health.avg_latency_ms * entry.health.request_count;
    }
  });
  if (requests <= 0) return empty;
  return {
    avg_latency_ms: latencyWeighted > 0 ? latencyWeighted / requests : null,
    error_count: errors,
    last_outcome_at: null,
    request_count: requests,
    success_rate: Math.max(0, (requests - errors) / requests),
  };
};

/** Blended cost basis for a pool, weighted by the routing weight actually in
 *  effect. Null when no source in the pool has a cost row — the tier card then
 *  shows "—" instead of a fabricated margin. */
export const poolCostBasis = (entries: ModelOpsPoolEntry[] | undefined) => {
  if (!entries?.length) {
    return { input: null, output: null, request: null, second: null };
  }
  const reduce = (
    field:
      | 'cost_input_per_mtok'
      | 'cost_output_per_mtok'
      | 'cost_request_usd'
      | 'cost_second_usd',
  ) => {
    let weightSum = 0;
    let valueSum = 0;
    entries.forEach((entry) => {
      if (!entry.enabled || entry.channel_status !== 1) return;
      const value = entry[field];
      if (value === null || value === undefined) return;
      // Match getRandomSatisfiedChannel exactly: it smooths every source by
      // +10 before the weighted random pick, including zero-weight sources.
      const weight = Math.max(0, entry.weight) + 10;
      weightSum += weight;
      valueSum += value * weight;
    });
    return weightSum > 0 ? valueSum / weightSum : null;
  };
  return {
    input: reduce('cost_input_per_mtok'),
    output: reduce('cost_output_per_mtok'),
    request: reduce('cost_request_usd'),
    second: reduce('cost_second_usd'),
  };
};

/** Gross margin against the blended cost basis, using output price weighted 1:1
 *  with input — the pool has no token-mix data, so this is an unweighted pair
 *  average and is labelled as an estimate in the UI. */
export const grossMargin = (
  sellInput: number | null | undefined,
  sellOutput: number | null | undefined,
  costInput: number | null,
  costOutput: number | null,
): number | null => {
  const pairs: Array<[number, number]> = [];
  if (sellInput != null && costInput != null)
    pairs.push([sellInput, costInput]);
  if (sellOutput != null && costOutput != null) {
    pairs.push([sellOutput, costOutput]);
  }
  if (!pairs.length) return null;
  const revenue = pairs.reduce((sum, [sell]) => sum + sell, 0);
  const cost = pairs.reduce((sum, [, value]) => sum + value, 0);
  if (revenue <= 0) return null;
  return (revenue - cost) / revenue;
};

export const marginClass = (margin: number) => {
  if (margin < 0) return 'model-ops__margin--negative';
  if (margin < 0.1) return 'model-ops__margin--thin';
  return '';
};

/** Newest active row per (model, lane); the sell-price table is append-only and
 *  versioned, so the first match in the API's ordering wins. */
/** Index sell prices by model and lane.
 *
 *  The lane is read as written, not folded onto 稳定. Lanes are a registry an
 *  operator adds to now, and the old `=== 'cheap' ? 'cheap' : 'direct'` shape
 *  filed every other lane's row under `direct` — so a third lane's price could
 *  be displayed as the stable price, and become the baseline a stable-price
 *  edit starts from. A lane this page does not render is simply absent. */
export const indexSellPrices = (rows: SellPriceRow[]) => {
  const map = new Map<string, SellPriceRow>();
  rows.forEach((row) => {
    const lane = normalizeLaneSlug(row.channel_group);
    const key = `${row.model_name}:${lane}`;
    if (!map.has(key)) map.set(key, row);
  });
  return map;
};
