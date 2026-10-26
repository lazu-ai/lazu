import { zodResolver } from '@hookform/resolvers/zod';
import { Check, Plus } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { z } from 'zod';
import { modalityLabel } from './modelInfo';
import { withModelInfo } from './modelRegistration';
import { Button } from '../../components/ui/button';
import { Field, FieldMessage } from '../../components/ui/field';
import { InlineNotice } from '../../components/ui/inline-notice';
import { Input } from '../../components/ui/input';
import { MultiSelect, Select, SelectItem } from '../../components/ui/select';
import { Switch } from '../../components/ui/switch';
import { Textarea } from '../../components/ui/textarea';
import {
  lookupModelInfo,
  useModelMakersQuery,
  type ModelInfoPrefill,
} from '../../hooks/models/useModelInfoQuery';
import {
  getDefaultModelFormValues,
  useModelDetailQuery,
} from '../../hooks/models/useModelsQuery';
import { makerFullLabel } from '../../lib/model-makers';
import type { ApiRecord } from './apiRecord';
import type { TFunction } from 'i18next';
import type { ReactNode } from 'react';
import type { UseFormProps } from 'react-hook-form';

const ns = 'page.console.models.form';

type LooseRecord = ApiRecord;

type ModelFormValues = ReturnType<typeof getDefaultModelFormValues>;

type OptionValue = number | string;

type SelectOption = {
  label: ReactNode;
  value: OptionValue;
};

type CapabilityKey =
  | 'supports_audio_input'
  | 'supports_audio_output'
  | 'supports_computer_use'
  | 'supports_function_calling'
  | 'supports_parallel_function_calling'
  | 'supports_pdf_input'
  | 'supports_prompt_caching'
  | 'supports_reasoning'
  | 'supports_response_schema'
  | 'supports_vision'
  | 'supports_web_search';

type EndpointGroup = LooseRecord & {
  id?: OptionValue;
  items?: unknown;
  name?: string;
};

type ModelMetaFormProps = {
  /** 厂商 is display-layer metadata, not an operations object, so it has no tab
   *  of its own — but a model cannot be saved without one, and the vendor CRUD
   *  endpoints had no UI at all. Creating the brand inline here is the only
   *  place an operator actually needs it. */
  createVendor: (name: string) => Promise<{ internal_id?: number } | undefined>;
  creatingVendor: boolean;
  editingModel?: LooseRecord | null;
  /** Rendered at the top of the form, inside its scroll area. */
  lead?: ReactNode;
  /** Runs before the model is saved; false stops the save. */
  beforeSave?: () => Promise<boolean>;
  models: {
    endpointGroups: EndpointGroup[];
    editorOptionsLoading?: boolean;
    saveModelMutation: {
      isPending: boolean;
      mutateAsync: (variables: {
        editingModel?: LooseRecord | null;
        values: LooseRecord;
      }) => Promise<unknown>;
    };
    t: TFunction;
    tagOptions: SelectOption[];
    vendors: Array<LooseRecord & { id?: OptionValue; name?: ReactNode }>;
  };
  onCancel: () => void;
  onDelete?: () => void;
  onSaved: () => void;
  /** A model id to register, looked up on models.dev as the form opens —
   *  资料更新's 登记 button sends one. */
  prefillModel?: string;
};

const MODALITY_OPTIONS = ['text', 'image', 'audio', 'video', 'pdf'];

/** Which vendor row a lab's model most plausibly belongs to. Vendors are
 *  whoever a model was imported from, so this only preselects; the operator
 *  can pick another. */
const MAKER_VENDOR_HINTS: Record<string, RegExp> = {
  alibaba: /alibaba|aliyun|bailian|dashscope|qwen/i,
  anthropic: /anthropic|claude/i,
  deepseek: /deepseek/i,
  google: /^google|gemini/i,
  minimax: /minimax/i,
  moonshot: /moonshot|kimi/i,
  openai: /^openai$/i,
  xai: /^xai$|grok/i,
  zhipu: /zhipu|z\.ai|^zai$/i,
};

const isJsonOrEmpty = (value: unknown) => {
  const text = String(value || '').trim();
  if (!text) return true;
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
};

