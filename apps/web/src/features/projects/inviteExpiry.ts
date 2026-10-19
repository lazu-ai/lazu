/** Exact expiry boundary, with display precision that follows the remaining time. */
export function inviteExpiry(expiresAt: number, now: number) {
  const remaining = expiresAt * 1000 - now;
  const minutes = Math.ceil(remaining / 60_000);
  return {
    expired: remaining <= 0,
    soon: remaining > 0 && remaining < 86_400_000,
    key:
      remaining <= 0
        ? 'projects.invite.expiredBadge'
        : minutes <= 1
          ? 'projects.invite.expiringSoon'
          : minutes >= 1440
            ? 'projects.invite.remainingDays'
            : minutes >= 60
              ? 'projects.invite.remainingHours'
              : 'projects.invite.remainingMinutes',
    days: Math.floor(minutes / 1440),
    hours: Math.floor(minutes / 60) % 24,
    minutes: minutes % 60,
  };
}
