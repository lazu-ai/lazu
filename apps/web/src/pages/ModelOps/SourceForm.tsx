import { zodResolver } from '@hookform/resolvers/zod';
import { ChevronDown, Eye, EyeOff } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { z } from 'zod';
import { DecisionFields, SharedCapacityFields } from './DecisionSourceFields';
import { FieldLabel } from './FieldLabel';
import { SOURCE_TYPES } from './formatters';
import { makerChips, type SourceCatalog } from './sourceCatalog';
import {
  compileIgnore,
  serializeModelMapping,
  serveUpstreamIds,
  suggestPublicName,
} from './sourceModels';
import SourceModelsTable, {
  type SourceModelStatusView,
} from './SourceModelsTable';
import SourceSyncPanel from './SourceSyncPanel';
import { sourceTypeLabel } from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { Field, FieldMessage } from '../../components/ui/field';
import { InlineNotice } from '../../components/ui/inline-notice';
import { Input } from '../../components/ui/input';
import { ProviderLogo } from '../../components/ui/provider-icons';
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
import { groupByMaker, makerBrand, makerLabel } from '../../lib/model-makers';
import type { UpstreamSyncView } from '../../hooks/models/useSourceUpstream';
import type { TFunction } from 'i18next';
import type { UseFormProps } from 'react-hook-form';

const ns = 'page.console.channel.form';

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

const ADVANCED_ERROR_FIELDS: ReadonlyArray<keyof ChannelFormValues> = [
  'param_override',
  'header_override',
  'status_code_mapping',
  'settings',
  'video_envelope',
  'upstream_sync_ignore',
  'upstream_sync_interval',
  'shared_max_rps',
  'shared_max_in_flight',
  'decision_timeout_ms',
];

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

/** The dialog footer's 保存 submits the form by this id. */
export const SOURCE_FORM_ID = 'source-form';

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
  initialAdvancedOpen?: boolean;
  initialModelView?: SourceModelStatusView;
  keyFingerprint?: string;
  layout: 'create' | 'page';
  onCancel: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  onResolve?: (body: { gone?: string[]; new?: string[] }) => void;
  onSaved: (ids: string[]) => Promise<void> | void;
  onServedCountChange?: (count: number) => void;
  resolving?: boolean;
  upstream?: UpstreamSyncView | null;
};

const savedIds = (result: unknown): string[] => {
  const ids = (result as { ids?: unknown } | null)?.ids;
  return Array.isArray(ids) ? ids.map(String) : [];
};

/**
 * Tabless vertical flow for SourceDialog (07-A):
 * 1. 供货模型 (hugs actual row count)
 * 2. 连接与凭证 (compact 2-col grid + single-line masked password input)
 * 3. 归属线路、权重与成本 (compact 4-col grid)
 * 4. 自动同步与高级参数覆写 (collapsible disclosure, auto-opens on error)
 */
