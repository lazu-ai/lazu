import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../i18n/i18n', () => ({
  default: {
    t: (key: string, options?: { count?: number }) =>
      options?.count === undefined ? key : `${key}:${options.count}`,
  },
}));

const { formatRelativeTime, formatTimestamp } = await import('./date');

describe('formatTimestamp', () => {
  it('formats seconds as local YYYY-MM-DD HH:mm:ss', () => {
    const local = new Date(2026, 0, 2, 3, 4, 5);
    expect(formatTimestamp(local.getTime() / 1000)).toBe('2026-01-02 03:04:05');
    const late = new Date(2026, 11, 31, 23, 59, 59);
    expect(formatTimestamp(late.getTime() / 1000)).toBe('2026-12-31 23:59:59');
  });
});

describe('formatRelativeTime', () => {
  const now = new Date(2026, 9, 26, 12, 0, 0);
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
  });
  afterEach(() => {
    vi.useRealTimers();
  });
  const ago = (ms: number) => new Date(now.getTime() - ms);
  const minute = 60_000;
  const day = 24 * 60 * minute;

  it('steps through minutes, hours, days, weeks, months and years', () => {
    expect(formatRelativeTime(ago(30_000))).toBe('common.relativeTime.justNow');
    expect(formatRelativeTime(ago(5 * minute))).toBe(
      'common.relativeTime.minutesAgo:5',
    );
    expect(formatRelativeTime(ago(3 * 60 * minute))).toBe(
      'common.relativeTime.hoursAgo:3',
    );
    expect(formatRelativeTime(ago(2 * day))).toBe(
      'common.relativeTime.daysAgo:2',
    );
    expect(formatRelativeTime(ago(15 * day))).toBe(
      'common.relativeTime.weeksAgo:2',
    );
    expect(formatRelativeTime(ago(90 * day))).toBe(
      'common.relativeTime.monthsAgo:3',
    );
    expect(formatRelativeTime(ago(400 * day))).toBe(
      'common.relativeTime.oneYearAgo',
    );
  });

  it('shows the date for old, future and unparseable moments', () => {
    expect(formatRelativeTime(new Date(2020, 4, 6))).toBe('2020-05-06');
    expect(formatRelativeTime(new Date(2027, 0, 9))).toBe('2027-01-09');
    expect(formatRelativeTime('not a date')).toBe('not a date');
    expect(formatRelativeTime(undefined)).toBe('');
  });
});
