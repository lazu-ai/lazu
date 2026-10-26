import { useQuery, useQueryClient } from '@tanstack/react-query';
import React, { useCallback, useMemo, type ReactNode } from 'react';
import { API } from '../../helpers/api';
import { readStoredStatus } from '../../helpers/statusStorage';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { StatusAction, StatusPayload, StatusState } from './reducer';

type FetchedStatus = Record<string, unknown> & { system_name?: string };

const fetchStatus = async (): Promise<FetchedStatus> => {
  const data = unwrap<unknown>(
    await API.get('/api/status', {
      skipErrorHandler: true,
    } as Parameters<typeof API.get>[1]),
  );
  return data && typeof data === 'object' ? (data as FetchedStatus) : {};
};

/* The deployment's public settings change only when an operator saves them;
 * the settings pages invalidate this query when they do. Five minutes covers
 * a change made from another browser. */
const STATUS_STALE_MS = 5 * 60 * 1000;

/* One set of options for every reader of queryKeys.status. A refetch runs
 * with the options the query last saw, so a read-only reader must carry the
 * real fetcher too, or an invalidation would refetch with a stub. */
export const statusQueryOptions = () => ({
  queryKey: queryKeys.status,
  queryFn: fetchStatus,
  staleTime: STATUS_STALE_MS,
});

/* The deployment's public settings (/api/status). PageLayout fetches them
 * into the query cache under queryKeys.status; everything else reads that
 * entry here without fetching. Until the first answer arrives, the copy the
 * last visit stored stands in, without being written to the cache, so it
 * never delays that fetch. */
export function useStatus(): StatusState {
  const { data } = useQuery({
    ...statusQueryOptions(),
    enabled: false,
    placeholderData: () => readStoredStatus() as FetchedStatus,
  });
  return useMemo(() => ({ status: data as StatusPayload }), [data]);
}

/* The reducer-shaped API extensions still use: [{ status }, dispatch] with
 * 'set' and 'unset'. It reads and writes the same cache entry. */
export type StatusContextValue = [StatusState, React.Dispatch<StatusAction>];

const noopDispatch: React.Dispatch<StatusAction> = () => undefined;

export const StatusContext = React.createContext<StatusContextValue>([
  { status: undefined },
  noopDispatch,
]);

export const StatusProvider = ({ children }: { children: ReactNode }) => {
  const state = useStatus();
  const queryClient = useQueryClient();
  const dispatch = useCallback<React.Dispatch<StatusAction>>(
    (action) => {
      if (action.type === 'set')
        queryClient.setQueryData(queryKeys.status, action.payload);
      else if (action.type === 'unset')
        queryClient.removeQueries({ queryKey: queryKeys.status });
    },
    [queryClient],
  );
  const value = useMemo<StatusContextValue>(
    () => [state, dispatch],
    [state, dispatch],
  );
  return (
    <StatusContext.Provider value={value}>{children}</StatusContext.Provider>
  );
};
