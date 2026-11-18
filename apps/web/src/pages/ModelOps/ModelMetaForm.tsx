import { zodResolver } from '@hookform/resolvers/zod';
import { Check, ChevronDown, Plus, TriangleAlert } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { Controller, useForm, type UseFormProps } from 'react-hook-form';
import { z } from 'zod';
import { formatInfoValue, modalityLabel } from './modelInfo';
import { withModelInfo } from './modelRegistration';
import { Button } from '../../components/ui/button';
import { Field, FieldMessage } from '../../components/ui/field';
import { InlineNotice } from '../../components/ui/inline-notice';
import { Input } from '../../components/ui/input';
import { MultiSelect, Select, SelectItem } from '../../components/ui/select';
import { Switch } from '../../components/ui/switch';
import { Textarea } from '../../components/ui/textarea';
import { API } from '../../helpers/api';
import {
  lookupModelInfo,
  useModelInfoMutations,
  useModelInfoReviewsQuery,
  useModelMakersQuery,
  type ModelInfoPrefill,
} from '../../hooks/models/useModelInfoQuery';
import {
  getDefaultModelFormValues,
  useModelDetailQuery,
} from '../../hooks/models/useModelsQuery';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { makerFullLabel } from '../../lib/model-makers';
import type { ApiRecord } from './apiRecord';
import type { TFunction } from 'i18next';

const ns = 'page.console.models.form';
const infoNs = 'page.console.modelOps.info';

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
   *  of its own — creating the brand inline here is the only place an operator
   *  actually needs it. */
  createVendor: (name: string) => Promise<{ internal_id?: number } | undefined>;
  creatingVendor: boolean;
  editingModel?: LooseRecord | null;
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
  /** A model id to register, looked up on models.dev as the form opens. */
  prefillModel?: string;
};

const INPUT_MODALITY_OPTIONS = ['text', 'image', 'pdf', 'audio', 'video'];
const OUTPUT_MODALITY_OPTIONS = ['text', 'image', 'audio'];

const CAPABILITY_CHIPS: Array<[CapabilityKey, string]> = [
  ['supports_reasoning', '推理'],
  ['supports_function_calling', '工具调用'],
  ['supports_prompt_caching', '提示缓存'],
  ['supports_response_schema', '结构化输出'],
  ['supports_vision', '视觉'],
  ['supports_pdf_input', 'PDF'],
  ['supports_computer_use', 'Computer Use'],
  ['supports_web_search', '联网搜索'],
  ['supports_parallel_function_calling', '并行工具'],
  ['supports_audio_input', '音频输入'],
  ['supports_audio_output', '音频输出'],
];

const capabilityKeys: CapabilityKey[] = CAPABILITY_CHIPS.map(([key]) => key);

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

const lifecycleStatuses = ['active', 'candidate', 'retired'];

const shortTokens = (raw: unknown) => {
  const cleaned = String(raw || '').replace(/,/g, '');
  const number = Number(cleaned) || 0;
  if (number <= 0) return '';
  if (number >= 1_000_000) return `${Math.round(number / 100_000) / 10}M`;
  return `${Math.round(number / 1000)}K`;
};

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

const ADVANCED_ERROR_FIELDS = [
  'model_name',
  'vendor_id',
  'endpoints',
  'video_capabilities',
] as const;

/**
 * Unified grouped 资料 form (08-A):
 * 1. 基础归属与时间线 (4-col grid + silent provenance, inline `· 已手改` only when manual)
 * 2. 窗口规格上限 (3-col grid: context, input limit, max output)
 * 3. 模态与能力芯片 (clickable toggle chips for input/output modalities & capabilities)
 * 4. 简介与资料锁 + collapsible 高级路由与端点参数
 */
