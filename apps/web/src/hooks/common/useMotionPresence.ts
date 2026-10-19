import { useCallback, useEffect, useState } from 'react';
import type { TransitionEvent } from 'react';

type MotionState = 'closed' | 'open';

/**
 * Keeps an element mounted while its CSS transition closes. Reopening before
 * transitionend simply retargets the same element, so motion continues from
 * its current visual state instead of restarting from a keyframe endpoint.
 */
export const useMotionPresence = (open: boolean) => {
  const [present, setPresent] = useState(open);
  const [state, setState] = useState<MotionState>(open ? 'open' : 'closed');

  useEffect(() => {
    if (open && !present) {
      setPresent(true);
      return undefined;
    }

    if (open && state === 'closed') {
      const frame = window.requestAnimationFrame(() => setState('open'));
      return () => window.cancelAnimationFrame(frame);
    }

    if (!open && present && state !== 'closed') {
      setState('closed');
    }
    return undefined;
  }, [open, present, state]);

  const onTransitionEnd = useCallback(
    (event: TransitionEvent<HTMLElement>) => {
      if (event.target === event.currentTarget && !open && state === 'closed') {
        setPresent(false);
      }
    },
    [open, state],
  );

  return { onTransitionEnd, present, state };
};