const capabilityKeys: CapabilityKey[] = [
  'supports_function_calling',
  'supports_parallel_function_calling',
  'supports_vision',
  'supports_reasoning',
  'supports_response_schema',
  'supports_prompt_caching',
  'supports_audio_input',
  'supports_audio_output',
  'supports_web_search',
  'supports_pdf_input',
  'supports_computer_use',
];

const lifecycleStatuses = ['candidate', 'active', 'retired'];

const buildModelFormSchema = (t: TFunction) =>
  z
    .object({
      capabilities: z
        .object({
          supports_audio_input: z.boolean().optional(),
          supports_audio_output: z.boolean().optional(),
          supports_computer_use: z.boolean().optional(),
          supports_function_calling: z.boolean().optional(),
          supports_parallel_function_calling: z.boolean().optional(),
          supports_pdf_input: z.boolean().optional(),
          supports_prompt_caching: z.boolean().optional(),
          supports_reasoning: z.boolean().optional(),
          supports_response_schema: z.boolean().optional(),
          supports_vision: z.boolean().optional(),
          supports_web_search: z.boolean().optional(),
        })
        .optional(),
      context_window: z.string().trim().optional(),
      description: z.string().trim().optional(),
      description_en: z.string().trim().optional(),
      description_zh: z.string().trim().optional(),
      endpoints: z
        .string()
        .trim()
        .refine(isJsonOrEmpty, {
          message: t(`${ns}.error.endpoints.json`),
        })
        .optional(),
      video_capabilities: z
        .string()
        .trim()
        .refine(isJsonOrEmpty, {
          message: t(`${ns}.error.video_capabilities.json`),
        })
        .optional(),
      family: z.string().trim().optional(),
      icon: z.string().trim().optional(),
      lifecycle_status: z.string().trim().optional(),
      max_tokens: z.string().trim().optional(),
      model_name: z
        .string()
        .trim()
        .min(1, t(`${ns}.error.model_name.required`))
        .transform((value) => value.toLowerCase()),
      name_rule: z.coerce.number().optional(),
      open_source: z.boolean(),
      replacement_model: z.string().trim().optional(),
      retire_after: z.string().trim().optional(),
      status: z.boolean(),
      sync_official: z.boolean(),
      tags: z.array(z.string()),
      vendor_id: z.coerce.number().min(1, t(`${ns}.error.vendor_id.required`)),
    })
    .passthrough();

