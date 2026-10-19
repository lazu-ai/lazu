import { describe, expect, it } from 'vitest';
import {
  autoOpenStorageKey,
  dayKeyFromDate,
  shouldAutoOpenToday,
} from './whatsNewAutoOpen';

describe('dayKeyFromDate', () => {
  it('formats a local date as zero-padded YYYY-MM-DD', () => {
    // Month is 0-indexed: 4 => May.
    expect(dayKeyFromDate(new Date(2026, 4, 31))).toBe('2026-05-31');
    expect(dayKeyFromDate(new Date(2026, 0, 9))).toBe('2026-01-09');
  });
});

describe('autoOpenStorageKey', () => {
  it('scopes the throttle key per user', () => {
    expect(autoOpenStorageKey(7)).toBe('whats_new_last_auto_day:7');
    expect(autoOpenStorageKey('alice')).not.toBe(autoOpenStorageKey('bob'));
  });
});

describe('shouldAutoOpenToday', () => {
  it('opens when there is unseen content and it has not opened today', () => {
    expect(shouldAutoOpenToday('2026-05-30', '2026-05-31', true)).toBe(true);
    expect(shouldAutoOpenToday(null, '2026-05-31', true)).toBe(true);
  });

  it('stays closed once it has already opened today', () => {
    expect(shouldAutoOpenToday('2026-05-31', '2026-05-31', true)).toBe(false);
  });

  it('stays closed when there is nothing unseen', () => {
    expect(shouldAutoOpenToday('2026-05-30', '2026-05-31', false)).toBe(false);
    expect(shouldAutoOpenToday(null, '2026-05-31', false)).toBe(false);
  });
});
