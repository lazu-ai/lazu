import { useSyncExternalStore } from 'react';
import {
  fetchAuthStatus,
  getAuthStatusSnapshot,
  subscribeAuthStatus,
  type AuthProvider,
  type AuthStatus,
  type AuthStatusSnapshot,
} from './authStatus';

export type { AuthProvider, AuthStatus, AuthStatusSnapshot };

/* Reads the one shared answer in `authStatus.ts`.
 *
 * The request is kicked off from the render body rather than an effect: it is
 * idempotent — every caller after the first gets the same promise — and an
 * effect would post it one paint later, which is exactly the window this page
 * is trying to close. Callers get `ready` alongside the data so they can wait
 * for the answer instead of rendering a guess of what it will say.
 */
export default function useAuthStatus(): AuthStatusSnapshot {
  void fetchAuthStatus();
  return useSyncExternalStore(
    subscribeAuthStatus,
    getAuthStatusSnapshot,
    getAuthStatusSnapshot,
  );
}
