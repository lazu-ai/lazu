import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { API } from '../../helpers/api';
import { showSuccess } from '../../helpers/utils';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { SearchCapability } from '../../constants/search.constants';

// Search backends reuse the existing channel model — these admin endpoints
// map to channel rows whose `type` is one of the search channel types (see
// search.constants.ts):
//   GET    /api/search/backends           -> { success, data: SearchBackendRecord[] }
//   POST   /api/search/backends           -> { success, data }
//   PUT    /api/search/backends/:id       -> { success, data }
//   DELETE /api/search/backends/:id       -> { success }
// Failures are real HTTP errors with a catalog code (docs/ERRORS.md).

export type SearchBackendRecord = {
  id?: string;
  name?: string;
  /** Provider id: tavily / exa / jina / serper / brave. */
  provider?: string;
  /** Channel type numeric id. */
  type?: number;
  api_key?: string;
  masked_key?: string;
  last_used_at?: number;
  base_url?: string;
  capabilities?: SearchCapability[];
  max_results?: number;
  timeout_ms?: number;
  priority?: number;
  status?: number;
  /** USD per search request. */
  search_price?: number;
  /** USD per fetched page. */
  fetch_price?: number;
  use_provider_cost?: boolean;
};

async function fetchSearchBackends(): Promise<SearchBackendRecord[]> {
  const data = unwrap<SearchBackendRecord[] | null>(
    await API.get('/api/search/backends', { skipErrorHandler: true }),
  );
  return Array.isArray(data) ? data : [];
}

export function useSearchBackendsAdmin() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  const listQuery = useQuery<SearchBackendRecord[], Error>({
    queryKey: queryKeys.console.table('search-backends', {}),
    queryFn: fetchSearchBackends,
    placeholderData: keepPreviousData,
    // The page renders the failure inline (ErrorState) instead of a toast.
    retry: false,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: queryKeys.console.table('search-backends', {}),
    });

  const saveMutation = useMutation<
    SearchBackendRecord | null,
    Error,
    SearchBackendRecord
  >({
    mutationFn: async (record) => {
      const isEdit = record.id !== undefined && record.id !== null;
      return unwrap<SearchBackendRecord | null>(
        isEdit
          ? await API.put(`/api/search/backends/${record.id}`, record, {
              skipErrorHandler: true,
            })
          : await API.post('/api/search/backends', record, {
              skipErrorHandler: true,
            }),
      );
    },
    onSuccess: () => {
      showSuccess(t('page.console.searchBackend.saved'));
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const deleteMutation = useMutation<unknown, Error, string>({
    mutationFn: async (id) =>
      unwrap<unknown>(
        await API.delete(`/api/search/backends/${id}`, {
          skipErrorHandler: true,
        }),
      ),
    onSuccess: () => {
      showSuccess(t('page.console.searchBackend.deleted'));
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const toggleMutation = useMutation<
    unknown,
    Error,
    { id: string; status: number }
  >({
    mutationFn: async ({ id, status }) =>
      unwrap<unknown>(
        await API.put(
          `/api/search/backends/${id}`,
          { id, status },
          { skipErrorHandler: true },
        ),
      ),
    onSuccess: () => invalidate(),
    onError: apiErrorHandler,
  });

  return {
    backends: listQuery.data || [],
    isLoading: listQuery.isLoading,
    error: listQuery.error,
    isFetching: listQuery.isFetching,
    refresh: invalidate,
    saveMutation,
    deleteMutation,
    toggleMutation,
    t,
  };
}