const withSelectedOptions = (
  options: SelectOption[],
  selectedValues: unknown[],
): SelectOption[] => {
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

const coerceEndpointGroup = (items: unknown): LooseRecord => {
  if (!items) return {};
  if (typeof items === 'string') {
    try {
      return JSON.parse(items || '{}');
    } catch {
      return {};
    }
  }
  return typeof items === 'object' && !Array.isArray(items) ? items : {};
};

const mergeEndpointGroup = (
  current: unknown,
  group?: EndpointGroup,
): string => {
  const groupItems = coerceEndpointGroup(group?.items);
  const currentText = String(current || '').trim();
  let base: LooseRecord = {};
  if (currentText) {
    try {
      const parsed = JSON.parse(currentText);
      base = typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
    } catch {
      base = {};
    }
  }
  return JSON.stringify({ ...base, ...groupItems }, null, 2);
};

const errorMessage = (message: unknown): ReactNode =>
  typeof message === 'string' ? message : null;

/**
 * The model's 资料 tab: who the model is, what it can do and how it is
 * reached. For a registered model the facts section (maker, release date,
 * lock) sits on top and is saved by the same button.
 */
function ModelMetaForm({
  beforeSave,
  createVendor,
  creatingVendor,
  editingModel,
  lead,
  models,
  onCancel,
  onDelete,
  onSaved,
  prefillModel,
}: ModelMetaFormProps) {
  const { t } = models;
  const makersQuery = useModelMakersQuery();
  /* models.dev lookup for a new model: what it found, and the values it put
     in, so the save can tell prefilled facts from typed ones. */
  const [lookup, setLookup] = useState<{
    error?: string;
    found: boolean;
    name: string;
    pending: boolean;
    prefill?: ModelInfoPrefill;
  } | null>(null);
  const lookupGeneration = useRef(0);
  const lookupPending = useRef(false);
  const manualFields = useRef(new Set<string>());
  const applyingPrefill = useRef(false);
  const appliedPrefill = useRef(
    new Map<string, { before: unknown; value: unknown }>(),
  );
  const [newVendorName, setNewVendorName] = useState('');
  const [vendorFormOpen, setVendorFormOpen] = useState(false);
  const isEdit = Boolean(editingModel?.id);
  const detailQuery = useModelDetailQuery(editingModel?.id, isEdit);
  const defaultValues = useMemo(() => getDefaultModelFormValues(), []);
  const schema = useMemo(() => buildModelFormSchema(t), [t]);
  const form = useForm<ModelFormValues>({
    defaultValues,
    resolver: zodResolver(
      schema,
    ) as unknown as UseFormProps<ModelFormValues>['resolver'],
  });
  const setLookupValue = useCallback(
    (key: string, value: unknown, shouldValidate = false) => {
      applyingPrefill.current = true;
      try {
        form.setValue(key as never, value as never, {
          shouldDirty: true,
          shouldValidate,
        });
      } finally {
        applyingPrefill.current = false;
      }
    },
    [form],
  );
  useEffect(() => {
    const subscription = form.watch((_values, { name, type }) => {
      // Some form notifications retain a previous change type; exclude our own writes explicitly.
      if (!applyingPrefill.current && name && type === 'change')
        manualFields.current.add(name.split('.')[0]);
    });
    return () => subscription.unsubscribe();
  }, [form]);
  const values = form.watch();
  const loading =
    models.saveModelMutation.isPending ||
    detailQuery.isFetching ||
    Boolean(models.editorOptionsLoading);
  const selectedTags = Array.isArray(values.tags) ? values.tags : [];

  useEffect(() => {
    if (isEdit && detailQuery.data) {
      form.reset({
        ...defaultValues,
        ...detailQuery.data,
      });
      return;
    }
    if (!isEdit) {
      manualFields.current.clear();
      form.reset({
        ...defaultValues,
        model_name: editingModel?.model_name || prefillModel || '',
      });
      if (prefillModel) void runLookup(prefillModel);
    }
    // runLookup is stable enough for a mount-time prefill.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defaultValues, detailQuery.data, editingModel?.model_name, form, isEdit]);

  // Preselect the vendor row a found model most plausibly belongs to, once
  // both the lookup and the vendor list have arrived.
  const foundMaker =
    lookup?.found &&
    lookup.name ===
      String(values.model_name || '')
        .trim()
        .toLowerCase()
      ? lookup.prefill?.maker
      : undefined;
  useEffect(() => {
    if (
      isEdit ||
      !foundMaker ||
      manualFields.current.has('vendor_id') ||
      form.getValues('vendor_id')
    )
      return;
    const hint = MAKER_VENDOR_HINTS[foundMaker];
    const vendor = hint
      ? models.vendors.find((item) => hint.test(String(item.name || '')))
      : undefined;
    if (vendor) {
      appliedPrefill.current.set('vendor_id', {
        before: form.getValues('vendor_id'),
        value: Number(vendor.internal_id ?? vendor.id),
      });
      setLookupValue(
        'vendor_id',
        Number(vendor.internal_id ?? vendor.id),
        true,
      );
    }
  }, [foundMaker, form, isEdit, models.vendors, setLookupValue]);

  const runLookup = async (rawName: string) => {
    const name = rawName.trim().toLowerCase();
    if (!name || isEdit) return;
    if (lookup?.name === name && !lookup.pending && !lookup.error) return;
    const generation = ++lookupGeneration.current;
    lookupPending.current = true;
    setLookup({ found: false, name, pending: true });
    try {
      const result = await lookupModelInfo(name);
      if (
        generation !== lookupGeneration.current ||
        name !== String(form.getValues('model_name')).trim().toLowerCase()
      )
        return;
      const prefill = result.prefill;
      setLookup({ found: result.found, name, pending: false, prefill });
      if (!result.found) return;
      const set = (key: string, value: unknown) => {
        if (
          manualFields.current.has(key) ||
          (key === 'open_weights' && manualFields.current.has('open_source'))
        )
          return;
        appliedPrefill.current.set(key, {
          before: form.getValues(key as never),
          value,
        });
        setLookupValue(key, value);
      };
      if (prefill.context_length)
        set('context_window', String(prefill.context_length));
      if (prefill.max_output_tokens)
        set('max_tokens', String(prefill.max_output_tokens));
      set('maker', prefill.maker || '');
      set('release_date', prefill.release_date || '');
      set('knowledge_cutoff', prefill.knowledge_cutoff || '');
      set('input_limit', prefill.input_limit || 0);
      set('modalities', prefill.modalities || { input: [], output: [] });
      set('reasoning_options', prefill.reasoning_options || {});
      set('unsupported_params', prefill.unsupported_params || []);
      set('open_weights', Boolean(prefill.open_weights));
      set('open_source', Boolean(prefill.open_weights));
      set('models_dev_id', prefill.models_dev_id || '');
      set('official_input_per_mtok', prefill.official_input_per_mtok ?? '');
      set('official_output_per_mtok', prefill.official_output_per_mtok ?? '');
      set(
        'capabilities',
        Object.fromEntries(
          capabilityKeys.map((key) => [
            key,
            Boolean(prefill.capabilities?.[key]),
          ]),
        ),
      );
      if (prefill.description && !form.getValues('description_en')) {
        set('description_en', prefill.description);
      }
    } catch (error) {
      if (generation !== lookupGeneration.current) return;
      setLookup({
        error: error instanceof Error ? error.message : String(error),
        found: false,
        name,
        pending: false,
      });
    } finally {
      if (generation === lookupGeneration.current)
        lookupPending.current = false;
    }
  };

  const clearLookup = () => {
    if (isEdit) return;
    lookupGeneration.current++;
    lookupPending.current = false;
    setLookup(null);
    for (const [key, { before, value }] of appliedPrefill.current) {
      if (
        !manualFields.current.has(key) &&
        JSON.stringify(form.getValues(key as never)) === JSON.stringify(value)
      ) {
        setLookupValue(key, before);
      }
    }
    appliedPrefill.current.clear();
  };

  const tagOptions = useMemo(
    () => withSelectedOptions(models.tagOptions, selectedTags),
    [models.tagOptions, selectedTags],
  );

  const submit = form.handleSubmit(async (formValues) => {
    if (!isEdit && lookupPending.current) return;
    if (beforeSave && !(await beforeSave())) return;
    const finalValues = isEdit
      ? {
          ...(detailQuery.data || editingModel || {}),
          ...formValues,
        }
      : withModelInfo(
          formValues,
          lookup?.found &&
            lookup.name === String(formValues.model_name).trim().toLowerCase()
            ? lookup.prefill
            : undefined,
          manualFields.current,
        );
    await models.saveModelMutation.mutateAsync({
      editingModel,
      values: finalValues,
    });
    onSaved();
  });

  return (
    <>
      <div className='mo-dialog__body'>
        {lead}
        <form
          id='model-form'
          className='model-ops-form__grid'
          onSubmit={submit}
        >
          {/* ───── LEFT pane: who is this model ───── */}
          <div className='model-ops-form__pane'>
            <section className='model-ops-form__section'>
              <h3 className='model-ops-form__section-title'>
                {t(`${ns}.section.identity`)}
              </h3>
              <p className='model-ops-form__section-desc'>
                {t(`${ns}.section.identityDescription`)}
              </p>
              <div className='model-ops-form__fields'>
                <Field>
                  <label className='lazu-label' htmlFor='model-name'>
                    {t(`${ns}.field.model_name`)}
                  </label>
                  <Input
                    disabled={loading}
                    id='model-name'
                    placeholder={t(`${ns}.placeholder.model_name`)}
                    {...form.register('model_name', {
                      onBlur: (event) => void runLookup(event.target.value),
                      onChange: clearLookup,
                    })}
                  />
                  <FieldMessage error>
                    {errorMessage(form.formState.errors.model_name?.message)}
                  </FieldMessage>
                </Field>
                {!isEdit && lookup && !lookup.pending ? (
                  lookup.error ? (
                    <InlineNotice tone='danger' title={lookup.error} />
                  ) : lookup.found ? (
                    <InlineNotice
                      tone='success'
                      title={t('page.console.modelOps.register.found', {
                        defaultValue: '在 models.dev 找到 {{name}}',
                        name: lookup.prefill?.name || lookup.name,
                      })}
                      description={t(
                        'page.console.modelOps.register.foundHint',
                        {
                          count: lookup.prefill?.filled.length || 0,
                          defaultValue:
                            '已填入 {{count}} 项。改过的字段会记成手填，以后同步不会覆盖。',
                        },
                      )}
                    />
                  ) : (
                    <InlineNotice
                      title={t('page.console.modelOps.register.notFound', {
                        defaultValue: 'models.dev 里没有这个 ID',
                      })}
                      description={t(
                        'page.console.modelOps.register.notFoundHint',
                        { defaultValue: '按需要手填下面的资料。' },
                      )}
                    />
                  )
                ) : null}
                <Field>
                  <label className='lazu-label' htmlFor='model-family'>
                    {t(`${ns}.field.family`, { defaultValue: '模型家族' })}
                  </label>
                  <Input
                    disabled={loading}
                    id='model-family'
                    placeholder={t(`${ns}.placeholder.family`, {
                      defaultValue: '例如 kimi-k2、gpt-5',
                    })}
                    {...form.register('family')}
                  />
                  <FieldMessage>
                    {t(`${ns}.help.family`, {
                      defaultValue:
                        '用于复用协议能力配置；同一能力族的模型填写相同值。',
                    })}
                  </FieldMessage>
                </Field>
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.vendor_id`)}
                  </label>
                  <Controller
                    control={form.control}
                    name='vendor_id'
                    render={({ field }) => (
                      <Select
                        disabled={loading}
                        placeholder={t(`${ns}.placeholder.vendor_id`)}
                        value={String(field.value || '')}
                        onValueChange={field.onChange}
                      >
                        {models.vendors.map((vendor) => (
                          <SelectItem
                            key={vendor.id}
                            value={String(vendor.internal_id ?? vendor.id)}
                          >
                            {vendor.name || vendor.id}
                          </SelectItem>
                        ))}
                      </Select>
                    )}
                  />
                  <FieldMessage error>
                    {errorMessage(form.formState.errors.vendor_id?.message)}
                  </FieldMessage>
                  {vendorFormOpen ? (
                    <div className='model-ops__vendor-row'>
                      <Input
                        aria-label={t(`${ns}.placeholder.new_vendor`, {
                          defaultValue: '新厂商名称，例如 Anthropic',
                        })}
                        disabled={creatingVendor}
                        placeholder={t(`${ns}.placeholder.new_vendor`, {
                          defaultValue: '新厂商名称，例如 Anthropic',
                        })}
                        value={newVendorName}
                        onChange={(event) =>
                          setNewVendorName(event.target.value)
                        }
                      />
                      <Button
                        disabled={!newVendorName.trim()}
                        loading={creatingVendor}
                        size='sm'
                        type='button'
                        variant='secondary'
                        onClick={async () => {
                          const created = await createVendor(newVendorName);
                          if (created?.internal_id) {
                            form.setValue(
                              'vendor_id',
                              created.internal_id as never,
                              { shouldDirty: true, shouldValidate: true },
                            );
                          }
                          setNewVendorName('');
                          setVendorFormOpen(false);
                        }}
                      >
                        <Check size={14} />
                        {t(`${ns}.action.createVendor`, {
                          defaultValue: '创建厂商',
                        })}
                      </Button>
                      <Button
                        size='sm'
                        type='button'
                        variant='ghost'
                        onClick={() => {
                          setNewVendorName('');
                          setVendorFormOpen(false);
                        }}
                      >
                        {t(`${ns}.action.cancel`)}
                      </Button>
                    </div>
                  ) : (
                    <Button
                      size='xs'
                      type='button'
                      variant='link'
                      onClick={() => setVendorFormOpen(true)}
                    >
                      <Plus size={12} />
                      {t(`${ns}.action.newVendor`, {
                        defaultValue: '找不到厂商？新建一个',
                      })}
                    </Button>
                  )}
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='model-icon'>
                    {t(`${ns}.field.icon`)}
                  </label>
                  <Input
                    disabled={loading}
                    id='model-icon'
                    placeholder={t(`${ns}.placeholder.icon`)}
                    {...form.register('icon')}
                  />
                </Field>
              </div>
            </section>

            {!isEdit ? (
              <section className='model-ops-form__section'>
                <h3 className='model-ops-form__section-title'>
                  {t('page.console.modelOps.register.section', {
                    defaultValue: '资料',
                  })}
                </h3>
                <p className='model-ops-form__section-desc'>
                  {t('page.console.modelOps.register.sectionHint', {
                    defaultValue:
                      '发布日期决定它在广场和台账里的位置；输入模型 ID 后会从 models.dev 自动填入。',
                  })}
                </p>
                <div className='model-ops-form__fields'>
                  <Field>
                    <label className='lazu-label'>
                      {t('page.console.modelOps.info.field.maker', {
                        defaultValue: '厂商',
                      })}
                    </label>
                    <Controller
                      control={form.control}
                      name={'maker' as never}
                      render={({ field }) => (
                        <Select
                          disabled={loading}
                          value={String(field.value || 'none')}
                          onValueChange={(value) =>
                            field.onChange(value === 'none' ? '' : value)
                          }
                        >
                          <SelectItem value='none'>
                            {t('page.console.modelOps.info.makerNone', {
                              defaultValue: '未归类',
                            })}
                          </SelectItem>
                          {(makersQuery.data?.makers || []).map((maker) => (
                            <SelectItem key={maker.key} value={maker.key}>
                              {makerFullLabel(maker.key, t)}
                            </SelectItem>
                          ))}
                        </Select>
                      )}
                    />
                  </Field>
                  <Field>
                    <label className='lazu-label' htmlFor='model-release'>
                      {t('page.console.modelOps.info.field.release_date', {
                        defaultValue: '发布日期',
                      })}
                    </label>
                    <Input
                      disabled={loading}
                      id='model-release'
                      placeholder='YYYY-MM-DD'
                      {...form.register('release_date' as never)}
                    />
                  </Field>
                  <Field>
                    <label className='lazu-label' htmlFor='model-knowledge'>
                      {t('page.console.modelOps.info.field.knowledge_cutoff', {
                        defaultValue: '知识截止',
                      })}
                    </label>
                    <Input
                      disabled={loading}
                      id='model-knowledge'
                      placeholder={t(
                        'page.console.modelOps.register.knowledgePlaceholder',
                        { defaultValue: '例如 2026-03' },
                      )}
                      {...form.register('knowledge_cutoff' as never)}
                    />
                  </Field>
                  <Field>
                    <label className='lazu-label' htmlFor='model-official-in'>
                      {t('page.console.modelOps.register.officialIn', {
                        defaultValue: '官方输入价 $/M',
                      })}
                    </label>
                    <Input
                      disabled={loading}
                      id='model-official-in'
                      inputMode='decimal'
                      {...form.register('official_input_per_mtok' as never)}
                    />
                  </Field>
                  <Field>
                    <label className='lazu-label' htmlFor='model-official-out'>
                      {t('page.console.modelOps.register.officialOut', {
                        defaultValue: '官方输出价 $/M',
                      })}
                    </label>
                    <Input
                      disabled={loading}
                      id='model-official-out'
                      inputMode='decimal'
                      {...form.register('official_output_per_mtok' as never)}
                    />
                  </Field>
                  <Field>
                    <label className='lazu-label'>
                      {t('page.console.modelOps.info.field.modalitiesIn', {
                        defaultValue: '输入',
                      })}
                    </label>
                    <Controller
                      control={form.control}
                      name={'modalities' as never}
                      render={({ field }) => {
                        const current = (field.value || {}) as {
                          input?: string[];
                          output?: string[];
                        };
                        return (
                          <MultiSelect
                            disabled={loading}
                            emptyText={t(`${ns}.empty.options`)}
                            options={MODALITY_OPTIONS.map((value) => ({
                              label: modalityLabel(value, t),
                              value,
                            }))}
                            value={current.input || []}
                            onValueChange={(input) =>
                              field.onChange({
                                ...current,
                                input,
                                output: current.output?.length
                                  ? current.output
                                  : ['text'],
                              })
                            }
                          />
                        );
                      }}
                    />
                  </Field>
                </div>
              </section>
            ) : null}

            <section className='model-ops-form__section'>
              <h3 className='model-ops-form__section-title'>
                {t(`${ns}.section.copy`)}
              </h3>
              <p className='model-ops-form__section-desc'>
                {t(`${ns}.section.copyDescription`)}
              </p>
              <div className='model-ops-form__fields'>
                <Field>
                  <label className='lazu-label' htmlFor='model-description'>
                    {t(`${ns}.field.description`)}
                  </label>
                  <Textarea
                    disabled={loading}
                    id='model-description'
                    placeholder={t(`${ns}.placeholder.description`)}
                    rows={2}
                    {...form.register('description')}
                  />
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='model-description-en'>
                    {t(`${ns}.field.description_en`)}
                  </label>
                  <Textarea
                    disabled={loading}
                    id='model-description-en'
                    placeholder={t(`${ns}.placeholder.description_en`)}
                    rows={3}
                    {...form.register('description_en')}
                  />
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='model-description-zh'>
                    {t(`${ns}.field.description_zh`)}
                  </label>
                  <Textarea
                    disabled={loading}
                    id='model-description-zh'
                    placeholder={t(`${ns}.placeholder.description_zh`)}
                    rows={3}
                    {...form.register('description_zh')}
                  />
                </Field>
              </div>
            </section>
          </div>

          {/* ───── RIGHT pane: how does this model behave ───── */}
          <div className='model-ops-form__pane'>
            <section className='model-ops-form__section'>
              <h3 className='model-ops-form__section-title'>
                {t(`${ns}.section.routing`)}
              </h3>
              <p className='model-ops-form__section-desc'>
                {t(`${ns}.section.routingDescription`)}
              </p>
              <div className='model-ops-form__fields'>
                <Field>
                  <label className='lazu-label'>{t(`${ns}.field.tags`)}</label>
                  <Controller
                    control={form.control}
                    name='tags'
                    render={({ field }) => (
                      <MultiSelect
                        disabled={loading}
                        emptyText={t(`${ns}.empty.options`)}
                        options={tagOptions}
                        placeholder={t(`${ns}.placeholder.tags`)}
                        value={Array.isArray(field.value) ? field.value : []}
                        onValueChange={field.onChange}
                      />
                    )}
                  />
                </Field>
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.endpoints`)}
                  </label>
                  <Controller
                    control={form.control}
                    name='endpoints'
                    render={({ field }) => (
                      <Textarea
                        disabled={loading}
                        placeholder={t(`${ns}.placeholder.endpoints`)}
                        rows={4}
                        value={String(field.value || '')}
                        onChange={field.onChange}
                      />
                    )}
                  />
                  {models.endpointGroups.length ? (
                    <div className='model-ops-form__presets'>
                      {models.endpointGroups.map((group) => (
                        <Button
                          key={group.id || group.name}
                          disabled={loading}
                          size='xs'
                          type='button'
                          variant='secondary'
                          onClick={() =>
                            form.setValue(
                              'endpoints',
                              mergeEndpointGroup(
                                form.getValues('endpoints'),
                                group,
                              ),
                              {
                                shouldDirty: true,
                                shouldValidate: true,
                              },
                            )
                          }
                        >
                          {group.name}
                        </Button>
                      ))}
                    </div>
                  ) : null}
                  <FieldMessage>{t(`${ns}.help.endpoints`)}</FieldMessage>
                  <FieldMessage error>
                    {errorMessage(form.formState.errors.endpoints?.message)}
                  </FieldMessage>
                </Field>
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.video_capabilities`)}
                  </label>
                  <Controller
                    control={form.control}
                    name='video_capabilities'
                    render={({ field }) => (
                      <Textarea
                        disabled={loading}
                        placeholder={t(`${ns}.placeholder.video_capabilities`)}
                        rows={3}
                        value={String(field.value || '')}
                        onChange={field.onChange}
                      />
                    )}
                  />
                  <FieldMessage>
                    {t(`${ns}.help.video_capabilities`)}
                  </FieldMessage>
                  <FieldMessage error>
                    {errorMessage(
                      form.formState.errors.video_capabilities?.message,
                    )}
                  </FieldMessage>
                </Field>
              </div>
            </section>

            <section className='model-ops-form__section'>
              <h3 className='model-ops-form__section-title'>
                {t(`${ns}.section.capabilities`)}
              </h3>
              <p className='model-ops-form__section-desc'>
                {t(`${ns}.section.capabilitiesDescription`)}
              </p>
              <div className='model-ops-form__capabilities'>
                {capabilityKeys.map((capability) => (
                  <Controller
                    key={capability}
                    control={form.control}
                    name={`capabilities.${capability}`}
                    render={({ field }) => (
                      <Switch
                        checked={Boolean(field.value)}
                        disabled={loading}
                        onCheckedChange={field.onChange}
                      >
                        {t(`${ns}.capability.${capability}`)}
                      </Switch>
                    )}
                  />
                ))}
              </div>
            </section>

            <section className='model-ops-form__section'>
              <h3 className='model-ops-form__section-title'>
                {t(`${ns}.section.billing`)}
              </h3>
              <p className='model-ops-form__section-desc'>
                {t(`${ns}.section.billingDescription`)}
              </p>
              <div className='model-ops-form__fields model-ops-form__fields--two-col'>
                <Field>
                  <label className='lazu-label' htmlFor='model-max-tokens'>
                    {t(`${ns}.field.max_tokens`)}
                  </label>
                  <Input
                    disabled={loading}
                    id='model-max-tokens'
                    placeholder={t(`${ns}.placeholder.max_tokens`)}
                    {...form.register('max_tokens')}
                  />
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='model-context-window'>
                    {t(`${ns}.field.context_window`)}
                  </label>
                  <Input
                    disabled={loading}
                    id='model-context-window'
                    placeholder={t(`${ns}.placeholder.context_window`)}
                    {...form.register('context_window')}
                  />
                </Field>
              </div>
              <div className='model-ops-form__fields'>
                <Field>
                  <label className='lazu-label'>
                    {t(`${ns}.field.lifecycle_status`)}
                  </label>
                  <Controller
                    control={form.control}
                    name='lifecycle_status'
                    render={({ field }) => (
                      <Select
                        disabled={loading}
                        value={String(field.value || 'active')}
                        onValueChange={field.onChange}
                      >
                        {lifecycleStatuses.map((status) => (
                          <SelectItem key={status} value={status}>
                            {t(`${ns}.lifecycle.${status}`)}
                          </SelectItem>
                        ))}
                      </Select>
                    )}
                  />
                </Field>
                <Field>
                  <label className='lazu-label' htmlFor='replacement-model'>
                    {t(`${ns}.field.replacement_model`)}
                  </label>
                  <Input
                    disabled={loading}
                    id='replacement-model'
                    placeholder={t(`${ns}.placeholder.replacement_model`)}
                    {...form.register('replacement_model')}
                  />
                </Field>
              </div>
              <div className='model-ops-form__switches'>
                <Controller
                  control={form.control}
                  name='open_source'
                  render={({ field }) => (
                    <Switch
                      checked={Boolean(field.value)}
                      disabled={loading}
                      onCheckedChange={field.onChange}
                    >
                      {t(`${ns}.field.open_source`)}
                    </Switch>
                  )}
                />
                <Controller
                  control={form.control}
                  name='status'
                  render={({ field }) => (
                    <Switch
                      checked={Boolean(field.value)}
                      disabled={loading}
                      onCheckedChange={field.onChange}
                    >
                      {t(`${ns}.field.status`)}
                    </Switch>
                  )}
                />
                <Controller
                  control={form.control}
                  name='sync_official'
                  render={({ field }) => (
                    <Switch
                      checked={Boolean(field.value)}
                      disabled={loading}
                      onCheckedChange={field.onChange}
                    >
                      {t(`${ns}.field.sync_official`)}
                    </Switch>
                  )}
                />
              </div>
            </section>
          </div>
        </form>
      </div>
      <div className='mo-dialog__foot'>
        {isEdit && onDelete ? (
          <Button
            size='sm'
            style={{ color: 'var(--lazu-danger)' }}
            type='button'
            variant='secondary'
            onClick={onDelete}
          >
            {t('page.console.modelOps.models.deleteTitle', {
              defaultValue: '删除模型',
            })}
          </Button>
        ) : (
          <span />
        )}
        <div className='mo-dialog__foot-actions'>
          <Button
            size='sm'
            type='button'
            variant='secondary'
            onClick={onCancel}
          >
            {t(`${ns}.action.cancel`)}
          </Button>
          <Button
            form='model-form'
            loading={loading || Boolean(lookup?.pending)}
            size='sm'
            type='submit'
            variant='primary'
          >
            <Check size={14} />
            {isEdit
              ? t('common.save', { defaultValue: '保存' })
              : t('page.console.modelOps.inspector.createModel', {
                  defaultValue: '登记模型',
                })}
          </Button>
        </div>
      </div>
    </>
  );
}

export default ModelMetaForm;
