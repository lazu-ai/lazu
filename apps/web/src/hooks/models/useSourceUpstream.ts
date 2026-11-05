import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { API } from '../../helpers/api';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

/** A source's upstream check, as /api/model_ops/sources/:id/upstream returns
 *  it (internal/controller/upstream_sync.go). */

type UpstreamOnNew = 'auto' | 'review';
type UpstreamOnGone = 'keep' | 'stop';

export type UpstreamSyncSettings = {
  ignore?: string[];
  interval_hours?: number;
  notify_ops?: boolean;
  on_gone: UpstreamOnGone;
  on_new: UpstreamOnNew;
};

export type UpstreamSyncLogEntry = {
  added?: string[];
  at: number;
  error?: string;
  gone?: string[];
  listed?: number;
  manual?: boolean;
  new?: string[];
  note?: string;
  stopped?: string[];
};

export type UpstreamSyncState = {
  checked_at?: number;
  error?: string;
  gone?: string[];
  listed_at?: number;
  log?: UpstreamSyncLogEntry[];
  new?: string[];
  stopped?: string[];
  upstream?: string[];
};

export type UpstreamSyncView = {
  next_check_at: number;
  requests_7d: Record<string, number>;
  settings: UpstreamSyncSettings;
  state: UpstreamSyncState | null;
};

/** What the source list shows of it; absent while the check is off and has
 *  never run. */
export type UpstreamSyncSummary = {
  checked_at?: number;
  error?: string;
  gone: number;
  interval_hours: number;
  new: number;
};

const upstreamKey = (id: string) =>
  queryKeys.console.detail('model-ops-source-upstream', id);

export function useSourceUpstreamQuery(id: string | null | undefined) {
  return useQuery({
    enabled: Boolean(id),
    queryKey: upstreamKey(id || ''),
    queryFn: async (): Promise<UpstreamSyncView> =>
      unwrap<UpstreamSyncView>(
        await API.get(`/api/model_ops/sources/${id}/upstream`, {
          skipErrorHandler: true,
        }),
      ),
  });
}

export function useSourceUpstreamMutations(id: string | null | undefined) {
  const queryClient = useQueryClient();
  const settle = (view: UpstreamSyncView) => {
    if (id) queryClient.setQueryData(upstreamKey(id), view);
    void queryClient.invalidateQueries({
      queryKey: [...queryKeys.console.root, 'model-ops-sources'],
    });
    void queryClient.invalidateQueries({
      queryKey: queryKeys.console.detail('channels', id || ''),
    });
  };
  const check = useMutation({
    mutationFn: async () =>
      unwrap<UpstreamSyncView>(
        await API.post(`/api/model_ops/sources/${id}/upstream/check`),
      ),
    onError: apiErrorHandler,
    onSuccess: settle,
  });
  const resolve = useMutation({
    mutationFn: async (body: { gone?: string[]; new?: string[] }) =>
      unwrap<UpstreamSyncView>(
        await API.post(`/api/model_ops/sources/${id}/upstream/resolve`, body),
      ),
    onError: apiErrorHandler,
    onSuccess: settle,
  });
  return { check, resolve };
}
