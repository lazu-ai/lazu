import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { trimFilterValues } from '../../components/ui/filter-bar';
import {
  coerceItemsPayload,
  createSearchParams,
} from '../../components/ui/table-tools';
import { ITEMS_PER_PAGE } from '../../constants/common.constant';
import { API } from '../../helpers/api';
import { asMicroUSD, type MicroUSD } from '../../helpers/money';
import { microUSDToUSD, usdToMicroUSD } from '../../helpers/render';
import {
  copy,
  showError,
  showSuccess,
  timestamp2string,
} from '../../helpers/utils';
import { apiErrorHandler, errorMessage, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import { chatTemplateLeaksKey } from '../../pages/Token/chatTemplate';
import type { RateLimitCeiling } from '../../constants/rate-limits';

const emptyFilters = {
  keyword: '',
  token: '',
};

const emptySort = {
  id: 'created_time',
  desc: true,
};

type TokenId = number | string;

type ApiRecord = Record<string, unknown>;

type ApiEnvelope<TData = unknown> = ApiRecord & {
  data?: TData;
  message?: string;
  success?: boolean;
};

type TokenFilters = typeof emptyFilters;

type TokenSort = {
  desc: boolean;
  id: string;
};

export type TokenRecord = ApiRecord & {
  id?: TokenId;
  key?: string;
  masked_key?: string;
  can_reveal_key?: boolean;
  product?: string;
  name?: string;
  remain_quota?: MicroUSD;
  unlimited_quota?: boolean;
  used_quota?: MicroUSD;
};

type TokenPageData = {
  items: TokenRecord[];
  meta: unknown;
  page: number;
  pageSize: number;
  total: number;
};

/** One priced lane of a model: its rate, and whether it can serve anyone
 *  today. `clientOnly` names the single client the lane's sources accept
 *  ("codex"), which is why a lane can be live and still unusable from another
 *  tool. */
export type TokenModelLaneFact = {
  available: boolean;
  clientOnly?: string;
  inputPerMtok?: number;
  key: 'cheap' | 'direct';
  outputPerMtok?: number;
};

/** One catalog row, as the model-scope picker needs it. */
export type TokenModelFact = {
  /** Protocols this model answers on (openai, anthropic, gemini, embeddings…),
   *  straight from the pricing catalog. An empty list means the deployment
   *  did not say, and the model is treated as usable everywhere. */
  endpointTypes: string[];
  inputPerMtok?: number;
  /** The lab behind the model and its rank in the operator's maker order. */
  maker?: string;
  makerRank?: number;
  model: string;
  /** What the model emits. An image, video or audio model is not something a
   *  chat client or coding agent can be pointed at. */
  modalityOutput?: string[];
  /** The lanes this model is sold on, with what each one can serve right now.
   *  A lane missing here is a lane the model was never priced for. */
  lanes?: TokenModelLaneFact[];
  outputPerMtok?: number;
  /** Unix seconds. The connect dialog orders each vendor's models by it. */
  releasedAt?: number;
  vendorID?: string;
};

type TokenVendorOptions = {
  models: TokenModelFact[];
  rateLimitCeiling?: RateLimitCeiling | null;
  vendors: Array<ApiRecord & { id?: TokenId }>;
};

export type TokenFormValues = ApiRecord & {
  allow_ips?: string;
  expired_time?: Date | null | number | string;
  expires_never?: boolean;
  ip_blacklist?: string;
  ip_blacklist_enabled?: boolean;
  ip_whitelist_enabled?: boolean;
  model_limits?: string[] | string;
  model_limits_enabled?: boolean;
  metadata?: string;
  model_lanes?: Record<string, 'direct' | 'cheap'>;
  product?: string;
  rate_limits_enabled?: boolean;
  remain_quota?: number | string;
  rpm_limit?: number | string;
  tokenCount?: number | string;
  tpm_limit?: number | string;
  unlimited_quota?: boolean;
  vendor_groups?: Record<string, 'direct' | 'cheap'>;
  vendor_limits?: Array<number | string>;
  vendor_limits_enabled?: boolean;
  search_limits_enabled?: boolean;
  search_providers?: string[];
  search_domain_allowlist?: string;
  /** Vendors the Key is scoped to wholesale, beside the individually named
   *  models in model_limits. */
  model_scope_vendors?: Array<number | string>;
  usage_alert_percent?: number | string;
  note?: string;
  /** Highest reasoning effort the Key may use; "" = no cap of its own. */
  reasoning_effort_cap?: string;
  decision_question_generation?: { enabled: boolean; model?: string };
  question_generation_enabled?: boolean;
  question_generation_model?: string;
  /** Flattened from the wire's period_cap object for the form's benefit. */
  period_cap_period?: '' | 'day' | 'month';
  period_cap_usd?: number | string;
  period_cap_spent_usd?: number;
};

type ManageTokenAction = 'delete' | 'disable' | 'enable';

type ManageTokenVariables = {
  action: ManageTokenAction;
  id: TokenId;
};

export type SaveTokenVariables = {
  editingToken?: TokenRecord | null;
  values: TokenFormValues;
};

type ChatTemplate = {
  value?: string;
};

/** A Key the create flow just made. The full value is only in memory. */
export type CreatedSecret = {
  id?: TokenId;
  key: string;
  name: string;
  token?: TokenRecord;
};

type UseTokensQueryOptions = (key?: string) => void;

const randomSuffix = () => {
  const characters =
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  let result = '';
  for (let index = 0; index < 6; index += 1) {
    result += characters.charAt(Math.floor(Math.random() * characters.length));
  }
  return result;
};

const toOption = (value: unknown) => ({
  label: String(value),
  value: String(value),
});

const isRecord = (value: unknown): value is ApiRecord =>
  value !== null && typeof value === 'object';

const normalizeTokenRecord = (data: ApiRecord): TokenRecord => ({
  ...data,
  key:
    data.key === undefined || data.key === null ? undefined : String(data.key),
  masked_key:
    data.masked_key === undefined || data.masked_key === null
      ? undefined
      : String(data.masked_key),
  name:
    data.name === undefined || data.name === null
      ? undefined
      : String(data.name),
  product:
    data.product === undefined || data.product === null
      ? undefined
      : String(data.product),
  remain_quota: asMicroUSD(data.remain_quota),
  unlimited_quota: Boolean(data.unlimited_quota),
  used_quota: asMicroUSD(data.used_quota),
});

const formatTokenKey = (key: unknown) => {
  const value = String(key || '').trim();
  if (!value) return '';
  return value.startsWith('sk-') ? value : `sk-${value}`;
};

// An empty note stays an empty field: the old '{}' placeholder looked like
// the form had put something there.
function formatTokenMetadata(value: unknown): string {
  if (value === undefined || value === null || value === '') {
    return '';
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) {
      return '{}';
    }
    try {
      const parsed = JSON.parse(trimmed);
      return JSON.stringify(parsed, null, 2);
    } catch {
      return trimmed;
    }
  }
  return JSON.stringify(value, null, 2);
}

