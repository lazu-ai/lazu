/* Shared wording and arithmetic for the session list, the session detail and
 * the session block in a request receipt. */
import { isKnownAgent } from '../../../components/ui/agent-badge';
import { splitDuration } from '../../../components/ui/metric-format';
import type { AgentSessionRange } from '../../../hooks/usage-logs/useAgentSessions';
import type { TFunction } from 'i18next';

export const SESSION_RANGES: AgentSessionRange[] = ['24h', '7d', '30d'];

/** i18n key prefix every new session string lives under. The older
 *  `page.console.log.session.*` leaves belong to the retired session view. */
export const SK = 'page.console.log.session';

export const isUngrouped = (source?: string | null) => source === 'token';

/** The agent to draw a badge for: the User-Agent's client, or the id's source
 *  when that names an agent we know. */
export const sessionClient = (session: {
  client?: string | null;
  source?: string | null;
}) =>
  session.client ||
  (session.source && isKnownAgent(session.source) ? session.source : '');

/** "3f2a9c1e-7d40-…" → "3f2a9c"; "ses_7Kq2…" → "ses_7Kq2". */
export const shortSessionId = (id?: string | null) => {
  const value = (id || '').trim();
  if (!value) return '';
  if (/^[a-z]{2,5}_/i.test(value)) return value.slice(0, 8);
  return value.slice(0, 6);
};

export const shortThreadId = (id?: string | null) => {
  const value = (id || '').trim();
  if (!value) return '';
  if (value.length <= 10) return value;
  if (/^[a-z]{2,6}[_-]/i.test(value) && value.length > 10)
    return value.slice(0, 10);
  return value.slice(0, 6);
};

/** A request's kind: the recorded one, else main / subagent by its thread. */
export const requestKind = (record: {
  request_kind?: string | null;
  thread_id?: string | null;
}) => record.request_kind || (record.thread_id ? 'subagent' : 'main');

/** Header or field the session id was read from. */
export const sessionSourceLabel = (
  source: string | undefined,
  t: TFunction,
) => {
  switch (source) {
    case 'codex':
      return 'session-id';
    case 'claude-code':
      return 'X-Claude-Code-Session-Id';
    case 'kilo':
      return 'X-KILOCODE-TASKID';
    case 'opencode':
    case 'x-session-id':
      return 'X-Session-Id';
    case 'goose':
      return 'agent-session-id';
    case 'cline':
      return 'X-Task-ID';
    case 'metadata':
      return 'metadata.session_id';
    case 'prompt_cache_key':
      return 'prompt_cache_key';
    case 'client-request-id':
      return 'x-client-request-id';
    case 'token':
      return t(`${SK}.source.token`, { defaultValue: '按 Key 归组' });
    default:
      return source || '—';
  }
};

export const rangeLabel = (range: AgentSessionRange, t: TFunction) =>
  range === '7d'
    ? t(`${SK}.range.7d`, { defaultValue: '7 天' })
    : range === '30d'
      ? t(`${SK}.range.30d`, { defaultValue: '30 天' })
      : range === '90d'
        ? t(`${SK}.range.90d`, { defaultValue: '90 天' })
        : t(`${SK}.range.24h`, { defaultValue: '24 小时' });

/** 46 分钟 / 2 小时 5 分钟 / 不到 1 分钟. */
export const durationText = (seconds: number, t: TFunction) => {
  const { hours, minutes } = splitDuration(seconds);
  if (hours > 0 && minutes > 0)
    return t(`${SK}.duration.hm`, {
      defaultValue: '{{hours}} 小时 {{minutes}} 分钟',
      hours,
      minutes,
    });
  if (hours > 0)
    return t(`${SK}.duration.h`, { defaultValue: '{{hours}} 小时', hours });
  if (minutes > 0)
    return t(`${SK}.duration.m`, { defaultValue: '{{minutes}} 分钟', minutes });
  return t(`${SK}.duration.lt1m`, { defaultValue: '不到 1 分钟' });
};

const pad = (value: number) => String(value).padStart(2, '0');

const dayStart = (date: Date) =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();

export const clockText = (unix: number) => {
  const date = new Date(unix * 1000);
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
};

export const clockSecondsText = (unix: number) => {
  const date = new Date(unix * 1000);
  return `${clockText(unix)}:${pad(date.getSeconds())}`;
};

/** 今天 / 昨天 / 9月28日 — the day part of a timestamp. */
export const dayText = (unix: number, t: TFunction) => {
  const date = new Date(unix * 1000);
  const diff = Math.round((dayStart(new Date()) - dayStart(date)) / 86_400_000);
  if (diff === 0) return t(`${SK}.day.today`, { defaultValue: '今天' });
  if (diff === 1) return t(`${SK}.day.yesterday`, { defaultValue: '昨天' });
  return t(`${SK}.day.date`, {
    defaultValue: '{{month}}月{{day}}日',
    day: date.getDate(),
    month: date.getMonth() + 1,
  });
};

