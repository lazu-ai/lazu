import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  DEFAULT_PROTOCOL_DIALECT,
  inferProtocolDialect,
} from './channelProtocolDialect';
import {
  coerceItemsPayload,
  createSearchParams,
} from '../../components/ui/table-tools';
import { ITEMS_PER_PAGE } from '../../constants/common.constant';
import { API } from '../../helpers/api';
import { copy, showError, showInfo, showSuccess } from '../../helpers/utils';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type LooseRecord = Record<string, unknown>;

type ChannelFilters = {
  group: string;
  keyword: string;
  model: string;
  status: string;
  type: string;
};

type ChannelRecord = LooseRecord & {
  id: number | string;
  key?: string;
  name?: string;
  status?: number;
};

type ChannelFormValues = LooseRecord & {
  auto_ban?: boolean;
  source_type?: string;
  groups?: string[];
  key?: string;
  models?: string[];
  observe_new_routes?: boolean;
  claude_code_client_only?: boolean;
  claude_code_user_agent?: string;
  provider_cost_prices?: string;
  protocol_dialect?: string;
  setting?: string;
  decision_dialect?: string;
  cf_account_id?: string;
  decision_timeout_ms?: number;
  max_rps?: number;
  max_in_flight?: number;
  shared_limit_group?: string;
  shared_max_rps?: number;
  shared_max_in_flight?: number;
  settings?: string;
  video_content_url_template?: string;
  video_envelope?: string;
  video_family?: string;
  video_status_url_template?: string;
};

type OptionItem = {
  label: string;
  value: string;
};

type FetchChannelsParams = {
  filters: ChannelFilters;
  idSort: boolean;
  page: number;
  pageSize: number;
  tagMode: boolean;
};

type PageData = {
  items: ChannelRecord[];
  meta?: LooseRecord;
  page: number;
  pageSize: number;
  total: number;
};

type ManageChannelVariables = {
  action:
    | 'balance'
    | 'copy'
    | 'delete'
    | 'disable'
    | 'enable'
    | 'priority'
    | 'test'
    | 'weight';
  id: number | string;
  record?: ChannelRecord;
  value?: unknown;
};

type SaveChannelVariables = {
  editingChannel?: ChannelRecord | null;
  values: ChannelFormValues;
};

type BulkAction = 'deleteDisabled' | 'fix' | 'testAll' | 'updateBalance';

const DEFAULT_CHANNEL_GROUP = 'direct';
const DEFAULT_SOURCE_TYPE = 'unclassified';
const SOURCE_TYPE_VALUES = [
  'unclassified',
  'official',
  'cloud',
  'aggregator',
  'reverse',
];

/** Mirrors model.NormalizeChannelSourceType — an unset or unknown value reads as
 *  未分类, never as a confident 官方. */
const normalizeSourceType = (value: unknown): string => {
  const text = String(value || '')
    .trim()
    .toLowerCase();
  return SOURCE_TYPE_VALUES.includes(text) ? text : DEFAULT_SOURCE_TYPE;
};

const normalizeChannelGroup = (value: unknown): string =>
  String(value || '').trim() === 'cheap' ? 'cheap' : DEFAULT_CHANNEL_GROUP;

const readBoolean = (key: string, fallback = false): boolean => {
  const value = localStorage.getItem(key);
  return value === null ? fallback : value === 'true';
};

const skipErrorHandlerConfig = { skipErrorHandler: true };

const getInitialFilters = (): ChannelFilters => ({
  keyword: '',
  group: 'all',
  model: '',
  status: localStorage.getItem('channel-status-filter') || 'all',
  type: 'all',
});