function ModelMetaForm({
  createVendor,
  creatingVendor,
  editingModel,
  models,
  onCancel,
  onDelete,
  onSaved,
  prefillModel,
}: ModelMetaFormProps) {
  const { t } = models;
  const makersQuery = useModelMakersQuery();
  const reviewsQuery = useModelInfoReviewsQuery();
  const { resolveMutation } = useModelInfoMutations(t);

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
  const [advancedOpen, setAdvancedOpen] = useState(false);

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
      if (!applyingPrefill.current && name && type === 'change') {
        manualFields.current.add(name.split('.')[0]);
      }
    });
    return () => subscription.unsubscribe();
  }, [form]);

  const values = form.watch();
  const loading =
    models.saveModelMutation.isPending ||
    detailQuery.isFetching ||
    Boolean(models.editorOptionsLoading);
  const selectedTags = Array.isArray(values.tags) ? values.tags : [];

  const baseRecord = (detailQuery.data || editingModel || {}) as LooseRecord;
  const sources = (baseRecord.info_sources as Record<string, string>) || {};
  const modelNameForReviews = String(
    baseRecord.model_name || values.model_name || '',
  );

  const pendingReviews = useMemo(
    () =>
      isEdit
        ? (reviewsQuery.data?.items || []).filter(
            (row) =>
              row.model_name === modelNameForReviews &&
              row.status === 'pending' &&
              (row.kind === 'diff' || row.kind === 'first_run'),
          )
        : [],
    [isEdit, modelNameForReviews, reviewsQuery.data],
  );
  const pendingFor = (field: string) =>
    pendingReviews.find((row) => row.field === field);
  const releaseReview = pendingFor('release_date');

  useEffect(() => {
    if (isEdit && detailQuery.data) {
      const rawMod = (detailQuery.data.modalities || {}) as {
        input?: unknown;
        output?: unknown;
      };
      const ctxNum = Number(
        detailQuery.data.context_window ?? detailQuery.data.context_length ?? 0,
      );
      const maxOutNum = Number(
        detailQuery.data.max_tokens ?? detailQuery.data.max_output_tokens ?? 0,
      );
      const inputLimitNum = Number(detailQuery.data.input_limit ?? 0);
      form.reset({
        ...defaultValues,
        ...detailQuery.data,
        context_window: ctxNum > 0 ? String(ctxNum) : '',
        input_limit: inputLimitNum > 0 ? String(inputLimitNum) : '',
        max_tokens: maxOutNum > 0 ? String(maxOutNum) : '',
        maker: String(
          detailQuery.data.maker || detailQuery.data.maker_resolved || '',
        ),
        release_date: String(detailQuery.data.release_date || ''),
        knowledge_cutoff: String(detailQuery.data.knowledge_cutoff || ''),
        info_locked: Boolean(detailQuery.data.info_locked),
        open_source: Boolean(
          detailQuery.data.open_source || detailQuery.data.open_weights,
        ),
        modalities: {
          input: Array.isArray(rawMod.input) ? rawMod.input.map(String) : [],
          output: Array.isArray(rawMod.output) ? rawMod.output.map(String) : [],
        },
      });
      return;
    }
    if (!isEdit) {
      manualFields.current.clear();
      form.reset({
        ...defaultValues,
        model_name: editingModel?.model_name || prefillModel || '',
        maker: '',
        release_date: '',
        knowledge_cutoff: '',
        input_limit: '',
        info_locked: false,
      });
      if (prefillModel) void runLookup(prefillModel);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defaultValues, detailQuery.data, editingModel?.model_name, form, isEdit]);

  // Auto-open advanced block on validation error inside it.
  useEffect(() => {
    const errs = form.formState.errors;
    if (
      ADVANCED_ERROR_FIELDS.some((field) => {
        if (!isEdit && (field === 'model_name' || field === 'vendor_id'))
          return false;
        return Boolean(errs[field]);
      })
    ) {
      setAdvancedOpen(true);
    }
  }, [form.formState.errors, isEdit]);

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
      set(
        'input_limit',
        prefill.input_limit ? String(prefill.input_limit) : '',
      );
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

  const submit = form.handleSubmit(
    async (formValues) => {
      if (!isEdit && lookupPending.current) return;
      const cleanedInputLimit =
        Number.parseInt(
          String(formValues.input_limit || '').replace(/,/g, ''),
          10,
        ) || 0;
      const cleanedContext = String(formValues.context_window || '').replace(
        /,/g,
        '',
      );
      const cleanedMaxOut = String(formValues.max_tokens || '').replace(
        /,/g,
        '',
      );

      const normalizedForm: LooseRecord = {
        ...formValues,
        context_window: cleanedContext,
        input_limit: cleanedInputLimit,
        max_tokens: cleanedMaxOut,
      };

      const normalizedMaker =
        !baseRecord.maker &&
        String(formValues.maker || '') ===
          String(baseRecord.maker_resolved || '')
          ? ''
          : String(formValues.maker || '');
      const normalizedReleaseDate = String(
        formValues.release_date || '',
      ).trim();
      const normalizedLocked = Boolean(formValues.info_locked);

      if (
        isEdit &&
        (String(formValues.maker || '') !==
          String(baseRecord.maker || baseRecord.maker_resolved || '') ||
          normalizedReleaseDate !== String(baseRecord.release_date || '') ||
          normalizedLocked !== Boolean(baseRecord.info_locked))
      ) {
        const infoId = Number(baseRecord.internal_id ?? baseRecord.id);
        if (Number.isFinite(infoId) && infoId > 0) {
          try {
            unwrap(
              await API.put('/api/model_info/model', {
                id: infoId,
                info_locked: normalizedLocked,
                maker: normalizedMaker,
                release_date: normalizedReleaseDate,
              }),
            );
          } catch (error) {
            apiErrorHandler(error);
            return;
          }
        }
      }

      const finalValues = isEdit
        ? {
            ...baseRecord,
            ...normalizedForm,
            info_locked: normalizedLocked,
            knowledge_cutoff: String(formValues.knowledge_cutoff || '').trim(),
            maker: normalizedMaker,
            open_weights: Boolean(formValues.open_source),
            release_date: normalizedReleaseDate,
          }
        : withModelInfo(
            normalizedForm,
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
    },
    (errors) => {
      if (
        ADVANCED_ERROR_FIELDS.some((field) => {
          if (!isEdit && (field === 'model_name' || field === 'vendor_id'))
            return false;
          return Boolean(errors[field]);
        })
      ) {
        setAdvancedOpen(true);
      }
    },
  );

  /** Only show inline `· 已手改` when manually overridden; models.dev stays silent. */
  const manualTag = (field: string) =>
    isEdit && sources[field] === 'manual' ? (
      <span className='mo-manual-tag'>
        {t(`${infoNs}.inlineManual`, { defaultValue: '· 已手改' })}
      </span>
    ) : null;

  const upstreamHint = (field: string) => {
    const review = pendingFor(field);
    return review ? (
      <span className='mo-info__upstream'>
        {t(`${infoNs}.upstream`, {
          defaultValue: '· 上游 {{value}}',
          value: formatInfoValue(field, review.upstream_value, t),
        })}
      </span>
    ) : null;
  };

  const currentModalities = (values.modalities || {}) as {
    input?: string[];
    output?: string[];
  };
  const inputModalities = Array.isArray(currentModalities.input)
    ? currentModalities.input
    : [];
  const outputModalities = Array.isArray(currentModalities.output)
    ? currentModalities.output
    : [];
  const outputOptions = Array.from(
    new Set([...OUTPUT_MODALITY_OPTIONS, ...outputModalities]),
  );

  const toggleModality = (dir: 'input' | 'output', mod: string) => {
    manualFields.current.add('modalities');
    const list = dir === 'input' ? inputModalities : outputModalities;
    const nextList = list.includes(mod)
      ? list.filter((item) => item !== mod)
      : [...list, mod];
    form.setValue(
      'modalities' as never,
      {
        input: dir === 'input' ? nextList : inputModalities,
        output: dir === 'output' ? nextList : outputModalities,
      } as never,
      { shouldDirty: true },
    );
  };

  const currentCaps = (values.capabilities || {}) as Record<string, boolean>;
  const toggleCapability = (cap: CapabilityKey) => {
    manualFields.current.add('capabilities');
    form.setValue(
      'capabilities' as never,
      { ...currentCaps, [cap]: !currentCaps[cap] } as never,
      { shouldDirty: true },
    );
  };

  const reasoning = (values.reasoning_options ||
    baseRecord.reasoning_options ||
    {}) as { efforts?: string[] };
  const unsupported = (values.unsupported_params ||
    baseRecord.unsupported_params ||
    []) as string[];
  const shortCtx = shortTokens(values.context_window);

  const renderVendorField = () => (
    <Field>
      <label className='lazu-label'>
        {t(`${ns}.field.vendor_id`, { defaultValue: '目录供应商' })}
      </label>
      <Controller
        control={form.control}
        name='vendor_id'
        render={({ field }) => (
          <Select
            disabled={loading}
            placeholder={t(`${ns}.placeholder.vendor_id`)}
            value={String(field.value || '')}
            onValueChange={(val) => {
              manualFields.current.add('vendor_id');
              field.onChange(val);
            }}
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
            onChange={(event) => setNewVendorName(event.target.value)}
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
                form.setValue('vendor_id', created.internal_id as never, {
                  shouldDirty: true,
                  shouldValidate: true,
                });
              }
              setNewVendorName('');
              setVendorFormOpen(false);
            }}
          >
            <Check size={14} />
            {t(`${ns}.action.createVendor`, { defaultValue: '创建厂商' })}
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
  );

  const renderModelNameField = () => (
    <Field>
      <label className='lazu-label' htmlFor='model-name'>
        {t(`${ns}.field.model_name`)}
      </label>
      <Input
        className='mo-mono'
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
  );

  return (
    <>
      <div className='mo-dialog__body'>
        <form className='mo-meta-form' id='model-form' onSubmit={submit}>
          {/* Registration lead block when creating a new model */}
          {!isEdit ? (
            <section className='mo-meta-sect mo-meta-sect--flush'>
              <div className='mo-meta-grid mo-meta-grid--2'>
                {renderModelNameField()}
                {renderVendorField()}
              </div>
              {lookup && !lookup.pending ? (
                lookup.error ? (
                  <InlineNotice tone='danger' title={lookup.error} />
                ) : lookup.found ? (
                  <InlineNotice
                    tone='success'
                    title={t('page.console.modelOps.register.found', {
                      defaultValue: '在 models.dev 找到 {{name}}',
                      name: lookup.prefill?.name || lookup.name,
                    })}
                    description={t('page.console.modelOps.register.foundHint', {
                      count: lookup.prefill?.filled.length || 0,
                      defaultValue:
                        '已填入 {{count}} 项。改过的字段会记成手填，以后同步不会覆盖。',
                    })}
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
              <div className='mo-meta-grid mo-meta-grid--2'>
                <Field>
                  <label className='lazu-label' htmlFor='model-official-in'>
                    {t('page.console.modelOps.register.officialIn', {
                      defaultValue: '官方输入价 $/M',
                    })}
                  </label>
                  <Input
                    className='mo-mono'
                    disabled={loading}
                    id='model-official-in'
                    inputMode='decimal'
                    placeholder='0.00'
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
                    className='mo-mono'
                    disabled={loading}
                    id='model-official-out'
                    inputMode='decimal'
                    placeholder='0.00'
                    {...form.register('official_output_per_mtok' as never)}
                  />
                </Field>
              </div>
            </section>
          ) : null}

          {/* Group 1 · 基础归属与时间线 */}
          <section
            className={`mo-meta-sect${isEdit ? ' mo-meta-sect--flush' : ''}`}
          >
            <div className='mo-meta-sect__head'>
              <span className='mo-meta-sect__title'>
                {t(`${infoNs}.group.identity`, {
                  defaultValue: '基础归属与时间线',
                })}
              </span>
              {isEdit ? (
                <span className='mo-meta-sect__note'>
                  {t(`${infoNs}.silentSyncNote`, {
                    defaultValue: '未标注项均自动同步自 models.dev',
                  })}
                </span>
              ) : null}
            </div>
            <div className='mo-meta-grid mo-meta-grid--4'>
              <Field>
                <label className='lazu-label'>
                  {t(`${infoNs}.field.maker`, { defaultValue: '归属厂商' })}
                  {manualTag('maker')}
                </label>
                <Controller
                  control={form.control}
                  name={'maker' as never}
                  render={({ field }) => (
                    <Select
                      aria-label={t(`${infoNs}.field.maker`, {
                        defaultValue: '归属厂商',
                      })}
                      disabled={loading}
                      value={String(field.value || 'none')}
                      onValueChange={(value) => {
                        manualFields.current.add('maker');
                        field.onChange(value === 'none' ? '' : value);
                      }}
                    >
                      <SelectItem value='none'>
                        {t(`${infoNs}.makerNone`, { defaultValue: '未归类' })}
                      </SelectItem>
                      {(makersQuery.data?.makers || []).map((item) => (
                        <SelectItem key={item.key} value={item.key}>
                          {makerFullLabel(item.key, t)}
                        </SelectItem>
                      ))}
                    </Select>
                  )}
                />
              </Field>

              <Field>
                <label className='lazu-label' htmlFor='model-release'>
                  {t(`${infoNs}.field.release_date`, {
                    defaultValue: '发布日期',
                  })}
                  {manualTag('release_date')}
                </label>
                <Input
                  className='mo-mono'
                  disabled={loading}
                  id='model-release'
                  placeholder='YYYY-MM-DD'
                  {...form.register('release_date' as never)}
                />
              </Field>

              <Field>
                <label className='lazu-label' htmlFor='model-knowledge'>
                  {t(`${infoNs}.field.knowledge_cutoff`, {
                    defaultValue: '知识截止',
                  })}
                  {manualTag('knowledge_cutoff')}
                </label>
                <Input
                  className='mo-mono'
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
                <label className='lazu-label'>
                  {t(`${ns}.field.lifecycle_status`, {
                    defaultValue: '目录生命周期',
                  })}
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
            </div>

            {releaseReview ? (
              <div className='mo-info__conflict' role='note'>
                <TriangleAlert aria-hidden='true' size={15} />
                <div>
                  <b>
                    {t(`${infoNs}.conflict.title`, {
                      date: releaseReview.upstream_value,
                      defaultValue: 'models.dev 记的是 {{date}}',
                    })}
                  </b>
                  <p>
                    {releaseReview.kind === 'first_run'
                      ? t(`${infoNs}.conflict.firstRun`, {
                          count: Number(releaseReview.note) || 0,
                          defaultValue:
                            '差了 {{count}} 天。这里原来记的多半是上架那天。',
                        })
                      : t(`${infoNs}.conflict.diff`, {
                          defaultValue:
                            '这个名字可能已经指向新版本，也可能是上游记错了。',
                        })}
                  </p>
                  <div className='mo-info__conflict-actions'>
                    <Button
                      disabled={resolveMutation.isPending}
                      size='xs'
                      type='button'
                      variant='secondary'
                      onClick={() =>
                        resolveMutation.mutate({
                          action: 'accept',
                          id: releaseReview.id,
                        })
                      }
                    >
                      {t(`${infoNs}.conflict.accept`, {
                        date: releaseReview.upstream_value,
                        defaultValue: '改成 {{date}}',
                      })}
                    </Button>
                    <Button
                      disabled={resolveMutation.isPending}
                      size='xs'
                      type='button'
                      variant='secondary'
                      onClick={() =>
                        resolveMutation.mutate({
                          action: 'keep',
                          id: releaseReview.id,
                        })
                      }
                    >
                      {t(`${infoNs}.conflict.keep`, {
                        defaultValue: '保留，不再提示',
                      })}
                    </Button>
                  </div>
                </div>
              </div>
            ) : null}
          </section>

          {/* Group 2 · 窗口规格上限 */}
          <section className='mo-meta-sect'>
            <div className='mo-meta-sect__head'>
              <span className='mo-meta-sect__title'>
                {t(`${infoNs}.group.limits`, { defaultValue: '窗口规格上限' })}
              </span>
            </div>
            <div className='mo-meta-grid mo-meta-grid--3'>
              <Field>
                <label className='lazu-label' htmlFor='model-context-window'>
                  {t(`${ns}.field.context_window`, {
                    defaultValue: '上下文窗口',
                  })}
                  {manualTag('context_length')}
                  {upstreamHint('context_length')}
                </label>
                <Input
                  className='mo-mono'
                  disabled={loading}
                  id='model-context-window'
                  placeholder={t(`${ns}.placeholder.context_window`, {
                    defaultValue: '例如 200000',
                  })}
                  {...form.register('context_window')}
                />
              </Field>

              <Field>
                <label className='lazu-label' htmlFor='model-input-limit'>
                  {t(`${infoNs}.field.input_limit`, {
                    defaultValue: '单次输入上限',
                  })}
                  {manualTag('input_limit')}
                  {upstreamHint('input_limit')}
                </label>
                <Input
                  className='mo-mono'
                  disabled={loading}
                  id='model-input-limit'
                  placeholder={
                    shortCtx
                      ? t(`${infoNs}.inputLimitPlaceholderCtx`, {
                          ctx: shortCtx,
                          defaultValue: '留空即与上下文相同 ({{ctx}})',
                        })
                      : t(`${infoNs}.inputLimitPlaceholder`, {
                          defaultValue: '留空即与上下文相同',
                        })
                  }
                  {...form.register('input_limit' as never)}
                />
              </Field>

              <Field>
                <label className='lazu-label' htmlFor='model-max-tokens'>
                  {t(`${ns}.field.max_tokens`, { defaultValue: '最大输出' })}
                  {manualTag('max_output_tokens')}
                  {upstreamHint('max_output_tokens')}
                </label>
                <Input
                  className='mo-mono'
                  disabled={loading}
                  id='model-max-tokens'
                  placeholder={t(`${ns}.placeholder.max_tokens`, {
                    defaultValue: '例如 64000',
                  })}
                  {...form.register('max_tokens')}
                />
              </Field>
            </div>
          </section>

          {/* Group 3 · 模态与能力芯片 */}
          <section className='mo-meta-sect'>
            <div className='mo-meta-grid mo-meta-grid--modalities'>
              <div className='mo-chip-group'>
                <div className='mo-chip-group__label'>
                  {t(`${infoNs}.modalitiesInToggle`, {
                    defaultValue: '输入模态（点击切换）',
                  })}
                  {manualTag('modalities')}
                </div>
                <div className='mo-chip-row'>
                  {INPUT_MODALITY_OPTIONS.map((mod) => {
                    const active = inputModalities.includes(mod);
                    return (
                      <button
                        aria-pressed={active}
                        className={`mo-chip-toggle${active ? ' is-on' : ''}`}
                        data-owns-chrome=''
                        disabled={loading}
                        key={mod}
                        type='button'
                        onClick={() => toggleModality('input', mod)}
                      >
                        {active ? <span aria-hidden='true'>✓ </span> : null}
                        {modalityLabel(mod, t)}
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className='mo-chip-group'>
                <div className='mo-chip-group__label'>
                  {t(`${infoNs}.modalitiesOutToggle`, {
                    defaultValue: '输出模态',
                  })}
                </div>
                <div className='mo-chip-row'>
                  {outputOptions.map((mod) => {
                    const active = outputModalities.includes(mod);
                    return (
                      <button
                        aria-pressed={active}
                        className={`mo-chip-toggle${active ? ' is-on' : ''}`}
                        data-owns-chrome=''
                        disabled={loading}
                        key={mod}
                        type='button'
                        onClick={() => toggleModality('output', mod)}
                      >
                        {active ? <span aria-hidden='true'>✓ </span> : null}
                        {modalityLabel(mod, t)}
                      </button>
                    );
                  })}
                </div>
              </div>
            </div>

            <div className='mo-chip-group'>
              <div className='mo-chip-group__label'>
                {t(`${infoNs}.capabilitiesAndThinking`, {
                  defaultValue: '模型能力与推理档位',
                })}
                {manualTag('capabilities')}
              </div>
              <div className='mo-chip-row'>
                {CAPABILITY_CHIPS.map(([cap, fallbackLabel]) => {
                  const active = Boolean(currentCaps[cap]);
                  const effortsSuffix =
                    cap === 'supports_reasoning' && reasoning.efforts?.length
                      ? ` (${reasoning.efforts.join('/')})`
                      : '';
                  return (
                    <button
                      aria-pressed={active}
                      className={`mo-chip-toggle${active ? ' is-on' : ''}`}
                      data-owns-chrome=''
                      disabled={loading}
                      key={cap}
                      type='button'
                      onClick={() => toggleCapability(cap)}
                    >
                      {active ? <span aria-hidden='true'>✓ </span> : null}
                      {t(`${ns}.capability.${cap}`, {
                        defaultValue: fallbackLabel,
                      })}
                      {effortsSuffix}
                    </button>
                  );
                })}
                <button
                  aria-pressed={Boolean(values.open_source)}
                  className={`mo-chip-toggle${values.open_source ? ' is-on' : ''}`}
                  data-owns-chrome=''
                  disabled={loading}
                  type='button'
                  onClick={() => {
                    manualFields.current.add('open_source');
                    const next = !values.open_source;
                    form.setValue('open_source', next, { shouldDirty: true });
                    form.setValue('open_weights' as never, next as never, {
                      shouldDirty: true,
                    });
                  }}
                >
                  {values.open_source ? (
                    <span aria-hidden='true'>✓ </span>
                  ) : null}
                  {t(`${ns}.field.open_source`, { defaultValue: '开源权重' })}
                </button>
              </div>
              {unsupported.length ? (
                <div className='mo-unsupported-note'>
                  {t(`${infoNs}.field.unsupported_params`, {
                    defaultValue: '不支持的参数',
                  })}
                  {': '}
                  <code>{unsupported.join(', ')}</code>
                </div>
              ) : null}
            </div>
          </section>

          {/* Group 4 · 简介与资料锁 + Collapsible 高级路由与端点参数 */}
          <section className='mo-meta-sect'>
            <div className='mo-meta-grid mo-meta-grid--2'>
              <Field>
                <label className='lazu-label' htmlFor='model-description-zh'>
                  {t(`${ns}.field.description_zh`, {
                    defaultValue: '模型中文简介（公开广场展示）',
                  })}
                </label>
                <Input
                  disabled={loading}
                  id='model-description-zh'
                  placeholder={t(`${ns}.placeholder.description_zh`)}
                  {...form.register('description_zh')}
                />
              </Field>
              <Field>
                <label className='lazu-label' htmlFor='model-description-en'>
                  {t(`${ns}.field.description_en`, {
                    defaultValue: '英文简介 (EN)',
                  })}
                </label>
                <Input
                  disabled={loading}
                  id='model-description-en'
                  placeholder={t(`${ns}.placeholder.description_en`)}
                  {...form.register('description_en')}
                />
              </Field>
            </div>

            {isEdit ? (
              <div className='mo-lock-bar'>
                <div className='mo-lock-bar__copy'>
                  <b>{t(`${infoNs}.lock`, { defaultValue: '锁定当前资料' })}</b>
                  <span>
                    {t(`${infoNs}.lockHintShort`, {
                      defaultValue:
                        ' · 开启后每日 models.dev 同步仅提示差异，不自动改写任何字段',
                    })}
                  </span>
                </div>
                <Controller
                  control={form.control}
                  name={'info_locked' as never}
                  render={({ field }) => (
                    <Switch
                      aria-label={t(`${infoNs}.lock`, {
                        defaultValue: '锁定当前资料',
                      })}
                      checked={Boolean(field.value)}
                      disabled={loading}
                      onCheckedChange={field.onChange}
                    />
                  )}
                />
              </div>
            ) : null}

            <details
              className='mo-meta-disclosure'
              open={advancedOpen}
              onToggle={(event) =>
                setAdvancedOpen(
                  (event.currentTarget as HTMLDetailsElement).open,
                )
              }
            >
              <summary className='mo-meta-disclosure__summary'>
                <span>
                  <b>
                    {t(`${infoNs}.advancedRouting`, {
                      defaultValue: '高级路由与端点参数',
                    })}
                  </b>
                  <span className='mo-meta-disclosure__meta'>
                    {t(`${infoNs}.advancedRoutingHint`, {
                      defaultValue: ' · 家族 / 标签 / 端点 JSON / 视频能力',
                    })}
                  </span>
                </span>
                <span className='mo-meta-disclosure__action'>
                  {advancedOpen
                    ? t('common.collapse', { defaultValue: '收起' })
                    : t('common.expand', { defaultValue: '展开' })}
                  <ChevronDown
                    aria-hidden='true'
                    className={`mo-meta-disclosure__chev${advancedOpen ? ' is-open' : ''}`}
                    size={14}
                  />
                </span>
              </summary>

              <div className='mo-meta-disclosure__body'>
                {isEdit ? (
                  <div className='mo-meta-grid mo-meta-grid--2'>
                    {renderModelNameField()}
                    {renderVendorField()}
                  </div>
                ) : null}

                <div className='mo-meta-grid mo-meta-grid--3'>
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

                <div className='mo-meta-grid mo-meta-grid--2'>
                  <Field>
                    <label className='lazu-label'>
                      {t(`${ns}.field.tags`)}
                    </label>
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
                    <label className='lazu-label' htmlFor='model-description'>
                      {t(`${ns}.field.description`)}
                    </label>
                    <Input
                      disabled={loading}
                      id='model-description'
                      placeholder={t(`${ns}.placeholder.description`)}
                      {...form.register('description')}
                    />
                  </Field>
                </div>

                <div className='mo-meta-grid mo-meta-grid--2'>
                  <Field>
                    <label className='lazu-label'>
                      {t(`${ns}.field.endpoints`)}
                    </label>
                    <Controller
                      control={form.control}
                      name='endpoints'
                      render={({ field }) => (
                        <Textarea
                          className='mo-mono'
                          disabled={loading}
                          placeholder={t(`${ns}.placeholder.endpoints`)}
                          rows={3}
                          value={String(field.value || '')}
                          onChange={field.onChange}
                        />
                      )}
                    />
                    {models.endpointGroups.length ? (
                      <div className='model-ops-form__presets'>
                        {models.endpointGroups.map((group) => (
                          <Button
                            disabled={loading}
                            key={group.id || group.name}
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
                          className='mo-mono'
                          disabled={loading}
                          placeholder={t(
                            `${ns}.placeholder.video_capabilities`,
                          )}
                          rows={3}
                          value={String(field.value || '')}
                          onChange={field.onChange}
                        />
                      )}
                    />
                    <FieldMessage error>
                      {errorMessage(
                        form.formState.errors.video_capabilities?.message,
                      )}
                    </FieldMessage>
                  </Field>
                </div>

                <div className='mo-form__switches'>
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
              </div>
            </details>
          </section>
        </form>
      </div>

      <div className='mo-dialog__foot'>
        {isEdit && onDelete ? (
          <Button
            className='mo-btn--danger-outline'
            size='sm'
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
