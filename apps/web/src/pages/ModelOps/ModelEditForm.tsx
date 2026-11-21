import { zodResolver } from '@hookform/resolvers/zod';
import { Check, ChevronDown, Plus, TriangleAlert } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { infoSourceLabel, modalityLabel } from './modelInfo';
import {
  buildModelFormSchema,
  capabilityKeys,
  lifecycleStatuses,
  mergeEndpointGroup,
  MODALITY_OPTIONS,
  type ModelMetaFormProps,
} from './ModelMetaForm';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { MultiSelect, Select, SelectItem } from '../../components/ui/select';
import { Skeleton } from '../../components/ui/skeleton';
import { Switch } from '../../components/ui/switch';
import { Textarea } from '../../components/ui/textarea';
import {
  useModelInfoMutations,
  useModelInfoReviewsQuery,
  useModelMakersQuery,
} from '../../hooks/models/useModelInfoQuery';
import {
  getDefaultModelFormValues,
  useModelDetailQuery,
} from '../../hooks/models/useModelsQuery';
import { makerBrand, makerFullLabel } from '../../lib/model-makers';
import { endpointLabel } from '../Routes/code-samples';
import { ModelFacts } from '../Routes/model-facts';
import type { ApiRecord } from './apiRecord';
import type { ModelView } from './modelView';
import type { CatalogModel } from '../Routes/catalog';
import type { TFunction } from 'i18next';
import type { UseFormProps } from 'react-hook-form';
import './model-edit.css';

const ns = 'page.console.models.form';
const infoNs = 'page.console.modelOps.info';
const editNs = 'page.console.modelOps.edit';

type Values = ReturnType<typeof getDefaultModelFormValues> & ApiRecord;
type Modalities = { input?: string[]; output?: string[] };

const OUTPUT_MODALITIES = ['text', 'image', 'audio', 'video'];

const digits = (value: unknown) =>
  String(value ?? '')
    .replace(/[,\s_]/g, '')
    .trim();
const tokens = (value: unknown) => {
  const n = Number(digits(value));
  return Number.isFinite(n) && n > 0 ? n : 0;
};
const same = (a: unknown, b: unknown) =>
  JSON.stringify(a ?? '') === JSON.stringify(b ?? '');
const listOf = (value: unknown) =>
  Array.isArray(value) ? value.map(String) : [];

/** The interfaces a model is reached on, read from its endpoints value: the
 *  list the server derives, or the keys of an operator's path overrides. */
const endpointTypes = (raw: unknown): string[] => {
  const text = String(raw ?? '').trim();
  if (!text) return [];
  try {
    const parsed = JSON.parse(text);
    if (Array.isArray(parsed)) return parsed.map(String);
    if (parsed && typeof parsed === 'object') return Object.keys(parsed);
  } catch {
    /* Not JSON: the field shows its own error. */
  }
  return [];
};

type Props = Pick<
  ModelMetaFormProps,
  'createVendor' | 'creatingVendor' | 'models' | 'onCancel' | 'onSaved'
> & {
  onDelete: () => void;
  t: TFunction;
  view: ModelView;
};

/**
 * 编辑资料 for a registered model (canvas board Model C): the fields on the
 * left in the order an operator checks them, and on the right the model as the
 * 模型 page will show it, with a list of what this save changes. Everything
 * goes out in one save, so a fact edited here is never resent stale.
 */
