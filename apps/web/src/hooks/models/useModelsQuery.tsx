import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { trimFilterValues } from '../../components/ui/filter-bar';
import {
  coerceItemsPayload,
  createSearchParams,
} from '../../components/ui/table-tools';
import { API, postSSE } from '../../helpers/api';
import {
  getUserIdFromLocalStorage,
  copy,
  showError,
  showSuccess,
} from '../../helpers/utils';
import {
  apiErrorHandler,
  apiErrorHandlerWith,
  unwrap,
} from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type LooseRecord = Record<string, unknown>;

type ModelFilters = {
  keyword: string;
  status: string;
  tier: string;
  vendor: string;
};

/** 'catalog' is the one display order every catalog uses: the operator's
 *  maker order, then newest release first. */
type ModelSort =
  | 'catalog'
  | 'created_desc'
  | 'updated_desc'
  | 'name_asc'
  | 'status_desc';

type ModelRecord = LooseRecord & {
  id: number | string;
  lifecycle_status?: string;
  model_name?: string;
  status?: number;
};

type VendorRecord = LooseRecord & {
  id: number | string;
  internal_id?: number | string;
};

type ModelFormValues = LooseRecord & {
  enable_groups?: string[];
  quota_types?: string[];
  tags?: string[] | string;
};

type ModelCapabilities = {
  supports_audio_input: boolean;
  supports_audio_output: boolean;
  supports_computer_use: boolean;
  supports_function_calling: boolean;
  supports_parallel_function_calling: boolean;
  supports_pdf_input: boolean;
  supports_prompt_caching: boolean;
  supports_reasoning: boolean;
  supports_response_schema: boolean;
  supports_vision: boolean;
  supports_web_search: boolean;
};

type PrefillOption = {
  label: string;
  value: string;
};

type FetchModelsParams = {
  filters: ModelFilters;
  page: number;
  pageSize: number;
  sortBy: ModelSort;
};

type PageData = {
  items: ModelRecord[];
  meta?: LooseRecord;
  page: number;
  pageSize: number;
  total: number;
};

type ManageModelVariables = {
  action: 'delete' | 'disable' | 'enable';
  id: number | string;
};

type SaveModelVariables = {
  editingModel?: ModelRecord | null;
  values: ModelFormValues;
};

const emptyFilters: ModelFilters = {
  keyword: '',
  status: 'offered',
  tier: 'all',
  vendor: 'all',
};

const defaultModelSort: ModelSort = 'catalog';
const MODEL_CATALOG_PAGE_SIZE = 100;

const emptyCapabilities: ModelCapabilities = {
  supports_audio_input: false,
  supports_audio_output: false,
  supports_computer_use: false,
  supports_function_calling: false,
  supports_parallel_function_calling: false,
  supports_pdf_input: false,
  supports_prompt_caching: false,
  supports_reasoning: false,
  supports_response_schema: false,
  supports_vision: false,
  supports_web_search: false,
};

async function fetchVendors(): Promise<VendorRecord[]> {
  const response = await API.get('/api/vendors/?page_size=1000');
  const data = unwrap<{ items?: VendorRecord[] } | VendorRecord[] | null>(
    response,
  );
  if (data && !Array.isArray(data) && Array.isArray(data.items)) {
    return data.items;
  }
  return Array.isArray(data) ? data : [];
}

async function fetchGroups(): Promise<LooseRecord[]> {
  const response = await API.get('/api/group/');
  const data = response?.data?.data;
  return Array.isArray(data) ? data : [];
}

const splitListValue = (value: unknown): string[] => {
  if (Array.isArray(value)) {
    return value.map(String).filter(Boolean);
  }
  if (value === undefined || value === null || value === '') {
    return [];
  }
  return String(value)
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
};

const normalizePrefillGroups = (groups: unknown): string[] => {
  if (!Array.isArray(groups)) {
    return [];
  }
  return groups.flatMap((group) => {
    const items = (group as LooseRecord | undefined)?.items;
    if (Array.isArray(items)) {
      return items;
    }
    if (items && typeof items === 'object') {
      return Object.keys(items);
    }
    if (typeof items === 'string') {
      try {
        const parsed = JSON.parse(items);
        if (Array.isArray(parsed)) {
          return parsed;
        }
        if (parsed && typeof parsed === 'object') {
          return Object.keys(parsed);
        }
      } catch {
        return splitListValue(items);
      }
    }
    return [];
  });
};