export default function SourceForm({
  catalog,
  channels,
  editingChannel,
  initialAdvancedOpen = false,
  initialModelView,
  keyFingerprint,
  layout,
  onCancel,
  onDirtyChange,
  onResolve,
  onSaved,
  onServedCountChange,
  resolving,
  upstream,
}: SourceFormProps) {
  const { t } = channels;
  const isEdit = Boolean(editingChannel?.id);
  const creating = layout === 'create';
  const protocolDialectManuallySelected = useRef(false);
  const keyInputRef = useRef<HTMLInputElement | null>(null);
  const detailQuery = useChannelDetailQuery(editingChannel?.id, isEdit);
  const lanes = useChannelLanesQuery().data ?? [];

  const [keyRevealed, setKeyRevealed] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(initialAdvancedOpen);

  useEffect(() => {
    if (initialAdvancedOpen) setAdvancedOpen(true);
  }, [initialAdvancedOpen]);

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
  const detailMissing = isEdit && !detailQuery.data;

  useEffect(() => {
    onDirtyChange?.(form.formState.isDirty);
  }, [form.formState.isDirty, onDirtyChange]);

  useEffect(() => {
    protocolDialectManuallySelected.current = isEdit;
    setKeyRevealed(false);
    if (isEdit && detailQuery.data) {
      form.reset(blankUnsetLimits({ ...defaultValues, ...detailQuery.data }));
      return;
    }
    if (!isEdit) form.reset(blankUnsetLimits(defaultValues));
  }, [defaultValues, detailQuery.data, form, isEdit, editingChannel?.id]);

  // Auto-expand Section 4 whenever any override/sync field has a validation error.
  useEffect(() => {
    const errs = form.formState.errors;
    if (ADVANCED_ERROR_FIELDS.some((field) => Boolean(errs[field]))) {
      setAdvancedOpen(true);
    }
  }, [form.formState.errors]);

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
      if (ADVANCED_ERROR_FIELDS.some((field) => Boolean(errors[field]))) {
        setAdvancedOpen(true);
      }
    },
  );

  const laneColor = (slug: string) =>
    lanes.find((lane) => lane.slug === slug)?.color || 'info';

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
  const [pickFolded, setPickFolded] = useState<Record<string, boolean>>({});
  const servedModelsCount = Array.isArray(values.models)
    ? values.models.length
    : 0;
  useEffect(() => {
    if (isEdit && detailQuery.data) {
      onServedCountChange?.(servedModelsCount);
    }
  }, [detailQuery.data, isEdit, onServedCountChange, servedModelsCount]);

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
  const pickGroups = useMemo(
    () =>
      groupByMaker(
        pickShown,
        (row) => catalog.maker(row.name),
        (row) => catalog.makerRank(catalog.maker(row.name)),
      ),
    [catalog, pickShown],
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
              {t(`${ns}.create.models`, { defaultValue: '供货模型' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.create.modelsHint`, {
                defaultValue: '候选默认不勾选，勾中的才接流量',
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
                    defaultValue: '全选 ({{count}})',
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
              <div className='src-mgroups'>
                {pickGroups.map((group) => {
                  const gKey = group.maker || '__other__';
                  const gLabel = makerLabel(group.maker, t);
                  const pickedInGroup = group.rows.filter((r) =>
                    picked.has(r.id),
                  ).length;
                  const allOn =
                    group.rows.length > 0 &&
                    pickedInGroup === group.rows.length;
                  const someOn = pickedInGroup > 0 && !allOn;
                  const isOpen =
                    Boolean(pickNeedle) ||
                    pickMaker !== null ||
                    !pickFolded[gKey];

                  return (
                    <div className='src-mgroup' key={gKey}>
                      <div className='src-mgroup__head'>
                        <Checkbox
                          aria-label={gLabel}
                          checked={allOn}
                          indeterminate={someOn}
                          onCheckedChange={(checked: boolean) =>
                            setPicked((previous) => {
                              const next = new Set(previous);
                              for (const row of group.rows) {
                                if (checked) next.add(row.id);
                                else next.delete(row.id);
                              }
                              return next;
                            })
                          }
                        />
                        <button
                          aria-expanded={isOpen}
                          className='src-mgroup__toggle'
                          data-owns-chrome=''
                          type='button'
                          onClick={() =>
                            setPickFolded((prev) => ({
                              ...prev,
                              [gKey]: !prev[gKey],
                            }))
                          }
                        >
                          <ProviderLogo
                            brand={makerBrand(
                              group.maker,
                              group.rows[0] ? { name: group.rows[0].name } : {},
                            )}
                            decorative
                            size={18}
                          />
                          <span className='src-mgroup__name'>{gLabel}</span>
                          <span className='src-num src-mgroup__count'>
                            {`${pickedInGroup} / ${group.rows.length}`}
                          </span>
                          <ChevronDown
                            aria-hidden='true'
                            className={`src-mgroup__chev${isOpen ? ' is-open' : ''}`}
                            size={14}
                          />
                        </button>
                      </div>
                      {isOpen ? (
                        <div className='src-mgroup__grid'>
                          {group.rows.map((row) => {
                            const on = picked.has(row.id);
                            const known = catalog.names.has(row.name);
                            const price = catalog.price(row.name);
                            const ctx = catalog.context(row.name);
                            return (
                              <div
                                className={`src-mcard${on ? ' is-served' : ''}`}
                                key={row.id}
                                role='row'
                                tabIndex={0}
                                onClick={(event) => {
                                  if (
                                    (
                                      event.target as HTMLElement | null
                                    )?.closest('button, input, label')
                                  ) {
                                    return;
                                  }
                                  setPicked((previous) => {
                                    const next = new Set(previous);
                                    if (on) next.delete(row.id);
                                    else next.add(row.id);
                                    return next;
                                  });
                                }}
                                onKeyDown={(event) => {
                                  if (
                                    (
                                      event.target as HTMLElement | null
                                    )?.closest('button, input, label')
                                  ) {
                                    return;
                                  }
                                  if (
                                    event.key === 'Enter' ||
                                    event.key === ' '
                                  ) {
                                    event.preventDefault();
                                    setPicked((previous) => {
                                      const next = new Set(previous);
                                      if (on) next.delete(row.id);
                                      else next.add(row.id);
                                      return next;
                                    });
                                  }
                                }}
                              >
                                <span className='src-mcard__box' role='cell'>
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
                                </span>
                                <span className='src-mcard__body' role='cell'>
                                  <span className='src-mcard__top'>
                                    <span className='src-mcard__id'>
                                      {row.id}
                                    </span>
                                    {!known ? (
                                      <span className='src-status is-unknown'>
                                        {t(`${ns}.create.notInCatalogShort`, {
                                          defaultValue: '新收录',
                                        })}
                                      </span>
                                    ) : null}
                                  </span>
                                  <span className='src-mcard__sub'>
                                    <span>
                                      {[
                                        price !== '—'
                                          ? price
                                          : t(`${ns}.create.unpriced`, {
                                              defaultValue: '待补定价',
                                            }),
                                        ctx,
                                      ]
                                        .filter(Boolean)
                                        .join(' · ')}
                                    </span>
                                    {row.name !== row.id ? (
                                      <span className='src-mcard__map'>
                                        {` · → ${row.name}`}
                                      </span>
                                    ) : null}
                                  </span>
                                </span>
                              </div>
                            );
                          })}
                        </div>
                      ) : null}
                    </div>
                  );
                })}
                {pickShown.length === 0 ? (
                  <div className='src-empty src-empty--compact'>
                    {t(`${ns}.create.noMatch`, {
                      defaultValue: '无匹配模型',
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
                  defaultValue: tested ? '补充手动模型' : '手动填写模型名',
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
              defaultValue: '进入 7 天观察期 · 每 6h 自动检查上游',
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
            size='sm'
            type='button'
            variant='secondary'
            onClick={onCancel}
          >
            {t(`${ns}.action.cancel`)}
          </Button>
          <Button
            disabled={!pickOpen || pickedCount === 0 || loading}
            loading={channels.saveChannelMutation.isPending}
            size='sm'
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

  const keyRegister = form.register('key');
  const hasNewKey = Boolean(String(values.key || '').trim());
  const syncInterval = Number(values.upstream_sync_interval || 0);
  const hasOverrides = Boolean(
    values.param_override?.trim() ||
    values.header_override?.trim() ||
    values.status_code_mapping?.trim(),
  );
  const syncSummary = syncInterval
    ? t(`${ns}.summary.syncEvery`, {
        defaultValue: '每 {{hours}} 小时巡检上游',
        hours: syncInterval,
      })
    : t(`${ns}.summary.syncOff`, { defaultValue: '不自动巡检上游' });
  const newPolicySummary =
    values.upstream_sync_on_new === 'auto'
      ? t(`${ns}.summary.newAuto`, { defaultValue: '新模型自动供货' })
      : t(`${ns}.summary.newReview`, { defaultValue: '新模型需人工确认' });
  const overrideSummary = hasOverrides
    ? t(`${ns}.summary.hasOverrides`, { defaultValue: '已设参数/Header覆写' })
    : t(`${ns}.summary.noOverrides`, { defaultValue: '无参数覆写' });

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
        className='src-flow'
        hidden={awaitingDetail}
        id={SOURCE_FORM_ID}
        onSubmit={(event) => void submit(event)}
      >
        {/* Section 1 · 供货模型 */}
        <SourceModelsTable
          catalog={catalog}
          disabled={loading || detailMissing}
          form={form}
          initialView={initialModelView}
          resolving={resolving}
          t={t}
          upstream={upstream}
          onResolve={(body) => onResolve?.(body)}
        />

        {/* Section 2 · 连接与凭证 */}
        <section className='src-flow__sect'>
          <div className='src-sect-head'>
            <span className='src-sect-head__title'>
              {t(`${ns}.section.connection`, { defaultValue: '连接与凭证' })}
            </span>
          </div>
          <div className='src-grid src-grid--name'>
            <Field>
              <label className='lazu-label' htmlFor='channel-name'>
                {t(`${ns}.field.name`, { defaultValue: '来源名称' })}
              </label>
              <Input
                disabled={loading}
                id='channel-name'
                placeholder={t(`${ns}.placeholder.name`)}
                {...form.register('name')}
              />
              <FieldMessage error>
                {form.formState.errors.name?.message}
              </FieldMessage>
            </Field>
            <Field>
              <label className='lazu-label'>
                {t(`${ns}.field.source_type`, { defaultValue: '来源类型' })}
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
            </Field>
          </div>

          <div className='src-grid src-grid--2'>
            <Field>
              <FieldLabel
                code='type'
                text={t(`${ns}.field.type`, { defaultValue: '供应商类型' })}
              />
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
              <FieldMessage error>
                {form.formState.errors.type?.message}
              </FieldMessage>
            </Field>
            <Field>
              <FieldLabel
                code='protocol_dialect'
                text={t(`${ns}.field.protocol_dialect`, {
                  defaultValue: '协议方言',
                })}
                tip={t(`${ns}.help.protocol_dialect`)}
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
            </Field>
          </div>

          <Field>
            <label className='lazu-label' htmlFor='channel-base-url'>
              {t(`${ns}.field.base_url`, {
                defaultValue: '基础地址 (Base URL)',
              })}
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
                    { shouldDirty: true, shouldValidate: true },
                  );
                },
              })}
            />
          </Field>

          <Field>
            <label className='lazu-label' htmlFor='channel-key'>
              {t(`${ns}.field.key`, { defaultValue: '上游 API 密钥' })}
              <span className='src-label-sub'>
                {keyFingerprint
                  ? t(`${ns}.help.keyMaskedFp`, {
                      defaultValue: ' · 已存掩码 …{{fp}}，输入新值即替换',
                      fp: keyFingerprint,
                    })
                  : t(`${ns}.help.keyMasked`, {
                      defaultValue:
                        ' · 已加密存储，输入新值即替换，留空保持不变',
                    })}
              </span>
            </label>
            <div className='src-key-row'>
              <Input
                autoComplete='off'
                className={`src-mono-input${hasNewKey ? '' : ' is-masked-idle'}`}
                disabled={loading}
                id='channel-key'
                placeholder={
                  keyFingerprint
                    ? `••••••••••••••••${keyFingerprint}`
                    : t(`${ns}.placeholder.keyReplace`, {
                        defaultValue: '输入新密钥替换，留空保持当前密钥不变',
                      })
                }
                type={keyRevealed ? 'text' : 'password'}
                {...keyRegister}
                ref={(el) => {
                  keyRegister.ref(el);
                  keyInputRef.current = el;
                }}
              />
              {hasNewKey ? (
                <Button
                  aria-label={
                    keyRevealed
                      ? t(`${ns}.hideKey`, { defaultValue: '隐藏密钥' })
                      : t(`${ns}.showKey`, { defaultValue: '显示密钥' })
                  }
                  size='sm'
                  type='button'
                  variant='secondary'
                  onClick={() => setKeyRevealed((open) => !open)}
                >
                  {keyRevealed ? <EyeOff size={14} /> : <Eye size={14} />}
                  {keyRevealed
                    ? t(`${ns}.hideShort`, { defaultValue: '隐藏' })
                    : t(`${ns}.showShort`, { defaultValue: '显示' })}
                </Button>
              ) : (
                <Button
                  size='sm'
                  type='button'
                  variant='secondary'
                  onClick={() => keyInputRef.current?.focus()}
                >
                  {t(`${ns}.replaceKey`, { defaultValue: '替换新密钥' })}
                </Button>
              )}
            </div>
            <FieldMessage error>
              {form.formState.errors.key?.message}
            </FieldMessage>
          </Field>

          {Number(values.type) === 14 ? (
            <div className='src-inline-switches'>
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
              {values.claude_code_client_only ? (
                <Input
                  aria-label={t(`${ns}.field.claude_code_user_agent`, {
                    defaultValue: '这个来源的抽测 User-Agent',
                  })}
                  disabled={loading}
                  placeholder='User-Agent override (optional)'
                  {...form.register('claude_code_user_agent')}
                />
              ) : null}
            </div>
          ) : null}
        </section>

        {/* Section 3 · 归属线路、权重与成本 */}
        <section className='src-flow__sect'>
          <div className='src-sect-head'>
            <span className='src-sect-head__title'>
              {t(`${ns}.section.routingCost`, {
                defaultValue: '归属线路、权重与成本',
              })}
            </span>
          </div>
          <div className='src-grid src-grid--4'>
            <Field>
              <FieldLabel
                text={t(`${ns}.field.groups`, { defaultValue: '归属线路' })}
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
                text={t(`${ns}.field.weight`, { defaultValue: '权重' })}
                tip={t(`${ns}.help.weightOverride`, {
                  defaultValue:
                    '同一线路里按权重分流；模型页里按模型改过的以那边为准。',
                })}
              />
              <Input
                className='src-mono-input'
                disabled={loading}
                id='channel-weight'
                min='0'
                type='number'
                {...form.register('weight')}
              />
              <FieldMessage error>
                {form.formState.errors.weight?.message}
              </FieldMessage>
            </Field>

            <Field>
              <FieldLabel
                code='priority'
                htmlFor='channel-priority'
                text={t(`${ns}.field.priority`, { defaultValue: '优先级' })}
                tip={t(`${ns}.help.priorityShort`, {
                  defaultValue: '高的先用，相同再看权重。',
                })}
              />
              <Input
                className='src-mono-input'
                disabled={loading}
                id='channel-priority'
                min='0'
                type='number'
                {...form.register('priority')}
              />
              <FieldMessage error>
                {form.formState.errors.priority?.message}
              </FieldMessage>
            </Field>

            <Field>
              <FieldLabel
                code='cost_multiplier'
                htmlFor='channel-cost'
                text={t(`${ns}.field.costPercentShort`, {
                  defaultValue: '上游成本折算 %',
                })}
                tip={t(`${ns}.help.costPercent`, {
                  defaultValue:
                    '按官方价的百分之几进货（如 65 代表 6.5 折），只用来算毛利，不影响售价。',
                })}
              />
              <Input
                className='src-mono-input'
                disabled={loading}
                id='channel-cost'
                inputMode='decimal'
                max='1000'
                min='0'
                placeholder='100'
                step='0.1'
                type='number'
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
            </Field>
          </div>

          <div className='src-grid src-grid--4 src-grid--align-center'>
            <Field>
              <FieldLabel
                code='max_rps'
                htmlFor='capacity-max_rps'
                text={t(`${ns}.field.max_rps`, {
                  defaultValue: '每秒请求 (RPS)',
                })}
              />
              <Input
                className='src-mono-input'
                disabled={loading}
                id='capacity-max_rps'
                min={0}
                placeholder={t(`${ns}.placeholder.unlimited`, {
                  defaultValue: '不限',
                })}
                type='number'
                {...form.register('max_rps')}
              />
              <FieldMessage error>
                {form.formState.errors.max_rps?.message}
              </FieldMessage>
            </Field>

            <Field>
              <FieldLabel
                code='max_in_flight'
                htmlFor='capacity-max_in_flight'
                text={t(`${ns}.field.max_in_flight`, {
                  defaultValue: '并发上限',
                })}
              />
              <Input
                className='src-mono-input'
                disabled={loading}
                id='capacity-max_in_flight'
                min={0}
                placeholder={t(`${ns}.placeholder.unlimited`, {
                  defaultValue: '不限',
                })}
                type='number'
                {...form.register('max_in_flight')}
              />
              <FieldMessage error>
                {form.formState.errors.max_in_flight?.message}
              </FieldMessage>
            </Field>

            <div className='src-switch-cell'>
              <Controller
                control={form.control}
                name='auto_ban'
                render={({ field }) => (
                  <Switch
                    checked={field.value}
                    disabled={loading}
                    onCheckedChange={field.onChange}
                  >
                    {t(`${ns}.field.auto_ban`, {
                      defaultValue: '异常自动封禁',
                    })}
                  </Switch>
                )}
              />
            </div>

            <div className='src-switch-cell'>
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
            </div>
          </div>
        </section>

        {/* Section 4 · 自动同步策略与高级参数覆写 */}
        <details
          className='src-disclosure'
          id='source-sect-advanced'
          open={advancedOpen}
          onToggle={(event) =>
            setAdvancedOpen((event.currentTarget as HTMLDetailsElement).open)
          }
        >
          <summary className='src-disclosure__summary'>
            <span className='src-disclosure__text'>
              <b>
                {t(`${ns}.section.syncAndAdvanced`, {
                  defaultValue: '自动同步策略与高级参数覆写',
                })}
              </b>
              <span className='src-disclosure__meta'>
                {` · ${syncSummary} · ${newPolicySummary} · ${overrideSummary}`}
              </span>
            </span>
            <span className='src-disclosure__action'>
              {advancedOpen
                ? t(`${ns}.collapseAdvanced`, { defaultValue: '收起设置' })
                : t(`${ns}.expandAdvanced`, { defaultValue: '展开设置' })}
              <ChevronDown
                aria-hidden='true'
                className={`src-disclosure__chev${advancedOpen ? ' is-open' : ''}`}
                size={14}
              />
            </span>
          </summary>

          <div className='src-disclosure__body'>
            <SourceSyncPanel
              disabled={loading || detailMissing}
              form={form}
              t={t}
              upstream={upstream}
            />

            <div className='src-flow__subsect'>
              <div className='src-sect-head'>
                <span className='src-sect-head__title'>
                  {t(`${ns}.section.sharedAndTimeout`, {
                    defaultValue: '共享容量、流式超时与备注',
                  })}
                </span>
              </div>
              <SharedCapacityFields disabled={loading} form={form} />
              <div className='src-grid src-grid--2'>
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
                    max='3600'
                    min='0'
                    placeholder='90'
                    step='1'
                    type='number'
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
                </Field>
                <DecisionFields disabled={loading} form={form} />
                <Field>
                  <FieldLabel
                    htmlFor='channel-tag'
                    text={t(`${ns}.field.tag`, { defaultValue: '标签' })}
                  />
                  <Input
                    disabled={loading}
                    id='channel-tag'
                    placeholder={t(`${ns}.placeholder.tag`)}
                    {...form.register('tag')}
                  />
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='channel-remark'>
                    {t(`${ns}.field.remark`, { defaultValue: '备注' })}
                  </label>
                  <Input
                    disabled={loading}
                    id='channel-remark'
                    placeholder={t(`${ns}.placeholder.remark`)}
                    {...form.register('remark')}
                  />
                </Field>
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
                </Field>
              </div>

              {values.video_family === 'envelope' ||
              Number(values.type) === 8 ? (
                <Field>
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
                      defaultValue: '{"preset":"tasks"}',
                    })}
                    rows={3}
                    {...form.register('video_envelope')}
                  />
                  <FieldMessage error>
                    {form.formState.errors.video_envelope?.message}
                  </FieldMessage>
                </Field>
              ) : null}

              {Number(values.type) === 8 ? (
                <div className='src-grid src-grid--2'>
                  <Field>
                    <label
                      className='lazu-label'
                      htmlFor='channel-video-status-url-template'
                    >
                      {t(`${ns}.field.video_status_url_template`)}
                    </label>
                    <Input
                      disabled={loading}
                      id='channel-video-status-url-template'
                      placeholder={t(
                        `${ns}.placeholder.video_status_url_template`,
                      )}
                      {...form.register('video_status_url_template')}
                    />
                  </Field>
                  <Field>
                    <label
                      className='lazu-label'
                      htmlFor='channel-video-content-url-template'
                    >
                      {t(`${ns}.field.video_content_url_template`)}
                    </label>
                    <Input
                      disabled={loading}
                      id='channel-video-content-url-template'
                      placeholder={t(
                        `${ns}.placeholder.video_content_url_template`,
                      )}
                      {...form.register('video_content_url_template')}
                    />
                  </Field>
                </div>
              ) : null}
            </div>

            <div className='src-flow__subsect'>
              <div className='src-sect-head'>
                <span className='src-sect-head__title'>
                  {t(`${ns}.section.overridesJson`, {
                    defaultValue: '成本明细与请求参数覆写 (JSON)',
                  })}
                </span>
              </div>
              <div className='src-grid src-grid--2'>
                <Field>
                  <FieldLabel
                    code='param_override'
                    htmlFor='channel-param-override'
                    text={t(`${ns}.field.param_override`)}
                    tip={t(`${ns}.help.param_override`)}
                  />
                  <Textarea
                    className='src-mono-input'
                    disabled={loading}
                    id='channel-param-override'
                    placeholder={t(`${ns}.placeholder.param_override`)}
                    rows={3}
                    {...form.register('param_override')}
                  />
                  <FieldMessage error>
                    {form.formState.errors.param_override?.message}
                  </FieldMessage>
                </Field>
                <Field>
                  <FieldLabel
                    code='header_override'
                    htmlFor='channel-header-override'
                    text={t(`${ns}.field.header_override`)}
                    tip={t(`${ns}.help.header_override`)}
                  />
                  <Textarea
                    className='src-mono-input'
                    disabled={loading}
                    id='channel-header-override'
                    placeholder={t(`${ns}.placeholder.header_override`)}
                    rows={3}
                    {...form.register('header_override')}
                  />
                  <FieldMessage error>
                    {form.formState.errors.header_override?.message}
                  </FieldMessage>
                </Field>
                <Field>
                  <FieldLabel
                    code='status_code_mapping'
                    htmlFor='channel-status-code-mapping'
                    text={t(`${ns}.field.status_code_mapping`)}
                    tip={t(`${ns}.help.status_code_mapping`)}
                  />
                  <Textarea
                    className='src-mono-input'
                    disabled={loading}
                    id='channel-status-code-mapping'
                    placeholder={t(`${ns}.placeholder.status_code_mapping`)}
                    rows={3}
                    {...form.register('status_code_mapping')}
                  />
                  <FieldMessage error>
                    {form.formState.errors.status_code_mapping?.message}
                  </FieldMessage>
                </Field>
                <Field>
                  <FieldLabel
                    code='provider_cost_prices'
                    htmlFor='channel-cost-prices'
                    text={t(`${ns}.field.provider_cost_prices`, {
                      defaultValue: '按模型成本明细',
                    })}
                    tip={t(`${ns}.help.costTier`)}
                  />
                  <Textarea
                    className='src-mono-input'
                    disabled={loading}
                    id='channel-cost-prices'
                    rows={3}
                    {...form.register('provider_cost_prices')}
                  />
                </Field>
              </div>
              <Field>
                <FieldLabel
                  code='settings'
                  htmlFor='channel-settings'
                  text={t(`${ns}.field.settings`)}
                  tip={t(`${ns}.help.settings`)}
                />
                <Textarea
                  className='src-mono-input'
                  disabled={loading}
                  id='channel-settings'
                  placeholder={t(`${ns}.placeholder.settings`)}
                  rows={3}
                  {...form.register('settings')}
                />
                <FieldMessage error>
                  {form.formState.errors.settings?.message}
                </FieldMessage>
              </Field>
            </div>
          </div>
        </details>
      </form>
    </>
  );
}