export default function ModelEditForm({
  createVendor,
  creatingVendor,
  models,
  onCancel,
  onDelete,
  onSaved,
  t,
  view,
}: Props) {
  const record = view.record as ApiRecord;
  const detailQuery = useModelDetailQuery(record.id as number, true);
  const makers = useModelMakersQuery();
  const reviews = useModelInfoReviewsQuery();
  const { resolveMutation } = useModelInfoMutations(t);
  const schema = useMemo(() => buildModelFormSchema(t), [t]);
  const form = useForm<Values>({
    defaultValues: getDefaultModelFormValues() as Values,
    resolver: zodResolver(
      schema,
    ) as unknown as UseFormProps<Values>['resolver'],
  });
  const [loaded, setLoaded] = useState<Values | null>(null);
  const [vendorFormOpen, setVendorFormOpen] = useState(false);
  const [newVendorName, setNewVendorName] = useState('');

  const detail = detailQuery.data as Values | undefined;
  useEffect(() => {
    if (!detail) return;
    const start = {
      ...getDefaultModelFormValues(),
      ...detail,
      // The picker shows the maker the catalog groups it under; an inferred
      // one stays unsaved unless the operator picks another (see submit).
      maker: String(detail.maker || detail.maker_resolved || ''),
      release_date: String(detail.release_date || ''),
      knowledge_cutoff: String(detail.knowledge_cutoff || ''),
      modalities: {
        input: listOf((detail.modalities as Modalities)?.input),
        output: listOf((detail.modalities as Modalities)?.output),
      },
      info_locked: Boolean(detail.info_locked),
    } as Values;
    form.reset(start);
    setLoaded(start);
  }, [detail, form]);

  const values = form.watch() as Values;
  const saving = models.saveModelMutation.isPending;
  // Tags and endpoint groups load after the dialog; a save before they
  // answer would write a form that never showed them.
  const optionsPending = Boolean(models.editorOptionsLoading);
  const sources = (record.info_sources as Record<string, string>) || {};
  const source = (field: string) => infoSourceLabel(sources[field], t);
  const modalities = (values.modalities as Modalities) || {};
  const capabilities = (values.capabilities || {}) as Record<string, boolean>;

  const releaseReview = (reviews.data?.items || []).find(
    (row) =>
      row.model_name === view.name &&
      row.status === 'pending' &&
      row.field === 'release_date' &&
      (row.kind === 'diff' || row.kind === 'first_run'),
  );

  const capLabel = (key: string) => t(`${ns}.capability.${key}`);
  const vendorName = (id: unknown) => {
    const vendor = models.vendors.find(
      (item) => String(item.internal_id ?? item.id) === String(id),
    );
    return String(vendor?.name ?? id ?? '');
  };
  const onOff = (on: unknown) =>
    on
      ? t(`${editNs}.on`, { defaultValue: '开' })
      : t(`${editNs}.off`, { defaultValue: '关' });

  /* 本次修改: each field that differs from what was loaded, said briefly. */
  const changes = useMemo(() => {
    if (!loaded) return [] as Array<{ key: string; text: string }>;
    const out: Array<{ key: string; text: string }> = [];
    const label = (key: string, fallback: string) =>
      t(`${editNs}.field.${key}`, { defaultValue: fallback });
    const arrow = (name: string, before: string, after: string) =>
      `${name} ${before || '—'} → ${after || '—'}`;
    const plusMinus = (
      name: string,
      before: string[],
      after: string[],
      show: (value: string) => string,
    ) => {
      const added = after.filter((item) => !before.includes(item));
      const removed = before.filter((item) => !after.includes(item));
      if (!added.length && !removed.length) return null;
      return [
        name,
        ...added.map((item) => `+ ${show(item)}`),
        ...removed.map((item) => `− ${show(item)}`),
      ].join(' ');
    };
    const num = (key: string, name: string) => {
      if (digits(loaded[key]) !== digits(values[key]))
        out.push({
          key,
          text: arrow(
            name,
            tokens(loaded[key]).toLocaleString('en-US'),
            tokens(values[key]).toLocaleString('en-US'),
          ),
        });
    };
    const text = (key: string, name: string, show = (v: unknown) => v) => {
      if (String(loaded[key] ?? '').trim() !== String(values[key] ?? '').trim())
        out.push({
          key,
          text: arrow(
            name,
            String(show(loaded[key]) ?? ''),
            String(show(values[key]) ?? ''),
          ),
        });
    };
    const edited = (key: string, name: string) => {
      if (!same(loaded[key], values[key]))
        out.push({
          key,
          text: `${name} ${t(`${editNs}.edited`, { defaultValue: '已改' })}`,
        });
    };
    const flag = (key: string, name: string) => {
      if (Boolean(loaded[key]) !== Boolean(values[key]))
        out.push({
          key,
          text: arrow(name, onOff(loaded[key]), onOff(values[key])),
        });
    };

    num('context_window', label('context', '上下文'));
    num('max_tokens', label('maxOutput', '最大输出'));
    text('knowledge_cutoff', label('knowledge', '知识截止'));
    const before = (loaded.modalities as Modalities) || {};
    const io = [
      plusMinus(
        label('input', '输入'),
        listOf(before.input),
        listOf(modalities.input),
        (v) => modalityLabel(v, t),
      ),
      plusMinus(
        label('output', '输出'),
        listOf(before.output),
        listOf(modalities.output),
        (v) => modalityLabel(v, t),
      ),
    ];
    io.forEach((line, index) => {
      if (line) out.push({ key: `modalities-${index}`, text: line });
    });
    const capsBefore = (loaded.capabilities || {}) as Record<string, boolean>;
    const caps = plusMinus(
      label('capabilities', '能力'),
      capabilityKeys.filter((key) => capsBefore[key]),
      capabilityKeys.filter((key) => capabilities[key]),
      capLabel,
    );
    if (caps) out.push({ key: 'capabilities', text: caps });
    edited('endpoints', label('endpoints', '端点路径'));
    text('lifecycle_status', label('lifecycle', '状态'), (v) =>
      t(`${ns}.lifecycle.${String(v || 'active')}`),
    );
    text('replacement_model', label('replacement', '停售后改用'));
    flag('status', t(`${ns}.field.status`));
    flag('sync_official', t(`${ns}.field.sync_official`));
    flag('open_source', t(`${ns}.field.open_source`));
    flag('info_locked', t(`${infoNs}.lockShort`, { defaultValue: '锁定资料' }));
    text('model_name', t(`${ns}.field.model_name`));
    text('family', t(`${ns}.field.family`, { defaultValue: '模型家族' }));
    text('vendor_id', t(`${ns}.field.vendor_id`), vendorName);
    text('maker', t(`${infoNs}.field.maker`, { defaultValue: '厂商' }), (v) =>
      v ? makerFullLabel(String(v), t) : '',
    );
    text(
      'release_date',
      t(`${infoNs}.field.release_date`, { defaultValue: '发布日期' }),
    );
    text('icon', t(`${ns}.field.icon`));
    edited('description', t(`${ns}.field.description`));
    edited('description_en', t(`${ns}.field.description_en`));
    edited('description_zh', t(`${ns}.field.description_zh`));
    edited('tags', t(`${ns}.field.tags`));
    edited('video_capabilities', t(`${ns}.field.video_capabilities`));
    return out;
    // `values` is a fresh object each render; the list is cheap.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded, values]);

  /* The draft as the 模型 page would read it. Prices are not edited here,
     so the preview shows none. */
  const previewModel: CatalogModel = {
    capabilities,
    context_length: tokens(values.context_window),
    description: String(values.description || ''),
    description_en: String(values.description_en || ''),
    description_zh: String(values.description_zh || ''),
    id: String(values.model_name || view.name),
    knowledge_cutoff: String(values.knowledge_cutoff || ''),
    maker: String(values.maker || ''),
    max_output_tokens: tokens(values.max_tokens),
    modality: {
      input: listOf(modalities.input),
      output: listOf(modalities.output),
    },
    release_date: String(values.release_date || '') || undefined,
  } as CatalogModel;
  const previewMaker = String(values.maker || '');

  const submit = form.handleSubmit(async (formValues) => {
    if (!detail || optionsPending) return;
    const next = { ...detail, ...formValues } as Values;
    // An inferred maker shown in the picker is not a choice the operator
    // made; only a real change is written (and then marked 手填).
    if (!detail.maker && next.maker === String(detail.maker_resolved || ''))
      next.maker = '';
    await models.saveModelMutation.mutateAsync({
      editingModel: record,
      values: next,
    });
    onSaved();
  });

  const chip = (
    key: string,
    label: ReactNode,
    on: boolean,
    toggle: () => void,
  ) => (
    <button
      aria-pressed={on}
      className='mdl-chip'
      data-owns-chrome=''
      disabled={saving}
      key={key}
      type='button'
      onClick={toggle}
    >
      {label}
    </button>
  );

  const toggleModality = (side: 'input' | 'output', value: string) => {
    const current = listOf(modalities[side]);
    const next = current.includes(value)
      ? current.filter((item) => item !== value)
      : [...current, value];
    form.setValue(
      'modalities' as never,
      { ...modalities, [side]: next } as never,
      { shouldDirty: true },
    );
  };

  const fieldHead = (htmlFor: string, label: string, field?: string) => (
    <span className='mdl-form__label'>
      <label htmlFor={htmlFor}>{label}</label>
      {field && source(field) ? <em>{source(field)}</em> : null}
    </span>
  );

  const section = (title: string, body: ReactNode) => (
    <section className='mdl-form__sec'>
      <h3>{title}</h3>
      {body}
    </section>
  );

  const err = (message: unknown) =>
    typeof message === 'string' ? (
      <span className='mdl-form__error'>{message}</span>
    ) : null;

  const interfaces = endpointTypes(values.endpoints);
  const sectionTitle = (key: string, fallback: string) =>
    t(`${editNs}.section.${key}`, { defaultValue: fallback });

  if (!loaded) {
    return (
      <div className='mdl-form mdl-form--wait'>
        <div className='mdl-form__main'>
          <Skeleton height={64} />
          <Skeleton height={88} />
          <Skeleton height={64} />
          <Skeleton height={120} />
        </div>
        <aside className='mdl-form__side'>
          <Skeleton height={240} />
        </aside>
      </div>
    );
  }

  return (
    <>
      <form className='mdl-form' id='model-edit-form' onSubmit={submit}>
        <div className='mdl-form__main'>
          {section(
            sectionTitle('spec', '规格'),
            <div className='mdl-form__grid mdl-form__grid--3'>
              <div className='mdl-form__field'>
                {fieldHead(
                  'mdl-context',
                  t(`${editNs}.field.context`, { defaultValue: '上下文' }),
                  'context_length',
                )}
                <Input
                  id='mdl-context'
                  inputMode='numeric'
                  {...form.register('context_window')}
                />
              </div>
              <div className='mdl-form__field'>
                {fieldHead(
                  'mdl-max-output',
                  t(`${editNs}.field.maxOutput`, { defaultValue: '最大输出' }),
                  'max_output_tokens',
                )}
                <Input
                  id='mdl-max-output'
                  inputMode='numeric'
                  {...form.register('max_tokens')}
                />
              </div>
              <div className='mdl-form__field'>
                {fieldHead(
                  'mdl-knowledge',
                  t(`${editNs}.field.knowledge`, { defaultValue: '知识截止' }),
                  'knowledge_cutoff',
                )}
                <Input
                  id='mdl-knowledge'
                  placeholder='2026-03'
                  {...form.register('knowledge_cutoff' as never)}
                />
              </div>
            </div>,
          )}

          {section(
            sectionTitle('io', '输入与输出'),
            <div className='mdl-form__io'>
              {(['input', 'output'] as const).map((side) => (
                <div className='mdl-form__io-row' key={side}>
                  <span className='mdl-form__io-label'>
                    {side === 'input'
                      ? t(`${editNs}.field.input`, { defaultValue: '输入' })
                      : t(`${editNs}.field.output`, { defaultValue: '输出' })}
                  </span>
                  <div
                    aria-label={
                      side === 'input'
                        ? t(`${editNs}.field.input`, { defaultValue: '输入' })
                        : t(`${editNs}.field.output`, { defaultValue: '输出' })
                    }
                    className='mdl-chips'
                    role='group'
                  >
                    {(side === 'input'
                      ? MODALITY_OPTIONS
                      : OUTPUT_MODALITIES
                    ).map((value) =>
                      chip(
                        value,
                        modalityLabel(value, t),
                        listOf(modalities[side]).includes(value),
                        () => toggleModality(side, value),
                      ),
                    )}
                  </div>
                </div>
              ))}
            </div>,
          )}

          {section(
            sectionTitle('capabilities', '能力'),
            <div
              aria-label={sectionTitle('capabilities', '能力')}
              className='mdl-chips'
              role='group'
            >
              {capabilityKeys.map((key) =>
                chip(key, capLabel(key), Boolean(capabilities[key]), () =>
                  form.setValue(
                    `capabilities.${key}` as never,
                    !capabilities[key] as never,
                    { shouldDirty: true },
                  ),
                ),
              )}
            </div>,
          )}

          {section(
            sectionTitle('interfaces', '接口'),
            <>
              {interfaces.length ? (
                <div className='mdl-chips'>
                  {interfaces.map((type) => (
                    <span className='mdl-chip mdl-chip--static' key={type}>
                      {endpointLabel(type)}
                    </span>
                  ))}
                </div>
              ) : (
                <p className='mdl-cap'>
                  {t(`${editNs}.interfacesNone`, {
                    defaultValue: '按来源自动判断',
                  })}
                </p>
              )}
              <details className='mdl-form__more'>
                <summary>
                  <ChevronDown aria-hidden='true' size={14} />
                  {t(`${editNs}.endpointPaths`, {
                    defaultValue: '端点路径',
                  })}
                </summary>
                <Controller
                  control={form.control}
                  name='endpoints'
                  render={({ field }) => (
                    <Textarea
                      aria-label={t(`${editNs}.endpointPaths`, {
                        defaultValue: '端点路径',
                      })}
                      placeholder={t(`${ns}.placeholder.endpoints`)}
                      rows={4}
                      value={String(field.value || '')}
                      onChange={field.onChange}
                    />
                  )}
                />
                {models.endpointGroups.length ? (
                  <div className='mdl-chips'>
                    {models.endpointGroups.map((group) => (
                      <Button
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
                            { shouldDirty: true, shouldValidate: true },
                          )
                        }
                      >
                        {group.name}
                      </Button>
                    ))}
                  </div>
                ) : null}
                <span className='mdl-cap'>{t(`${ns}.help.endpoints`)}</span>
                {err(form.formState.errors.endpoints?.message)}
              </details>
            </>,
          )}

          {section(
            sectionTitle('listing', '上架'),
            <>
              <div className='mdl-form__grid mdl-form__grid--2'>
                <div className='mdl-form__field'>
                  <span className='mdl-form__label'>
                    <span>
                      {t(`${editNs}.field.lifecycle`, {
                        defaultValue: '状态',
                      })}
                    </span>
                  </span>
                  <Controller
                    control={form.control}
                    name='lifecycle_status'
                    render={({ field }) => (
                      <Select
                        aria-label={t(`${editNs}.field.lifecycle`, {
                          defaultValue: '状态',
                        })}
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
                </div>
                <div className='mdl-form__field'>
                  {fieldHead(
                    'mdl-replacement',
                    t(`${editNs}.field.replacement`, {
                      defaultValue: '停售后改用',
                    }),
                  )}
                  <Input
                    id='mdl-replacement'
                    placeholder={t(`${ns}.placeholder.replacement_model`)}
                    {...form.register('replacement_model')}
                  />
                </div>
              </div>
              <div className='mdl-form__switches'>
                {(
                  [
                    ['status', t(`${ns}.field.status`)],
                    ['sync_official', t(`${ns}.field.sync_official`)],
                    ['open_source', t(`${ns}.field.open_source`)],
                    [
                      'info_locked',
                      t(`${infoNs}.lockShort`, { defaultValue: '锁定资料' }),
                    ],
                  ] as const
                ).map(([name, label]) => (
                  <Controller
                    control={form.control}
                    key={name}
                    name={name as never}
                    render={({ field }) => (
                      <Switch
                        checked={Boolean(field.value)}
                        onCheckedChange={field.onChange}
                      >
                        {label}
                      </Switch>
                    )}
                  />
                ))}
              </div>
            </>,
          )}

          {section(
            sectionTitle('identity', '名称与厂商'),
            <div className='mdl-form__grid mdl-form__grid--2'>
              <div className='mdl-form__field'>
                {fieldHead('mdl-name', t(`${ns}.field.model_name`))}
                <Input id='mdl-name' {...form.register('model_name')} />
                {err(form.formState.errors.model_name?.message)}
              </div>
              <div className='mdl-form__field'>
                {fieldHead(
                  'mdl-family',
                  t(`${ns}.field.family`, { defaultValue: '模型家族' }),
                )}
                <Input
                  id='mdl-family'
                  placeholder={t(`${ns}.placeholder.family`, {
                    defaultValue: '例如 kimi-k2、gpt-5',
                  })}
                  {...form.register('family')}
                />
              </div>
              <div className='mdl-form__field'>
                <span className='mdl-form__label'>
                  <span>
                    {t(`${infoNs}.field.maker`, { defaultValue: '厂商' })}
                  </span>
                  {source('maker') || (!detail?.maker && values.maker) ? (
                    <em>
                      {source('maker') ||
                        t(`${infoNs}.source.inferred`, {
                          defaultValue: '推断',
                        })}
                    </em>
                  ) : null}
                </span>
                <Controller
                  control={form.control}
                  name={'maker' as never}
                  render={({ field }) => (
                    <Select
                      aria-label={t(`${infoNs}.field.maker`, {
                        defaultValue: '厂商',
                      })}
                      value={String(field.value || 'none')}
                      onValueChange={(value) =>
                        field.onChange(value === 'none' ? '' : value)
                      }
                    >
                      <SelectItem value='none'>
                        {t(`${infoNs}.makerNone`, { defaultValue: '未归类' })}
                      </SelectItem>
                      {(makers.data?.makers || []).map((maker) => (
                        <SelectItem key={maker.key} value={maker.key}>
                          {makerFullLabel(maker.key, t)}
                        </SelectItem>
                      ))}
                    </Select>
                  )}
                />
              </div>
              <div className='mdl-form__field'>
                <span className='mdl-form__label'>
                  <span>{t(`${ns}.field.vendor_id`)}</span>
                </span>
                <Controller
                  control={form.control}
                  name='vendor_id'
                  render={({ field }) => (
                    <Select
                      aria-label={t(`${ns}.field.vendor_id`)}
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
                {err(form.formState.errors.vendor_id?.message)}
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
                  </div>
                ) : (
                  <Button
                    className='mdl-form__link'
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
              </div>
              <div className='mdl-form__field'>
                {fieldHead(
                  'mdl-release',
                  t(`${infoNs}.field.release_date`, {
                    defaultValue: '发布日期',
                  }),
                  'release_date',
                )}
                <Input
                  id='mdl-release'
                  placeholder='YYYY-MM-DD'
                  {...form.register('release_date' as never)}
                />
              </div>
              <div className='mdl-form__field'>
                {fieldHead('mdl-icon', t(`${ns}.field.icon`))}
                <Input
                  id='mdl-icon'
                  placeholder={t(`${ns}.placeholder.icon`)}
                  {...form.register('icon')}
                />
              </div>
              {releaseReview ? (
                <div className='mo-info__conflict mdl-form__wide' role='note'>
                  <TriangleAlert aria-hidden='true' size={16} />
                  <div>
                    <b>
                      {t(`${infoNs}.conflict.title`, {
                        defaultValue: 'models.dev 记的是 {{date}}',
                        date: releaseReview.upstream_value,
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
                          defaultValue: '改成 {{date}}',
                          date: releaseReview.upstream_value,
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
            </div>,
          )}

          {section(
            sectionTitle('copy', '介绍'),
            <div className='mdl-form__grid mdl-form__grid--2'>
              <div className='mdl-form__field mdl-form__wide'>
                {fieldHead('mdl-desc', t(`${ns}.field.description`))}
                <Textarea
                  id='mdl-desc'
                  rows={2}
                  {...form.register('description')}
                />
              </div>
              <div className='mdl-form__field'>
                {fieldHead('mdl-desc-zh', t(`${ns}.field.description_zh`))}
                <Textarea
                  id='mdl-desc-zh'
                  rows={3}
                  {...form.register('description_zh')}
                />
              </div>
              <div className='mdl-form__field'>
                {fieldHead('mdl-desc-en', t(`${ns}.field.description_en`))}
                <Textarea
                  id='mdl-desc-en'
                  rows={3}
                  {...form.register('description_en')}
                />
              </div>
              <div className='mdl-form__field mdl-form__wide'>
                <span className='mdl-form__label'>
                  <span>{t(`${ns}.field.tags`)}</span>
                </span>
                <Controller
                  control={form.control}
                  name='tags'
                  render={({ field }) => (
                    <MultiSelect
                      aria-label={t(`${ns}.field.tags`)}
                      emptyText={t(`${ns}.empty.options`)}
                      options={[
                        ...models.tagOptions,
                        ...listOf(field.value)
                          .filter(
                            (tag) =>
                              !models.tagOptions.some(
                                (option) => String(option.value) === tag,
                              ),
                          )
                          .map((tag) => ({ label: tag, value: tag })),
                      ]}
                      placeholder={t(`${ns}.placeholder.tags`)}
                      value={listOf(field.value)}
                      onValueChange={field.onChange}
                    />
                  )}
                />
              </div>
            </div>,
          )}

          <details className='mdl-form__more mdl-form__sec'>
            <summary>
              <ChevronDown aria-hidden='true' size={14} />
              {t(`${ns}.field.video_capabilities`)}
            </summary>
            <Controller
              control={form.control}
              name='video_capabilities'
              render={({ field }) => (
                <Textarea
                  aria-label={t(`${ns}.field.video_capabilities`)}
                  placeholder={t(`${ns}.placeholder.video_capabilities`)}
                  rows={3}
                  value={String(field.value || '')}
                  onChange={field.onChange}
                />
              )}
            />
            {err(form.formState.errors.video_capabilities?.message)}
          </details>
        </div>

        <aside className='mdl-form__side'>
          <p className='mdl-cap'>
            {t(`${editNs}.previewTitle`, {
              defaultValue: '客户在「模型」页看到的样子',
            })}
          </p>
          <div className='mdl-preview'>
            <div className='mdl-preview__head'>
              <b>{previewModel.id}</b>
              {previewMaker ? (
                <span className='plaza-card__maker'>
                  <ProviderLogo
                    brand={makerBrand(previewMaker)}
                    decorative
                    size={16}
                  />
                  {makerFullLabel(previewMaker, t)}
                </span>
              ) : null}
            </div>
            <div className='plaza-detail mdl-preview__body'>
              <ModelFacts model={previewModel} />
            </div>
          </div>
          {changes.length ? (
            <div className='mdl-changes'>
              <h4>{t(`${editNs}.changes`, { defaultValue: '本次修改' })}</h4>
              <ul>
                {changes.map((change) => (
                  <li key={change.key}>{change.text}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </aside>
      </form>
      <div className='mdl-form__foot'>
        <Button
          className='mdl-form__delete'
          size='sm'
          type='button'
          variant='ghost'
          onClick={onDelete}
        >
          {t('page.console.modelOps.models.deleteTitle', {
            defaultValue: '删除模型',
          })}
        </Button>
        <div className='mdl-form__actions'>
          <Button
            size='sm'
            type='button'
            variant='secondary'
            onClick={onCancel}
          >
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={!changes.length || optionsPending}
            form='model-edit-form'
            loading={saving}
            size='sm'
            type='submit'
            variant='primary'
          >
            {changes.length
              ? t(`${editNs}.saveCount`, {
                  count: changes.length,
                  defaultValue: '保存 {{count}} 处修改',
                })
              : t('common.save', { defaultValue: '保存' })}
          </Button>
        </div>
      </div>
    </>
  );
}
