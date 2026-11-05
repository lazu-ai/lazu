import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { z } from 'zod';
import { CapacityFields, DecisionFields } from './DecisionSourceFields';
import { FieldLabel } from './FieldLabel';
import { SOURCE_TYPES } from './formatters';
import { makerChips, type SourceCatalog } from './sourceCatalog';
import {
  compileIgnore,
  serializeModelMapping,
  serveUpstreamIds,
  suggestPublicName,
} from './sourceModels';
import SourceModelsTable from './SourceModelsTable';
import SourceSyncPanel from './SourceSyncPanel';
import { sourceTypeLabel } from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { Field, FieldMessage } from '../../components/ui/field';
import { InlineNotice } from '../../components/ui/inline-notice';
import { Input } from '../../components/ui/input';
import { Select, SelectItem } from '../../components/ui/select';
import { Skeleton } from '../../components/ui/skeleton';
import { Switch } from '../../components/ui/switch';
import { Textarea } from '../../components/ui/textarea';
import {
  CHANNEL_OPTIONS,
  getChannelOptionLabel,
} from '../../constants/channel.constants';
import {
  inferProtocolDialect,
  protocolDialectAfterBaseURLChange,
} from '../../hooks/channels/channelProtocolDialect';
import {
  fitsStatusCodeMapping,
  isHeaderOverride,
  isParamOverride,
  isStatusCodeMapping,
  STATUS_CODE_MAPPING_MAX_LENGTH,
} from '../../hooks/channels/source-overrides';
import {
  getDefaultChannelFormValues,
  useChannelDetailQuery,
  useChannelsQuery,
} from '../../hooks/channels/useChannelsQuery';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import { makerLabel } from '../../lib/model-makers';
import type { UpstreamSyncView } from '../../hooks/models/useSourceUpstream';
import type { TFunction } from 'i18next';
import type { UseFormProps } from 'react-hook-form';

const ns = 'page.console.channel.form';

/* What a row of 成本明细 looks like, printed as a block rather than one
   unbroken line that ran past the dialog. */
const COST_PRICES_EXAMPLE = JSON.stringify(
  [
    {
      model_name: 'gpt-5.5',
      provider: 'manual',
      input_per_mtok: 5,
      output_per_mtok: 30,
      context_tier_threshold: 272000,
      context_tier_input_per_mtok: 10,
      context_tier_output_per_mtok: 45,
    },
  ],
  null,
  2,
);

/* A limit of 0 is no limit, so the input shows 不限 (its placeholder)
   rather than a 0 that reads as "allow nothing". The schema reads an empty
   input back as 0. */
const LIMIT_FIELDS = [
  'max_rps',
  'max_in_flight',
  'shared_max_rps',
  'shared_max_in_flight',
] as const;
const blankUnsetLimits = <T extends Partial<ChannelFormValues>>(values: T) => {
  const next = { ...values } as Record<string, unknown>;
  for (const name of LIMIT_FIELDS) {
    if (!Number(next[name])) next[name] = '';
  }
  return next as unknown as T;
};

/* 线路 tab sections, in order, for the anchor list beside them (canvas 3B). */
const ROUTING_SECTIONS = [
  'lane',
  'limit',
  'behave',
  'cost',
  'advanced',
] as const;
type RoutingSection = (typeof ROUTING_SECTIONS)[number];

type ChannelsQuery = ReturnType<typeof useChannelsQuery>;
type ChannelRecord = ChannelsQuery['channels'][number];

type OptionItem = { label: string; value: string };

export type ChannelFormValues = {
  setting?: string;
  decision_dialect: string;
  cf_account_id: string;
  decision_timeout_ms: number;
  max_rps: number;
  max_in_flight: number;
  shared_limit_group: string;
  shared_max_rps: number;
  shared_max_in_flight: number;

  auto_ban: boolean;
  source_type: string;
  base_url?: string;
  claude_code_client_only: boolean;
  claude_code_user_agent?: string;
  groups: string[];
  key?: string;
  models: string[];
  /** JSON, public name → upstream ID; "" when nothing is mapped. */
  model_mapping?: string;
  name: string;
  observe_new_routes: boolean;
  priority: number;
  protocol_dialect: string;
  provider_cost_prices?: string;
  remark?: string;
  param_override?: string;
  header_override?: string;
  status_code_mapping?: string;
  settings?: string;
  status: boolean;
  tag?: string;
  type: number;
  upstream_sync_ignore: string;
  upstream_sync_interval: number;
  upstream_sync_notify_ops: boolean;
  upstream_sync_on_gone: 'keep' | 'stop';
  upstream_sync_on_new: 'auto' | 'review';
  video_content_url_template?: string;
  video_envelope?: string;
  video_family?: string;
  video_status_url_template?: string;
  weight: number;
};

const splitFreeEntry = (value: unknown): string[] =>
  String(value || '')
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);

const uniqueValues = (values: string[]): string[] =>
  Array.from(new Set(values.filter(Boolean)));

const withSelectedOptions = (
  options: OptionItem[],
  selectedValues: unknown[],
): OptionItem[] => {
  const seen = new Set(options.map((option) => String(option.value)));
  const appended = selectedValues
    .map(String)
    .filter(Boolean)
    .filter((value) => {
      if (seen.has(value)) return false;
      seen.add(value);
      return true;
    })
    .map((value) => ({ label: value, value }));
  return [...options, ...appended];
};

/* cost_multiplier lives inside the source's settings JSON: upstream cost is
   the official list price times it whenever the source has no cost row of its
   own for a model. The field edits it as a percentage of list. */
function readCostPercent(settings: string | undefined): string {
  if (!settings?.trim()) return '';
  try {
    const parsed = JSON.parse(settings) as Record<string, unknown>;
    const multiplier = Number(parsed?.cost_multiplier);
    return multiplier > 0 ? String(Math.round(multiplier * 10000) / 100) : '';
  } catch {
    return '';
  }
}

/* Sets or removes one key of the settings JSON. null means the JSON in the
   box is not an object, so the field leaves it for the operator to fix. */
function writeSettingsKey(
  settings: string | undefined,
  key: string,
  value: number | undefined,
): string | null {
  let parsed: Record<string, unknown> = {};
  if (settings?.trim()) {
    try {
      const current = JSON.parse(settings) as unknown;
      if (!current || typeof current !== 'object' || Array.isArray(current))
        return null;
      parsed = current as Record<string, unknown>;
    } catch {
      return null;
    }
  }
  if (value === undefined) delete parsed[key];
  else parsed[key] = value;
  return Object.keys(parsed).length ? JSON.stringify(parsed, null, 2) : '';
}

function writeCostPercent(
  settings: string | undefined,
  percent: string,
): string | null {
  const number = Number(percent);
  return writeSettingsKey(
    settings,
    'cost_multiplier',
    percent.trim() && number > 0 ? number / 100 : undefined,
  );
}

/* first_event_timeout_seconds: how long a streamed attempt on this source
   waits for its first data event after the upstream's headers. Empty keeps
   the request's stream start deadline. */
function readFirstEventTimeout(settings: string | undefined): string {
  if (!settings?.trim()) return '';
  try {
    const parsed = JSON.parse(settings) as Record<string, unknown>;
    const seconds = Number(parsed?.first_event_timeout_seconds);
    return seconds > 0 ? String(seconds) : '';
  } catch {
    return '';
  }
}