async function fetchPrefillOptions(type: string): Promise<PrefillOption[]> {
  const response = await API.get(`/api/prefill_group?type=${type}`);
  const groups = unwrap<unknown>(response);
  return Array.from(new Set(normalizePrefillGroups(groups))).map((value) => ({
    label: String(value),
    value: String(value),
  }));
}

async function fetchPrefillGroups(type: string): Promise<LooseRecord[]> {
  const response = await API.get(`/api/prefill_group?type=${type}`);
  const groups = unwrap<unknown>(response);
  return Array.isArray(groups) ? (groups as LooseRecord[]) : [];
}

async function fetchModels({
  filters,
  page,
  pageSize,
  sortBy,
}: FetchModelsParams): Promise<PageData> {
  const hasSearch = Boolean(
    filters.keyword ||
    filters.vendor !== 'all' ||
    filters.status !== 'all' ||
    filters.tier !== 'all',
  );
  const endpoint = hasSearch ? '/api/models/search' : '/api/models/';
  const query = createSearchParams({
    p: page,
    page_size: pageSize,
    sort: sortBy,
    keyword: hasSearch ? filters.keyword : undefined,
    status: hasSearch && filters.status !== 'all' ? filters.status : undefined,
    tier: hasSearch && filters.tier !== 'all' ? filters.tier : undefined,
    vendor: hasSearch && filters.vendor !== 'all' ? filters.vendor : undefined,
  });
  const response = await API.get(`${endpoint}?${query}`, {
    skipErrorHandler: true,
  });
  return coerceItemsPayload(
    { data: unwrap<unknown>(response) },
    page,
    pageSize,
  ) as PageData;
}

const normalizeModelDetail = (data: LooseRecord = {}): ModelFormValues => ({
  ...data,
  capabilities: {
    ...emptyCapabilities,
    ...(data.capabilities && typeof data.capabilities === 'object'
      ? data.capabilities
      : {}),
  },
  context_window: String(data.context_window ?? data.context_length ?? ''),
  description: data.description || '',
  description_en: data.description_en || '',
  description_zh: data.description_zh || '',
  enable_groups: splitListValue(data.enable_groups),
  endpoints: data.endpoints || '',
  icon: data.icon || '',
  family: data.family || '',
  max_tokens: String(data.max_tokens ?? data.max_output_tokens ?? ''),
  model_name: data.model_name || '',
  name_rule: Number(data.name_rule ?? 0),
  open_source: Boolean(data.open_source),
  quota_types: splitListValue(data.quota_types).map(String),
  lifecycle_status: data.lifecycle_status || 'active',
  replacement_model: data.replacement_model || '',
  retire_after: String(data.retire_after || ''),
  status: data.status !== 0,
  sync_official: (data.sync_official ?? 1) === 1,
  tags: splitListValue(data.tags),
  vendor_id:
    data.vendor_id === undefined || data.vendor_id === null
      ? ''
      : String(data.vendor_id),
});

const asIntegerOrZero = (value: unknown): number => {
  if (value === undefined || value === null || value === '') {
    return 0;
  }
  const parsed = Number.parseInt(String(value), 10);
  return Number.isFinite(parsed) ? parsed : 0;
};

const normalizeModelPayload = (values: ModelFormValues): LooseRecord => {
  const payload = { ...values };
  delete payload.enable_groups;
  delete payload.quota_types;

  return {
    ...payload,
    capabilities: {
      ...emptyCapabilities,
      ...(values.capabilities && typeof values.capabilities === 'object'
        ? values.capabilities
        : {}),
    },
    context_length: asIntegerOrZero(values.context_window),
    endpoints: values.endpoints || '',
    family: String(values.family || '')
      .trim()
      .toLowerCase(),
    max_output_tokens: asIntegerOrZero(values.max_tokens),
    model_name: String(values.model_name || '')
      .trim()
      .toLowerCase(),
    name_rule: Number(values.name_rule ?? 0),
    open_source: Boolean(values.open_source),
    lifecycle_status: String(values.lifecycle_status || 'active'),
    replacement_model: String(values.replacement_model || '').trim(),
    retire_after: asIntegerOrZero(values.retire_after),
    status: values.status ? 1 : 0,
    sync_official: values.sync_official ? 1 : 0,
    tags: Array.isArray(values.tags)
      ? values.tags.join(',')
      : values.tags || '',
    vendor_id: Number(values.vendor_id || 0),
  };
};

async function fetchModelDetail(id: number | string): Promise<ModelFormValues> {
  const response = await API.get(`/api/models/${id}`);
  return normalizeModelDetail(unwrap<LooseRecord | null>(response) || {});
}

