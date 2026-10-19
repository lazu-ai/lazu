/* The door's outline, and the one clock that decides when it shows.
 *
 * Two different things can keep the column empty — the route chunk still
 * arriving, and the sign-in configuration still outstanding — and they happen
 * back to back. Measured from the moment the route was entered, they are one
 * wait, so they share one grace period: whoever draws the outline first anchors
 * the clock, and the second one inherits it instead of restarting it. Without
 * that, a slow load blinked — outline, nothing, outline again — which is the
 * same jitter the page was fixed to remove.
 */
import { useEffect, useState } from 'react';
import { AuthExits } from './AuthExits';
import TermsNotice from './TermsNotice';
import { Skeleton } from '../ui';

const REVEAL_DELAY_MS = 300;

let enteredAt: number | null = null;

/** Called by whatever is waiting; only the first call sets the clock. */
export function markDoorEntry() {
  if (enteredAt === null) enteredAt = Date.now();
}

/** Called once the real door is on screen, so the next visit waits afresh. */
export function endDoorEntry() {
  enteredAt = null;
}

/* Nothing paints for the first 300ms: most doors open inside that window, and
 * the best loading state is the one nobody ever saw. */
export function useDoorReveal(active = true) {
  if (active) markDoorEntry();
  const elapsed = active && enteredAt !== null ? Date.now() - enteredAt : 0;
  const remaining = Math.max(0, REVEAL_DELAY_MS - elapsed);
  const [revealed, setRevealed] = useState(active && remaining <= 0);

  useEffect(() => {
    if (!active || revealed) return undefined;
    const timer = setTimeout(() => setRevealed(true), remaining);
    return () => clearTimeout(timer);
  }, [active, remaining, revealed]);

  return active && revealed;
}

type KnownDoor = {
  credential: 'code' | 'password' | null;
  providerCount: number;
  showSwitch: boolean;
};

/* Before configuration arrives, outline the widest likely door. Once it is
   known, a session check can keep waiting while reserving that exact door. */
export default function DoorSkeleton({ layout }: { layout?: KnownDoor }) {
  const providerCount = layout?.providerCount ?? 2;
  const credential = layout?.credential ?? 'code';
  const showSwitch = layout?.showSwitch ?? true;
  const hasForm = layout ? layout.credential !== null : true;
  return (
    <div
      aria-busy='true'
      aria-live='polite'
      className='auth-skeleton'
      role='status'
    >
      <Skeleton className='auth-skeleton__mark' height={30} width={96} />
      {providerCount > 0 ? (
        <div className='auth-skeleton__group'>
          {Array.from({ length: providerCount }, (_, index) => (
            <Skeleton height={40} key={index} width='100%' />
          ))}
        </div>
      ) : null}
      {providerCount > 0 && hasForm ? (
        <div className='auth-skeleton__divider'>
          <Skeleton height={10} width='18%' />
        </div>
      ) : null}
      {showSwitch ? <Skeleton height={38} width='100%' /> : null}
      {hasForm ? (
        <div className='auth-skeleton__form'>
          {Array.from(
            { length: credential === 'password' ? 2 : 1 },
            (_, index) => (
              <div className='auth-skeleton__field' key={index}>
                <div className='auth-skeleton__labelrow'>
                  <Skeleton height={12} width='24%' />
                </div>
                <Skeleton height={40} width='100%' />
              </div>
            ),
          )}
          <Skeleton height={40} width='100%' />
        </div>
      ) : null}
      {providerCount > 0 || hasForm ? (
        // A bar cannot wrap like the real sentence, so paint the notice.
        <TermsNotice />
      ) : null}
      <AuthExits />
    </div>
  );
}
