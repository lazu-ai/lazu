/* The signed-in account (`/api/user/self`), read once and shared.
 *
 * The sidebar, the route guards, the top bar and the project switcher all
 * need this one answer. Each used to ask for it on its own, on every page
 * switch and every 30 seconds per tab, which made it most of the console's
 * traffic. They now share one cached copy: it is fetched once, refreshed when
 * the tab comes back into view after a minute or more, and refreshed on
 * purpose after something that changes it (`refreshSelf`). */
import { useQuery, type QueryClient } from '@tanstack/react-query';
import { API } from '../../helpers/api';
import { getUserIdFromLocalStorage } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

export type SelfRecord = Record<string, unknown>;

export const SELF_QUERY_KEY = [...queryKeys.user, 'self'] as const;

/** Membership and roles change from other people's actions, so a returning
 *  tab re-reads after this long; within it the cached answer is current. */
export const SELF_STALE_MS = 60_000;

export const selfQueryOptions = () => {
  const accountId = getUserIdFromLocalStorage();
  return {
    queryKey: [...SELF_QUERY_KEY, accountId],
    queryFn: async ({ signal }: { signal: AbortSignal }) =>
      unwrap<SelfRecord | null>(
        await API.get('/api/user/self', {
          signal,
          skipErrorHandler: true,
          disableDuplicate: true,
          headers: { 'X-Lazu-User': String(accountId) },
        } as Parameters<typeof API.get>[1]),
      ) ?? null,
    staleTime: SELF_STALE_MS,
    refetchOnWindowFocus: true,
  };
};

/** Bootstrap shares any fresh server answer; mutations explicitly refresh. */
export const readSelf = (client: QueryClient) =>
  client.fetchQuery(selfQueryOptions());
export const refreshSelf = (client: QueryClient) =>
  client.fetchQuery({ ...selfQueryOptions(), staleTime: 0 });
export function useSelfQuery(enabled = true) {
  return useQuery({
    ...selfQueryOptions(),
    enabled: enabled && getUserIdFromLocalStorage() !== -1,
  });
}
