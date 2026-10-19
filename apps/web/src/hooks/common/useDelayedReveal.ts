import { useEffect, useState } from 'react';

export const REVEAL_DELAY_MS = 300;

/* Nothing paints until the wait has actually been a wait.
 *
 * Most answers arrive inside 300ms, and a loading state that flashes for two
 * frames is worse than no loading state at all — so callers hold the space and
 * draw nothing until this says otherwise. `active` lets a caller keep the hook
 * in a stable position while the thing it is waiting for has already arrived.
 */
export function useDelayedReveal(active = true, delay = REVEAL_DELAY_MS) {
  const [revealed, setRevealed] = useState(false);

  useEffect(() => {
    if (!active) {
      setRevealed(false);
      return undefined;
    }
    const timer = setTimeout(() => setRevealed(true), delay);
    return () => clearTimeout(timer);
  }, [active, delay]);

  return active && revealed;
}
