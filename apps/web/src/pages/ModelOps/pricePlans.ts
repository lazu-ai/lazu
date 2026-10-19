/* 专属折扣 — price plans: a negotiated discount off the vendor's official
 * price that a set of customers share. Pure helpers only; the queries live in
 * usePricePlans.ts.
 *
 * The rule the server bills by (internal/model/price_plan.go and
 * controller.buildCatalogPlanPricing): one rule prices a request, the most
 * specific that applies — a model exception, then the model's vendor, then the
 * plan default — and the customer pays min(lane price, official × keep) per
 * field. A field with no official price takes the percentage off the lane
 * price instead. */
import type { TFunction } from 'i18next';

export type PlanLanes = 'all' | 'cheap' | 'direct';

export type PricePlanRule = {
  id?: number;
  kind: 'model' | 'vendor';
  model_name?: string;
  pct: number;
  /** The vendor's public id, as `/api/pricing` lists vendors. */
  vendor_id?: string;
  vendor_name?: string;
};

export type PricePlan = {
  active: boolean;
  default_pct: number | null;
  ends_at: string | null;
  id: number;
  lanes: PlanLanes;
  member_count: number;
  name: string;
  note: string;
  rules: PricePlanRule[];
  starts_at: string | null;
};

export type PricePlanMember = {
  display_name: string;
  email: string;
  id: number;
  public_id: string;
  username: string;
};

export type PricingLaneRow = {
  channel_group: string;
  input_per_mtok?: number | null;
};

export type PricingRow = {
  input_per_mtok?: number | null;
  lanes?: PricingLaneRow[];
  model_name: string;
  official_input_price?: number | null;
  vendor_id?: string;
};

export type PricingVendor = { id: string; name: string };

export type PlanStatus = 'expired' | 'live' | 'off' | 'upcoming';

export const planStatus = (
  plan: Pick<PricePlan, 'active' | 'ends_at' | 'starts_at'>,
  now = Date.now(),
): PlanStatus => {
  if (!plan.active) return 'off';
  if (plan.starts_at && Date.parse(plan.starts_at) > now) return 'upcoming';
  if (plan.ends_at && Date.parse(plan.ends_at) <= now) return 'expired';
  return 'live';
};

const trimNumber = (value: number) => String(Number(value.toFixed(2)));

/** 40 → "6 折", 5 → "95 折", 12.5 → "87.5 折". */
export const zheLabel = (pct: number) => {
  const keep = 100 - pct;
  return `${trimNumber(keep % 10 === 0 ? keep / 10 : keep)} 折`;
};

export const pctLabel = (pct: number | null | undefined) =>
  pct == null ? '—' : `−${trimNumber(pct)}%`;

/** official_* columns hold dollars per token (LiteLLM's unit), but a few rows
 *  were typed per million. Anything that small can only be per token. */
export const officialPerMtok = (value: unknown): number | null => {
  const number = Number(value) || 0;
  if (number <= 0) return null;
  return number < 0.001 ? number * 1e6 : number;
};

/* ---- editor draft ------------------------------------------------------- */

export type VendorRuleDraft = {
  pct: string;
  vendor_id: string;
  vendor_name: string;
};

export type ModelRuleDraft = { model_name: string; pct: string };

export type PlanDraft = {
  active: boolean;
  defaultPct: string;
  endsAt: string;
  lanes: PlanLanes;
  models: ModelRuleDraft[];
  name: string;
  note: string;
  startsAt: string;
  vendors: VendorRuleDraft[];
};

export const emptyDraft = (): PlanDraft => ({
  active: true,
  defaultPct: '',
  endsAt: '',
  lanes: 'direct',
  models: [],
  name: '',
  note: '',
  startsAt: '',
  vendors: [],
});

const pctText = (value: number | null | undefined) =>
  value == null ? '' : trimNumber(value);

export const draftFromPlan = (plan: PricePlan): PlanDraft => ({
  active: plan.active,
  defaultPct: pctText(plan.default_pct),
  endsAt: plan.ends_at ?? '',
  lanes: plan.lanes || 'direct',
  models: plan.rules
    .filter((rule) => rule.kind === 'model')
    .map((rule) => ({
      model_name: rule.model_name ?? '',
      pct: pctText(rule.pct),
    })),
  name: plan.name,
  note: plan.note ?? '',
  startsAt: plan.starts_at ?? '',
  vendors: plan.rules
    .filter((rule) => rule.kind === 'vendor')
    .map((rule) => ({
      pct: pctText(rule.pct),
      vendor_id: rule.vendor_id ?? '',
      vendor_name: rule.vendor_name ?? rule.vendor_id ?? '',
    })),
});

/** A percentage the server accepts: strictly between 0 and 100. */
export const parsePct = (value: string, allowZero = false): number | null => {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const number = Number(trimmed);
  return Number.isFinite(number) &&
    (allowZero ? number >= 0 : number > 0) &&
    number < 100
    ? number
    : null;
};

