import { useEffect, useState } from 'react';

/** One clock per surface; a deadline timer also disables acceptance at exact expiry. */
export function useInviteClock(deadlines: readonly number[]) {
  const [now, setNow] = useState(Date.now);
  const next = Math.min(...deadlines.filter((time) => time * 1000 > now));
  useEffect(() => {
    const refresh = () => setNow(Date.now());
    const interval = window.setInterval(refresh, 60_000);
    const timeout = Number.isFinite(next)
      ? window.setTimeout(
          refresh,
          Math.min(2_147_483_647, Math.max(0, next * 1000 - Date.now())),
        )
      : undefined;
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.clearInterval(interval);
      window.clearTimeout(timeout);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [next]);
  return now;
}
