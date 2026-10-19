import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';

export type OnboardingState = {
  active: boolean;
  has_alert: boolean;
  has_key: boolean;
  has_request: boolean;
  needs_password: boolean;
  password_set: boolean;
};

const readBoolean = (source: Record<string, unknown>, key: string) =>
  Boolean(source[key]);

// Steps are done in other tabs or tools (a Key created, a first request
// sent), so the guide re-reads on a slow clock while it is on screen, and at
// once when the tab comes back into view.
const ONBOARDING_REFRESH_INTERVAL_MS = 30_000;

export default function useOnboarding() {
  const { t } = useTranslation();
  const [state, setState] = useState<OnboardingState | null>(null);
  const mountedRef = useRef(false);
  const stateRef = useRef<OnboardingState | null>(null);
  const requestGenerationRef = useRef(0);
  const dismissPendingRef = useRef(false);

  const commitState = useCallback((nextState: OnboardingState | null) => {
    stateRef.current = nextState;
    setState(nextState);
  }, []);

  const refresh = useCallback(async () => {
    if (dismissPendingRef.current) return;
    const requestGeneration = ++requestGenerationRef.current;
    try {
      // The guide is optional and polled; a failed refresh keeps the last
      // state quietly instead of toasting every few seconds.
      const data = unwrap<unknown>(
        await API.get('/api/user/self/onboarding', { skipErrorHandler: true }),
      );
      if (
        !mountedRef.current ||
        dismissPendingRef.current ||
        requestGeneration !== requestGenerationRef.current ||
        data === null ||
        typeof data !== 'object'
      ) {
        return;
      }
      const raw = data as Record<string, unknown>;
      commitState({
        active: readBoolean(raw, 'active'),
        has_alert: readBoolean(raw, 'has_alert'),
        has_key: readBoolean(raw, 'has_key'),
        has_request: readBoolean(raw, 'has_request'),
        needs_password: readBoolean(raw, 'needs_password'),
        password_set: readBoolean(raw, 'password_set'),
      });
    } catch {
      // Keep the last known state when a refresh fails so a transient network
      // error does not make the optional guide disappear.
    }
  }, [commitState]);

  useEffect(() => {
    mountedRef.current = true;
    void refresh();
    return () => {
      mountedRef.current = false;
      requestGenerationRef.current += 1;
    };
  }, [refresh]);

  useEffect(() => {
    // Retry an initial failed probe (`state === null`) and keep refreshing an
    // active guide. Stop only after the server explicitly reports inactive.
    if (state && !state.active) return undefined;

    const refreshWhileVisible = () => {
      if (document.visibilityState === 'visible') void refresh();
    };
    const interval = window.setInterval(
      refreshWhileVisible,
      ONBOARDING_REFRESH_INTERVAL_MS,
    );
    document.addEventListener('visibilitychange', refreshWhileVisible);

    return () => {
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', refreshWhileVisible);
    };
  }, [refresh, state?.active]);

  const dismiss = useCallback(async () => {
    const previousState = stateRef.current;
    if (!previousState || dismissPendingRef.current) return;

    dismissPendingRef.current = true;
    const requestGeneration = ++requestGenerationRef.current;
    commitState({ ...previousState, active: false });

    try {
      unwrap(
        await API.post(
          '/api/user/self/onboarding/dismiss',
          {},
          { skipErrorHandler: true },
        ),
      );
    } catch (failure) {
      if (
        mountedRef.current &&
        requestGeneration === requestGenerationRef.current
      ) {
        commitState(previousState);
        showError(failure, t('page.console.onboarding.dismissFailed'));
      }
    } finally {
      if (requestGeneration === requestGenerationRef.current) {
        dismissPendingRef.current = false;
      }
    }
  }, [commitState, t]);

  return { dismiss, state };
}
