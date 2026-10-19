import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { API } from '../../helpers/api';
import { showSuccess } from '../../helpers/utils';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { TFunction } from 'i18next';

/** 线路 — the lanes a model is sold on.
 *
 *  Lanes used to be a `direct | cheap` union in the client and a pair of
 *  constants in Go. They arrive from the server now, so nothing in the UI may
 *  assume a count or a name. See /api/model_ops/lanes. (Spend tiers, the
 *  other axis of the old 折扣 matrix, were replaced by 专属折扣 price plans.) */

export type ChannelLane = {
  slug: string;
  name: string;
  note?: string;
  color?: string;
  enabled: boolean;
  builtin: boolean;
  /** A lane that has ever carried traffic can be stopped but never deleted:
   *  the billing history and sell prices written against it must keep
   *  resolving. Deletion is only for one created by mistake. */
  ever_enabled: boolean;
  sort: number;
  /** 这档现在的样子 — what the panel reports before you stop it. */
  sources: number;
  priced: number;
  /** Default sell price, percent of the official price. */
  price_ratio: number;
  /** Models whose price on this lane follows the official price / is fixed. */
  following: number;
  fixed: number;
};

const listOf = <T,>(payload: unknown): T[] => {
  const items = (payload as { items?: unknown })?.items;
  return Array.isArray(items) ? (items as T[]) : [];
};

export function useChannelLanesQuery() {
  return useQuery({
    queryKey: queryKeys.console.table('model-ops-lanes', {}),
    queryFn: async (): Promise<ChannelLane[]> => {
      const response = await API.get('/api/model_ops/lanes');
      return listOf<ChannelLane>(unwrap<unknown>(response));
    },
    staleTime: 60 * 1000,
  });
}

/* A failed write rejects in axios and the response interceptor reports it
 * once, by its code. The mutations here only read the saved record back. */

export function useModelOpsDimensionMutations(t: TFunction) {
  const queryClient = useQueryClient();
  const invalidate = (...names: string[]) => {
    names.forEach((name) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.console.table(name, {}),
        exact: false,
      });
    });
  };
  const invalidateAll = () =>
    invalidate('model-ops-lanes', 'model-ops-sell-prices');

  const saveLane = useMutation({
    mutationFn: async (lane: Partial<ChannelLane> & { slug: string }) => {
      const existing = Boolean(lane.builtin || lane.ever_enabled || lane.sort);
      const path = existing
        ? `/api/model_ops/lanes/${encodeURIComponent(lane.slug)}`
        : '/api/model_ops/lanes';
      const response = existing
        ? await API.put(path, lane)
        : await API.post(path, lane);
      return unwrap<ChannelLane>(response);
    },
    onSuccess: () => {
      showSuccess(t('common.saved'));
      // A new ratio reprices every model that follows it.
      void queryClient.invalidateQueries({ queryKey: ['console'] });
    },
    onError: apiErrorHandler,
  });

  const setLaneEnabled = useMutation({
    mutationFn: async ({
      slug,
      enabled,
    }: {
      slug: string;
      enabled: boolean;
    }) => {
      const response = await API.put(
        `/api/model_ops/lanes/${encodeURIComponent(slug)}/enabled`,
        { enabled },
      );
      return unwrap<ChannelLane>(response);
    },
    onSuccess: () => invalidateAll(),
    onError: apiErrorHandler,
  });

  const deleteLane = useMutation({
    mutationFn: async (slug: string) => {
      const response = await API.delete(
        `/api/model_ops/lanes/${encodeURIComponent(slug)}`,
      );
      unwrap<unknown>(response);
      return slug;
    },
    onSuccess: () => invalidateAll(),
    onError: apiErrorHandler,
  });

  return {
    deleteLane,
    saveLane,
    setLaneEnabled,
  };
}
