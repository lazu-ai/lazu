/* 用量与日志's one time range. API 调用, Agent 会话 and 生成任务 share it
 * through the address bar, so picking 7 天 on one tab is 7 天 on the next:
 *
 *   ?range=24h | 7d | 30d          a preset (none means 7d)
 *   ?from=2026-10-21&to=2026-10-28 whole local days from the calendar
 *   ?start_timestamp=…&end_timestamp=…  an exact window from a deep link
 */
import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  type DayKey,
  type TimePickerValue,
  rangeToQueryWindow,
  resolvePreset,
} from '../../components/dashboard/time-picker-v3';

export const USAGE_RANGE_PRESETS = ['24h', '7d', '30d'] as const;
export type UsageRangePreset = (typeof USAGE_RANGE_PRESETS)[number];
export const DEFAULT_USAGE_RANGE: UsageRangePreset = '7d';

/** Address-bar keys that belong to the range; tab switches keep them. */
export const USAGE_RANGE_PARAMS = [
  'range',
  'from',
  'to',
  'start_timestamp',
  'end_timestamp',
] as const;

export type UsageRange = {
  /** Whole days the window touches, for 「8 天」 on narrow screens. */
  days: number;
  /** Unix seconds; the end is the last second of the last day. */
  end: number;
  /** 「10/21 – 10/28」 for a custom range, empty for a preset. */
  label: string;
  picker: TimePickerValue;
  preset: UsageRangePreset | 'custom';
  start: number;
};

const DAY = 86_400;
const pad = (n: number) => String(n).padStart(2, '0');
const toDayKey = (date: Date): DayKey => ({
  y: date.getFullYear(),
  m: date.getMonth(),
  d: date.getDate(),
});
export const dayParam = (day: DayKey) =>
  `${day.y}-${pad(day.m + 1)}-${pad(day.d)}`;
const parseDay = (value: string | null): DayKey | null => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value ?? '');
  if (!match) return null;
  const date = new Date(+match[1], +match[2] - 1, +match[3]);
  return date.getDate() === +match[3] ? toDayKey(date) : null;
};
const sameDay = (a: DayKey, b: DayKey) =>
  a.y === b.y && a.m === b.m && a.d === b.d;
const shortDay = (day: DayKey) => `${day.m + 1}/${day.d}`;
const clock = (date: Date) =>
  `${pad(date.getHours())}:${pad(date.getMinutes())}`;

const isPreset = (value: string | null): value is UsageRangePreset =>
  (USAGE_RANGE_PRESETS as readonly string[]).includes(value ?? '');

function presetRange(preset: UsageRangePreset, now: Date): UsageRange {
  const resolved = resolvePreset(preset, now)!;
  const picker = { presetId: preset, range: resolved };
  const window = rangeToQueryWindow(picker);
  const endOfToday = Math.floor(
    new Date(
      now.getFullYear(),
      now.getMonth(),
      now.getDate(),
      23,
      59,
      59,
    ).getTime() / 1000,
  );
  // 24 小时 runs to the end of today rather than to this hour, so rows
  // written after the range was chosen still land inside it.
  const start =
    preset === '24h'
      ? Math.floor(now.getTime() / 1000) - DAY
      : window.startTimestamp;
  return {
    days: preset === '24h' ? 1 : preset === '7d' ? 7 : 30,
    end: endOfToday,
    label: '',
    picker,
    preset,
    start,
  };
}

export function readUsageRange(
  params: URLSearchParams,
  now: Date = new Date(),
): UsageRange {
  const range = params.get('range');
  if (isPreset(range)) return presetRange(range, now);

  const from = parseDay(params.get('from'));
  const to = parseDay(params.get('to'));
  if (from && to) {
    const [first, last] =
      dayParam(from) <= dayParam(to) ? [from, to] : [to, from];
    const picker: TimePickerValue = {
      presetId: 'custom',
      range: { mode: 'day', label: '自定义', start: first, end: last },
    };
    const window = rangeToQueryWindow(picker);
    return {
      days: Math.round((window.endTimestamp - window.startTimestamp) / DAY),
      end: window.endTimestamp,
      label: sameDay(first, last)
        ? shortDay(first)
        : `${shortDay(first)} – ${shortDay(last)}`,
      picker,
      preset: 'custom',
      start: window.startTimestamp,
    };
  }

  const start = Number(params.get('start_timestamp'));
  const end = Number(params.get('end_timestamp'));
  if (
    Number.isFinite(start) &&
    Number.isFinite(end) &&
    start > 0 &&
    end > start
  ) {
    const startDate = new Date(start * 1000);
    const endDate = new Date(end * 1000);
    const first = toDayKey(startDate);
    const last = toDayKey(endDate);
    return {
      days: Math.max(1, Math.ceil((end - start) / DAY)),
      end,
      label: sameDay(first, last)
        ? `${shortDay(first)} ${clock(startDate)} – ${clock(endDate)}`
        : `${shortDay(first)} ${clock(startDate)} – ${shortDay(last)} ${clock(endDate)}`,
      picker: {
        presetId: 'custom',
        range: {
          mode: 'hour',
          label: '自定义',
          start: { ...first, hh: startDate.getHours() },
          end: { ...last, hh: endDate.getHours() },
        },
      },
      preset: 'custom',
      start,
    };
  }

  // 生成任务 used ?days=1|7|30 before the tabs shared a range.
  const days = params.get('days');
  if (days === '1') return presetRange('24h', now);
  if (days === '30') return presetRange('30d', now);
  return presetRange(DEFAULT_USAGE_RANGE, now);
}

/** The shared range and its setters. The window is fixed when the range is
 *  chosen, so polling keeps one query key instead of a moving one. */
export function useUsageRange() {
  const [params, setParams] = useSearchParams();
  const signature = [...USAGE_RANGE_PARAMS, 'days']
    .map((key) => params.get(key) ?? '')
    .join('|');
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const range = useMemo(() => readUsageRange(params), [signature]);

  const write = (apply: (next: URLSearchParams) => void) => {
    const next = new URLSearchParams(params);
    for (const key of [...USAGE_RANGE_PARAMS, 'days']) next.delete(key);
    apply(next);
    setParams(next, { replace: true });
  };

  const setPreset = (preset: UsageRangePreset) =>
    write((next) => next.set('range', preset));

  /* The calendar's quick picks include 今天, 本月 and the like; the three that
     the segments already show go back to being presets. */
  const setPicker = (value: TimePickerValue) => {
    if (isPreset(value.presetId)) {
      setPreset(value.presetId);
      return;
    }
    const { start, end } = value.range;
    write((next) => {
      next.set('from', dayParam(start));
      next.set('to', dayParam(end));
    });
  };

  return { range, setPicker, setPreset };
}