export type DraftProblem =
  | 'dates'
  | 'defaultPct'
  | 'modelPct'
  | 'name'
  | 'vendorPct';

export const draftProblems = (draft: PlanDraft): DraftProblem[] => {
  const problems: DraftProblem[] = [];
  if (!draft.name.trim()) problems.push('name');
  if (draft.defaultPct.trim() && parsePct(draft.defaultPct) == null)
    problems.push('defaultPct');
  if (draft.vendors.some((rule) => parsePct(rule.pct) == null))
    problems.push('vendorPct');
  if (draft.models.some((rule) => parsePct(rule.pct, true) == null))
    problems.push('modelPct');
  if (
    draft.startsAt &&
    draft.endsAt &&
    Date.parse(draft.endsAt) <= Date.parse(draft.startsAt)
  )
    problems.push('dates');
  return problems;
};

export const payloadFromDraft = (draft: PlanDraft) => ({
  active: draft.active,
  default_pct: parsePct(draft.defaultPct),
  ends_at: draft.endsAt || null,
  lanes: draft.lanes,
  name: draft.name.trim(),
  note: draft.note.trim(),
  rules: [
    ...draft.vendors.map((rule) => ({
      kind: 'vendor' as const,
      pct: parsePct(rule.pct) ?? 0,
      vendor_id: rule.vendor_id,
    })),
    ...draft.models.map((rule) => ({
      kind: 'model' as const,
      model_name: rule.model_name,
      pct: parsePct(rule.pct) ?? 0,
    })),
  ],
  starts_at: draft.startsAt || null,
});

const sameDate = (left: string, right: string) =>
  (left ? Date.parse(left) : 0) === (right ? Date.parse(right) : 0);

const samePct = (left: string, right: string) =>
  left.trim() === right.trim() ||
  (parsePct(left) != null && parsePct(left) === parsePct(right));

/** How many things the save bar would write: each changed field and each
 *  rule added, removed or re-priced counts once. */
export const countChanges = (draft: PlanDraft, initial: PlanDraft) => {
  let count = 0;
  if (draft.name.trim() !== initial.name.trim()) count += 1;
  if (draft.note.trim() !== initial.note.trim()) count += 1;
  if (!sameDate(draft.startsAt, initial.startsAt)) count += 1;
  if (!sameDate(draft.endsAt, initial.endsAt)) count += 1;
  if (draft.lanes !== initial.lanes) count += 1;
  if (draft.active !== initial.active) count += 1;
  if (!samePct(draft.defaultPct, initial.defaultPct)) count += 1;
  const diff = <T>(
    next: T[],
    before: T[],
    keyOf: (item: T) => string,
    pctOf: (item: T) => string,
  ) => {
    const was = new Map(before.map((item) => [keyOf(item), pctOf(item)]));
    const now = new Map(next.map((item) => [keyOf(item), pctOf(item)]));
    let changed = 0;
    now.forEach((pct, key) => {
      if (!was.has(key) || !samePct(pct, was.get(key) ?? '')) changed += 1;
    });
    was.forEach((_, key) => {
      if (!now.has(key)) changed += 1;
    });
    return changed;
  };
  count += diff(
    draft.vendors,
    initial.vendors,
    (rule) => rule.vendor_id,
    (rule) => rule.pct,
  );
  count += diff(
    draft.models,
    initial.models,
    (rule) => rule.model_name,
    (rule) => rule.pct,
  );
  return count;
};

/* ---- preview ------------------------------------------------------------ */

export type RuleHit = {
  kind: 'default' | 'model' | 'vendor';
  pct: number;
  subject: string;
};

/** The one rule of the draft that prices this model, or null. */
export const resolveRule = (
  draft: PlanDraft,
  row: Pick<PricingRow, 'model_name' | 'vendor_id'>,
): RuleHit | null => {
  const model = draft.models.find((rule) => rule.model_name === row.model_name);
  const modelPct = model ? parsePct(model.pct, true) : null;
  if (model && modelPct != null)
    return { kind: 'model', pct: modelPct, subject: model.model_name };
  const vendor = row.vendor_id
    ? draft.vendors.find((rule) => rule.vendor_id === row.vendor_id)
    : undefined;
  const vendorPct = vendor ? parsePct(vendor.pct) : null;
  if (vendor && vendorPct != null)
    return { kind: 'vendor', pct: vendorPct, subject: vendor.vendor_name };
  const fallback = parsePct(draft.defaultPct);
  if (fallback != null) return { kind: 'default', pct: fallback, subject: '' };
  return null;
};

/** The lane a preview prices against: 仅 Agent 专线 reads the agent lane, the other
 *  two read 稳定, the lane every request can fall back to. */
export const previewLane = (lanes: PlanLanes) =>
  lanes === 'cheap' ? 'cheap' : 'direct';

