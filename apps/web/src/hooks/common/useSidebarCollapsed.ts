import { useCallback, useState } from 'react';

const KEY = 'default_collapse_sidebar';

export const useSidebarCollapsed = (): [
  boolean,
  () => void,
  (value: boolean) => void,
] => {
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(KEY) === 'true';
    } catch {
      return false;
    }
  });

  const toggle = useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(KEY, next.toString());
      } catch {
        // ignore storage failures
      }
      return next;
    });
  }, []);

  const set = useCallback((value: boolean) => {
    setCollapsed(value);
    try {
      localStorage.setItem(KEY, value.toString());
    } catch {
      // ignore storage failures
    }
  }, []);

  return [collapsed, toggle, set];
};