/** 今天 16:40 / 昨天 21:30 / 9月28日. */
export const whenText = (unix: number, t: TFunction) => {
  const date = new Date(unix * 1000);
  const diff = Math.round((dayStart(new Date()) - dayStart(date)) / 86_400_000);
  return diff <= 1
    ? `${dayText(unix, t)} ${clockText(unix)}`
    : dayText(unix, t);
};

/**
 * "14:02 – 14:48" inside today, "昨天 23:10 – 今天 14:55" across days,
 * "9月28日 14:02 – 14:48" on an earlier day. `withToday` prefixes 今天 on a
 * same-day range too (the detail header reads "今天 14:02 – 14:48").
 */
export const timeRangeText = (
  start: number,
  end: number,
  t: TFunction,
  withToday = false,
) => {
  const sameDay =
    dayStart(new Date(start * 1000)) === dayStart(new Date(end * 1000));
  const isToday = dayStart(new Date(start * 1000)) === dayStart(new Date());
  if (sameDay) {
    const range = `${clockText(start)} – ${clockText(end)}`;
    return isToday && !withToday ? range : `${dayText(start, t)} ${range}`;
  }
  return `${dayText(start, t)} ${clockText(start)} – ${dayText(end, t)} ${clockText(end)}`;
};

export const ratio = (part: number, whole: number) =>
  whole > 0 ? part / whole : null;

export const pct = (value: number | null) =>
  value === null ? '—' : `${Math.round(value * 100)}%`;

/** "09-30 14:02 → 15:47"; the end carries its own date only when the
 *  session ran past midnight: "09-29 23:31 → 09-30 00:33". */
export const spanText = (start: number, end: number) => {
  const a = new Date(start * 1000);
  const b = new Date(end * 1000);
  const date = (d: Date) => `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const sameDay = dayStart(a) === dayStart(b);
  return `${date(a)} ${clockText(start)} → ${sameDay ? '' : `${date(b)} `}${clockText(end)}`;
};

const SUPERSCRIPT = '⁰¹²³⁴⁵⁶⁷⁸⁹';

/** spanText for a narrow cell: an end on a later day keeps only its clock
 *  and a day count, "09-29 23:31 → 00:33⁺¹". */
export const spanTextCompact = (start: number, end: number) => {
  const days = Math.round(
    (dayStart(new Date(end * 1000)) - dayStart(new Date(start * 1000))) /
      86_400_000,
  );
  if (days <= 0) return spanText(start, end);
  const a = new Date(start * 1000);
  const offset = `⁺${String(days)
    .split('')
    .map((digit) => SUPERSCRIPT[Number(digit)])
    .join('')}`;
  return `${pad(a.getMonth() + 1)}-${pad(a.getDate())} ${clockText(start)} → ${clockText(end)}${offset}`;
};

export type SessionState = 'ok' | 'retried' | 'failed';

/** 成功: nothing failed. 重试过: something failed and later requests went
 *  through. 失败: the session's last request failed. */
export const sessionState = (session: {
  error_count: number;
  last_at: number;
  last_error_at?: number;
}): SessionState => {
  if (session.error_count <= 0) return 'ok';
  return (session.last_error_at ?? 0) >= session.last_at ? 'failed' : 'retried';
};

/** What the requests that carried a fair list price would have cost at it,
 *  against what they were charged. Only a real saving: no "省 0%". */
export const officialSaving = (figures: {
  compared_microusd?: number;
  official_microusd?: number;
}) => {
  const official = Number(figures.official_microusd) || 0;
  const compared = Number(figures.compared_microusd) || 0;
  if (official <= 0 || compared <= 0) return null;
  const saved = official - compared;
  const pct = Math.round((saved / official) * 100);
  if (pct < 1) return null;
  return { official, pct, saved };
};

export type SessionLane = 'stable' | 'eco' | 'fallback';

/** How many of a session's requests ran on each lane, across its models. */
export const sessionLanes = (
  models: {
    eco_requests?: number;
    fallback_requests?: number;
    stable_requests?: number;
  }[],
) => {
  const counts = { eco: 0, fallback: 0, stable: 0 };
  for (const model of models) {
    counts.stable += Number(model.stable_requests) || 0;
    counts.eco += Number(model.eco_requests) || 0;
    counts.fallback += Number(model.fallback_requests) || 0;
  }
  return (['stable', 'eco', 'fallback'] as SessionLane[])
    .filter((lane) => counts[lane] > 0)
    .map((lane) => ({ count: counts[lane], lane }));
};