async function fetchGroups(): Promise<string[]> {
  const data = unwrap<unknown>(await API.get('/api/group/'));
  return Array.isArray(data) ? data.map(String) : [];
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

const normalizeChannelModelOptions = (models: unknown): OptionItem[] => {
  if (!Array.isArray(models)) {
    return [];
  }
  const seen = new Set<string>();
  return models
    .map((model) => {
      if (typeof model === 'string') {
        return model.trim();
      }
      return String(
        model?.id ??
          model?.model_name ??
          model?.name ??
          model?.value ??
          model?.label ??
          '',
      ).trim();
    })
    .filter(Boolean)
    .filter((model) => {
      if (seen.has(model)) {
        return false;
      }
      seen.add(model);
      return true;
    })
    .map((model) => ({ label: model, value: model }));
};

const readChannelSettings = (value: unknown): LooseRecord => {
  let parsed = value;
  if (typeof value === 'string' && value.trim()) {
    try {
      parsed = JSON.parse(value) as unknown;
    } catch {
      return {};
    }
  }
  return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
    ? (parsed as LooseRecord)
    : {};
};

const normalizeChannelDetail = (data: LooseRecord = {}): ChannelFormValues => {
  const groups = splitListValue(data.groups || data.group);
  const settings = readChannelSettings(data.settings);
  const capacity = readChannelSettings(data.setting);
  return {
    ...data,
    setting: JSON.stringify(capacity),
    decision_dialect: String(capacity.decision_dialect ?? ''),
    cf_account_id: String(capacity.cf_account_id ?? ''),
    decision_timeout_ms: Number(capacity.decision_timeout_ms ?? 0),
    max_rps: Number(capacity.max_rps ?? 0),
    max_in_flight: Number(capacity.max_in_flight ?? 0),
    shared_limit_group: String(capacity.shared_limit_group ?? ''),
    shared_max_rps: Number(capacity.shared_max_rps ?? 0),
    shared_max_in_flight: Number(capacity.shared_max_in_flight ?? 0),

    auto_ban:
      data.auto_ban === undefined || data.auto_ban === null
        ? true
        : Number(data.auto_ban) !== 0,
    base_url: data.base_url || '',
    groups: groups.length
      ? [normalizeChannelGroup(groups[0])]
      : [DEFAULT_CHANNEL_GROUP],
    key: '',
    models: splitListValue(data.models),
    name: data.name || '',
    observe_new_routes: settings.observe_new_routes === true,
    claude_code_client_only: settings.claude_code_client_only === true,
    claude_code_user_agent: String(settings.claude_code_user_agent || ''),
    priority: Number(data.priority ?? 0),
    protocol_dialect: String(
      settings.protocol_dialect ||
        inferProtocolDialect(data.base_url, data.type),
    ),
    provider_cost_prices: JSON.stringify(
      data.provider_cost_prices || [],
      null,
      2,
    ),
    remark: data.remark || '',
    settings: JSON.stringify(settings),
    source_type: normalizeSourceType(data.source_type),
    status: data.status === undefined ? true : Number(data.status) === 1,
    tag: data.tag || '',
    type: String(data.type ?? 1),
    video_content_url_template: String(
      settings.video_content_url_template || '',
    ),
    video_envelope:
      settings.video_envelope && typeof settings.video_envelope === 'object'
        ? JSON.stringify(settings.video_envelope, null, 2)
        : '',
    video_family: String(settings.video_family || ''),
    video_status_url_template: String(settings.video_status_url_template || ''),
    weight: Number(data.weight ?? 0),
  };
};

const normalizeChannelPayload = (
  values: ChannelFormValues,
  editingChannel?: ChannelRecord | null,
): LooseRecord => {
  const baseURL = String(values.base_url || '')
    .trim()
    .replace(/\/+$/, '');
  const payload: LooseRecord = {
    auto_ban: values.auto_ban ? 1 : 0,
    base_url: baseURL,
    group: normalizeChannelGroup((values.groups || [])[0]),
    key: String(values.key || '').trim(),
    models: (values.models || []).join(','),
    name: String(values.name || '').trim(),
    priority: Number(values.priority || 0),
    remark: String(values.remark || '').trim(),
    source_type: normalizeSourceType(values.source_type),
    status: values.status ? 1 : 2,
    tag: String(values.tag || '').trim(),
    type: Number(values.type || 1),
    weight: Math.max(0, Number(values.weight || 0)),
  };

  if (editingChannel?.id && !payload.key) {
    delete payload.key;
  }
  const providerCostText = String(values.provider_cost_prices || '').trim();
  if (providerCostText) {
    payload.provider_cost_prices = JSON.parse(providerCostText);
  }
  const settings = readChannelSettings(values.settings);
  settings.protocol_dialect = String(
    values.protocol_dialect || inferProtocolDialect(baseURL, values.type),
  );
  const videoStatusTemplate = String(
    values.video_status_url_template || '',
  ).trim();
  const videoContentTemplate = String(
    values.video_content_url_template || '',
  ).trim();
  if (videoStatusTemplate) {
    settings.video_status_url_template = videoStatusTemplate;
  } else {
    delete settings.video_status_url_template;
  }
  if (videoContentTemplate) {
    settings.video_content_url_template = videoContentTemplate;
  } else {
    delete settings.video_content_url_template;
  }
  const videoFamily = String(values.video_family || '').trim();
  if (videoFamily) {
    settings.video_family = videoFamily;
  } else {
    delete settings.video_family;
  }
  const videoEnvelopeText = String(values.video_envelope || '').trim();
  if (videoEnvelopeText) {
    settings.video_envelope = JSON.parse(videoEnvelopeText);
  } else {
    delete settings.video_envelope;
  }
  if (values.observe_new_routes) {
    settings.observe_new_routes = true;
  } else {
    delete settings.observe_new_routes;
  }
  // Only an Anthropic source can be Claude Code-only; switching the type away
  // clears the flag rather than leaving a source no client can reach.
  const claudeCodeUserAgent = String(
    values.claude_code_user_agent || '',
  ).trim();
  if (values.claude_code_client_only && Number(values.type) === 14) {
    settings.claude_code_client_only = true;
    if (claudeCodeUserAgent) {
      settings.claude_code_user_agent = claudeCodeUserAgent;
    } else {
      delete settings.claude_code_user_agent;
    }
  } else {
    delete settings.claude_code_client_only;
    delete settings.claude_code_user_agent;
  }
  payload.settings = JSON.stringify(settings);
  const capacity = readChannelSettings(values.setting);
  capacity.decision_dialect = String(values.decision_dialect ?? '');
  capacity.cf_account_id = String(values.cf_account_id ?? '');
  capacity.decision_timeout_ms = Number(values.decision_timeout_ms ?? 0);
  capacity.max_rps = Number(values.max_rps ?? 0);
  capacity.max_in_flight = Number(values.max_in_flight ?? 0);
  capacity.shared_limit_group = String(values.shared_limit_group ?? '');
  capacity.shared_max_rps = Number(values.shared_max_rps ?? 0);
  capacity.shared_max_in_flight = Number(values.shared_max_in_flight ?? 0);
  payload.setting = JSON.stringify(capacity);

  return payload;
};

async function fetchChannelModelOptions(): Promise<OptionItem[]> {
  const response = await API.get('/api/channel/models');
  return normalizeChannelModelOptions(unwrap<unknown>(response));
}

async function fetchChannelDetail(
  id: number | string,
): Promise<ChannelFormValues> {
  const response = await API.get(`/api/channel/${id}`);
  return normalizeChannelDetail(unwrap<LooseRecord | null>(response) || {});
}

export function getDefaultChannelFormValues() {
  return normalizeChannelDetail({
    auto_ban: 1,
    group: DEFAULT_CHANNEL_GROUP,
    // A new source is watched for a week before it carries traffic, unless
    // the operator says otherwise on the routing step.
    settings: { observe_new_routes: true },
    source_type: DEFAULT_SOURCE_TYPE,
    status: 1,
    type: 1,
  });
}

export function useChannelDetailQuery(
  id?: number | string | null,
  enabled?: boolean,
) {
  return useQuery({
    enabled: Boolean(id && enabled),
    queryKey: queryKeys.console.detail('channels', id || 'new'),
    queryFn: () => fetchChannelDetail(id as number | string),
  });
}

async function fetchChannels({
  filters,
  idSort,
  page,
  pageSize,
  tagMode,
}: FetchChannelsParams): Promise<PageData> {
  const hasSearch = Boolean(
    filters.keyword || filters.group !== 'all' || filters.model,
  );
  const endpoint = hasSearch ? '/api/channel/search' : '/api/channel/';
  const query = createSearchParams({
    p: page,
    page_size: pageSize,
    id_sort: idSort,
    tag_mode: tagMode,
    keyword: hasSearch ? filters.keyword : undefined,
    group: hasSearch && filters.group !== 'all' ? filters.group : undefined,
    model: hasSearch ? filters.model : undefined,
    status: filters.status !== 'all' ? filters.status : undefined,
    type: filters.type !== 'all' ? filters.type : undefined,
  });
  const response = await API.get(`${endpoint}?${query}`);
  return coerceItemsPayload(
    unwrap<unknown>(response),
    page,
    pageSize,
  ) as PageData;
}

export function useChannelsQuery({ listEnabled = true } = {}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [filters, setFilters] = useState(getInitialFilters);
  const [draftFilters, setDraftFilters] = useState(getInitialFilters);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(() => {
    return Number(localStorage.getItem('page-size')) || ITEMS_PER_PAGE;
  });
  const [idSort, setIdSort] = useState(() => readBoolean('id-sort'));
  const [tagMode, setTagMode] = useState(() => readBoolean('enable-tag-mode'));

  const params = useMemo(
    () => ({ filters, idSort, page, pageSize, tagMode }),
    [filters, idSort, page, pageSize, tagMode],
  );

  const channelsQuery = useQuery({
    enabled: listEnabled,
    queryKey: queryKeys.console.table('channels', params),
    queryFn: () => fetchChannels(params),
    placeholderData: keepPreviousData,
  });

  const groupsQuery = useQuery({
    queryKey: queryKeys.console.detail('channels', 'groups'),
    queryFn: fetchGroups,
    staleTime: 5 * 60 * 1000,
  });

  const modelOptionsQuery = useQuery({
    queryKey: queryKeys.console.detail('channels', 'models'),
    queryFn: fetchChannelModelOptions,
    staleTime: 5 * 60 * 1000,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: queryKeys.console.root,
    });

  const manageChannelMutation = useMutation({
    mutationFn: async ({
      action,
      id,
      record,
      value,
    }: ManageChannelVariables) => {
      const data: LooseRecord = { id };
      let response;
      if (action === 'delete') {
        response = await API.delete(`/api/channel/${id}/`);
      } else if (action === 'enable' || action === 'disable') {
        data.status = action === 'enable' ? 1 : 2;
        response = await API.put('/api/channel/', data);
      } else if (action === 'priority') {
        data.priority = Number(value);
        response = await API.put('/api/channel/', data);
      } else if (action === 'weight') {
        data.weight = Math.max(0, Number(value));
        response = await API.put('/api/channel/', data);
      } else if (action === 'copy') {
        response = await API.post(`/api/channel/copy/${record?.id}`);
      } else if (action === 'balance') {
        response = await API.get(`/api/channel/update_balance/${record?.id}/`);
      } else if (action === 'test') {
        response = await API.get(`/api/channel/test/${record?.id}?model=`);
      }
      return response ? unwrap<unknown>(response) : undefined;
    },
    onSuccess: (payload, variables) => {
      if (variables.action === 'test') {
        showInfo(
          t('page.console.channel.testStarted', {
            name: variables.record?.name || variables.id,
          }),
        );
      } else {
        showSuccess(t('page.console.tables.actionSuccess'));
      }
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const saveChannelMutation = useMutation({
    mutationFn: async ({ editingChannel, values }: SaveChannelVariables) => {
      const payload = normalizeChannelPayload(values, editingChannel);
      const response = editingChannel?.id
        ? await API.put('/api/channel/', {
            ...payload,
            id: editingChannel.id,
          })
        : await API.post('/api/channel/', {
            channel: payload,
            mode: 'single',
          });
      return unwrap<unknown>(response);
    },
    onSuccess: (_payload, variables) => {
      showSuccess(
        variables.editingChannel?.id
          ? t('page.console.channel.form.saveSuccess.edit')
          : t('page.console.channel.form.saveSuccess.create'),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const fetchUpstreamModelsMutation = useMutation({
    mutationFn: async ({ editingChannel, values }: SaveChannelVariables) => {
      const response = editingChannel?.id
        ? await API.get(`/api/channel/fetch_models/${editingChannel.id}`, {
            ...skipErrorHandlerConfig,
          })
        : await API.post(
            '/api/channel/fetch_models',
            {
              base_url: values.base_url || '',
              key: values.key || '',
              type: Number(values.type || 1),
            },
            skipErrorHandlerConfig,
          );
      return splitListValue(unwrap<unknown>(response));
    },
    onSuccess: (models) => {
      showSuccess(
        t('page.console.channel.form.fetchModelsSuccess', {
          count: models.length,
        }),
      );
    },
    onError: (error) =>
      showError(error, t('page.console.channel.form.fetchModelsFailed')),
  });

  const bulkMutation = useMutation({
    mutationFn: async (action: BulkAction) => {
      let response;
      if (action === 'testAll') {
        response = await API.get('/api/channel/test');
      } else if (action === 'deleteDisabled') {
        response = await API.delete('/api/channel/disabled');
      } else if (action === 'updateBalance') {
        response = await API.get('/api/channel/update_balance');
      } else if (action === 'fix') {
        response = await API.post('/api/channel/fix');
      }
      return response ? unwrap<unknown>(response) : undefined;
    },
    onSuccess: () => {
      showSuccess(t('page.console.tables.actionSuccess'));
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const copyChannel = async (record: ChannelRecord) => {
    const text = [record?.id, record?.name, record?.key]
      .filter(Boolean)
      .join(' ');
    if (await copy(text)) {
      showSuccess(t('page.console.tables.copied'));
    } else {
      showError(t('page.console.tables.copyFailed'));
    }
  };

  const submitFilters = () => {
    localStorage.setItem('channel-status-filter', draftFilters.status);
    setFilters(draftFilters);
    setPage(1);
  };

  const resetFilters = () => {
    const next = getInitialFilters();
    setDraftFilters(next);
    setFilters(next);
    setPage(1);
  };

  const setPageSizeAndPersist = (nextSize: number) => {
    localStorage.setItem('page-size', String(nextSize));
    setPageSize(nextSize);
    setPage(1);
  };

  const setIdSortAndPersist = (checked: boolean) => {
    localStorage.setItem('id-sort', String(checked));
    setIdSort(checked);
    setPage(1);
  };

  const setTagModeAndPersist = (checked: boolean) => {
    localStorage.setItem('enable-tag-mode', String(checked));
    setTagMode(checked);
    setPage(1);
  };

  const pageData = channelsQuery.data || {
    items: [],
    page,
    pageSize,
    total: 0,
    meta: {},
  };

  return {
    bulkMutation,
    channels: pageData.items,
    copyChannel,
    channelModelOptions: modelOptionsQuery.data || [],
    draftFilters,
    fetchUpstreamModelsMutation,
    filters,
    groups: groupsQuery.data || [],
    idSort,
    isFetching: channelsQuery.isFetching,
    isLoading: channelsQuery.isLoading,
    manageChannelMutation,
    page: pageData.page || page,
    pageSize: pageData.pageSize || pageSize,
    refresh: () => invalidate(),
    resetFilters,
    saveChannelMutation,
    setDraftFilters,
    setFilters,
    setIdSort: setIdSortAndPersist,
    setPage,
    setPageSize: setPageSizeAndPersist,
    setTagMode: setTagModeAndPersist,
    submitFilters,
    t,
    tagMode,
    total: pageData.total,
    typeCounts:
      (pageData.meta?.type_counts as Record<string, number> | undefined) || {},
  };
}