function writeFirstEventTimeout(
  settings: string | undefined,
  seconds: string,
): string | null {
  const number = Math.trunc(Number(seconds));
  return writeSettingsKey(
    settings,
    'first_event_timeout_seconds',
    seconds.trim() && number > 0 ? number : undefined,
  );
}

const buildChannelFormSchema = (t: TFunction, isEdit: boolean) =>
  z.object({
    auto_ban: z.boolean(),
    base_url: z.string().trim().optional(),
    groups: z.array(z.string()).min(1, t(`${ns}.error.groups.required`)),
    key: isEdit
      ? z.string().optional()
      : z
          .string()
          .trim()
          .min(1, t(`${ns}.error.key.required`)),
    models: z.array(z.string()).min(1, t(`${ns}.error.models.required`)),
    model_mapping: z.string().optional(),
    name: z
      .string()
      .trim()
      .min(1, t(`${ns}.error.name.required`)),
    observe_new_routes: z.boolean(),
    claude_code_client_only: z.boolean(),
    claude_code_user_agent: z.string().trim().optional(),
    priority: z.coerce.number().min(0, t(`${ns}.error.priority.min`)),
    protocol_dialect: z.string().trim().min(1),
    setting: z.string().optional(),
    decision_dialect: z.string().trim(),
    cf_account_id: z.string().trim(),
    decision_timeout_ms: z.coerce.number().int().min(0),
    max_rps: z.coerce.number().int().min(0),
    max_in_flight: z.coerce.number().int().min(0),
    shared_limit_group: z.string().trim(),
    shared_max_rps: z.coerce.number().int().min(0),
    shared_max_in_flight: z.coerce.number().int().min(0),

    provider_cost_prices: z.string().optional(),
    remark: z.string().trim().optional(),
    param_override: z
      .string()
      .optional()
      .refine(isParamOverride, t(`${ns}.error.param_override.object`)),
    header_override: z
      .string()
      .optional()
      .refine(isHeaderOverride, t(`${ns}.error.header_override.object`)),
    status_code_mapping: z
      .string()
      .optional()
      .refine(isStatusCodeMapping, t(`${ns}.error.status_code_mapping.object`))
      .refine(
        fitsStatusCodeMapping,
        t(`${ns}.error.status_code_mapping.length`, {
          max: STATUS_CODE_MAPPING_MAX_LENGTH,
        }),
      ),
    settings: z
      .string()
      .optional()
      .refine(
        (value) => {
          if (!value?.trim()) return true;
          try {
            const parsed = JSON.parse(value) as unknown;
            return Boolean(
              parsed && typeof parsed === 'object' && !Array.isArray(parsed),
            );
          } catch {
            return false;
          }
        },
        t(`${ns}.error.settings.object`),
      ),
    source_type: z.string(),
    status: z.boolean(),
    tag: z.string().trim().optional(),
    type: z.coerce.number().min(1, t(`${ns}.error.type.required`)),
    upstream_sync_ignore: z.string().refine(
      (value) => compileIgnore(value).invalid.length === 0,
      t(`${ns}.error.upstream_sync_ignore`, {
        defaultValue: '有一条 re: 规则不是合法的正则表达式',
      }),
    ),
    upstream_sync_interval: z.coerce.number().int().min(0).max(168),
    upstream_sync_notify_ops: z.boolean(),
    upstream_sync_on_gone: z.enum(['keep', 'stop']),
    upstream_sync_on_new: z.enum(['auto', 'review']),
    video_content_url_template: z.string().trim().optional(),
    video_envelope: z
      .string()
      .trim()
      .optional()
      .refine(
        (value) => {
          if (!value?.trim()) return true;
          try {
            const parsed = JSON.parse(value) as unknown;
            return Boolean(
              parsed && typeof parsed === 'object' && !Array.isArray(parsed),
            );
          } catch {
            return false;
          }
        },
        t(`${ns}.error.video_envelope.json`, {
          defaultValue: '视频信封配置必须是 JSON 对象',
        }),
      ),
    video_family: z.string().trim().optional(),
    video_status_url_template: z.string().trim().optional(),
    weight: z.coerce.number().min(0, t(`${ns}.error.weight.min`)),
  });

/** The detail page's tabs that edit the source; 记录 reads only. */
export type SourceTab = 'connection' | 'models' | 'routing' | 'sync';

/** The page header's 保存 submits the form by this id. */
export const SOURCE_FORM_ID = 'source-form';

/* Which tab holds a field, so a save that fails validation can show the
   field that stopped it. Anything not listed sits on 线路. */
const FIELD_TAB: Partial<Record<keyof ChannelFormValues, SourceTab>> = {
  base_url: 'connection',
  claude_code_user_agent: 'connection',
  key: 'connection',
  model_mapping: 'models',
  models: 'models',
  name: 'connection',
  protocol_dialect: 'connection',
  source_type: 'connection',
  type: 'connection',
  upstream_sync_ignore: 'sync',
  upstream_sync_interval: 'sync',
  upstream_sync_notify_ops: 'sync',
  upstream_sync_on_gone: 'sync',
  upstream_sync_on_new: 'sync',
  video_content_url_template: 'connection',
  video_envelope: 'connection',
  video_status_url_template: 'connection',
};

/* 新建来源's type buttons: the adapters the console offers, in the order
   operators reach for them. */
const CREATE_TYPES: Array<[number, string, string]> = [
  [1, 'openai', 'OpenAI 兼容'],
  [14, 'anthropic', 'Anthropic'],
  [24, 'gemini', 'Gemini'],
  [48, 'xai', 'xAI'],
  [8, 'custom', '自定义'],
];

type SourceFormProps = {
  catalog: SourceCatalog;
  channels: ChannelsQuery;
  editingChannel: ChannelRecord | null;
  /** create: the one-page 新建来源. page: one tab of a source's page. */
  layout: 'create' | 'page';
  onCancel: () => void;
  onResolve?: (body: { gone?: string[]; new?: string[] }) => void;
  /** Called with the public ids the save returned (one when creating). */
  onSaved: (ids: string[]) => Promise<void> | void;
  onTabChange?: (tab: SourceTab) => void;
  resolving?: boolean;
  tab?: SourceTab;
  upstream?: UpstreamSyncView | null;
};

const savedIds = (result: unknown): string[] => {
  const ids = (result as { ids?: unknown } | null)?.ids;
  return Array.isArray(ids) ? ids.map(String) : [];
};

/**
 * The source form, laid out as a page.
 *
 * Creating is one page in three parts, each grey until the one above it is
 * done: the adapter, the connection (tested against the upstream), and which
 * of the models the upstream lists this source will serve.
 *
 * A saved source has a page of its own with tabs; this renders the tab that
 * edits, inside one form, so the header's 保存 writes every tab at once.
 */
