import { useSyncExternalStore } from 'react';

export const MOBILE_BREAKPOINT = 768;

export const useIsMobile = (enabled = true): boolean => {
  const query = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`;
  return useSyncExternalStore(
    (callback) => {
      if (!enabled) return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener('change', callback);
      return () => mql.removeEventListener('change', callback);
    },
    () => enabled && window.matchMedia(query).matches,
    () => false,
  );
};
