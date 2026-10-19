/* 专属折扣 reads and writes. All of /api/price-plans is root-only, so every
 * query here takes an `enabled` flag and callers pass isRoot(). */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { API } from '../../helpers';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type {
  PricePlan,
  PricePlanMember,
  PricingRow,
  PricingVendor,
  payloadFromDraft,
} from './pricePlans';

const LIST_KEY = queryKeys.console.table('price-plans', {});
const detailKey = (id: number) => queryKeys.console.detail('price-plan', id);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);

const toPlan = (raw: unknown): PricePlan => {
  const record = isRecord(raw) ? raw : {};
  const rules = Array.isArray(record.rules) ? record.rules : [];
  const lanes = String(record.lanes || 'direct');
  return {
    active: record.active !== false,
    default_pct:
      record.default_pct == null ? null : Number(record.default_pct) || null,
    ends_at: typeof record.ends_at === 'string' ? record.ends_at : null,
    id: Number(record.id) || 0,
    lanes: lanes === 'all' || lanes === 'cheap' ? lanes : 'direct',
    member_count: Number(record.member_count) || 0,
    name: String(record.name || ''),
    note: String(record.note || ''),
    rules: rules.filter(isRecord).map((rule) => ({
      id: Number(rule.id) || undefined,
      kind: rule.kind === 'model' ? 'model' : 'vendor',
      model_name: String(rule.model_name || ''),
      pct: Number(rule.pct) || 0,
      vendor_id: String(rule.vendor_id || ''),
      vendor_name: String(rule.vendor_name || ''),
    })),
    starts_at: typeof record.starts_at === 'string' ? record.starts_at : null,
  };
};

export function usePricePlansQuery(enabled: boolean) {
  return useQuery({
    enabled,
    queryKey: LIST_KEY,
    queryFn: async (): Promise<PricePlan[]> => {
      const data = unwrap<unknown>(await API.get('/api/price-plans'));
      if (!Array.isArray(data)) throw new Error('Invalid price plan list');
      return data.map(toPlan);
    },
  });
}

export function usePricePlanQuery(id: number | null) {
  return useQuery({
    enabled: id != null && id > 0,
    queryKey: detailKey(id ?? 0),
    queryFn: async (): Promise<{
      members: PricePlanMember[];
      plan: PricePlan;
    }> => {
      const data = unwrap<unknown>(await API.get(`/api/price-plans/${id}`));
      if (!isRecord(data)) throw new Error('Invalid price plan');
      const members = Array.isArray(data.members) ? data.members : [];
      return {
        members: members.filter(isRecord).map((member) => ({
          display_name: String(member.display_name || ''),
          email: String(member.email || ''),
          id: Number(member.id) || 0,
          public_id: String(member.public_id || ''),
          username: String(member.username || ''),
        })),
        plan: toPlan(data.plan),
      };
    },
  });
}

/** The public catalog: rows for the model picker and the preview, vendors for
 *  the vendor picker. Same source as the model plaza, so a plan can only name
 *  what a customer can actually buy. */
export function usePricingCatalogQuery(enabled: boolean) {
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('price-plan-catalog', {}),
    staleTime: 60 * 1000,
    queryFn: async (): Promise<{
      rows: PricingRow[];
      vendors: PricingVendor[];
    }> => {
      const response = await API.get('/api/pricing', {
        skipErrorHandler: true,
      });
      const rows = unwrap<unknown>(response);
      const body = response.data as { vendors?: unknown } | undefined;
      if (!Array.isArray(rows)) throw new Error('Invalid pricing response');
      const vendors = Array.isArray(body?.vendors) ? body.vendors : [];
      return {
        rows: rows.filter(isRecord).map((row) => ({
          input_per_mtok:
            row.input_per_mtok == null ? null : Number(row.input_per_mtok),
          lanes: Array.isArray(row.lanes)
            ? row.lanes.filter(isRecord).map((lane) => ({
                channel_group: String(lane.channel_group || ''),
                input_per_mtok:
                  lane.input_per_mtok == null
                    ? null
                    : Number(lane.input_per_mtok),
              }))
            : [],
          model_name: String(row.model_name || ''),
          official_input_price:
            row.official_input_price == null
              ? null
              : Number(row.official_input_price),
          vendor_id: row.vendor_id ? String(row.vendor_id) : undefined,
        })),
        vendors: vendors.filter(isRecord).map((vendor) => ({
          id: String(vendor.id || ''),
          name: String(vendor.name || vendor.id || ''),
        })),
      };
    },
  });
}

type PlanPayload = ReturnType<typeof payloadFromDraft>;

export function usePricePlanMutations() {
  const queryClient = useQueryClient();
  const refresh = (id?: number) => {
    void queryClient.invalidateQueries({ queryKey: LIST_KEY });
    if (id) void queryClient.invalidateQueries({ queryKey: detailKey(id) });
    // Account rows and the drawer show the plan by name.
    void queryClient.invalidateQueries({ queryKey: ['organizations'] });
    void queryClient.invalidateQueries({
      queryKey: ['organizations-summary'],
    });
  };

  const save = useMutation({
    mutationFn: async ({
      id,
      payload,
    }: {
      id: number | null;
      payload: PlanPayload;
    }) => {
      const response = id
        ? await API.put(`/api/price-plans/${id}`, payload)
        : await API.post('/api/price-plans', payload);
      return toPlan(unwrap<unknown>(response));
    },
    onSuccess: (plan) => refresh(plan.id),
    onError: apiErrorHandler,
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      unwrap(await API.delete(`/api/price-plans/${id}`));
      return id;
    },
    onSuccess: () => refresh(),
    onError: apiErrorHandler,
  });

  const addMember = useMutation({
    mutationFn: async ({ id, user }: { id: number; user: string }) => {
      unwrap(await API.post(`/api/price-plans/${id}/members`, { user }));
      return id;
    },
    onSuccess: (id) => refresh(id),
    onError: apiErrorHandler,
  });

  const removeMember = useMutation({
    mutationFn: async ({ id, user }: { id: number; user: string }) => {
      unwrap(
        await API.delete(
          `/api/price-plans/${id}/members/${encodeURIComponent(user)}`,
        ),
      );
      return id;
    },
    onSuccess: (id) => refresh(id),
    onError: apiErrorHandler,
  });

  return { addMember, remove, removeMember, save };
}