function parseTokenMetadata(value: unknown): ApiRecord {
  const raw = String(value ?? '').trim();
  if (!raw) {
    return {};
  }
  const parsed = JSON.parse(raw);
  if (!isRecord(parsed) || Array.isArray(parsed)) {
    throw new Error('Token metadata must be a JSON object');
  }
  return parsed;
}

export const normalizeTokenDetail = (
  data: TokenFormValues = {},
): TokenFormValues => {
  const modelLimits =
    typeof data.model_limits === 'string' && data.model_limits
      ? data.model_limits.split(',').filter(Boolean)
      : Array.isArray(data.model_limits)
        ? data.model_limits
        : [];
  const rawExpiredTime = Number(data.expired_time);
  const expiredTime =
    rawExpiredTime && rawExpiredTime !== -1
      ? new Date(rawExpiredTime * 1000)
      : null;

  return {
    ...data,
    allow_ips: data.allow_ips || '',
    expired_time: expiredTime,
    expires_never: !expiredTime,
    ip_blacklist: data.ip_blacklist || '',
    ip_blacklist_enabled: Boolean(data.ip_blacklist_enabled),
    ip_whitelist_enabled: Boolean(
      data.ip_whitelist_enabled ?? Boolean(data.allow_ips),
    ),
    model_limits: modelLimits,
    model_limits_enabled: Boolean(
      data.model_limits_enabled &&
      (modelLimits.length ||
        (Array.isArray(data.model_scope_vendors) &&
          data.model_scope_vendors.length)),
    ),
    metadata: formatTokenMetadata(data.metadata),
    product: String(data.product || ''),
    rate_limits_enabled: Boolean(
      data.rate_limits_enabled &&
      (Number(data.rpm_limit) > 0 || Number(data.tpm_limit) > 0),
    ),
    rpm_limit: Math.max(0, Math.trunc(Number(data.rpm_limit) || 0)),
    tpm_limit: Math.max(0, Math.trunc(Number(data.tpm_limit) || 0)),
    remain_quota: microUSDToUSD(asMicroUSD(data.remain_quota)),
    tokenCount: 1,
    unlimited_quota: Boolean(data.unlimited_quota),
    model_lanes: normalizeVendorGroups(data.model_lanes),
    vendor_groups: normalizeVendorGroups(data.vendor_groups),
    vendor_limits: Array.isArray(data.vendor_limits)
      ? data.vendor_limits.map(String)
      : [],
    vendor_limits_enabled: Boolean(
      data.vendor_limits_enabled &&
      Array.isArray(data.vendor_limits) &&
      data.vendor_limits.length,
    ),
    search_limits_enabled: Boolean(data.search_limits_enabled),
    search_providers: Array.isArray(data.search_providers)
      ? data.search_providers.map(String)
      : [],
    search_domain_allowlist: String(data.search_domain_allowlist || ''),
    model_scope_vendors: Array.isArray(data.model_scope_vendors)
      ? data.model_scope_vendors.map(String)
      : [],
    usage_alert_percent: Math.max(
      0,
      Math.min(100, Math.trunc(Number(data.usage_alert_percent) || 0)),
    ),
    note: String(data.note || ''),
    reasoning_effort_cap: String(data.reasoning_effort_cap || ''),
    question_generation_enabled:
      data.decision_question_generation?.enabled === true,
    question_generation_model: String(
      data.decision_question_generation?.model || '',
    ),
    period_cap_period: readPeriodCapPeriod(data.period_cap),
    period_cap_usd: readPeriodCapLimit(data.period_cap),
    period_cap_spent_usd: readPeriodCapSpent(data.period_cap),
  };
};

