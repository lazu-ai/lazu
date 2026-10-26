import { zodResolver } from '@hookform/resolvers/zod';
import { Plus, RefreshCw } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { z } from 'zod';
import { DecisionSourceFields } from './DecisionSourceFields';
import { SOURCE_TYPES } from './formatters';
import { sourceTypeLabel } from './SourceTypeBadge';
import {
  Button,
  Field,
  FieldMessage,
  Input,
  MultiSelect,
  Select,
  SelectItem,
  Switch,
  Textarea,
} from '../../components/ui';
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
import type { TFunction } from 'i18next';
import type { UseFormProps } from 'react-hook-form';

const ns = 'page.console.channel.form';

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
  video_content_url_template?: string;
  video_envelope?: string;
  video_family?: string;
  video_status_url_template?: string;
  weight: number;
};

/** Which fields belong to which step of 新增来源. Editing shows them all. */
export const SOURCE_STEPS = ['credential', 'models', 'routing'] as const;
export type SourceStep = (typeof SOURCE_STEPS)[number];

const STEP_FIELDS: Record<SourceStep, Array<keyof ChannelFormValues>> = {
  credential: ['name', 'type', 'protocol_dialect', 'key', 'base_url'],
  models: ['models'],
  routing: ['groups', 'weight', 'priority'],
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

function writeCostPercent(
  settings: string | undefined,
  percent: string,
): string | null {
  let parsed: Record<string, unknown> = {};
  if (settings?.trim()) {
    try {
      const value = JSON.parse(settings) as unknown;
      if (!value || typeof value !== 'object' || Array.isArray(value))
        return null;
      parsed = value as Record<string, unknown>;
    } catch {
      return null;
    }
  }
  const number = Number(percent);
  if (percent.trim() && number > 0) parsed.cost_multiplier = number / 100;
  else delete parsed.cost_multiplier;
  return Object.keys(parsed).length ? JSON.stringify(parsed, null, 2) : '';
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

type SourceFormProps = {
  channels: ChannelsQuery;
  editingChannel: ChannelRecord | null;
  onCancel: () => void;
  onSaved: () => void;
  step: SourceStep;
  onStepChange: (step: SourceStep) => void;
};

/**
 * The source form, mounted inside the inspector rather than in a dialog.
 *
 * It used to be six stacked `DataCard`s inside an extra-wide modal — a card
 * inside a card inside an overlay, with the ledger it was about hidden behind
 * it. Here the panel is the paper: sections are a hairline and a small label,
 * and the fields sit in the order the work happens.
 *
 * Creating walks the three steps a new source actually goes through: prove the
 * credential, decide which of its models we take, then place them in a tier.
 * Editing shows every section at once, because you arrive knowing what you
 * came to change.
 */
export default function SourceForm({
  channels,
  editingChannel,
  onCancel,
  onSaved,
  onStepChange,
  step,
}: SourceFormProps) {
  const { t } = channels;
  const isEdit = Boolean(editingChannel?.id);
  const [customModels, setCustomModels] = useState('');
  const protocolDialectManuallySelected = useRef(false);
  const detailQuery = useChannelDetailQuery(editingChannel?.id, isEdit);

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

  useEffect(() => {
    setCustomModels('');
    protocolDialectManuallySelected.current = isEdit;
    if (isEdit && detailQuery.data) {
      form.reset({ ...defaultValues, ...detailQuery.data });
      return;
    }
    if (!isEdit) form.reset(defaultValues);
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
        channels.groups.map((group) => ({ label: group, value: group })),
        values.groups || [],
      ),
    [channels.groups, values.groups],
  );

  const modelOptions = useMemo(
    () =>
      withSelectedOptions(channels.channelModelOptions, values.models || []),
    [channels.channelModelOptions, values.models],
  );

  const addModels = (items: unknown) => {
    const additions = splitFreeEntry(items);
    if (!additions.length) return;
    form.setValue(
      'models',
      uniqueValues([...(form.getValues('models') || []), ...additions]),
      { shouldDirty: true, shouldValidate: true },
    );
    setCustomModels('');
  };

  /* Both mutations report their own failure; the form just stays open. */
  const fetchUpstreamModels = async () => {
    try {
      const fetched = await channels.fetchUpstreamModelsMutation.mutateAsync({
        editingChannel,
        values: form.getValues(),
      });
      addModels(fetched);
    } catch {
      /* Reported by the mutation. */
    }
  };

  const submit = form.handleSubmit(async (formValues) => {
    try {
      await channels.saveChannelMutation.mutateAsync({
        editingChannel,
        values: formValues,
      });
    } catch {
      return;
    }
    onSaved();
  });

  /* Stepping forward validates only what this step asked for, so a missing
     model list cannot block the credential step it has nothing to do with. */
  const goNext = async () => {
    const ok = await form.trigger(STEP_FIELDS[step]);
    if (!ok) return;
    const index = SOURCE_STEPS.indexOf(step);
    if (index < SOURCE_STEPS.length - 1) onStepChange(SOURCE_STEPS[index + 1]);
  };

  const goBack = () => {
    const index = SOURCE_STEPS.indexOf(step);
    if (index > 0) onStepChange(SOURCE_STEPS[index - 1]);
  };

  const handleFormSubmit = (event: FormEvent<HTMLFormElement>) => {
    if (!isEdit && step !== 'routing') {
      event.preventDefault();
      void goNext();
      return;
    }
    void submit(event);
  };

  /* Creating walks the three steps in order; editing shows them as tabs, so
     either way exactly one group is on screen and the panel keeps its shape. */
  const showStep = (candidate: SourceStep) => step === candidate;

  const sectionLabel = (candidate: SourceStep) =>
    candidate === 'credential'
      ? t(`${ns}.step.credential`, { defaultValue: '凭据' })
      : candidate === 'models'
        ? t(`${ns}.step.models`, { defaultValue: '模型' })
        : t(`${ns}.step.routing`, { defaultValue: '档位' });

  return (
    <>
      <div className='mo-dialog__body'>
        <form className='mo-form' id='source-form' onSubmit={handleFormSubmit}>
          {showStep('credential') ? (
            <>
              <div className='mo-sect mo-sect--flush'>
                {sectionLabel('credential')}
              </div>
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
                  <FieldMessage error>
                    {form.formState.errors.name?.message}
                  </FieldMessage>
                </Field>
                <Field>
                  {/* Channel.Type is the API-protocol adapter; SourceType is
                      where the supply comes from. They are independent — a
                      cloud resale and a first-party account can speak the same
                      protocol. */}
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
                  <FieldMessage>
                    {t(`${ns}.help.source_type`, {
                      defaultValue:
                        '官方 / 云厂商 / 聚合 / 非官方。只影响台账分类与筛选，不改变路由。',
                    })}
                  </FieldMessage>
                </Field>
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.type`)}{' '}
                    <code className='mo-code'>type</code>
                  </label>
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
                            inferProtocolDialect(
                              form.getValues('base_url'),
                              value,
                            ),
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
                  <label className='lazu-label'>
                    {t(`${ns}.field.protocol_dialect`, {
                      defaultValue: '协议方言',
                    })}{' '}
                    <code className='mo-code'>protocol_dialect</code>
                  </label>
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
                        <SelectItem value='azure_openai'>
                          Azure OpenAI
                        </SelectItem>
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
                    {isEdit
                      ? t(`${ns}.help.keyEdit`)
                      : t(`${ns}.help.keyCreate`)}
                  </FieldMessage>
                  <FieldMessage error>
                    {form.formState.errors.key?.message}
                  </FieldMessage>
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
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.video_family`, {
                      defaultValue: '视频上游族',
                    })}{' '}
                    <code className='mo-code'>video_family</code>
                  </label>
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
                {values.video_family === 'envelope' ||
                Number(values.type) === 8 ? (
                  <Field className='mo-form__wide'>
                    <label
                      className='lazu-label'
                      htmlFor='channel-video-envelope'
                    >
                      {t(`${ns}.field.video_envelope`, {
                        defaultValue: '视频信封配置',
                      })}{' '}
                      <code className='mo-code'>video_envelope</code>
                    </label>
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
                        placeholder={t(
                          `${ns}.placeholder.video_status_url_template`,
                        )}
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
                        placeholder={t(
                          `${ns}.placeholder.video_content_url_template`,
                        )}
                        {...form.register('video_content_url_template')}
                      />
                      <FieldMessage>
                        {t(`${ns}.help.video_content_url_template`)}
                      </FieldMessage>
                    </Field>
                  </>
                ) : null}
              </div>
            </>
          ) : null}

          {showStep('models') ? (
            <>
              <div className={`mo-sect${isEdit ? '' : ' mo-sect--flush'}`}>
                {sectionLabel('models')}
                <span className='mo-sect__note'>
                  {t(`${ns}.help.modelsCount`, {
                    count: values.models?.length || 0,
                    defaultValue: '· 已选 {{count}} 个',
                  })}
                </span>
                <Button
                  className='mo-sect__action'
                  disabled={loading || (!isEdit && !values.key)}
                  loading={channels.fetchUpstreamModelsMutation.isPending}
                  size='xs'
                  type='button'
                  variant='secondary'
                  onClick={fetchUpstreamModels}
                >
                  <RefreshCw size={13} />
                  {t(`${ns}.action.fetchModels`)}
                </Button>
              </div>
              <div className='mo-form__grid'>
                <Field className='mo-form__wide'>
                  <label className='lazu-label'>
                    {t(`${ns}.field.models`)}
                  </label>
                  <Controller
                    control={form.control}
                    name='models'
                    render={({ field }) => (
                      <MultiSelect
                        disabled={loading}
                        emptyText={t(`${ns}.empty.options`)}
                        options={modelOptions}
                        placeholder={t(`${ns}.placeholder.models`)}
                        value={field.value}
                        onValueChange={field.onChange}
                      />
                    )}
                  />
                  <FieldMessage>
                    {t(`${ns}.help.modelsCandidate`, {
                      defaultValue:
                        '目录里没有的名字会登记为「候选」，进模型账簿等定价，不会自动上架。',
                    })}
                  </FieldMessage>
                  <FieldMessage error>
                    {form.formState.errors.models?.message}
                  </FieldMessage>
                </Field>
                <Field className='mo-form__wide'>
                  <label className='lazu-label' htmlFor='channel-custom-models'>
                    {t(`${ns}.field.custom_models`)}
                  </label>
                  <div className='mo-form__row'>
                    <Input
                      disabled={loading}
                      id='channel-custom-models'
                      placeholder={t(`${ns}.placeholder.custom_models`)}
                      value={customModels}
                      onChange={(event) => setCustomModels(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                          event.preventDefault();
                          addModels(customModels);
                        }
                      }}
                    />
                    <Button
                      disabled={loading || !customModels.trim()}
                      size='sm'
                      type='button'
                      variant='secondary'
                      onClick={() => addModels(customModels)}
                    >
                      <Plus size={14} />
                      {t(`${ns}.action.addModels`)}
                    </Button>
                  </div>
                  <FieldMessage>{t(`${ns}.help.custom_models`)}</FieldMessage>
                </Field>
              </div>
            </>
          ) : null}

          {showStep('routing') ? (
            <>
              <div className={`mo-sect${isEdit ? '' : ' mo-sect--flush'}`}>
                {sectionLabel('routing')}
              </div>
              <div className='mo-form__grid'>
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.groups`, { defaultValue: '供货档位' })}
                  </label>
                  <Controller
                    control={form.control}
                    name='groups'
                    render={({ field }) => (
                      <Select
                        disabled={loading}
                        value={field.value?.[0] || 'direct'}
                        onValueChange={(value) => field.onChange([value])}
                      >
                        {groupOptions.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            {option.label}
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
                  <label className='lazu-label' htmlFor='channel-weight'>
                    {t(`${ns}.field.weight`)}{' '}
                    <code className='mo-code'>weight</code>
                  </label>
                  <Input
                    disabled={loading}
                    id='channel-weight'
                    min='0'
                    type='number'
                    {...form.register('weight')}
                  />
                  <FieldMessage>
                    {t(`${ns}.help.weightOverride`, {
                      defaultValue:
                        '这个来源在各档位池里的默认权重；模型页里按模型改过的以那边为准。',
                    })}
                  </FieldMessage>
                  <FieldMessage error>
                    {form.formState.errors.weight?.message}
                  </FieldMessage>
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='channel-priority'>
                    {t(`${ns}.field.priority`)}{' '}
                    <code className='mo-code'>priority</code>
                  </label>
                  <Input
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
                <DecisionSourceFields form={form} disabled={loading} />
                <Field>
                  <label className='lazu-label' htmlFor='channel-cost'>
                    {t(`${ns}.field.costPercent`, {
                      defaultValue: '成本折扣',
                    })}{' '}
                    <code className='mo-code'>cost_multiplier</code>
                  </label>
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
                <Field>
                  <label className='lazu-label' htmlFor='channel-tag'>
                    {t(`${ns}.field.tag`)}
                  </label>
                  <Input
                    disabled={loading}
                    id='channel-tag'
                    placeholder={t(`${ns}.placeholder.tag`)}
                    {...form.register('tag')}
                  />
                </Field>
                <div className='mo-form__switches'>
                  <Controller
                    control={form.control}
                    name='status'
                    render={({ field }) => (
                      <Switch
                        checked={field.value}
                        disabled={loading}
                        onCheckedChange={field.onChange}
                      >
                        {t(`${ns}.field.status`)}
                      </Switch>
                    )}
                  />
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
                        '新接入的模型先放进库存，只被探测、不接流量；观察期满且各项检查通过后，在来源台账或模型的档位卡上线。已在服务的模型不受影响。',
                    })}
                  </FieldMessage>
                </div>
              </div>

              <div className='mo-sect'>
                {t(`${ns}.section.cost`, { defaultValue: '成本基准' })}
                <span className='mo-sect__note'>
                  {t(`${ns}.section.costShort`, {
                    defaultValue: '· 用来算毛利，不影响售价',
                  })}
                </span>
              </div>
              <Field>
                <label className='lazu-label' htmlFor='channel-cost-prices'>
                  {t(`${ns}.field.provider_cost_prices`, {
                    defaultValue: '成本明细',
                  })}{' '}
                  <code className='mo-code'>provider_cost_prices</code>
                </label>
                <Textarea
                  disabled={loading}
                  id='channel-cost-prices'
                  rows={4}
                  {...form.register('provider_cost_prices')}
                />
                <FieldMessage>
                  {`[{"model_name":"gpt-5.5","provider":"manual","input_per_mtok":5,"output_per_mtok":30,"context_tier_threshold":272000,"context_tier_input_per_mtok":10,"context_tier_output_per_mtok":45}]`}
                </FieldMessage>
                <FieldMessage>
                  {t(`${ns}.help.costTier`, {
                    defaultValue:
                      '这条来源按长上下文加价时才填 context_tier_*：输入（含缓存）超过阈值，整单按分档成本算。不填就按基础成本算；官方价兜底行会自动带上官方分档。',
                  })}
                </FieldMessage>
              </Field>

              {/* Rarely touched, so folded rather than dropped. */}
              <details className='mo-advanced'>
                <summary>
                  {t(`${ns}.section.advanced`)}
                  <span className='mo-sub'>
                    {t(`${ns}.section.advancedShort`, {
                      defaultValue: '· 模型映射 · 参数覆盖 · 备注',
                    })}
                  </span>
                </summary>
                <div className='mo-form__grid'>
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
                    <label
                      className='lazu-label'
                      htmlFor='channel-param-override'
                    >
                      {t(`${ns}.field.param_override`)}{' '}
                      <code className='mo-code'>param_override</code>
                    </label>
                    <Textarea
                      disabled={loading}
                      id='channel-param-override'
                      placeholder={t(`${ns}.placeholder.param_override`)}
                      rows={4}
                      {...form.register('param_override')}
                    />
                    <FieldMessage>
                      {t(`${ns}.help.param_override`)}
                    </FieldMessage>
                    <FieldMessage error>
                      {form.formState.errors.param_override?.message}
                    </FieldMessage>
                  </Field>
                  <Field className='mo-form__wide'>
                    <label
                      className='lazu-label'
                      htmlFor='channel-header-override'
                    >
                      {t(`${ns}.field.header_override`)}{' '}
                      <code className='mo-code'>header_override</code>
                    </label>
                    <Textarea
                      disabled={loading}
                      id='channel-header-override'
                      placeholder={t(`${ns}.placeholder.header_override`)}
                      rows={3}
                      {...form.register('header_override')}
                    />
                    <FieldMessage>
                      {t(`${ns}.help.header_override`)}
                    </FieldMessage>
                    <FieldMessage error>
                      {form.formState.errors.header_override?.message}
                    </FieldMessage>
                  </Field>
                  <Field className='mo-form__wide'>
                    <label
                      className='lazu-label'
                      htmlFor='channel-status-code-mapping'
                    >
                      {t(`${ns}.field.status_code_mapping`)}{' '}
                      <code className='mo-code'>status_code_mapping</code>
                    </label>
                    <Textarea
                      disabled={loading}
                      id='channel-status-code-mapping'
                      placeholder={t(`${ns}.placeholder.status_code_mapping`)}
                      rows={3}
                      {...form.register('status_code_mapping')}
                    />
                    <FieldMessage>
                      {t(`${ns}.help.status_code_mapping`)}
                    </FieldMessage>
                    <FieldMessage error>
                      {form.formState.errors.status_code_mapping?.message}
                    </FieldMessage>
                  </Field>
                  <Field className='mo-form__wide'>
                    <label className='lazu-label' htmlFor='channel-settings'>
                      {t(`${ns}.field.settings`)}{' '}
                      <code className='mo-code'>settings</code>
                    </label>
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
              </details>
            </>
          ) : null}
        </form>
      </div>

      <div className='mo-dialog__foot'>
        <Button
          disabled={loading}
          size='sm'
          type='button'
          variant='secondary'
          onClick={step === 'credential' || isEdit ? onCancel : goBack}
        >
          {step === 'credential' || isEdit
            ? t(`${ns}.action.cancel`)
            : t(`${ns}.action.back`, { defaultValue: '上一步' })}
        </Button>
        <div className='mo-dialog__foot-actions'>
          {!isEdit && step !== 'routing' ? (
            <Button
              disabled={loading}
              size='sm'
              type='button'
              variant='primary'
              onClick={() => void goNext()}
            >
              {step === 'credential'
                ? t(`${ns}.action.nextModels`, { defaultValue: '下一步：模型' })
                : t(`${ns}.action.nextRouting`, {
                    defaultValue: '下一步：档位与权重',
                  })}
            </Button>
          ) : (
            <Button
              form='source-form'
              loading={channels.saveChannelMutation.isPending}
              size='sm'
              type='submit'
              variant='primary'
            >
              {isEdit
                ? t(`${ns}.action.submit`)
                : t(`${ns}.action.create`, { defaultValue: '创建来源' })}
            </Button>
          )}
        </div>
      </div>
    </>
  );
}
