import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { notify } from '../../components/ui/toast';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { AxiosResponse } from 'axios';
import type { TFunction } from 'i18next';

/** Model facts from models.dev, the 资料更新 review queue, and the maker order
 *  every catalog reads — see internal/controller/model_info.go. */

export type ModelInfoReviewKind =
  | 'diff'
  | 'filled'
  | 'first_run'
  | 'new_release'
  | 'tier_gap'
  | 'price_followed';

export type ModelInfoReview = {
  created_time: number;
  field: string;
  id: number;
  kind: ModelInfoReviewKind;
  local_value: string;
  maker: string;
  /** Catalog id of the model; 0 for a release not registered yet. */
  model_id: number;
  model_name: string;
  /** diff: a hint code (release_moved, limit_changed, price_changed);
   *  first_run: days apart; tier_gap: the threshold; new_release: lab|channel. */
  note: string;
  payload?: string;
  status: string;
  sync_run: number;
  upstream_value: string;
};

export type ModelInfoReviews = {
  missing_release_dates?: {
    model_id: number;
    model_name: string;
    maker: string;
  }[];
  items: ModelInfoReview[];
  last_run_at: number;
  pending_count: number;
};

export type ModelMaker = {
  key: string;
  label: string;
  models: number;
  rank: number | null;
};

export type ModelInfoPrefill = {
  cache_read_per_mtok?: number;
  cache_write_per_mtok?: number;
  capabilities?: Record<string, boolean>;
  context_length?: number;
  context_tier?: {
    cache_read_per_mtok?: number;
    cache_write_5m_per_mtok?: number;
    input_per_mtok?: number;
    output_per_mtok?: number;
    threshold: number;
  } | null;
  description?: string;
  filled: string[];
  input_limit?: number;
  knowledge_cutoff?: string;
  maker?: string;
  max_output_tokens?: number;
  modalities?: { input?: string[]; output?: string[] };
  model_name: string;
  models_dev_id?: string;
  name?: string;
  official_input_per_mtok?: number;
  official_output_per_mtok?: number;
  open_weights?: boolean;
  reasoning_options?: { efforts?: string[]; toggle?: boolean };
  release_date?: string;
  unsupported_params?: string[];
};

const readData = <T>(response: AxiosResponse<unknown>, fallback: T): T =>
  unwrap<T>(response) ?? fallback;

const assertSuccess = (response: AxiosResponse<unknown>) => {
  unwrap(response);
};

export function useModelInfoReviewsQuery(enabled = true) {
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('model-info-reviews', {}),
    queryFn: async (): Promise<ModelInfoReviews> => {
      const response = await API.get('/api/model_info/reviews');
      return readData<ModelInfoReviews>(response, {
        items: [],
        last_run_at: 0,
        pending_count: 0,
      });
    },
    staleTime: 30 * 1000,
  });
}

export function useModelMakersQuery(enabled = true) {
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('model-makers', {}),
    queryFn: async (): Promise<{ makers: ModelMaker[]; order: string[] }> => {
      const response = await API.get('/api/model_info/makers');
      return readData(response, { makers: [], order: [] });
    },
    staleTime: 60 * 1000,
  });
}

/** Registration defaults for a model id, from models.dev. */
export async function lookupModelInfo(
  modelName: string,
): Promise<{ found: boolean; prefill: ModelInfoPrefill }> {
  const response = await API.get(
    `/api/model_info/lookup?model=${encodeURIComponent(modelName)}`,
    { skipErrorHandler: true },
  );
  return readData(response, {
    found: false,
    prefill: { filled: [], model_name: modelName },
  });
}

export function useModelInfoMutations(t: TFunction) {
  const queryClient = useQueryClient();
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: queryKeys.console.root });
  const ns = 'page.console.modelOps.updates';

  const resolveMutation = useMutation({
    mutationFn: async (variables: {
      action: 'accept' | 'keep' | 'undo';
      id: number;
    }) => {
      const response = await API.post(
        `/api/model_info/reviews/${variables.id}/${variables.action}`,
      );
      assertSuccess(response);
    },
    onSuccess: invalidate,
    onError: (error) => {
      showError(error);
      void invalidate();
    },
  });

  const acceptFirstRunMutation = useMutation({
    mutationFn: async (ids: number[]) => {
      const response = await API.post(
        '/api/model_info/reviews/first_run/accept',
        { ids },
      );
      assertSuccess(response);
      return readData<{ applied: number }>(response, { applied: 0 });
    },
    onSuccess: (data) => {
      notify.success(
        t(`${ns}.firstRunApplied`, {
          count: data.applied,
          defaultValue: '已采用 {{count}} 个发布日期',
        }),
      );
      invalidate();
    },
    onError: (error) => {
      showError(error);
      void invalidate();
    },
  });

  const syncMutation = useMutation({
    mutationFn: async () => {
      const response = await API.post('/api/model_info/sync');
      assertSuccess(response);
    },
    onSuccess: () => {
      notify.success(
        t(`${ns}.synced`, { defaultValue: '已从 models.dev 同步' }),
      );
      invalidate();
    },
    onError: (error) => {
      showError(error);
      void invalidate();
    },
  });

  const makerOrderMutation = useMutation({
    mutationFn: async (order: string[]) => {
      const response = await API.put('/api/model_info/makers', { order });
      assertSuccess(response);
    },
    onSuccess: () => {
      notify.success(
        t('page.console.modelOps.makers.saved', {
          defaultValue: '厂商顺序已保存',
        }),
      );
      invalidate();
    },
    onError: (error) => {
      showError(error);
      void invalidate();
    },
  });

  return {
    acceptFirstRunMutation,
    makerOrderMutation,
    resolveMutation,
    syncMutation,
  };
}