/* The cap comes back as its own object because it is stored as a spend budget
   rather than as columns on the Key; the form is flatter than the wire. */
function readPeriodCapPeriod(value: unknown): '' | 'day' | 'month' {
  if (!isRecord(value)) return '';
  const period = String(value.period || '');
  return period === 'day' || period === 'month' ? period : '';
}

function readPeriodCapLimit(value: unknown): number {
  if (!isRecord(value)) return 0;
  const limit = Number(value.limit_usd);
  return Number.isFinite(limit) && limit > 0 ? limit : 0;
}

function readPeriodCapSpent(value: unknown): number {
  if (!isRecord(value)) return 0;
  const spent = Number(value.spent_usd);
  return Number.isFinite(spent) && spent > 0 ? spent : 0;
}

function normalizeVendorGroups(
  value: unknown,
): Record<string, 'direct' | 'cheap'> {
  if (!isRecord(value)) {
    return {};
  }
  const result: Record<string, 'direct' | 'cheap'> = {};
  Object.entries(value).forEach(([vendorId, lane]) => {
    if (lane === 'cheap') {
      result[vendorId] = 'cheap';
    }
  });
  return result;
}

function normalizeRateLimitCeiling(value: unknown): RateLimitCeiling | null {
  if (!isRecord(value)) {
    return null;
  }
  const rpm = Number(value.rpm);
  const tpm = Number(value.tpm);
  if (!Number.isFinite(rpm) || !Number.isFinite(tpm)) {
    return null;
  }
  return {
    rpm: Math.max(0, Math.trunc(rpm)),
    tpm: Math.max(0, Math.trunc(tpm)),
  };
}

