import { describe, expect, it } from 'vitest';
import { inviteExpiry } from './inviteExpiry';

describe('invitation expiry', () => {
  const now = Date.UTC(2026, 8, 22, 6, 30);
  const at = (milliseconds: number) =>
    inviteExpiry((now + milliseconds) / 1000, now);
  it('moves from days to hours, then minutes without a fixed three-day label', () => {
    expect(at(3 * 86_400_000)).toMatchObject({
      key: 'projects.invite.remainingDays',
      days: 3,
      hours: 0,
      expired: false,
    });
    expect(at(2 * 86_400_000 + 5 * 3_600_000)).toMatchObject({
      days: 2,
      hours: 5,
    });
    expect(at(8_400_000)).toMatchObject({
      key: 'projects.invite.remainingHours',
      hours: 2,
      minutes: 20,
      soon: true,
    });
    expect(at(120_000)).toMatchObject({
      key: 'projects.invite.remainingMinutes',
      minutes: 2,
    });
  });
  it('never says expired before the actual deadline and expires at that exact instant', () => {
    expect(at(1)).toMatchObject({
      key: 'projects.invite.expiringSoon',
      expired: false,
    });
    expect(at(0)).toMatchObject({
      key: 'projects.invite.expiredBadge',
      expired: true,
      soon: false,
    });
    expect(at(-1)).toMatchObject({ expired: true });
  });
});
