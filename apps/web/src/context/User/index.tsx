import { useQuery, useQueryClient } from '@tanstack/react-query';
import React, { useCallback, useMemo, type ReactNode } from 'react';
import { queryKeys } from '../../lib/query-keys';
import type { UserAction, UserPayload, UserState } from './reducer';

/* The signed-in user the console shows, as set when a sign-in or a profile
 * change returns it. It lives in the query cache under its own key, never
 * fetched, so every component reads one copy without a reducer. The account
 * as the server sees it now is useSelfQuery's job. */
export const SESSION_QUERY_KEY = ['session', 'user'] as const;

/* What survives a sign-out: the deployment's public settings, and the session
 * entry itself, which signOut sets to null right after. */
const keepAcrossSessions = [queryKeys.status, SESSION_QUERY_KEY] as const;

export type Session = {
  user: UserPayload;
  signIn: (user: UserPayload) => void;
  signOut: () => void;
};

export function useSession(): Session {
  const queryClient = useQueryClient();
  const { data } = useQuery<UserPayload | null>({
    queryKey: SESSION_QUERY_KEY,
    queryFn: () =>
      queryClient.getQueryData<UserPayload | null>(SESSION_QUERY_KEY) ?? null,
    enabled: false,
    staleTime: Infinity,
    gcTime: Infinity,
  });
  const signIn = useCallback(
    (user: UserPayload) => {
      queryClient.setQueryData(SESSION_QUERY_KEY, user ?? null);
    },
    [queryClient],
  );
  const signOut = useCallback(() => {
    // A new session must not reuse anything the old one read: memberships,
    // flags, balances, keys, logs. Every cached answer is dropped except the
    // deployment's public settings, which are the same for every visitor;
    // otherwise another account signing in on this tab within a query's
    // freshness window would first see the previous account's data. Removal
    // also cancels in-flight reads before they can refill the cache.
    queryClient.removeQueries({
      predicate: (query) =>
        !keepAcrossSessions.some((key) => query.queryKey[0] === key[0]),
    });
    queryClient.setQueryData(SESSION_QUERY_KEY, null);
  }, [queryClient]);
  return useMemo(
    () => ({ user: data ?? undefined, signIn, signOut }),
    [data, signIn, signOut],
  );
}

/* The reducer-shaped API extensions still use: [{ user }, dispatch] with
 * 'login' and 'logout' actions. It reads and writes the same session. */
export type UserContextValue = [UserState, React.Dispatch<UserAction>];

const noopDispatch: React.Dispatch<UserAction> = () => undefined;

export const UserContext = React.createContext<UserContextValue>([
  { user: undefined },
  noopDispatch,
]);

export const UserProvider = ({ children }: { children: ReactNode }) => {
  const { user, signIn, signOut } = useSession();
  const dispatch = useCallback<React.Dispatch<UserAction>>(
    (action) => {
      if (action.type === 'login') signIn(action.payload);
      else if (action.type === 'logout') signOut();
    },
    [signIn, signOut],
  );
  const value = useMemo<UserContextValue>(
    () => [{ user }, dispatch],
    [user, dispatch],
  );
  return <UserContext.Provider value={value}>{children}</UserContext.Provider>;
};