export const normalizeTokenPayload = (values: TokenFormValues): ApiRecord => {
  const payload: ApiRecord = {
    ...values,
    ip_blacklist_enabled: Boolean(values.ip_blacklist_enabled),
    ip_whitelist_enabled: Boolean(values.ip_whitelist_enabled),
    remain_quota: usdToMicroUSD(Number(values.remain_quota) || 0),
    unlimited_quota: Boolean(values.unlimited_quota),
  };

  if (values.expires_never || !values.expired_time) {
    payload.expired_time = -1;
  } else {
    const time = new Date(values.expired_time).getTime();
    payload.expired_time = Number.isNaN(time) ? -1 : Math.ceil(time / 1000);
  }

  const modelLimits = Array.isArray(values.model_limits)
    ? values.model_limits
    : [];
  payload.model_limits_enabled =
    Boolean(values.model_limits_enabled) && modelLimits.length > 0;
  payload.model_limits = payload.model_limits_enabled
    ? modelLimits.join(',')
    : '';

  const vendorLimits = (values.vendor_limits || [])
    .map((id) => String(id).trim())
    .filter(Boolean);
  payload.vendor_limits_enabled =
    Boolean(values.vendor_limits_enabled) && vendorLimits.length > 0;
  payload.vendor_limits = payload.vendor_limits_enabled ? vendorLimits : [];
  payload.vendor_groups = normalizeVendorGroups(values.vendor_groups);
  payload.model_lanes = normalizeVendorGroups(values.model_lanes);
  const searchProviders = (values.search_providers || [])
    .map((id) => String(id).trim())
    .filter(Boolean);
  payload.search_limits_enabled = Boolean(values.search_limits_enabled);
  payload.search_providers = payload.search_limits_enabled
    ? searchProviders
    : [];
  payload.search_domain_allowlist = String(
    values.search_domain_allowlist || '',
  ).trim();
  payload.product = String(values.product || '').trim();
  payload.metadata = parseTokenMetadata(values.metadata);

  const rpmLimit = Math.max(0, Math.trunc(Number(values.rpm_limit) || 0));
  const tpmLimit = Math.max(0, Math.trunc(Number(values.tpm_limit) || 0));
  payload.rate_limits_enabled =
    Boolean(values.rate_limits_enabled) && (rpmLimit > 0 || tpmLimit > 0);
  payload.rpm_limit = payload.rate_limits_enabled ? rpmLimit : 0;
  payload.tpm_limit = payload.rate_limits_enabled ? tpmLimit : 0;

  const scopeVendors = (values.model_scope_vendors || [])
    .map((id) => String(id).trim())
    .filter(Boolean);
  // Model scope is on when it narrows anything at all — a named vendor counts
  // as much as a named model, or a Key scoped to "everything Anthropic ships"
  // would silently mean "everything".
  payload.model_limits_enabled =
    Boolean(values.model_limits_enabled) &&
    (modelLimits.length > 0 || scopeVendors.length > 0);
  payload.model_limits = payload.model_limits_enabled
    ? modelLimits.join(',')
    : '';
  payload.model_scope_vendors = payload.model_limits_enabled
    ? scopeVendors
    : [];
  payload.usage_alert_percent = Math.max(
    0,
    Math.min(100, Math.trunc(Number(values.usage_alert_percent) || 0)),
  );
  payload.note = String(values.note || '').trim();
  payload.reasoning_effort_cap = String(values.reasoning_effort_cap || '');
  payload.decision_question_generation = {
    enabled: values.question_generation_enabled === true,
    model: String(values.question_generation_model || '').trim(),
  };
  delete payload.question_generation_enabled;
  delete payload.question_generation_model;
  const capUSD = Number(values.period_cap_usd) || 0;
  const capPeriod = String(values.period_cap_period || '');
  payload.period_cap =
    capUSD > 0 && (capPeriod === 'day' || capPeriod === 'month')
      ? { period: capPeriod, limit_usd: capUSD }
      : null;

  delete payload.expires_never;
  delete payload.tokenCount;
  delete payload.period_cap_period;
  delete payload.period_cap_usd;
  delete payload.period_cap_spent_usd;
  return payload;
};

const sortItems = (items: TokenRecord[], sort: TokenSort): TokenRecord[] => {
  if (!sort?.id) {
    return items;
  }
  return [...items].sort((a, b) => {
    const left = a?.[sort.id];
    const right = b?.[sort.id];
    const result = String(left ?? '').localeCompare(
      String(right ?? ''),
      undefined,
      {
        numeric: true,
      },
    );
    return sort.desc ? -result : result;
  });
};

async function fetchTokens({
  filters,
  page,
  pageSize,
  sort,
  status,
}: {
  filters: TokenFilters;
  page: number;
  pageSize: number;
  sort: TokenSort;
  status: string;
}): Promise<TokenPageData> {
  const hasSearch = Boolean(filters.keyword || filters.token);
  if (hasSearch) {
    const query = createSearchParams({
      keyword: filters.keyword,
      status: status === 'all' ? undefined : status,
      token: filters.token,
    });
    const found = unwrap<unknown>(await API.get(`/api/token/search?${query}`));
    const allItems = sortItems(
      Array.isArray(found)
        ? found.filter(isRecord).map(normalizeTokenRecord)
        : [],
      sort,
    );
    const start = (page - 1) * pageSize;
    return {
      items: allItems.slice(start, start + pageSize),
      page,
      pageSize,
      total: allItems.length,
      meta: {},
    };
  }

  const query = createSearchParams({
    p: page,
    size: pageSize,
    status: status === 'all' ? undefined : status,
  });
  const pageData = coerceItemsPayload<ApiRecord>(
    unwrap<unknown>(await API.get(`/api/token/?${query}`)),
    page,
    pageSize,
  );
  return {
    ...pageData,
    items: sortItems(
      pageData.items.filter(isRecord).map(normalizeTokenRecord),
      sort,
    ),
  };
}

