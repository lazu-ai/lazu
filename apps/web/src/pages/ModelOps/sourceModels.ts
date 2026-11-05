/* What a source serves, as the console edits it: public model names (the
   channel's `models`) plus the upstream ID each one is requested as (its
   `model_mapping`, public → upstream). These helpers keep the two in step. */

import type {
  UpstreamSyncLogEntry,
  UpstreamSyncState,
} from '../../hooks/models/useSourceUpstream';

type ModelMapping = Record<string, string>;

export const parseModelMapping = (raw?: string | null): ModelMapping => {
  if (!raw || !raw.trim()) return {};
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {};
    }
    const out: ModelMapping = {};
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === 'string' && value.trim()) {
        out[key.trim()] = value.trim();
      }
    }
    return out;
  } catch {
    return {};
  }
};

export const serializeModelMapping = (mapping: ModelMapping) =>
  Object.keys(mapping).length ? JSON.stringify(mapping) : '';

/** The upstream ID a public name is requested as. */
const upstreamIdOf = (name: string, mapping: ModelMapping) =>
  mapping[name] || name;

/** The name an upstream ID should be sold under. An aggregator that prefixes
 *  its IDs ("vendor/claude-opus-5-5") maps onto the catalog's own name when
 *  the catalog knows the rest of it and not the whole. */
export const suggestPublicName = (
  upstreamId: string,
  catalog: ReadonlySet<string>,
) => {
  if (catalog.has(upstreamId)) return upstreamId;
  const slash = upstreamId.lastIndexOf('/');
  if (slash > 0) {
    const tail = upstreamId.slice(slash + 1);
    if (tail && catalog.has(tail)) return tail;
  }
  return upstreamId;
};

/** Adds upstream IDs to what a source serves. A suggested public name that
 *  another selected ID already holds falls back to the raw ID, so two
 *  upstreams never land on one name. */
export const serveUpstreamIds = (
  models: string[],
  mapping: ModelMapping,
  upstreamIds: string[],
  catalog: ReadonlySet<string>,
) => {
  const nextModels = [...models];
  const nextMapping = { ...mapping };
  const taken = new Set(nextModels);
  const servedUpstream = new Set(
    nextModels.map((name) => upstreamIdOf(name, nextMapping)),
  );
  for (const id of upstreamIds) {
    if (servedUpstream.has(id)) continue;
    let name = suggestPublicName(id, catalog);
    if (taken.has(name)) name = id;
    if (taken.has(name)) continue;
    taken.add(name);
    servedUpstream.add(id);
    nextModels.push(name);
    if (name !== id) nextMapping[name] = id;
  }
  return { mapping: nextMapping, models: nextModels };
};

/** Stops serving public names; their mapping goes with them. */
export const stopServing = (
  models: string[],
  mapping: ModelMapping,
  names: string[],
) => {
  const drop = new Set(names);
  const nextMapping = { ...mapping };
  for (const name of names) delete nextMapping[name];
  return {
    mapping: nextMapping,
    models: models.filter((name) => !drop.has(name)),
  };
};

/** The ignore patterns the gateway applies (dto.CompileUpstreamIgnore):
 *  "re:" starts a regular expression, anything else is matched whole with
 *  * as a wildcard. A pattern that does not compile matches nothing. */
export const compileIgnore = (text: string) => {
  const matchers: RegExp[] = [];
  const invalid: string[] = [];
  for (const raw of text.split('\n')) {
    const pattern = raw.trim();
    if (!pattern) continue;
    try {
      if (pattern.startsWith('re:')) {
        matchers.push(new RegExp(pattern.slice(3).trim()));
      } else {
        const body = pattern
          .split('*')
          .map((part) => part.replace(/[.+?^${}()|[\]\\]/g, '\\$&'))
          .join('.*');
        matchers.push(new RegExp(`^${body}$`));
      }
    } catch {
      invalid.push(pattern);
    }
  }
  return {
    invalid,
    match: (id: string) => matchers.some((re) => re.test(id)),
  };
};

export type UpstreamStatus = 'gone' | 'in' | 'new' | 'unknown';

/** One row of a source's 模型 table. */
export type SourceModelRow = {
  /** The public name, or the suggested one for a model not served yet. */
  name: string;
  served: boolean;
  status: UpstreamStatus;
  upstreamId: string;
};

/** Every model a source serves or could serve: what it serves now, what the
 *  upstream listed at the last check, and what a check stopped serving. */
export const buildSourceModelRows = (
  models: string[],
  mapping: ModelMapping,
  state: UpstreamSyncState | null | undefined,
  catalog: ReadonlySet<string>,
): SourceModelRow[] => {
  const listed = new Set(state?.upstream ?? []);
  const known = Boolean(state?.listed_at);
  const pending = new Set(state?.new ?? []);
  const rows: SourceModelRow[] = [];
  const covered = new Set<string>();
  const usedNames = new Set(models);
  for (const name of models) {
    const upstreamId = upstreamIdOf(name, mapping);
    covered.add(upstreamId);
    covered.add(name);
    rows.push({
      name,
      served: true,
      status: !known
        ? 'unknown'
        : listed.has(upstreamId) || listed.has(name)
          ? 'in'
          : 'gone',
      upstreamId,
    });
  }
  for (const name of state?.stopped ?? []) {
    if (covered.has(name) || usedNames.has(name)) continue;
    covered.add(name);
    usedNames.add(name);
    rows.push({
      name,
      served: false,
      status: 'gone',
      upstreamId: upstreamIdOf(name, mapping),
    });
  }
  for (const upstreamId of state?.upstream ?? []) {
    if (covered.has(upstreamId)) continue;
    covered.add(upstreamId);
    let name = suggestPublicName(upstreamId, catalog);
    if (usedNames.has(name)) name = upstreamId;
    usedNames.add(name);
    rows.push({
      name,
      served: false,
      status: pending.has(upstreamId) ? 'new' : 'in',
      upstreamId,
    });
  }
  return rows;
};

/** "+3 新出现：a、b；−2 不再列出：c" — what one check found, in a line. */
export const describeLogChange = (entry: UpstreamSyncLogEntry) => {
  if (entry.error) return { tone: 'bad' as const, text: entry.error };
  const parts: string[] = [];
  if (entry.new?.length) {
    parts.push(`+${entry.new.length} 新出现：${entry.new.join('、')}`);
  }
  if (entry.gone?.length) {
    parts.push(`−${entry.gone.length} 不再列出：${entry.gone.join('、')}`);
  }
  if (parts.length) return { tone: 'change' as const, text: parts.join('；') };
  return { tone: 'none' as const, text: '' };
};

/** "11-05 22:12": the short local time every log line starts with. */
export const formatCheckTime = (unix?: number) => {
  if (!unix) return '—';
  const date = new Date(unix * 1000);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
};