export default function SourceForm({
  catalog,
  channels,
  editingChannel,
  layout,
  onCancel,
  onResolve,
  onSaved,
  onTabChange,
  resolving,
  tab = 'models',
  upstream,
}: SourceFormProps) {
  const { t } = channels;
  const isEdit = Boolean(editingChannel?.id);
  const creating = layout === 'create';
  const protocolDialectManuallySelected = useRef(false);
  const detailQuery = useChannelDetailQuery(editingChannel?.id, isEdit);
  /* The lane select names lanes the way the rest of the console does,
     never by slug. */
  const lanes = useChannelLanesQuery().data ?? [];

  const defaultValues = useMemo(() => {
    const values = getDefaultChannelFormValues() as ChannelFormValues;
    values.groups = [channels.groups[0] || values.groups[0] || 'direct'];
    return values;
  }, [channels.groups]);
  const schema = useMemo(() => buildChannelFormSchema(t, isEdit), [isEdit, t]);
  const form = useForm<ChannelFormValues>({
    defaultValues,
    resolver: zodResolver(
      schema,
    ) as unknown as UseFormProps<ChannelFormValues>['resolver'],
  });
  const values = form.watch();
  const loading =
    channels.saveChannelMutation.isPending ||
    channels.fetchUpstreamModelsMutation.isPending ||
    detailQuery.isFetching;
  /* Editing saves the whole record, so a form that never received the saved
     one must not be saved: it would write the defaults over every field the
     admin did not retype, the overrides among them. */
  const detailMissing = isEdit && !detailQuery.data;

  useEffect(() => {
    protocolDialectManuallySelected.current = isEdit;
    if (isEdit && detailQuery.data) {
      form.reset(blankUnsetLimits({ ...defaultValues, ...detailQuery.data }));
      return;
    }
    if (!isEdit) form.reset(blankUnsetLimits(defaultValues));
  }, [defaultValues, detailQuery.data, form, isEdit, editingChannel?.id]);

  const typeOptions = useMemo(
    () =>
      CHANNEL_OPTIONS.map((option) => ({
        label: getChannelOptionLabel(option, t),
        value: String(option.value),
      })),
    [t],
  );

  const groupOptions = useMemo(
    () =>
      withSelectedOptions(
        (lanes.length ? lanes.map((lane) => lane.slug) : channels.groups).map(
          (group) => ({ label: group, value: group }),
        ),
        values.groups || [],
      ).map((option) => ({
        ...option,
        label:
          lanes.find((lane) => lane.slug === option.value)?.name ||
          option.label,
      })),
    [channels.groups, lanes, values.groups],
  );

  const submit = form.handleSubmit(
    async (formValues) => {
      if (detailMissing) return;
      let result: unknown;
      try {
        result = await channels.saveChannelMutation.mutateAsync({
          editingChannel,
          values: formValues,
        });
      } catch {
        return;
      }
      await onSaved(savedIds(result));
    },
    (errors) => {
      if (creating) return;
      const first = Object.keys(errors)[0] as
        | keyof ChannelFormValues
        | undefined;
      if (!first) return;
      const holder = FIELD_TAB[first] ?? 'routing';
      if (holder !== tab) onTabChange?.(holder);
    },
  );

  const laneColor = (slug: string) =>
    lanes.find((lane) => lane.slug === slug)?.color || 'info';

  /* 视频上游族 only matters to a source that serves video: it sits with the
     credential once it is set (or for Custom, which always speaks the
     envelope), and waits in 高级设置 otherwise. */
  const videoInCredential =
    Boolean(values.video_family) || Number(values.type) === 8;
  const videoFamilyField = (
    <Field>
      <FieldLabel
        code='video_family'
        text={t(`${ns}.field.video_family`, {
          defaultValue: '视频上游族',
        })}
      />
      <Controller
        control={form.control}
        name='video_family'
        render={({ field }) => (
          <Select
            disabled={loading}
            value={field.value || 'auto'}
            onValueChange={(value) =>
              field.onChange(value === 'auto' ? '' : value)
            }
          >
            <SelectItem value='auto'>
              {t(`${ns}.option.videoFamily.auto`, {
                defaultValue: '自动（按类型与模型名）',
              })}
            </SelectItem>
            <SelectItem value='gateway'>
              {t(`${ns}.option.videoFamily.gateway`, {
                defaultValue: 'OpenAI 兼容网关（/v1/videos）',
              })}
            </SelectItem>
            <SelectItem value='ark'>
              {t(`${ns}.option.videoFamily.ark`, {
                defaultValue: '火山方舟原生（Seedance）',
              })}
            </SelectItem>
            <SelectItem value='gemini'>
              {t(`${ns}.option.videoFamily.gemini`, {
                defaultValue: 'Gemini（Veo）',
              })}
            </SelectItem>
            <SelectItem value='envelope'>
              {t(`${ns}.option.videoFamily.envelope`, {
                defaultValue: '统一信封聚合站',
              })}
            </SelectItem>
          </Select>
        )}
      />
      <FieldMessage>
        {t(`${ns}.help.video_family`, {
          defaultValue:
            '只影响 /v1/videos。自动：Gemini 类型走 Veo，模型名含 seedance 走方舟，Custom 走信封，其余走 OpenAI 兼容网关。',
        })}
      </FieldMessage>
    </Field>
  );

  /* The 线路 tab is long, so it carries an anchor list that follows the
     scroll: the section whose heading last passed the top is current. */
  const split = !creating && tab === 'routing';
  const scrollRef = useRef<HTMLDivElement>(null);
  const navLock = useRef(0);
  const [activeSection, setActiveSection] = useState<RoutingSection>('lane');
  useEffect(() => {
    const root = scrollRef.current;
    if (!split || !root) return;
    const onScroll = () => {
      if (Date.now() < navLock.current) return;
      const top = root.getBoundingClientRect().top;
      let current: RoutingSection = ROUTING_SECTIONS[0];
      for (const key of ROUTING_SECTIONS) {
        const node = root.querySelector<HTMLElement>(`#source-sect-${key}`);
        if (node && node.getBoundingClientRect().top - top <= 32) {
          current = key;
        }
      }
      if (root.scrollTop + root.clientHeight >= root.scrollHeight - 4) {
        current = ROUTING_SECTIONS[ROUTING_SECTIONS.length - 1];
      }
      setActiveSection(current);
    };
    root.addEventListener('scroll', onScroll, { passive: true });
    return () => root.removeEventListener('scroll', onScroll);
  }, [split]);
  const jumpTo = (key: RoutingSection) => {
    navLock.current = Date.now() + 700;
    setActiveSection(key);
    scrollRef.current
      ?.querySelector<HTMLElement>(`#source-sect-${key}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };
  const limited = LIMIT_FIELDS.some((name) => Number(values[name]) > 0);
  const costPercent = readCostPercent(values.settings);
  const sectionNav: Array<[RoutingSection, string, string | null]> = [
    [
      'lane',
      t(`${ns}.section.laneWeight`, { defaultValue: '线路与权重' }),
      null,
    ],
    [
      'limit',
      t(`${ns}.section.limit`, { defaultValue: '限流' }),
      limited
        ? t(`${ns}.section.limitSet`, { defaultValue: '已设' })
        : t(`${ns}.section.limitNone`, { defaultValue: '未限制' }),
    ],
    ['behave', t(`${ns}.section.behave`, { defaultValue: '行为' }), null],
    [
      'cost',
      t(`${ns}.section.cost`, { defaultValue: '成本基准' }),
      costPercent ? `${costPercent}%` : null,
    ],
    ['advanced', t(`${ns}.section.advanced`), null],
  ];

  /* Editing waits for the saved record before it shows a field: until then
     the body is the form's outline, not inputs holding defaults (canvas 6A). */
  const awaitingDetail = isEdit && !detailQuery.data && !detailQuery.isError;

  /* ---- 新建来源: test the connection, then pick from what it lists. ---- */
  const [listed, setListed] = useState<string[] | null>(null);
  const [testedAs, setTestedAs] = useState('');
  const [testMs, setTestMs] = useState(0);
  const [testError, setTestError] = useState('');
  const [manualMode, setManualMode] = useState(false);
  const [manual, setManual] = useState('');
  const [picked, setPicked] = useState<Set<string>>(() => new Set());
  const [pickQuery, setPickQuery] = useState('');
  const [pickMaker, setPickMaker] = useState<string | null>(null);
  /* A test holds for the connection it ran against; changing the type,
     address or key greys the model list until it runs again. */
  const connectionAs = `${values.type}|${String(values.base_url || '').trim()}|${String(values.key || '').trim()}`;
  const tested = listed !== null && testedAs === connectionAs;
  const pickOpen = tested || manualMode;
  const manualNames = uniqueValues(splitFreeEntry(manual));
  const pickRows = useMemo(
    () =>
      (listed ?? []).map((id) => ({
        id,
        name: suggestPublicName(id, catalog.names),
      })),
    [catalog.names, listed],
  );
  const pickNeedle = pickQuery.trim().toLowerCase();
  const pickBase = pickRows.filter(
    (row) =>
      !pickNeedle ||
      row.id.toLowerCase().includes(pickNeedle) ||
      row.name.toLowerCase().includes(pickNeedle),
  );
  const pickShown = pickBase.filter(
    (row) => pickMaker === null || catalog.maker(row.name) === pickMaker,
  );
  const pickChips = makerChips(
    pickRows.map((row) => row.name),
    catalog,
  );
  const pickedCount = tested
    ? pickRows.filter((row) => picked.has(row.id)).length + manualNames.length
    : manualNames.length;

  const testConnection = async () => {
    const ok = await form.trigger(['key']);
    if (!ok) return;
    const as = connectionAs;
    setTestError('');
    const started = performance.now();
    try {
      const fetched = uniqueValues(
        await channels.fetchUpstreamModelsMutation.mutateAsync({
          editingChannel: null,
          values: form.getValues(),
        }),
      );
      setTestMs(performance.now() - started);
      setListed(fetched);
      setTestedAs(as);
      setPicked((previous) => {
        const keep = new Set(fetched);
        return new Set([...previous].filter((id) => keep.has(id)));
      });
      if (!fetched.length) setManualMode(true);
    } catch (error) {
      setListed(null);
      setTestError(
        error instanceof Error && error.message
          ? error.message
          : t(`${ns}.create.testFailed`, { defaultValue: '没连上' }),
      );
    }
  };

  const create = () => {
    const ids = tested
      ? pickRows.filter((row) => picked.has(row.id)).map((row) => row.id)
      : [];
    const served = serveUpstreamIds([], {}, ids, catalog.names);
    const models = uniqueValues([...served.models, ...manualNames]);
    form.setValue('models', models);
    form.setValue('model_mapping', serializeModelMapping(served.mapping));
    void submit();
  };

  const credentialGrid = (
    <div className='mo-form__grid'>
      <Field>
        <label className='lazu-label' htmlFor='channel-name'>
          {t(`${ns}.field.name`)}
        </label>
        <Input
          disabled={loading}
          id='channel-name'
          placeholder={t(`${ns}.placeholder.name`)}
          {...form.register('name')}
        />
        <FieldMessage error>{form.formState.errors.name?.message}</FieldMessage>
      </Field>
      <Field>
        {/* Channel.Type is the API-protocol adapter; SourceType is
          where the supply comes from. They are independent — a
          cloud resale and a first-party account can speak the same
          protocol. */}
        <label className='lazu-label'>
          {t(`${ns}.field.source_type`, {
            defaultValue: '来源类型',
          })}
        </label>
        <Controller
          control={form.control}
          name='source_type'
          render={({ field }) => (
            <Select
              disabled={loading}
              value={String(field.value || 'unclassified')}
              onValueChange={field.onChange}
            >
              {SOURCE_TYPES.map((option) => (
                <SelectItem key={option} value={option}>
                  {sourceTypeLabel(option, t)}
                </SelectItem>
              ))}
            </Select>
          )}
        />
        <FieldMessage>
          {t(`${ns}.help.source_type`, {
            defaultValue:
              '官方 / 云厂商 / 聚合 / 非官方。只影响台账分类与筛选，不改变路由。',
          })}
        </FieldMessage>
      </Field>
      <Field>
        <FieldLabel code='type' text={t(`${ns}.field.type`)} />
        <Controller
          control={form.control}
          name='type'
          render={({ field }) => (
            <Select
              disabled={loading}
              placeholder={t(`${ns}.placeholder.type`)}
              value={String(field.value || '')}
              onValueChange={(value) => {
                field.onChange(value);
                protocolDialectManuallySelected.current = false;
                form.setValue(
                  'protocol_dialect',
                  inferProtocolDialect(form.getValues('base_url'), value),
                  { shouldDirty: true, shouldValidate: true },
                );
              }}
            >
              {typeOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </Select>
          )}
        />
        <FieldMessage error>{form.formState.errors.type?.message}</FieldMessage>
      </Field>
      <Field>
        <FieldLabel
          code='protocol_dialect'
          text={t(`${ns}.field.protocol_dialect`, {
            defaultValue: '协议方言',
          })}
        />
        <Controller
          control={form.control}
          name='protocol_dialect'
          render={({ field }) => (
            <Select
              disabled={loading}
              value={field.value || 'openai_chat_compatible'}
              onValueChange={(value) => {
                protocolDialectManuallySelected.current = true;
                field.onChange(value);
              }}
            >
              <SelectItem value='openai_chat_compatible'>
                {t(`${ns}.option.protocolDialect.compatible`, {
                  defaultValue: 'OpenAI-compatible（保守）',
                })}
              </SelectItem>
              <SelectItem value='openai'>OpenAI</SelectItem>
              <SelectItem value='azure_openai'>Azure OpenAI</SelectItem>
              <SelectItem value='openrouter'>OpenRouter</SelectItem>
              <SelectItem value='deepseek'>DeepSeek</SelectItem>
              <SelectItem value='cloudflare_ai_v1'>
                Cloudflare Workers AI
              </SelectItem>
              <SelectItem value='anthropic'>Anthropic</SelectItem>
              <SelectItem value='gemini'>Gemini</SelectItem>
            </Select>
          )}
        />
        <FieldMessage>
          {t(`${ns}.help.protocol_dialect`, {
            defaultValue:
              '决定原生端点与 wire 能力；Cloudflare Workers AI 请选择 Cloudflare。',
          })}
        </FieldMessage>
      </Field>
      {Number(values.type) === 14 ? (
        <Field className='mo-form__wide'>
          <Controller
            control={form.control}
            name='claude_code_client_only'
            render={({ field }) => (
              <Switch
                checked={field.value}
                disabled={loading}
                onCheckedChange={field.onChange}
              >
                {t(`${ns}.field.claude_code_client_only`, {
                  defaultValue: '只给 Claude Code 用',
                })}
              </Switch>
            )}
          />
          <FieldMessage>
            {t(`${ns}.help.claude_code_client_only`, {
              defaultValue:
                '上游只接受官方 Claude Code 客户端时打开。只有 Claude Code 的请求会路由到这里，并带上它自己的客户端标识。抽测和观察检查使用网关配置的探测身份；下面可以只为这个来源改 User-Agent。',
            })}
          </FieldMessage>
          {values.claude_code_client_only ? (
            <Input
              aria-label={t(`${ns}.field.claude_code_user_agent`, {
                defaultValue: '这个来源的抽测 User-Agent',
              })}
              disabled={loading}
              {...form.register('claude_code_user_agent')}
            />
          ) : null}
        </Field>
      ) : null}
      <Field className='mo-form__wide'>
        <label className='lazu-label' htmlFor='channel-key'>
          {t(`${ns}.field.key`)}
        </label>
        <Textarea
          disabled={loading}
          id='channel-key'
          placeholder={t(`${ns}.placeholder.key`)}
          rows={3}
          {...form.register('key')}
        />
        <FieldMessage>
          {isEdit ? t(`${ns}.help.keyEdit`) : t(`${ns}.help.keyCreate`)}
        </FieldMessage>
        <FieldMessage error>{form.formState.errors.key?.message}</FieldMessage>
      </Field>
      <Field className='mo-form__wide'>
        <label className='lazu-label' htmlFor='channel-base-url'>
          {t(`${ns}.field.base_url`)}
        </label>
        <Input
          disabled={loading}
          id='channel-base-url'
          placeholder={t(`${ns}.placeholder.base_url`)}
          {...form.register('base_url', {
            onChange: (event) => {
              form.setValue(
                'protocol_dialect',
                protocolDialectAfterBaseURLChange(
                  event.target.value,
                  form.getValues('type'),
                  form.getValues('protocol_dialect'),
                  protocolDialectManuallySelected.current,
                ),
                { shouldDirty: true, shouldValidate: true },
              );
            },
          })}
        />
      </Field>
      {videoInCredential ? videoFamilyField : null}
      {values.video_family === 'envelope' || Number(values.type) === 8 ? (
        <Field className='mo-form__wide'>
          <FieldLabel
            code='video_envelope'
            htmlFor='channel-video-envelope'
            text={t(`${ns}.field.video_envelope`, {
              defaultValue: '视频信封配置',
            })}
          />
          <Textarea
            disabled={loading}
            id='channel-video-envelope'
            placeholder={t(`${ns}.placeholder.video_envelope`, {
              defaultValue:
                '{"preset":"tasks"} 或 {"submit_url":"/v1/videos/generations","status_url":"/v1/tasks/{id}","job_id_paths":["data.0.task_id"],"status_path":"data.status","artifact_paths":["data.result.videos.0.url.0"],"cost_path":"data.cost"}',
            })}
            rows={4}
            {...form.register('video_envelope')}
          />
          <FieldMessage>
            {t(`${ns}.help.video_envelope`, {
              defaultValue:
                'preset 为 tasks 或 openai；显式字段覆盖预设。URL 可相对基础地址，{id} 为上游任务 id。',
            })}
          </FieldMessage>
          <FieldMessage error>
            {form.formState.errors.video_envelope?.message}
          </FieldMessage>
        </Field>
      ) : null}
      {Number(values.type) === 8 ? (
        <>
          <Field className='mo-form__wide'>
            <label
              className='lazu-label'
              htmlFor='channel-video-status-url-template'
            >
              {t(`${ns}.field.video_status_url_template`)}
            </label>
            <Input
              disabled={loading}
              id='channel-video-status-url-template'
              placeholder={t(`${ns}.placeholder.video_status_url_template`)}
              {...form.register('video_status_url_template')}
            />
            <FieldMessage>
              {t(`${ns}.help.video_status_url_template`)}
            </FieldMessage>
          </Field>
          <Field className='mo-form__wide'>
            <label
              className='lazu-label'
              htmlFor='channel-video-content-url-template'
            >
              {t(`${ns}.field.video_content_url_template`)}
            </label>
            <Input
              disabled={loading}
              id='channel-video-content-url-template'
              placeholder={t(`${ns}.placeholder.video_content_url_template`)}
              {...form.register('video_content_url_template')}
            />
            <FieldMessage>
              {t(`${ns}.help.video_content_url_template`)}
            </FieldMessage>
          </Field>
        </>
      ) : null}
    </div>
  );

  const routingSections = (
    <>
      <section className='mo-form-sect' id='source-sect-lane'>
        <h3 className='mo-form-sect__title'>
          {t(`${ns}.section.laneWeight`, {
            defaultValue: '线路与权重',
          })}
        </h3>
        <div className='mo-form__grid mo-form__grid--3'>
          <Field>
            <FieldLabel
              text={t(`${ns}.field.groups`, { defaultValue: '线路' })}
            />
            <Controller
              control={form.control}
              name='groups'
              render={({ field }) => (
                <Select
                  aria-label={t(`${ns}.field.groups`, {
                    defaultValue: '线路',
                  })}
                  disabled={loading}
                  value={field.value?.[0] || 'direct'}
                  onValueChange={(value) => field.onChange([value])}
                >
                  {groupOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      <span className='mo-lane-option'>
                        <span
                          aria-hidden='true'
                          className={`mo-dot mo-dot--lane-${laneColor(option.value)}`}
                        />
                        {option.label}
                      </span>
                    </SelectItem>
                  ))}
                </Select>
              )}
            />
            <FieldMessage error>
              {form.formState.errors.groups?.message}
            </FieldMessage>
          </Field>
          <Field>
            <FieldLabel
              code='weight'
              htmlFor='channel-weight'
              text={t(`${ns}.field.weight`)}
              tip={t(`${ns}.help.weightOverride`, {
                defaultValue:
                  '这个来源在各线路池里的默认权重；模型页里按模型改过的以那边为准。',
              })}
            />
            <Input
              disabled={loading}
              id='channel-weight'
              min='0'
              type='number'
              {...form.register('weight')}
            />
            <FieldMessage>
              {t(`${ns}.help.weightShort`, {
                defaultValue: '同一线路里按权重分流。',
              })}
            </FieldMessage>
            <FieldMessage error>
              {form.formState.errors.weight?.message}
            </FieldMessage>
          </Field>
          <Field>
            <FieldLabel
              code='priority'
              htmlFor='channel-priority'
              text={t(`${ns}.field.priority`)}
            />
            <Input
              disabled={loading}
              id='channel-priority'
              min='0'
              type='number'
              {...form.register('priority')}
            />
            <FieldMessage>
              {t(`${ns}.help.priorityShort`, {
                defaultValue: '高的先用，相同再看权重。',
              })}
            </FieldMessage>
            <FieldMessage error>
              {form.formState.errors.priority?.message}
            </FieldMessage>
          </Field>
        </div>
      </section>

      <section className='mo-form-sect' id='source-sect-limit'>
        <h3 className='mo-form-sect__title'>
          {t(`${ns}.section.limit`, { defaultValue: '限流' })}
          <span className='mo-form-sect__note'>
            {t(`${ns}.section.limitShort`, {
              defaultValue: '留空即不限',
            })}
          </span>
        </h3>
        <CapacityFields disabled={loading} form={form} />
      </section>

      <section className='mo-form-sect' id='source-sect-behave'>
        <h3 className='mo-form-sect__title'>
          {t(`${ns}.section.behave`, { defaultValue: '行为' })}
        </h3>
        <div className='mo-form__switches'>
          <Controller
            control={form.control}
            name='auto_ban'
            render={({ field }) => (
              <Switch
                checked={field.value}
                disabled={loading}
                onCheckedChange={field.onChange}
              >
                {t(`${ns}.field.auto_ban`)}
              </Switch>
            )}
          />
          <Controller
            control={form.control}
            name='observe_new_routes'
            render={({ field }) => (
              <Switch
                checked={field.value}
                disabled={loading}
                onCheckedChange={field.onChange}
              >
                {t(`${ns}.field.observe_new_routes`, {
                  defaultValue: '先观察再上线',
                })}
              </Switch>
            )}
          />
          <FieldMessage className='mo-form__switch-note'>
            {t(`${ns}.help.observe_new_routes`, {
              defaultValue:
                '新接入的模型先放进库存，只被探测、不接流量；观察期满且各项检查通过后，在来源台账或模型的线路卡上线。已在服务的模型不受影响。',
            })}
          </FieldMessage>
        </div>
        <div className='mo-form__grid'>
          <Field>
            <FieldLabel
              code='first_event_timeout_seconds'
              htmlFor='channel-first-event-timeout'
              text={t(`${ns}.field.firstEventTimeout`, {
                defaultValue: '首个事件等待（秒）',
              })}
            />
            <Input
              disabled={loading}
              id='channel-first-event-timeout'
              inputMode='numeric'
              min='0'
              max='3600'
              step='1'
              type='number'
              placeholder='90'
              value={readFirstEventTimeout(values.settings)}
              onChange={(event) => {
                const next = writeFirstEventTimeout(
                  form.getValues('settings'),
                  event.target.value,
                );
                if (next === null) return;
                form.setValue('settings', next, {
                  shouldDirty: true,
                  shouldValidate: true,
                });
              }}
            />
            <FieldMessage>
              {t(`${ns}.help.firstEventTimeout`, {
                defaultValue:
                  '流式请求收到上游响应头后，最多等多久第一个数据事件。上游先回响应头、再思考很久才出字的，调大；回了头常卡住不出字的，调小。留空按请求的起流时限（90 秒；已换过渠道时放宽到 STREAMING_TIMEOUT）。',
              })}
            </FieldMessage>
          </Field>
          <DecisionFields disabled={loading} form={form} />
        </div>
      </section>

      <section className='mo-form-sect' id='source-sect-cost'>
        <h3 className='mo-form-sect__title'>
          {t(`${ns}.section.cost`, { defaultValue: '成本基准' })}
          <span className='mo-form-sect__note'>
            {t(`${ns}.section.costShort`, {
              defaultValue: '· 用来算毛利，不影响售价',
            })}
          </span>
        </h3>
        <div className='mo-form__grid'>
          <Field>
            <FieldLabel
              code='cost_multiplier'
              htmlFor='channel-cost'
              text={t(`${ns}.field.costPercent`, {
                defaultValue: '成本折扣',
              })}
            />
            <Input
              disabled={loading}
              id='channel-cost'
              inputMode='decimal'
              min='0'
              max='1000'
              step='0.1'
              type='number'
              placeholder='100'
              value={readCostPercent(values.settings)}
              onChange={(event) => {
                const next = writeCostPercent(
                  form.getValues('settings'),
                  event.target.value,
                );
                if (next === null) return;
                form.setValue('settings', next, {
                  shouldDirty: true,
                  shouldValidate: true,
                });
              }}
            />
            <FieldMessage>
              {t(`${ns}.help.costPercent`, {
                defaultValue:
                  '按官方价的百分之几进货，30 就是三折。没单独配成本价的模型按这个算上游成本和毛利；留空按官方价。',
              })}
            </FieldMessage>
          </Field>
          <Field className='mo-form__wide'>
            <FieldLabel
              code='provider_cost_prices'
              htmlFor='channel-cost-prices'
              text={t(`${ns}.field.provider_cost_prices`, {
                defaultValue: '成本明细',
              })}
              tip={t(`${ns}.help.costTier`, {
                defaultValue:
                  '这条来源按长上下文加价时才填 context_tier_*：输入（含缓存）超过阈值，整单按分档成本算。不填就按基础成本算；官方价兜底行会自动带上官方分档。',
              })}
            />
            <Textarea
              disabled={loading}
              id='channel-cost-prices'
              rows={4}
              {...form.register('provider_cost_prices')}
            />
            <span className='mo-example-label'>
              {t(`${ns}.help.example`, { defaultValue: '示例' })}
            </span>
            <pre className='mo-example'>{COST_PRICES_EXAMPLE}</pre>
          </Field>
        </div>
      </section>

      <section className='mo-form-sect' id='source-sect-advanced'>
        <h3 className='mo-form-sect__title'>
          {t(`${ns}.section.advanced`)}
          <span className='mo-form-sect__note'>
            {t(`${ns}.section.advancedShort`, {
              defaultValue: '· 模型映射 · 参数覆盖 · 备注',
            })}
          </span>
        </h3>
        <div className='mo-form__grid'>
          <Field>
            <FieldLabel htmlFor='channel-tag' text={t(`${ns}.field.tag`)} />
            <Input
              disabled={loading}
              id='channel-tag'
              placeholder={t(`${ns}.placeholder.tag`)}
              {...form.register('tag')}
            />
          </Field>
          {videoInCredential ? null : videoFamilyField}

          <Field className='mo-form__wide'>
            <label className='lazu-label' htmlFor='channel-remark'>
              {t(`${ns}.field.remark`)}
            </label>
            <Input
              disabled={loading}
              id='channel-remark'
              placeholder={t(`${ns}.placeholder.remark`)}
              {...form.register('remark')}
            />
          </Field>
          <Field className='mo-form__wide'>
            <FieldLabel
              code='param_override'
              htmlFor='channel-param-override'
              text={t(`${ns}.field.param_override`)}
            />
            <Textarea
              disabled={loading}
              id='channel-param-override'
              placeholder={t(`${ns}.placeholder.param_override`)}
              rows={4}
              {...form.register('param_override')}
            />
            <FieldMessage>{t(`${ns}.help.param_override`)}</FieldMessage>
            <FieldMessage error>
              {form.formState.errors.param_override?.message}
            </FieldMessage>
          </Field>
          <Field className='mo-form__wide'>
            <FieldLabel
              code='header_override'
              htmlFor='channel-header-override'
              text={t(`${ns}.field.header_override`)}
            />
            <Textarea
              disabled={loading}
              id='channel-header-override'
              placeholder={t(`${ns}.placeholder.header_override`)}
              rows={3}
              {...form.register('header_override')}
            />
            <FieldMessage>{t(`${ns}.help.header_override`)}</FieldMessage>
            <FieldMessage error>
              {form.formState.errors.header_override?.message}
            </FieldMessage>
          </Field>
          <Field className='mo-form__wide'>
            <FieldLabel
              code='status_code_mapping'
              htmlFor='channel-status-code-mapping'
              text={t(`${ns}.field.status_code_mapping`)}
            />
            <Textarea
              disabled={loading}
              id='channel-status-code-mapping'
              placeholder={t(`${ns}.placeholder.status_code_mapping`)}
              rows={3}
              {...form.register('status_code_mapping')}
            />
            <FieldMessage>{t(`${ns}.help.status_code_mapping`)}</FieldMessage>
            <FieldMessage error>
              {form.formState.errors.status_code_mapping?.message}
            </FieldMessage>
          </Field>
          <Field className='mo-form__wide'>
            <FieldLabel
              code='settings'
              htmlFor='channel-settings'
              text={t(`${ns}.field.settings`)}
            />
            <Textarea
              disabled={loading}
              id='channel-settings'
              placeholder={t(`${ns}.placeholder.settings`)}
              rows={5}
              {...form.register('settings')}
            />
            <FieldMessage>{t(`${ns}.help.settings`)}</FieldMessage>
            <FieldMessage error>
              {form.formState.errors.settings?.message}
            </FieldMessage>
          </Field>
        </div>
      </section>
    </>
  );

  const detailError =
    isEdit && detailQuery.isError && !detailQuery.data ? (
      <InlineNotice
        tone='danger'
        title={t(`${ns}.detailError.title`)}
        description={t(`${ns}.detailError.description`)}
        actions={
          <Button
            size='sm'
            type='button'
            variant='secondary'
            loading={detailQuery.isFetching}
            onClick={() => void detailQuery.refetch()}
          >
            {t('common.retry')}
          </Button>
        }
      />
    ) : null;

  if (creating) {
    const typeValue = Number(values.type || 1);
    return (
      <form
        className='src-create'
        id={SOURCE_FORM_ID}
        onSubmit={(event) => {
          event.preventDefault();
          if (pickOpen && pickedCount > 0) create();
        }}
      >
        <section className='src-panel src-step src-step--line'>
          <span className='src-step__num'>1</span>
          <span className='src-step__title src-step__title--fixed'>
            {t(`${ns}.create.type`, { defaultValue: '类型' })}
          </span>
          <span className='src-step__types' role='radiogroup'>
            {CREATE_TYPES.map(([value, key, label]) => (
              <button
                data-owns-chrome=''
                aria-checked={typeValue === value}
                className={`src-toggle${typeValue === value ? ' is-on' : ''}`}
                disabled={loading}
                key={key}
                role='radio'
                type='button'
                onClick={() => {
                  form.setValue('type', value, { shouldDirty: true });
                  protocolDialectManuallySelected.current = false;
                  form.setValue(
                    'protocol_dialect',
                    inferProtocolDialect(form.getValues('base_url'), value),
                    { shouldDirty: true },
                  );
                }}
              >
                {t(`${ns}.create.typeOption.${key}`, { defaultValue: label })}
              </button>
            ))}
          </span>
          <span className='src-hint src-step__aside'>
            {t(`${ns}.create.typeHint`, {
              defaultValue: '聚合商大多选「OpenAI 兼容」',
            })}
          </span>
        </section>

        <section className='src-panel src-step'>
          <div className='src-step__head'>
            <span className='src-step__num'>2</span>
            <span className='src-step__title'>
              {t(`${ns}.create.connection`, { defaultValue: '连接' })}
            </span>
          </div>
          <div className='src-connect'>
            <Field>
              <label className='lazu-label' htmlFor='channel-name'>
                {t(`${ns}.create.name`, { defaultValue: '名称' })}
              </label>
              <Input
                disabled={loading}
                id='channel-name'
                placeholder={t(`${ns}.placeholder.name`)}
                {...form.register('name')}
              />
            </Field>
            <Field>
              <label className='lazu-label' htmlFor='channel-base-url'>
                {t(`${ns}.create.baseUrl`, { defaultValue: '地址' })}
              </label>
              <Input
                className='src-mono-input'
                disabled={loading}
                id='channel-base-url'
                placeholder={t(`${ns}.placeholder.base_url`)}
                {...form.register('base_url', {
                  onChange: (event) => {
                    form.setValue(
                      'protocol_dialect',
                      protocolDialectAfterBaseURLChange(
                        event.target.value,
                        form.getValues('type'),
                        form.getValues('protocol_dialect'),
                        protocolDialectManuallySelected.current,
                      ),
                    );
                  },
                })}
              />
            </Field>
            <Field>
              <label className='lazu-label' htmlFor='channel-key'>
                {t(`${ns}.create.key`, { defaultValue: '密钥' })}
              </label>
              <Input
                autoComplete='off'
                className='src-mono-input'
                disabled={loading}
                id='channel-key'
                placeholder={t(`${ns}.create.keyPlaceholder`, {
                  defaultValue: 'sk-…',
                })}
                type='password'
                {...form.register('key')}
              />
            </Field>
            <Button
              disabled={loading || !String(values.key || '').trim()}
              loading={channels.fetchUpstreamModelsMutation.isPending}
              size='md'
              type='button'
              variant='secondary'
              onClick={() => void testConnection()}
            >
              {tested
                ? t(`${ns}.create.retest`, { defaultValue: '重新测试' })
                : t(`${ns}.create.test`, { defaultValue: '测试连接' })}
            </Button>
          </div>
          {form.formState.errors.name || form.formState.errors.key ? (
            <FieldMessage error>
              {form.formState.errors.name?.message ||
                form.formState.errors.key?.message}
            </FieldMessage>
          ) : null}
          <div
            className={`src-connect__msg${tested ? ' is-ok' : testError ? ' is-bad' : ''}`}
            role='status'
          >
            {tested
              ? t(`${ns}.create.testOk`, {
                  count: listed?.length ?? 0,
                  defaultValue:
                    '✓ 连上了 · {{seconds}} s · 上游列出 {{count}} 个模型',
                  seconds: (testMs / 1000).toFixed(2),
                })
              : testError
                ? t(`${ns}.create.testBad`, {
                    defaultValue: '没连上：{{error}}',
                    error: testError,
                  })
                : t(`${ns}.create.testFirst`, {
                    defaultValue: '先测试连接，通过后下面才会列出上游的模型。',
                  })}
            {testError && !manualMode ? (
              <>
                {' '}
                <Button
                  size='xs'
                  type='button'
                  variant='link'
                  onClick={() => setManualMode(true)}
                >
                  {t(`${ns}.create.manualLink`, {
                    defaultValue: '上游列不出模型？手动填写',
                  })}
                </Button>
              </>
            ) : null}
          </div>
        </section>

        <section
          className={`src-panel src-step src-pick${pickOpen ? '' : ' is-waiting'}`}
        >
          <div className='src-step__head'>
            <span className='src-step__num'>3</span>
            <span className='src-step__title'>
              {t(`${ns}.create.models`, { defaultValue: '供货哪些模型' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.create.modelsHint`, {
                defaultValue:
                  '上游列出的模型都在下面，一个都没选；选中的才会接流量',
              })}
            </span>
            <span className='src-mono src-step__aside'>
              {t(`${ns}.create.picked`, {
                count: pickedCount,
                defaultValue: '已选 {{count}}',
              })}
            </span>
          </div>
          {tested && pickRows.length ? (
            <>
              <div className='src-pick__tools'>
                <Input
                  aria-label={t(`${ns}.create.search`, {
                    defaultValue: '搜上游模型',
                  })}
                  className='src-pick__search'
                  placeholder={t(`${ns}.create.search`, {
                    defaultValue: '搜上游模型',
                  })}
                  value={pickQuery}
                  onChange={(event) => setPickQuery(event.target.value)}
                />
                <button
                  data-owns-chrome=''
                  aria-pressed={pickMaker === null}
                  className={`src-chip${pickMaker === null ? ' is-on' : ''}`}
                  type='button'
                  onClick={() => setPickMaker(null)}
                >
                  {t(`${ns}.create.allMakers`, { defaultValue: '全部' })}{' '}
                  <span className='src-num'>{pickRows.length}</span>
                </button>
                {pickChips.map((chip) => (
                  <button
                    data-owns-chrome=''
                    aria-pressed={pickMaker === chip.key}
                    className={`src-chip${pickMaker === chip.key ? ' is-on' : ''}`}
                    key={chip.key || 'other'}
                    type='button'
                    onClick={() =>
                      setPickMaker(pickMaker === chip.key ? null : chip.key)
                    }
                  >
                    {makerLabel(chip.key, t)}{' '}
                    <span className='src-num'>{chip.count}</span>
                  </button>
                ))}
                <Button
                  className='src-pick__all'
                  disabled={!pickShown.length}
                  size='sm'
                  type='button'
                  variant='secondary'
                  onClick={() =>
                    setPicked((previous) => {
                      const next = new Set(previous);
                      for (const row of pickShown) next.add(row.id);
                      return next;
                    })
                  }
                >
                  {t(`${ns}.create.pickShown`, {
                    count: pickShown.length,
                    defaultValue: '全选筛出的 {{count}} 个',
                  })}
                </Button>
                <Button
                  disabled={!picked.size}
                  size='sm'
                  type='button'
                  variant='secondary'
                  onClick={() => setPicked(new Set())}
                >
                  {t(`${ns}.create.clear`, { defaultValue: '清空' })}
                </Button>
              </div>
              <div className='src-pickrow src-pickrow--head'>
                <span />
                <span className='src-th'>
                  {t(`${ns}.create.colId`, {
                    defaultValue: '上游模型 ID → 对外名',
                  })}
                </span>
                <span className='src-th'>
                  {t(`${ns}.create.colMaker`, { defaultValue: '厂商' })}
                </span>
                <span className='src-th is-num'>
                  {t(`${ns}.create.colPrice`, { defaultValue: '目录价 入/出' })}
                </span>
                <span className='src-th is-num'>
                  {t(`${ns}.create.colCatalog`, { defaultValue: '目录' })}
                </span>
              </div>
              <div className='src-pick__rows'>
                {pickShown.map((row) => {
                  const on = picked.has(row.id);
                  const known = catalog.names.has(row.name);
                  const maker = catalog.maker(row.name);
                  return (
                    <label
                      className={`src-pickrow${on ? ' is-on' : ''}`}
                      key={row.id}
                    >
                      <Checkbox
                        aria-label={row.id}
                        checked={on}
                        onCheckedChange={(checked: boolean) =>
                          setPicked((previous) => {
                            const next = new Set(previous);
                            if (checked) next.add(row.id);
                            else next.delete(row.id);
                            return next;
                          })
                        }
                      />
                      <span className='src-mono src-pickrow__id'>
                        {row.id}
                        {row.name !== row.id ? (
                          <span className='src-soft'>{`  →  ${row.name}`}</span>
                        ) : null}
                      </span>
                      <span className='src-hint'>
                        {maker ? makerLabel(maker, t) : '—'}
                      </span>
                      <span className='src-mono is-num'>
                        {catalog.price(row.name)}
                      </span>
                      <span
                        className={`src-pickrow__catalog is-num${known ? '' : ' is-new'}`}
                      >
                        {known
                          ? t(`${ns}.create.inCatalog`, {
                              defaultValue: '已收录',
                            })
                          : t(`${ns}.create.notInCatalog`, {
                              defaultValue: '未收录 · 创建时补资料',
                            })}
                      </span>
                    </label>
                  );
                })}
                {pickShown.length === 0 ? (
                  <div className='src-empty'>
                    {t(`${ns}.create.noMatch`, {
                      defaultValue: '没有符合筛选的模型',
                    })}
                  </div>
                ) : null}
              </div>
            </>
          ) : null}
          {manualMode ? (
            <div className='src-pick__manual'>
              <label className='lazu-label' htmlFor='channel-manual-models'>
                {t(`${ns}.create.manual`, {
                  defaultValue: tested
                    ? '再加上游列表里没有的模型'
                    : '手动填要供货的模型名',
                })}
              </label>
              <Textarea
                id='channel-manual-models'
                placeholder={t(`${ns}.create.manualPlaceholder`, {
                  defaultValue: '每行一个，或用逗号分开',
                })}
                rows={3}
                value={manual}
                onChange={(event) => setManual(event.target.value)}
              />
            </div>
          ) : null}
          {!pickOpen ? <div className='src-pick__placeholder' /> : null}
        </section>

        <div className='src-create__foot'>
          <span className='src-hint'>
            {t(`${ns}.create.footHint`, {
              defaultValue:
                '创建后默认每 6 小时检查上游；新出现的模型先不供货，等你核对。可在详情页「同步」里改。',
            })}
          </span>
          {form.formState.errors.models ? (
            <FieldMessage error>
              {form.formState.errors.models.message}
            </FieldMessage>
          ) : null}
          <Button
            className='src-create__cancel'
            disabled={channels.saveChannelMutation.isPending}
            size='md'
            type='button'
            variant='secondary'
            onClick={onCancel}
          >
            {t(`${ns}.action.cancel`)}
          </Button>
          <Button
            disabled={!pickOpen || pickedCount === 0 || loading}
            loading={channels.saveChannelMutation.isPending}
            size='md'
            type='submit'
            variant='primary'
          >
            {t(`${ns}.create.submit`, {
              count: pickedCount,
              defaultValue: '创建来源并供货 {{count}} 个',
            })}
          </Button>
        </div>
      </form>
    );
  }

  return (
    <>
      {detailError}
      {awaitingDetail ? (
        <div
          aria-busy='true'
          aria-label={t(`${ns}.loadingDetail`, {
            defaultValue: '正在读取来源详情',
          })}
          className='src-panel src-panel--pad mo-form-skeleton'
          role='status'
        >
          {[56, 56, 72, 72].map((width, index) => (
            <span className='mo-form-skeleton__field' key={index}>
              <Skeleton height={10} width={width} />
              <Skeleton height={36} />
            </span>
          ))}
        </div>
      ) : null}
      <form
        className='src-form'
        hidden={awaitingDetail}
        id={SOURCE_FORM_ID}
        onSubmit={(event) => void submit(event)}
      >
        {tab === 'models' ? (
          <SourceModelsTable
            catalog={catalog}
            disabled={loading || detailMissing}
            form={form}
            resolving={resolving}
            t={t}
            upstream={upstream}
            onResolve={(body) => onResolve?.(body)}
          />
        ) : null}
        {tab === 'connection' ? (
          <section className='src-panel src-panel--pad mo-form'>
            {credentialGrid}
          </section>
        ) : null}
        {tab === 'routing' ? (
          <section className='src-panel src-routing'>
            <nav
              aria-label={t(`${ns}.section.navLabel`, {
                defaultValue: '线路设置分段',
              })}
              className='mo-source-nav'
            >
              {sectionNav.map(([key, label, note]) => (
                <button
                  aria-current={activeSection === key ? 'location' : undefined}
                  className={`mo-source-nav__item${activeSection === key ? ' is-active' : ''}`}
                  data-owns-chrome=''
                  key={key}
                  type='button'
                  onClick={() => jumpTo(key)}
                >
                  <span>{label}</span>
                  {note ? (
                    <span className='mo-source-nav__note'>{note}</span>
                  ) : null}
                </button>
              ))}
            </nav>
            <div className='src-routing__scroll mo-form' ref={scrollRef}>
              {routingSections}
            </div>
          </section>
        ) : null}
        {tab === 'sync' ? (
          <SourceSyncPanel
            disabled={loading || detailMissing}
            form={form}
            t={t}
            upstream={upstream}
          />
        ) : null}
      </form>
    </>
  );
}
