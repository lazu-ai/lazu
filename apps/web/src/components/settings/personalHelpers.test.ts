import { describe, expect, it } from 'vitest';
import {
  DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
  mailPrefsFromUser,
  settingsFromUser,
  toNotificationPayload,
  zheOf,
} from './personalHelpers';

describe('personal settings helpers', () => {
  it('reads USD warning threshold from microUSD setting and preserves 0 (off)', () => {
    expect(settingsFromUser(null).warningThreshold).toBe(
      DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
    );
    expect(
      settingsFromUser({
        setting: JSON.stringify({
          notify_type: 'telegram',
          quota_warning_threshold: 2_000_000,
        }),
      }),
    ).toMatchObject({
      warningType: 'telegram',
      warningThreshold: 2,
    });
    expect(
      settingsFromUser({
        setting: JSON.stringify({ quota_warning_threshold: 0 }),
      }).warningThreshold,
    ).toBe(0);
  });

  it('converts USD threshold back to microUSD payload', () => {
    const payload = toNotificationPayload({
      warningType: 'email',
      warningThreshold: 2.5,
      gotifyPriority: 5,
    });
    expect(payload.quota_warning_threshold).toBe(2_500_000);
    expect(payload.notify_type).toBe('email');
  });

  it('parses mail digest flags and timezone safely', () => {
    expect(mailPrefsFromUser(undefined)).toEqual({
      weeklyDigest: true,
      productUpdates: true,
      timezone: '',
    });
    expect(
      mailPrefsFromUser({
        setting: JSON.stringify({
          weekly_digest_off: true,
          product_updates_off: false,
          timezone: 'Asia/Shanghai',
        }),
      }),
    ).toEqual({
      weeklyDigest: false,
      productUpdates: true,
      timezone: 'Asia/Shanghai',
    });
  });

  it('computes Chinese discount zhe cleanly', () => {
    expect(zheOf(15)).toBe(8.5);
    expect(zheOf(20)).toBe(8);
  });
});