export function getDefaultModelFormValues() {
  return normalizeModelDetail({
    lifecycle_status: 'active',
    name_rule: 0,
    status: 1,
    sync_official: 1,
  });
}

export function useModelDetailQuery(
  id?: number | string | null,
  enabled?: boolean,
) {
  return useQuery({
    enabled: Boolean(id && enabled),
    queryKey: queryKeys.console.detail('models', id || 'new'),
    queryFn: () => fetchModelDetail(id as number | string),
  });
}

export const modelsQueryOptions = (
  params: FetchModelsParams = {
    filters: emptyFilters,
    page: 1,
    pageSize: MODEL_CATALOG_PAGE_SIZE,
    sortBy: defaultModelSort,
  },
) => ({
  queryKey: queryKeys.console.table('models', {
    ...params,
    accountId: getUserIdFromLocalStorage(),
  }),
  queryFn: () => fetchModels(params),
});

export function useModelsQuery(loadEditorOptions = true) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [filters, setFilters] = useState(emptyFilters);
  const [draftFilters, setDraftFilters] = useState(emptyFilters);
  const [page, setPage] = useState(1);
  // The catalog is a pricing reference first. Fetch the largest backend
  // page so the table can be scanned as one continuous surface instead of
  // forcing users through the legacy ten-row default.
  const [pageSize, setPageSize] = useState(MODEL_CATALOG_PAGE_SIZE);
  const [sortBy, setSortByState] = useState<ModelSort>(defaultModelSort);
  const [selectedModels, setSelectedModels] = useState<ModelRecord[]>([]);

  const params = useMemo(
    () => ({ filters, page, pageSize, sortBy }),
    [filters, page, pageSize, sortBy],
  );

  const modelsQuery = useQuery({
    ...modelsQueryOptions(params),
    placeholderData: keepPreviousData,
  });

  // Speculative reads stay quiet; failures belong to the page once opened.
  useEffect(() => {
    if (modelsQuery.error) showError(modelsQuery.error);
  }, [modelsQuery.error]);

  const vendorsQuery = useQuery({
    queryKey: queryKeys.console.detail('models', 'vendors'),
    queryFn: fetchVendors,
    staleTime: 5 * 60 * 1000,
  });

  const groupsQuery = useQuery({
    enabled: loadEditorOptions,
    queryKey: queryKeys.console.detail('models', 'groups'),
    queryFn: fetchGroups,
    staleTime: 5 * 60 * 1000,
  });

  const tagOptionsQuery = useQuery({
    enabled: loadEditorOptions,
    queryKey: queryKeys.console.detail('models', 'prefill-tags'),
    queryFn: () => fetchPrefillOptions('tag'),
    staleTime: 5 * 60 * 1000,
  });

  const endpointOptionsQuery = useQuery({
    enabled: loadEditorOptions,
    queryKey: queryKeys.console.detail('models', 'prefill-endpoint-groups'),
    queryFn: () => fetchPrefillGroups('endpoint'),
    staleTime: 5 * 60 * 1000,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: queryKeys.console.root,
    });

  const manageModelMutation = useMutation({
    mutationFn: async ({ action, id }: ManageModelVariables) => {
      let response;
      if (action === 'delete') {
        response = await API.delete(`/api/models/${id}`);
      } else {
        response = await API.put('/api/models/?status_only=true', {
          id,
          status: action === 'enable' ? 1 : 0,
        });
      }
      return unwrap<unknown>(response);
    },
    onSuccess: () => {
      showSuccess(t('page.console.tables.actionSuccess'));
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const syncMutation = useMutation({
    mutationFn: async () => {
      const response = await API.post('/api/models/sync_upstream', {});
      return unwrap<LooseRecord | null>(response) || {};
    },
    onSuccess: (data) => {
      showSuccess(
        t('page.console.models.syncComplete', {
          createdModels: data.created_models || 0,
          createdVendors: data.created_vendors || 0,
          updatedModels: data.updated_models || 0,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.models.syncFailed')),
  });

  const syncLiteLLMMutation = useMutation({
    mutationFn: () => postSSE<LooseRecord>('/api/models/sync_litellm', {}),
    onSuccess: (data) => {
      showSuccess(
        t('page.console.models.syncLiteLLMComplete', {
          matched: data.matched || 0,
          updatedPricing: data.updated_pricing || 0,
          updatedCapabilities: data.updated_capabilities || 0,
          updatedSellPrices: data.updated_sell_prices || 0,
          skipped: data.skipped || 0,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.models.syncLiteLLMFailed')),
  });

  const syncOpenRouterMutation = useMutation({
    mutationFn: () => postSSE<LooseRecord>('/api/models/sync_openrouter', {}),
    onSuccess: (data) => {
      showSuccess(
        t('page.console.models.syncOpenRouterComplete', {
          matched: data.matched || 0,
          updatedDescEn: data.updated_desc_en || 0,
          updatedTags: data.updated_tags || 0,
          updatedCapabilities: data.updated_capabilities || 0,
          skipped: data.skipped || 0,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.models.syncOpenRouterFailed')),
  });

  const batchDeleteMutation = useMutation({
    mutationFn: async () => {
      const ids = selectedModels.map((model) => model.id);
      const results = await Promise.all(
        ids.map((id) => API.delete(`/api/models/${id}`)),
      );
      results.forEach((response) => unwrap<unknown>(response));
      return ids.length;
    },
    onSuccess: (count) => {
      showSuccess(t('page.console.models.batchDeleted', { count }));
      setSelectedModels([]);
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.models.batchDeleteFailed')),
  });

  const saveModelMutation = useMutation({
    mutationFn: async ({ editingModel, values }: SaveModelVariables) => {
      const payload = normalizeModelPayload(values);
      const response = editingModel?.id
        ? await API.put('/api/models/', {
            ...payload,
            id: Number(editingModel.id),
          })
        : await API.post('/api/models/', payload);
      return unwrap<unknown>(response);
    },
    onSuccess: (_payload, variables) => {
      showSuccess(
        variables.editingModel?.id
          ? t('page.console.models.form.saveSuccess.edit')
          : t('page.console.models.form.saveSuccess.create'),
      );
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.models.form.saveFailed')),
  });

  const copyModel = async (modelName: unknown) => {
    if (await copy(modelName)) {
      showSuccess(t('page.console.tables.copied'));
    } else {
      showError(t('page.console.tables.copyFailed'));
    }
  };

  // Accepts an optional override so callers that just mutated draftFilters in
  // the same tick (e.g. the search-clear button) can submit the new value
  // without waiting for the React state update batch — otherwise submitFilters
  // would read the stale draftFilters and the change would round-trip late.
  const submitFilters = (override?: ModelFilters) => {
    setFilters(trimFilterValues(override ?? draftFilters));
    setPage(1);
  };

  const resetFilters = () => {
    setDraftFilters(emptyFilters);
    setFilters(emptyFilters);
    setPage(1);
  };

  const setSortBy = (nextSort: string) => {
    setSortByState(
      nextSort === 'updated_desc' ||
        nextSort === 'created_desc' ||
        nextSort === 'name_asc' ||
        nextSort === 'status_desc'
        ? nextSort
        : defaultModelSort,
    );
    setPage(1);
  };

  const pageData = modelsQuery.data || {
    items: [],
    page,
    pageSize,
    total: 0,
    meta: {},
  };

  const vendors = vendorsQuery.data || [];
  const vendorMap = useMemo(
    () =>
      vendors.reduce<Record<string, VendorRecord>>((acc, vendor) => {
        const id = vendor.internal_id ?? vendor.id;
        acc[String(id)] = vendor;
        return acc;
      }, {}),
    [vendors],
  );

  return {
    batchDeleteMutation,
    copyModel,
    draftFilters,
    filters,
    isFetching: modelsQuery.isFetching,
    isLoading: modelsQuery.isLoading,
    manageModelMutation,
    models: pageData.items,
    endpointGroups: endpointOptionsQuery.data || [],
    editorOptionsLoading:
      tagOptionsQuery.isFetching || endpointOptionsQuery.isFetching,
    page: pageData.page || page,
    pageSize: pageData.pageSize || pageSize,
    refresh: () => invalidate(),
    resetFilters,
    saveModelMutation,
    selectedModels,
    setDraftFilters,
    setFilters,
    setPage,
    setPageSize: (nextSize: number) => {
      setPageSize(nextSize);
      setPage(1);
    },
    setSelectedModels,
    setSortBy,
    submitFilters,
    sortBy,
    syncMutation,
    syncLiteLLMMutation,
    syncOpenRouterMutation,
    t,
    total: pageData.total,
    groups: groupsQuery.data || [],
    tagOptions: tagOptionsQuery.data || [],
    vendorCounts: pageData.meta?.vendor_counts || {},
    vendorMap,
    vendors,
  };
}