/** Every Key this viewer can see, walked page by page. The list endpoint caps
 *  a page at 100, and a figure summed over one page would silently shrink as
 *  the account grows. Twenty pages is far past the per-member Key limit; the
 *  cap only guards against a server that keeps reporting more. */
export async function fetchAllTokenRecords(): Promise<TokenRecord[]> {
  const size = 100;
  const records: TokenRecord[] = [];
  for (let page = 1; page <= 20; page += 1) {
    const query = createSearchParams({ p: page, size });
    const pageData = coerceItemsPayload<ApiRecord>(
      unwrap<unknown>(await API.get(`/api/token/?${query}`)),
      page,
      size,
    );
    const items = pageData.items.filter(isRecord).map(normalizeTokenRecord);
    records.push(...items);
    if (items.length < size || records.length >= pageData.total) break;
  }
  return records;
}

async function fetchTokenModels(): Promise<string[]> {
  const models = unwrap<unknown>(await API.get('/api/user/models'));
  return Array.isArray(models) ? models.map(String) : [];
}

function normalizeModelLaneFacts(value: unknown): TokenModelLaneFact[] {
  if (!Array.isArray(value)) return [];
  const rate = (raw: unknown) =>
    raw != null && Number.isFinite(Number(raw)) ? Number(raw) : undefined;
  const lanes: TokenModelLaneFact[] = [];
  value.filter(isRecord).forEach((row) => {
    const key = String(row.channel_group || '');
    if (key !== 'cheap' && key !== 'direct') return;
    lanes.push({
      available: Boolean(row.available),
      clientOnly: row.client_only ? String(row.client_only) : undefined,
      inputPerMtok: rate(row.input_per_mtok),
      key,
      outputPerMtok: rate(row.output_per_mtok),
    });
  });
  return lanes;
}

export async function fetchTokenVendorOptions(): Promise<TokenVendorOptions> {
  const response = await API.get('/api/pricing');
  // The catalog rows are `data`; the rate-limit ceiling and vendors sit beside it.
  unwrap(response);
  const payload = response?.data as ApiEnvelope<ApiRecord[]> & {
    rate_limit_ceiling?: unknown;
    vendors?: TokenVendorOptions['vendors'];
  };
  const models = (Array.isArray(payload.data) ? payload.data : [])
    .filter(isRecord)
    .map((row) => ({
      endpointTypes: Array.isArray(row.supported_endpoint_types)
        ? row.supported_endpoint_types.map(String)
        : [],
      inputPerMtok: Number.isFinite(Number(row.input_per_mtok))
        ? Number(row.input_per_mtok)
        : undefined,
      lanes: normalizeModelLaneFacts(row.lanes),
      maker: row.maker ? String(row.maker) : undefined,
      makerRank: Number.isFinite(Number(row.maker_rank))
        ? Number(row.maker_rank)
        : undefined,
      modalityOutput: Array.isArray(row.modality_output)
        ? row.modality_output.map(String)
        : undefined,
      model: String(row.model_name || ''),
      outputPerMtok: Number.isFinite(Number(row.output_per_mtok))
        ? Number(row.output_per_mtok)
        : undefined,
      releasedAt:
        row.released_at != null && Number.isFinite(Number(row.released_at))
          ? Number(row.released_at)
          : undefined,
      vendorID: row.vendor_id ? String(row.vendor_id) : undefined,
    }))
    .filter((row) => row.model);
  return {
    models,
    rateLimitCeiling: normalizeRateLimitCeiling(payload.rate_limit_ceiling),
    vendors: Array.isArray(payload.vendors) ? payload.vendors : [],
  };
}

async function fetchTokenDetail(id: TokenId): Promise<TokenFormValues> {
  const detail = unwrap<unknown>(await API.get(`/api/token/${id}`));
  return normalizeTokenDetail(isRecord(detail) ? detail : {});
}

const DEFAULT_TOKEN_QUOTA_USD = 50;

/** Fields PUT /api/token/ copies onto a Key (see controller.UpdateToken). */
const TOKEN_UPDATE_FIELDS = [
  'name',
  'product',
  'metadata',
  'expired_time',
  'remain_quota',
  'unlimited_quota',
  'rate_limits_enabled',
  'rpm_limit',
  'tpm_limit',
  'model_limits_enabled',
  'model_limits',
  'allow_ips',
  'ip_whitelist_enabled',
  'ip_blacklist',
  'ip_blacklist_enabled',
  'vendor_limits_enabled',
  'vendor_limits',
  'vendor_groups',
  'model_lanes',
  'search_limits_enabled',
  'search_providers',
  'search_domain_allowlist',
  'reasoning_effort_cap',
  'decision_question_generation',
] as const;