export const laneInputPrice = (
  row: PricingRow,
  lane: string,
): number | null => {
  const match = row.lanes?.find((item) => item.channel_group === lane);
  const value = match
    ? match.input_per_mtok
    : lane === 'direct'
      ? row.input_per_mtok
      : null;
  return value != null && Number.isFinite(value) ? Number(value) : null;
};

export type PreviewRow = {
  hit: RuleHit | null;
  mine: number | null;
  model: string;
  official: number | null;
  standard: number;
};

/** Input price per 1M tokens, as the member pays it on the previewed lane. */
export const previewRow = (
  draft: PlanDraft,
  row: PricingRow,
): PreviewRow | null => {
  const standard = laneInputPrice(row, previewLane(draft.lanes));
  if (standard == null) return null;
  const official = officialPerMtok(row.official_input_price);
  const hit = resolveRule(draft, row);
  if (!hit)
    return { hit, mine: null, model: row.model_name, official, standard };
  if (hit.pct === 0)
    return { hit, mine: standard, model: row.model_name, official, standard };
  const keep = 1 - hit.pct / 100;
  const mine =
    official != null ? Math.min(standard, official * keep) : standard * keep;
  return { hit, mine, model: row.model_name, official, standard };
};

/** Rows worth previewing: models a rule names first, then their vendors'
 *  models, then (with a default) the rest, in catalog order. */
export const previewRows = (
  draft: PlanDraft,
  rows: PricingRow[],
  limit = 8,
): { rows: PreviewRow[]; total: number } => {
  const rank = (row: PricingRow) => {
    const hit = resolveRule(draft, row);
    if (!hit) return 3;
    return hit.kind === 'model' ? 0 : hit.kind === 'vendor' ? 1 : 2;
  };
  const priced = rows
    .map((row, index) => ({ index, rank: rank(row), row }))
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map(({ row }) => previewRow(draft, row))
    .filter((row): row is PreviewRow => row != null);
  const hits = priced.filter((row) => row.hit);
  const shown = hits.length ? hits : priced;
  return { rows: shown.slice(0, limit), total: shown.length };
};

export const formatUsd = (value: number | null) => {
  if (value == null) return '—';
  if (value === 0) return '$0';
  if (value < 0.01) return `$${Number(value.toPrecision(2))}`;
  return `$${value.toFixed(2)}`;
};

/** Models per vendor in the catalog, keyed by the vendor's public id. */
export const vendorModelCounts = (rows: PricingRow[]) => {
  const counts = new Map<string, number>();
  rows.forEach((row) => {
    if (!row.vendor_id) return;
    counts.set(row.vendor_id, (counts.get(row.vendor_id) ?? 0) + 1);
  });
  return counts;
};

/** "7 条 · 5%–40%" in the list; the range only when there is one. */
export const vendorRuleSpan = (rules: PricePlanRule[]) => {
  const pcts = rules
    .filter((rule) => rule.kind === 'vendor')
    .map((rule) => rule.pct);
  if (!pcts.length) return { count: 0, range: '' };
  const min = Math.min(...pcts);
  const max = Math.max(...pcts);
  return {
    count: pcts.length,
    range:
      min === max
        ? `${trimNumber(min)}%`
        : `${trimNumber(min)}%–${trimNumber(max)}%`,
  };
};

/** A date input's value (YYYY-MM-DD) to the instant the plan switches:
 *  local midnight of that day. */
export const dateToIso = (date?: Date) =>
  date
    ? new Date(
        date.getFullYear(),
        date.getMonth(),
        date.getDate(),
      ).toISOString()
    : '';

export const isoToDate = (iso: string) => (iso ? new Date(iso) : undefined);

/* A plan ends *before* ends_at, so "至 12-31" is stored as the midnight that
   opens 1 月 1 日 and read back one millisecond earlier. */
export const endDateToIso = (date?: Date) =>
  date
    ? new Date(
        date.getFullYear(),
        date.getMonth(),
        date.getDate() + 1,
      ).toISOString()
    : '';

export const endIsoToDate = (iso: string | null | undefined) =>
  iso ? new Date(Date.parse(iso) - 1) : undefined;

export const formatEndDay = (iso: string | null | undefined) =>
  iso ? formatDay(new Date(Date.parse(iso) - 1).toISOString()) : '';

export const formatDay = (iso: string | null | undefined) => {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
};

export const laneLabel = (t: TFunction, lanes: PlanLanes) =>
  lanes === 'all'
    ? t('page.console.modelOps.plans.lanes.all', { defaultValue: '全部线路' })
    : lanes === 'cheap'
      ? t('page.console.modelOps.plans.lanes.cheap', {
          defaultValue: '仅 Agent 专线',
        })
      : t('page.console.modelOps.plans.lanes.direct', {
          defaultValue: '仅稳定',
        });
