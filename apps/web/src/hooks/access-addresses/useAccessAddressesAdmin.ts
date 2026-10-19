import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { API } from '../../helpers/api';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

export type AccessAddressKind = 'global' | 'cn_direct';

export type AdminAccessAddress = {
  kind: AccessAddressKind;
  url: string;
  user_count: number;
  user_ids: number[];
};

const KEY = queryKeys.console.detail('access-addresses', 'admin');

const normalize = (data: unknown): AdminAccessAddress[] =>
  Array.isArray(data)
    ? data.flatMap((item) => {
        if (typeof item !== 'object' || item === null) return [];
        const record = item as Record<string, unknown>;
        if (typeof record.url !== 'string') return [];
        const ids = Array.isArray(record.user_ids)
          ? record.user_ids.map(Number).filter(Number.isFinite)
          : [];
        return [
          {
            url: record.url,
            kind: record.kind === 'cn_direct' ? 'cn_direct' : 'global',
            user_ids: ids,
            user_count: ids.length,
          },
        ];
      })
    : [];

/** The extra API addresses and the accounts each is opened to. */
export function useAccessAddressesAdmin(enabled = true) {
  return useQuery({
    queryKey: KEY,
    queryFn: async () =>
      normalize(unwrap<unknown>(await API.get('/api/access-address/'))),
    enabled,
  });
}

export function useSaveAccessAddresses() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (addresses: { kind: AccessAddressKind; url: string }[]) =>
      normalize(
        unwrap<unknown>(await API.put('/api/access-address/', { addresses })),
      ),
    onSuccess: (data) => client.setQueryData(KEY, data),
    onError: apiErrorHandler,
  });
}

export function useSetAccessAddressGrant() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (input: {
      enabled: boolean;
      url: string;
      userId: number | string;
    }) =>
      normalize(
        unwrap<unknown>(
          await API.put('/api/access-address/grant', {
            url: input.url,
            user_id: String(input.userId),
            enabled: input.enabled,
          }),
        ),
      ),
    onSuccess: (data) => client.setQueryData(KEY, data),
    onError: apiErrorHandler,
  });
}