export function getDefaultTokenFormValues(): TokenFormValues {
  return {
    ...normalizeTokenDetail({
      tokenCount: 1,
      unlimited_quota: false,
    }),
    remain_quota: DEFAULT_TOKEN_QUOTA_USD,
  };
}

export function useTokenDetailQuery(id?: TokenId, enabled?: boolean) {
  return useQuery<TokenFormValues, Error>({
    enabled: Boolean(id && enabled),
    queryKey: queryKeys.console.detail('tokens', id || 'new'),
    queryFn: () => fetchTokenDetail(id as TokenId),
  });
}

export function useTokensQuery(
  openFluentNotification?: UseTokensQueryOptions,
  status = 'all',
) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [filters, setFilters] = useState(emptyFilters);
  const [draftFilters, setDraftFilters] = useState(emptyFilters);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(() => {
    return Number(localStorage.getItem('page-size')) || ITEMS_PER_PAGE;
  });
  const [sort, setSort] = useState<TokenSort>(emptySort);
  const [selectedIds, setSelectedIds] = useState<TokenId[]>([]);

  const params = useMemo(
    () => ({ filters, page, pageSize, sort, status }),
    [filters, page, pageSize, sort, status],
  );

  const tokensQuery = useQuery<TokenPageData, Error>({
    queryKey: queryKeys.console.table('tokens', params),
    queryFn: () => fetchTokens(params),
    placeholderData: keepPreviousData,
  });

  const modelsQuery = useQuery<string[], Error>({
    queryKey: queryKeys.console.detail('tokens', 'models'),
    queryFn: fetchTokenModels,
    staleTime: 5 * 60 * 1000,
  });

  const vendorOptionsQuery = useQuery<TokenVendorOptions, Error>({
    queryKey: queryKeys.console.detail('tokens', 'vendor-options'),
    queryFn: fetchTokenVendorOptions,
    staleTime: 5 * 60 * 1000,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: queryKeys.console.root,
    });

  const pageData = tokensQuery.data || {
    items: [],
    page,
    pageSize,
    total: 0,
    meta: {},
  };

  const selectedRows = useMemo(() => {
    const selected = new Set(selectedIds.map(String));
    return pageData.items.filter((token) => selected.has(String(token.id)));
  }, [pageData.items, selectedIds]);

  const manageTokenMutation = useMutation<
    ApiEnvelope,
    Error,
    ManageTokenVariables
  >({
    mutationFn: async ({ action, id }) => {
      let response;
      if (action === 'delete') {
        response = await API.delete(`/api/token/${id}/`);
      } else {
        response = await API.put('/api/token/?status_only=true', {
          id,
          status: action === 'enable' ? 1 : 2,
        });
      }
      unwrap(response);
      return response.data as ApiEnvelope;
    },
    onSuccess: () => {
      showSuccess(t('page.console.tables.actionSuccess'));
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const saveTokenMutation = useMutation<ApiEnvelope, Error, SaveTokenVariables>(
    {
      mutationFn: async ({ editingToken, values }) => {
        if (editingToken?.id) {
          const response = await API.put('/api/token/', {
            ...normalizeTokenPayload(values),
            id: editingToken.id,
          });
          unwrap(response);
          return response.data as ApiEnvelope;
        }

        const count = Math.max(
          1,
          Number.parseInt(String(values.tokenCount ?? ''), 10) || 1,
        );
        const baseName = String(values.name || '').trim() || 'default';
        const createdSecrets: CreatedSecret[] = [];
        for (let index = 0; index < count; index += 1) {
          const payload = normalizeTokenPayload({
            ...values,
            name:
              index === 0 && String(values.name || '').trim()
                ? baseName
                : `${baseName}-${randomSuffix()}`,
          });
          const data = unwrap<unknown>(await API.post('/api/token/', payload));
          const created = isRecord(data) ? data : {};
          const secret = String(created.key || '');
          if (secret) {
            createdSecrets.push({
              id:
                typeof created.id === 'string' || typeof created.id === 'number'
                  ? created.id
                  : undefined,
              key: formatTokenKey(secret),
              name: String(payload.name),
              token: isRecord(created.token)
                ? normalizeTokenRecord(created.token)
                : undefined,
            });
          }
        }
        return { success: true, data: createdSecrets };
      },
      onSuccess: (_payload, variables) => {
        // A created Key is announced by the banner that shows its value, so
        // only an edit needs a toast.
        if (variables.editingToken?.id) {
          showSuccess(t('legacy.i18n.token_updated_successfully.0b51462075'));
        }
        invalidate();
      },
      onError: apiErrorHandler,
    },
  );

  const batchDisableMutation = useMutation<ApiEnvelope, Error, void>({
    mutationFn: async () => {
      const ids = selectedRows
        .filter((token) => Number(token.status_code ?? token.status) === 1)
        .map((token) => token.id);
      if (!ids.length) {
        throw new Error(t('page.console.token.selection.noneEnabled'));
      }
      // Each Key answers on its own. The requests skip the global toast so
      // a batch reports once: a Key that refused to stop is counted here and
      // never as stopped.
      const results = await Promise.all(
        ids.map(async (id) => {
          try {
            unwrap(
              await API.put(
                '/api/token/?status_only=true',
                { id, status: 2 },
                { skipErrorHandler: true },
              ),
            );
            return { ok: true as const };
          } catch (error) {
            return { ok: false as const, error };
          }
        }),
      );
      const failed = results.filter((result) => !result.ok);
      if (failed.length === results.length) {
        throw failed[0]?.error;
      }
      if (failed.length) {
        showError(
          t('page.console.token.selection.disableFailed', {
            count: failed.length,
            message: errorMessage(failed[0]?.error),
          }),
        );
      }
      return { success: true, data: results.length - failed.length };
    },
    onSuccess: (payload) => {
      showSuccess(
        t('page.console.token.selection.disabled', {
          count: Number(payload?.data || 0),
        }),
      );
      setSelectedIds([]);
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const batchDeleteMutation = useMutation<ApiEnvelope, Error, void>({
    mutationFn: async () => {
      const ids = selectedRows.map((token) => token.id);
      if (!ids.length) {
        throw new Error(
          t('legacy.i18n.please_select_the_token_to_be_deleted.f401dbca8b'),
        );
      }
      const response = await API.post('/api/token/batch', { ids });
      unwrap(response);
      return response.data as ApiEnvelope;
    },
    onSuccess: (payload) => {
      const count = Number(payload?.data || selectedRows.length);
      showSuccess(t('legacy.i18n.deleted_tokens.0a91776377', { count }));
      setSelectedIds([]);
      invalidate();
    },
    onError: apiErrorHandler,
  });

  /** Reads a Key's full value. The value stays with the caller; nothing is
   *  cached in the query client. */
  const revealKey = async (id: TokenId): Promise<string> => {
    const data = unwrap<{ key?: unknown } | null>(
      await API.post(`/api/token/${id}/reveal`),
    );
    if (typeof data?.key !== 'string') {
      throw new Error(t('page.console.tables.actionFailed'));
    }
    return formatTokenKey(data.key);
  };

  /** Issues a new value for a Key and returns it; the old value stops working
   *  at once. */
  /** `operationKey` identifies one rotation attempt, retries included. A
   *  rotation whose answer was lost still happened; retrying under a new key
   *  would issue a second Key and throw away the one the user never saw, so
   *  the caller holds this key for as long as the confirmation stays open. */
  const rotateKey = async (
    id: TokenId,
    operationKey: string,
  ): Promise<string> => {
    const response = await API.post(
      `/api/token/${id}/rotate`,
      { old_expires: 'now' },
      { headers: { 'Idempotency-Key': operationKey } },
    );
    const data = unwrap<{ key?: unknown } | null>(response);
    if (typeof data?.key !== 'string') {
      throw new Error(t('page.console.token.saveFailed'));
    }
    invalidate();
    return formatTokenKey(data.key);
  };

  /** Applies a change to a few fields of an existing Key: reads the full
   *  record, edits it, and writes it back through the normal update path. */
  /** Saves the two fields the connect dialog owns — which tool a Key is for
   *  and which lane it takes. It deliberately does not read the Key back and
   *  resend it: the dialog opens with a row the list loaded minutes ago, and
   *  a full update would write that stale quota and expiry back over whatever
   *  the user changed since. */
  const updateTokenConnect = async (
    id: TokenId,
    fields: {
      expected_version?: number;
      model_lanes?: Record<string, 'direct' | 'cheap'>;
      product?: string;
      vendor_groups?: Record<string, 'direct' | 'cheap'>;
    },
  ) => {
    const payload: Record<string, unknown> = {
      id,
      expected_version: fields.expected_version,
    };
    if (fields.product !== undefined) payload.product = fields.product;
    if (fields.vendor_groups !== undefined) {
      payload.vendor_groups = normalizeVendorGroups(fields.vendor_groups);
    }
    if (fields.model_lanes !== undefined) {
      payload.model_lanes = normalizeVendorGroups(fields.model_lanes);
    }
    const response = await API.put('/api/token/?connect_only=true', payload);
    unwrap(response);
    invalidate();
    return response.data as ApiEnvelope<TokenRecord>;
  };

  const copyText = async (text: string) => {
    if (await copy(text)) {
      showSuccess(t('page.console.tables.copied'));
      return true;
    }
    showError(t('page.console.tables.copyFailed'));
    return false;
  };

  const copySelected = async () => {
    if (!selectedRows.length) {
      showError(t('legacy.i18n.please_select_at_least_one_token.ada3702300'));
      return;
    }
    const content = selectedRows.map((token) => token.name).join('\n');
    await copyText(`${content}\n`);
  };

  /** `pending` is a tab the caller already opened on the user's click; the
   *  link is written into it once the Key is in hand. Without it a browser
   *  treats the open as unsolicited and blocks it. */
  const openChatLink = (
    record: TokenRecord,
    template?: ChatTemplate | null,
    pending?: Window | null,
  ) => {
    if (!template?.value) {
      pending?.close();
      showError(
        t(
          'legacy.i18n.please_contact_the_administrator_to_configure_the_chat_link.e093a72dfa',
        ),
      );
      return;
    }
    if (template.value.startsWith('fluent')) {
      pending?.close();
      openFluentNotification?.(record.key);
      return;
    }
    // An https destination would receive the Key as part of the URL, and keep
    // it in its access log. Only a custom scheme, which hands the value to an
    // app on this machine, may carry it.
    if (chatTemplateLeaksKey(template.value)) {
      pending?.close();
      showError(
        t('page.console.token.chat.webTemplateRefused', {
          defaultValue:
            '这个对话链接会把密钥放进网址，已拦下；请用「接入」里的手动步骤。',
        }),
      );
      return;
    }
    const status = localStorage.getItem('status');
    let serverAddress = '';
    if (status) {
      try {
        const parsed = JSON.parse(status) as { server_address?: string };
        serverAddress = parsed.server_address || '';
      } catch {
        serverAddress = '';
      }
    }
    if (!serverAddress) {
      serverAddress = window.location.origin;
    }
    let url = template.value;
    if (url.includes('{cherryConfig}')) {
      const cherryConfig = {
        apiKey: formatTokenKey(record.key),
        baseUrl: serverAddress,
        id: 'lazu',
      };
      url = url.replaceAll(
        '{cherryConfig}',
        encodeURIComponent(btoa(JSON.stringify(cherryConfig))),
      );
    } else {
      url = url
        .replaceAll('{address}', encodeURIComponent(serverAddress))
        .replaceAll('{key}', formatTokenKey(record.key));
    }
    if (pending && !pending.closed) {
      pending.location.replace(url);
      return;
    }
    window.open(url, '_blank', 'noopener');
  };

  const submitFilters = () => {
    setFilters(trimFilterValues(draftFilters));
    setPage(1);
  };

  const resetFilters = () => {
    setDraftFilters(emptyFilters);
    setFilters(emptyFilters);
    setPage(1);
  };

  const setPageSizeAndPersist = (nextSize: number) => {
    localStorage.setItem('page-size', String(nextSize));
    setPageSize(nextSize);
    setPage(1);
  };

  const modelOptions = (modelsQuery.data || []).map(toOption);
  const vendorOptions = vendorOptionsQuery.data || { models: [], vendors: [] };

  return {
    batchDeleteMutation,
    batchDisableMutation,
    copySelected,
    copyText,
    draftFilters,
    filters,
    isFetching:
      tokensQuery.isFetching ||
      modelsQuery.isFetching ||
      vendorOptionsQuery.isFetching,
    isLoading: tokensQuery.isLoading,
    manageTokenMutation,
    modelOptions,
    openChatLink,
    page: pageData.page || page,
    pageSize: pageData.pageSize || pageSize,
    refresh: () => invalidate(),
    revealKey,
    rotateKey,
    updateTokenConnect,
    resetFilters,
    saveTokenMutation,
    selectedIds,
    selectedRows,
    setDraftFilters,
    setFilters,
    setPage,
    setPageSize: setPageSizeAndPersist,
    setSelectedIds,
    setSort,
    sort,
    submitFilters,
    t,
    tokens: pageData.items,
    total: pageData.total,
    vendorOptions,
    timestamp2string,
  };
}
