import { zodResolver } from '@hookform/resolvers/zod';
import { Check, ChevronRight, CircleAlert, Copy, Search } from 'lucide-react';
import React, { useEffect, useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { classifyTier } from './connect/modelPicker';
import { modelFamily } from './connect/tools';
import { isLegacyKey, shortKeyLabel } from './tokenKeyLabel';
import {
  Button,
  Checkbox,
  DatePicker,
  FieldMessage,
  Input,
  Modal,
  MultiSelect,
  Select,
  SelectItem,
  Switch,
  Textarea,
} from '../../components/ui';
import {
  EFFORT_LEVELS,
  EffortCapControl,
  effortRank,
  lowerEffortCap,
} from '../../components/ui/effort-cap';
import { PendingBlock } from '../../components/ui/pending';
import { compactNumber, formatUnixTime } from '../../components/ui/table-tools';
import {
  presetValues,
  RATE_LIMIT_PRESETS,
  type RateLimitCeiling,
} from '../../constants/rate-limits';
import { SEARCH_PROVIDERS } from '../../constants/search.constants';
import { asMicroUSD } from '../../helpers/money';
import { buildQuotaDisplay } from '../../helpers/quotaDisplay';
import { renderQuota, usdToMicroUSD } from '../../helpers/render';
import {
  getDefaultTokenFormValues,
  useTokenDetailQuery,
  useTokensQuery,
  type CreatedSecret,
  type TokenModelFact,
  type TokenRecord,
} from '../../hooks/tokens/useTokensQuery';
import { errorMessage } from '../../lib/api-error';
import { cn } from '../../lib/cn';
import './rate-limit-slider.css';
import './token-page.css';
import type { ColumnDef } from '@tanstack/react-table';
import type { TFunction } from 'i18next';
import type { UseFormReturn } from 'react-hook-form';

const tokenFormSchema = z
  .object({
    allow_ips: z.string().optional(),
    expired_time: z
      .union([z.date(), z.number(), z.string()])
      .nullable()
      .optional(),
    expires_never: z.boolean(),
    ip_blacklist: z.string().optional(),
    ip_blacklist_enabled: z.boolean(),
    ip_whitelist_enabled: z.boolean(),
    model_limits: z.array(z.string()),
    model_limits_enabled: z.boolean(),
    search_limits_enabled: z.boolean(),
    search_providers: z.array(z.string()),
    search_domain_allowlist: z.string().optional(),
    metadata: z.string().refine((value) => {
      const raw = String(value || '').trim();
      if (!raw) return true;
      try {
        const parsed = JSON.parse(raw);
        return (
          parsed !== null &&
          typeof parsed === 'object' &&
          !Array.isArray(parsed)
        );
      } catch {
        return false;
      }
    }, 'Metadata must be a JSON object'),
    name: z.string().trim().min(1),
    product: z.string().trim().max(128),
    rate_limits_enabled: z.boolean(),
    remain_quota: z.coerce.number().min(0),
    rpm_limit: z.coerce.number().int().min(0),
    tokenCount: z.coerce.number().int().min(1).max(100),
    tpm_limit: z.coerce.number().int().min(0),
    unlimited_quota: z.boolean(),
    vendor_groups: z.record(z.string(), z.enum(['direct', 'cheap'])),
    vendor_limits: z.array(z.string()),
    vendor_limits_enabled: z.boolean(),
    model_scope_vendors: z.array(z.string()),
    usage_alert_percent: z.coerce.number().int().min(0).max(100),
    note: z.string().max(256),
    reasoning_effort_cap: z.string(),
    question_generation_enabled: z.boolean(),
    question_generation_model: z.string().trim().max(200),
    period_cap_period: z.enum(['', 'day', 'month']),
    period_cap_usd: z.coerce.number().min(0),
    period_cap_spent_usd: z.coerce.number().min(0).optional(),
  })
  .refine(
    (values) =>
      !values.model_limits_enabled ||
      values.model_limits.length > 0 ||
      values.model_scope_vendors.length > 0,
    {
      path: ['model_limits'],
      message: 'legacy.i18n.please_select_model.7330bf2340',
    },
  );

type TokenVendorRecord = {
  id?: number | string;
  icon?: string;
  name?: string;
};

export type TokenLaneVendorFamily = 'openai' | 'claude' | 'gemini';

export type TokenLaneVendorRecord = TokenVendorRecord & {
  laneFamily: TokenLaneVendorFamily;
  laneLabel: string;
  lanePriority: number;
};

type TokensQuery = ReturnType<typeof useTokensQuery>;
export type TokenFormServices = Pick<
  TokensQuery,
  't' | 'modelOptions' | 'vendorOptions' | 'copyText' | 'revealKey'
> & {
  saveTokenMutation: {
    isPending: boolean;
    mutateAsync: (
      input: import('../../hooks/tokens/useTokensQuery').SaveTokenVariables,
    ) => Promise<{ data?: unknown }>;
  };
};
export type TeamTokenFormContext = {
  detailValues?: import('../../hooks/tokens/useTokensQuery').TokenFormValues;
  content: React.ReactNode;
  countLabel: string;
  disabled: boolean;
  /** The owning member's reasoning-effort cap; the Key's can only lower it. */
  memberEffortCap?: string;
  memberName?: string;
};
type TokenFormInput = z.input<typeof tokenFormSchema>;
type TokenFormData = z.output<typeof tokenFormSchema>;
type TokenForm = UseFormReturn<TokenFormInput, unknown, TokenFormData>;
type ChatTemplate = { name: string; value: string | undefined };
export type TokenColumn = ColumnDef<TokenRecord, unknown>;

type OpenChangeProps = {
  onOpenChange: (open: boolean) => void;
  open: boolean;
};

const tokenLaneVendorOrder: TokenLaneVendorFamily[] = [
  'openai',
  'claude',
  'gemini',
];

const compactVendorText = (value?: string) =>
  String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]/g, '');

const getTokenLaneVendorMeta = (
  vendor: TokenVendorRecord,
): {
  family: TokenLaneVendorFamily;
  label: string;
  priority: number;
} | null => {
  const name = compactVendorText(vendor.name);
  const icon = compactVendorText(vendor.icon);
  if (name === 'openai' || icon === 'openai' || icon.startsWith('openai')) {
    return {
      family: 'openai',
      label: 'OpenAI',
      priority: name === 'openai' ? 0 : 1,
    };
  }
  if (name === 'anthropic' || name === 'claude' || icon.includes('claude')) {
    return {
      family: 'claude',
      label: 'Claude',
      priority: name === 'anthropic' ? 0 : name === 'claude' ? 1 : 2,
    };
  }
  if (name === 'google' || name === 'gemini' || icon.includes('gemini')) {
    return {
      family: 'gemini',
      label: 'Gemini',
      priority: name === 'google' ? 0 : name === 'gemini' ? 1 : 2,
    };
  }
  return null;
};

/** The OpenAI, Claude and Gemini vendors a Key can pin to the stable or
 *  agent lane, one per family. */
export function useTokenLaneVendors(vendors: TokenVendorRecord[]) {
  return useMemo(() => {
    const byFamily = new Map<TokenLaneVendorFamily, TokenLaneVendorRecord>();
    vendors.forEach((vendor: TokenVendorRecord) => {
      const vendorId = String(vendor.id || '').trim();
      if (!vendorId) {
        return;
      }
      const meta = getTokenLaneVendorMeta(vendor);
      if (!meta) {
        return;
      }
      const candidate: TokenLaneVendorRecord = {
        ...vendor,
        laneFamily: meta.family,
        laneLabel: meta.label,
        lanePriority: meta.priority,
      };
      const existing = byFamily.get(meta.family);
      if (!existing || candidate.lanePriority < existing.lanePriority) {
        byFamily.set(meta.family, candidate);
      }
    });
    return tokenLaneVendorOrder
      .map((family) => byFamily.get(family))
      .filter(Boolean) as TokenLaneVendorRecord[];
  }, [vendors]);
}

export const parseChatTemplates = (): ChatTemplate[] => {
  try {
    const parsed = JSON.parse(localStorage.getItem('chats') || '[]');
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed
      .map((item: unknown) => {
        if (!item || typeof item !== 'object') return null;
        const record = item as Record<string, unknown>;
        const name = Object.keys(record)[0] || '';
        return name
          ? {
              name,
              value:
                typeof record[name] === 'string' ? record[name] : undefined,
            }
          : null;
      })
      .filter((item): item is ChatTemplate => item !== null);
  } catch {
    return [];
  }
};

export { isLegacyKey, shortKeyLabel } from './tokenKeyLabel';

export const tokenStatus = (
  status: unknown,
  t: TFunction,
): {
  label: string;
  variant: 'danger' | 'neutral' | 'success' | 'warning';
} => {
  if (status === 1) {
    return { label: t('page.console.tables.enabled'), variant: 'success' };
  }
  if (status === 2) {
    return { label: t('page.console.tables.disabled'), variant: 'danger' };
  }
  if (status === 3) {
    return {
      label: t('page.console.token.status.expired'),
      variant: 'warning',
    };
  }
  if (status === 4) {
    return {
      label: t('page.console.token.status.exhausted'),
      variant: 'danger',
    };
  }
  return { label: t('page.console.tables.unknown'), variant: 'neutral' };
};

const formatTokenKey = (key: unknown) => {
  const value = String(key || '').trim();
  if (!value) return '';
  return value.startsWith('sk-') ? value : `sk-${value}`;
};

export function TokenKeyCell({
  onCopy,
  record,
  revealed,
}: {
  onCopy: (record: TokenRecord) => Promise<boolean>;
  record: TokenRecord;
  revealed?: string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const legacy = isLegacyKey(record);
  const available = !legacy && record.can_reveal_key !== false;
  useEffect(() => {
    if (!copied) return undefined;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const label = copied
    ? t('secret.copied', { defaultValue: '已复制' })
    : t('secret.copy', { defaultValue: '复制' });
  return (
    <span className={cn('tok-key', legacy && 'tok-key--legacy')}>
      <span
        className={cn('tok-key__value', revealed && 'tok-key__value--full')}
        title={revealed ? undefined : shortKeyLabel(record)}
      >
        {revealed || shortKeyLabel(record)}
      </span>
      <button
        type='button'
        className='tok-key__copy'
        data-owns-chrome='true'
        aria-label={`${label} ${String(record.name || '')}`}
        title={
          legacy
            ? t('secret.legacy', {
                defaultValue: '旧密钥仅存摘要，无法显示全文',
              })
            : label
        }
        disabled={!available || busy}
        onClick={async (event) => {
          event.stopPropagation();
          setBusy(true);
          try {
            setCopied(await onCopy(record));
          } finally {
            setBusy(false);
          }
        }}
      >
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </span>
  );
}

export function CopyTokensModal({
  onOpenChange,
  open,
  selectedRows,
  tokens,
}: OpenChangeProps & {
  selectedRows: TokenRecord[];
  tokens: TokensQuery;
}) {
  const { t } = tokens;
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={t('legacy.i18n.copy_token.c9bdcf3f41')}
      description={t('page.console.token.copySelectedDescription', {
        count: selectedRows.length,
      })}
      body={
        <div className='grid gap-2 sm:grid-cols-2'>
          <Button
            variant='secondary'
            onClick={async () => {
              await tokens.copySelected();
              onOpenChange(false);
            }}
          >
            Copy names
          </Button>
        </div>
      }
    />
  );
}

function RateLimitField({
  ceiling,
  field,
  form,
  label,
  t,
  value,
}: {
  ceiling?: number;
  field: 'rpm_limit' | 'tpm_limit';
  form: TokenForm;
  label: string;
  t: TFunction;
  value: number;
}) {
  const hasCeiling = typeof ceiling === 'number' && ceiling > 0;
  const inherits = value <= 0;
  const over = hasCeiling && value > ceiling;
  const percent =
    !hasCeiling || inherits
      ? 0
      : Math.min(100, Math.round((value / ceiling) * 100));
  const fill = over ? 'var(--ui-warning)' : 'var(--ui-accent)';
  const step = hasCeiling ? Math.max(1, Math.round(ceiling / 200)) : 1;
  const sliderValue = hasCeiling ? Math.min(ceiling, Math.max(0, value)) : 0;
  return (
    <div className='tokv2-f'>
      <label className='tokv2-f__label' htmlFor={`token-${field}`}>
        {label}
      </label>
      <Input
        id={`token-${field}`}
        min='0'
        placeholder='0'
        type='number'
        {...form.register(field)}
      />
      {hasCeiling ? (
        <div className='grid gap-1'>
          <input
            aria-label={label}
            className={`rl-slider${over ? ' rl-slider--over' : ''}`}
            max={ceiling}
            min={0}
            step={step}
            type='range'
            value={sliderValue}
            onChange={(event) =>
              form.setValue(field, Number(event.target.value), {
                shouldDirty: true,
              })
            }
            style={{
              background: `linear-gradient(to right, ${fill} 0 ${percent}%, var(--ui-bg-muted) ${percent}% 100%)`,
            }}
          />
          <span className='tokv2-hint'>
            {inherits
              ? t('page.console.token.rateLimit.inheritsPlan')
              : t('page.console.token.rateLimit.ofCeiling', {
                  ceiling: compactNumber(ceiling),
                  value: compactNumber(value),
                })}
          </span>
        </div>
      ) : null}
    </div>
  );
}

function RateLimitFields({
  ceiling: rateLimitCeiling,
  form,
  t,
  values,
}: {
  ceiling?: RateLimitCeiling | null;
  form: TokenForm;
  t: TFunction;
  values: TokenFormInput;
}) {
  const ceiling: RateLimitCeiling | null = rateLimitCeiling || null;
  const enabled = Boolean(values.rate_limits_enabled);
  const rpm = Math.max(0, Math.trunc(Number(values.rpm_limit) || 0));
  const tpm = Math.max(0, Math.trunc(Number(values.tpm_limit) || 0));

  const applyPreset = (fraction: number) => {
    if (!ceiling) {
      return;
    }
    const next = presetValues(ceiling, fraction);
    form.setValue('rpm_limit', next.rpm, { shouldDirty: true });
    form.setValue('tpm_limit', next.tpm, { shouldDirty: true });
  };

  const activePreset = ceiling
    ? RATE_LIMIT_PRESETS.find((preset) => {
        const next = presetValues(ceiling, preset.fraction);
        return rpm === next.rpm && tpm === next.tpm;
      })?.key
    : undefined;

  const overCeiling = Boolean(
    ceiling && (rpm > ceiling.rpm || tpm > ceiling.tpm),
  );

  return (
    <div className='tokv2-f'>
      {enabled ? (
        <>
          {ceiling ? (
            <div className='tokv2-row'>
              {RATE_LIMIT_PRESETS.map((preset) => (
                <button
                  key={preset.key}
                  type='button'
                  className={cn(
                    'tokv2-chip',
                    activePreset === preset.key && 'tokv2-chip--evi',
                  )}
                  onClick={() => applyPreset(preset.fraction)}
                >
                  {t(`page.console.token.rateLimit.preset.${preset.key}`)}
                </button>
              ))}
            </div>
          ) : null}
          <div className='tokv2-row tokv2-row--nowrap'>
            <div className='tokv2-row__grow'>
              <RateLimitField
                ceiling={ceiling?.rpm}
                field='rpm_limit'
                form={form}
                label={t('page.console.token.rateLimit.rpmLabel')}
                t={t}
                value={rpm}
              />
            </div>
            <div className='tokv2-row__grow'>
              <RateLimitField
                ceiling={ceiling?.tpm}
                field='tpm_limit'
                form={form}
                label={t('page.console.token.rateLimit.tpmLabel')}
                t={t}
                value={tpm}
              />
            </div>
          </div>
          {overCeiling ? (
            <FieldMessage error>
              {t('page.console.token.rateLimit.exceedsCeiling')}
            </FieldMessage>
          ) : null}
          <span className='tokv2-hint'>
            {t('page.console.token.rateLimit.hint')}
          </span>
          <span className='tokv2-hint'>
            {t('page.console.token.rateLimit.explain')}
          </span>
        </>
      ) : null}
    </div>
  );
}

/** The example model a lane row names: that vendor's newest flagship, so the
 *  row illustrates itself rather than borrowing another vendor's model. */
function laneSampleModel(facts: TokenModelFact[]): string {
  const ranked = [...facts].sort((left, right) => {
    const byDate = (right.releasedAt ?? -1) - (left.releasedAt ?? -1);
    if (byDate !== 0) return byDate;
    return left.model.localeCompare(right.model);
  });
  const flagship = ranked.find((fact) => classifyTier(fact) === 'flagship');
  return (flagship || ranked[0])?.model || '';
}

const perMtok = (fact?: TokenModelFact) => {
  if (!fact || fact.inputPerMtok === undefined) return '';
  const output =
    fact.outputPerMtok === undefined ? '' : ` / $${fact.outputPerMtok}`;
  return `$${fact.inputPerMtok}${output}`;
};

/** What a Key's model scope comes to, in one line: the vendors taken whole
 *  and the models named one by one. A vendor counts as a vendor, not as its
 *  current model tally — ticking it also covers what it ships later. */
function modelScopeSummary(
  t: TFunction,
  values: Pick<
    TokenFormInput,
    'model_limits' | 'model_limits_enabled' | 'model_scope_vendors'
  >,
) {
  if (!values.model_limits_enabled) {
    return t('page.console.token.scope.allHint', {
      defaultValue: '全部模型都能调，包括之后上新的',
    });
  }
  const vendors = Array.isArray(values.model_scope_vendors)
    ? values.model_scope_vendors.length
    : 0;
  const models = Array.isArray(values.model_limits)
    ? values.model_limits.length
    : 0;
  const parts = [
    vendors
      ? t('page.console.token.scope.summaryVendors', {
          count: vendors,
          defaultValue: '{{count}} 个厂商整家',
        })
      : '',
    models
      ? t('page.console.token.scope.summaryModels', {
          count: models,
          defaultValue: '{{count}} 个单选模型',
        })
      : '',
  ].filter(Boolean);
  return parts.length
    ? parts.join(' · ')
    : t('page.console.token.scope.summaryNone', { defaultValue: '还没选' });
}

/** The tree a Key's model scope is really shaped like, laid out as two panes.
 *
 *  Two questions look like one: "which vendors may this Key reach" and "which
 *  models inside them". A flat checklist can only answer the second, so a Key
 *  meant to cover a vendor had to be re-edited every time that vendor shipped
 *  a model. The left pane answers the first — its box takes a vendor whole —
 *  and the right pane the second, for whichever vendor is open. The two
 *  compose as a union at request time. */
function ModelScopeField({
  form,
  t,
  tokens,
  values,
}: {
  form: TokenForm;
  t: TFunction;
  tokens: Pick<TokenFormServices, 'modelOptions' | 'vendorOptions'>;
  values: TokenFormInput;
}) {
  const [query, setQuery] = useState('');
  const [tier, setTier] = useState<'' | 'flagship' | 'balanced' | 'light'>('');
  const [activeVendor, setActiveVendor] = useState('');
  const selectedModels = Array.isArray(values.model_limits)
    ? values.model_limits.map(String)
    : [];
  const selectedVendors = Array.isArray(values.model_scope_vendors)
    ? values.model_scope_vendors.map(String)
    : [];
  const enabled = Boolean(values.model_limits_enabled);

  const vendorName = useMemo(
    () =>
      new Map(
        tokens.vendorOptions.vendors.map((vendor: TokenVendorRecord) => [
          String(vendor.id),
          String(vendor.name || ''),
        ]),
      ),
    [tokens.vendorOptions.vendors],
  );

  /* Grouped by the vendor the catalog records, not by a guess from the model
     name: scoping stores a vendor id, so a row whose id we invented would tick
     a vendor the gateway will never match. */
  const groups = useMemo(() => {
    const factByModel = new Map(
      tokens.vendorOptions.models.map((fact: TokenModelFact) => [
        fact.model,
        fact,
      ]),
    );
    const needle = query.trim().toLowerCase();
    const byVendor = new Map<
      string,
      {
        id: string;
        label: string;
        models: Array<{ fact?: TokenModelFact; name: string }>;
        scopable: boolean;
      }
    >();
    tokens.modelOptions.forEach((option: { label: string; value: string }) => {
      const name = String(option.value);
      const fact = factByModel.get(name);
      if (tier && (!fact || classifyTier(fact) !== tier)) return;
      /* A model the catalog records no vendor for still has to be selectable,
         so it falls back to the family its name implies. Those groups tick
         their models by name rather than storing a vendor: there is no vendor
         id to store, and inventing one would scope the Key to a vendor the
         gateway has never heard of. */
      const vendorID = String(fact?.vendorID || '');
      const id = vendorID || `family:${modelFamily(name) || 'other'}`;
      const label = vendorID
        ? vendorName.get(vendorID) || vendorID
        : modelFamily(name) || 'Other';
      // The search matches a vendor's name as well as a model's, so typing
      // "google" lands on the Google row rather than an empty pane.
      if (
        needle &&
        !name.toLowerCase().includes(needle) &&
        !label.toLowerCase().includes(needle)
      ) {
        return;
      }
      const group = byVendor.get(id) || {
        id,
        label,
        models: [],
        scopable: Boolean(vendorID),
      };
      group.models.push({ fact, name });
      byVendor.set(id, group);
    });
    return [...byVendor.values()].sort((a, b) =>
      a.label.localeCompare(b.label),
    );
  }, [
    query,
    tier,
    tokens.modelOptions,
    tokens.vendorOptions.models,
    vendorName,
  ]);

  const setModels = (next: string[]) =>
    form.setValue('model_limits', [...new Set(next)], { shouldDirty: true });
  const setVendors = (next: string[]) =>
    form.setValue('model_scope_vendors', [...new Set(next)], {
      shouldDirty: true,
    });

  const toggleModel = (name: string) =>
    setModels(
      selectedModels.includes(name)
        ? selectedModels.filter((item) => item !== name)
        : [...selectedModels, name],
    );

  const vendorState = (group: (typeof groups)[number]) => {
    if (selectedVendors.includes(group.id)) return 'all';
    const chosen = group.models.filter((model) =>
      selectedModels.includes(model.name),
    ).length;
    if (chosen === 0) return 'none';
    return chosen === group.models.length ? 'all' : 'some';
  };

  /** A box that is fully on turns the vendor off; any other state — empty or
   *  partly ticked — takes it whole. */
  const toggleVendor = (group: (typeof groups)[number]) => {
    const names = group.models.map((model) => model.name);
    if (vendorState(group) === 'all') {
      setVendors(selectedVendors.filter((id) => id !== group.id));
      setModels(selectedModels.filter((name) => !names.includes(name)));
      return;
    }
    // Taking the vendor wholesale makes its individually named models
    // redundant, and leaving them behind would freeze the scope at today's
    // catalog — the opposite of what ticking the vendor asked for.
    if (!group.scopable) {
      setModels([...selectedModels, ...names]);
      return;
    }
    setVendors([...selectedVendors, group.id]);
    setModels(selectedModels.filter((name) => !names.includes(name)));
  };

  if (!enabled) return null;

  /* The pane opens on the vendor asked for, else the first one this Key
     already reaches into, else the first in the list. */
  const active =
    groups.find((group) => group.id === activeVendor) ||
    groups.find((group) => vendorState(group) !== 'none') ||
    groups[0];
  const activeState = active ? vendorState(active) : 'none';
  const activeChosen = active
    ? active.models.filter(
        (model) => activeState === 'all' || selectedModels.includes(model.name),
      ).length
    : 0;
  const nothingPicked = !selectedModels.length && !selectedVendors.length;
  const invalid = Boolean(form.formState.errors.model_limits);

  return (
    <div className='tok-scope'>
      <div className='tok-scope__filters'>
        <span className='tok-scope__search'>
          <Search aria-hidden='true' className='tok-scope__icon' size={16} />
          <Input
            aria-label={t('page.console.token.scope.search', {
              defaultValue: '搜索模型',
            })}
            className='tok-scope__input'
            placeholder={t('page.console.token.scope.search', {
              defaultValue: '搜索模型',
            })}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </span>
        <div
          className='tokv2-seg tok-scope__tiers'
          role='group'
          aria-label={t('page.console.token.scope.tierLabel', {
            defaultValue: '按档位筛选',
          })}
        >
          {(
            [
              [
                'flagship',
                t('page.console.token.scope.tier.flagship', {
                  defaultValue: '旗舰',
                }),
              ],
              [
                'balanced',
                t('page.console.token.scope.tier.balanced', {
                  defaultValue: '均衡',
                }),
              ],
              [
                'light',
                t('page.console.token.scope.tier.light', {
                  defaultValue: '轻量',
                }),
              ],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type='button'
              data-owns-chrome='true'
              aria-pressed={tier === key}
              className={cn('tokv2-seg__btn', tier === key && 'is-on')}
              onClick={() => setTier(tier === key ? '' : key)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {groups.length && active ? (
        <div className='tok-scope__panes'>
          <div className='tok-scope__vendors'>
            {groups.map((group) => {
              const state = vendorState(group);
              const chosen = group.models.filter(
                (model) =>
                  state === 'all' || selectedModels.includes(model.name),
              ).length;
              const isActive = group.id === active.id;
              return (
                <div
                  className={cn(
                    'tok-scope__vrow',
                    isActive && 'is-active',
                    state !== 'none' && 'is-on',
                  )}
                  key={group.id}
                >
                  <Checkbox
                    aria-label={t('page.console.token.scope.selectVendor', {
                      defaultValue: '选择 {{vendor}} 的全部模型',
                      vendor: group.label,
                    })}
                    checked={state === 'all'}
                    indeterminate={state === 'some'}
                    onCheckedChange={() => toggleVendor(group)}
                  />
                  <button
                    type='button'
                    data-owns-chrome='true'
                    aria-pressed={isActive}
                    className='tok-scope__vpick'
                    onClick={() => setActiveVendor(group.id)}
                  >
                    <span className='tok-scope__vname'>{group.label}</span>
                    <span className='tok-scope__vcount'>
                      {state === 'all' && group.scopable
                        ? t('page.console.token.scope.vendorAll', {
                            defaultValue: '全部',
                          })
                        : `${chosen}/${group.models.length}`}
                    </span>
                  </button>
                </div>
              );
            })}
          </div>

          <div className='tok-scope__models'>
            <div className='tok-scope__mhead'>
              <b className='tok-scope__mtitle'>{active.label}</b>
              <span className='tokv2-hint'>
                {t('page.console.token.scope.chosenOf', {
                  chosen: activeChosen,
                  defaultValue: '已选 {{chosen}} / {{total}}',
                  total: active.models.length,
                })}
              </span>
              <Checkbox
                checked={activeState === 'all'}
                labelClassName='tok-scope__whole'
                onCheckedChange={() => toggleVendor(active)}
              >
                {active.scopable
                  ? t('page.console.token.scope.wholeVendor', {
                      defaultValue: '整家纳入',
                    })
                  : t('page.console.token.scope.selectAll', {
                      defaultValue: '全选',
                    })}
              </Checkbox>
            </div>
            <div className='tok-scope__mlist'>
              {active.models.map((model) => (
                <Checkbox
                  checked={
                    activeState === 'all' || selectedModels.includes(model.name)
                  }
                  disabled={selectedVendors.includes(active.id)}
                  key={model.name}
                  labelClassName='tok-scope__mrow'
                  onCheckedChange={() => toggleModel(model.name)}
                >
                  <span className='tok-scope__mname'>{model.name}</span>
                  {perMtok(model.fact) ? (
                    <span className='tok-scope__price'>
                      {perMtok(model.fact)}
                    </span>
                  ) : null}
                </Checkbox>
              ))}
            </div>
          </div>
        </div>
      ) : (
        <div className='tok-scope__empty'>
          {t('legacy.i18n.no_results_found.3f5746269e')}
        </div>
      )}

      {/* Switched on with nothing ticked cannot be saved (the schema refuses
          it, since the gateway would read it as no scope at all). Say so
          before the Save press rather than after it. */}
      {nothingPicked ? (
        <div
          className={cn('tok-scope__warn', invalid && 'is-error')}
          role={invalid ? 'alert' : 'status'}
        >
          <CircleAlert aria-hidden='true' size={16} />
          <span>
            {t('page.console.token.scope.emptyWarning', {
              defaultValue:
                '还没勾选。至少勾一个厂商或模型才能保存；不想限制就把开关关上。',
            })}
          </span>
        </div>
      ) : null}

      <span className='tokv2-hint'>
        {t('page.console.token.scope.vendorHint', {
          defaultValue:
            '勾左边的厂商 = 整家纳入，之后上新的模型也自动加入；点厂商名在右边单选模型。',
        })}
      </span>
    </div>
  );
}

/** Fields that live behind 更多设置. A validation error on any of them has
 *  nowhere to render while the section is collapsed. */
const ADVANCED_FIELDS = new Set([
  'allow_ips',
  'ip_blacklist',
  'metadata',
  'model_limits',
  'rpm_limit',
  'search_domain_allowlist',
  'search_providers',
  'tokenCount',
  'tpm_limit',
]);

/** One question in 更多设置: a title, a sentence saying what it is for, and
 *  the switch that turns it on. Closed, it costs one line; open, it shows only
 *  its own fields. Nine of these stacked read as a menu rather than a form. */
function MoreSection({
  children,
  control,
  hint,
  open = true,
  title,
}: {
  children?: React.ReactNode;
  control?: React.ReactNode;
  hint?: React.ReactNode;
  open?: boolean;
  title: React.ReactNode;
}) {
  return (
    <section className='tok-more__sec'>
      <div className='tok-more__head'>
        <b className='tok-more__title'>{title}</b>
        {hint ? <span className='tok-more__hint'>{hint}</span> : null}
        {control ? <span className='tok-more__switch'>{control}</span> : null}
      </div>
      {open ? <div className='tok-more__body'>{children}</div> : null}
    </section>
  );
}

export function TokenFormDrawer({
  editingToken,
  onOpenChange,
  onSecretsCreated,
  open,
  tokens,
  team,
}: OpenChangeProps & {
  editingToken: TokenRecord | null;
  onSecretsCreated: (secrets: CreatedSecret[]) => void;
  tokens: TokenFormServices;
  team?: TeamTokenFormContext;
}) {
  const { t } = tokens;
  const isEdit = Boolean(editingToken?.id);
  const detailQuery = useTokenDetailQuery(
    editingToken?.id,
    open && isEdit && !team,
  );
  const editValues = team ? team.detailValues : detailQuery.data;
  const isTeam = Boolean(team);
  const defaultValues = useMemo(
    () => ({
      ...getDefaultTokenFormValues(),
      ...(isTeam ? { remain_quota: 20 } : {}),
    }),
    [isTeam],
  );
  const form = useForm<TokenFormInput, unknown, TokenFormData>({
    defaultValues: defaultValues as TokenFormInput,
    resolver: zodResolver(tokenFormSchema),
  });
  const values = form.watch();
  const [moreOpen, setMoreOpen] = useState(false);
  const [expiryChoice, setExpiryChoice] = useState<
    'never' | '30' | '90' | 'custom'
  >('never');
  /** The amounts most Keys are created with; anything else is typed. */
  const quotaPresets = team ? [20, 50, 100] : [10, 50, 200];
  const setQuota = (amount: number) => {
    form.setValue('unlimited_quota', false, { shouldDirty: true });
    form.setValue('remain_quota', amount, { shouldDirty: true });
  };
  const chooseExpiry = (choice: 'never' | '30' | '90' | 'custom') => {
    setExpiryChoice(choice);
    if (choice === 'never') {
      setExpiryOffset(null);
    } else if (choice === 'custom') {
      form.setValue('expires_never', false);
    } else {
      setExpiryOffset(Number(choice));
    }
  };

  // The expiry segments belong to the create form; the edit form has its own
  // date field. Leaving a create-time "自定义" behind would make Save on an
  // unrelated edit fail against a control that is not even on screen.
  useEffect(() => {
    if (!open || isEdit) return;
    setExpiryChoice('never');
  }, [isEdit, open]);

  useEffect(() => {
    if (!open) {
      return;
    }
    if (isEdit && editValues) {
      form.reset({
        ...defaultValues,
        ...editValues,
      } as TokenFormInput);
      setExpiryChoice(editValues.expires_never ? 'never' : 'custom');
      return;
    }
    if (!isEdit) {
      form.reset(defaultValues as TokenFormInput);
      setMoreOpen(false);
      setExpiryChoice('never');
    }
  }, [defaultValues, editValues, form, isEdit, open]);

  const setExpiryOffset = (days: number | null) => {
    if (days === null) {
      form.setValue('expires_never', true);
      form.setValue('expired_time', null);
      return;
    }
    const next = new Date();
    next.setDate(next.getDate() + days);
    form.setValue('expires_never', false);
    form.setValue('expired_time', next);
  };

  const tokenLaneVendors = useTokenLaneVendors(tokens.vendorOptions.vendors);
  const allowedLaneVendorIds = useMemo(
    () =>
      new Set(
        tokenLaneVendors
          .map((vendor) => String(vendor.id || '').trim())
          .filter(Boolean),
      ),
    [tokenLaneVendors],
  );
  const submit = form.handleSubmit(
    async (formValues) => {
      // "自定义" with no date reaches the API as expired_time -1, i.e. a Key
      // that never expires — the opposite of what the form shows. The segments
      // only exist on the create form, so only creation is checked here.
      if (
        !isEdit &&
        expiryChoice === 'custom' &&
        !(formValues.expired_time instanceof Date)
      ) {
        setMoreOpen((current) => current);
        form.setError('expired_time', {
          type: 'manual',
          message: t('page.console.token.create.expiryRequired', {
            defaultValue: '选一个到期日期，或改回「永不过期」',
          }),
        });
        return;
      }
      await save(formValues);
    },
    (errors) => {
      // A field inside 更多设置 fails silently while the section is closed:
      // no message is mounted, and the button just looks broken.
      if (Object.keys(errors).some((name) => ADVANCED_FIELDS.has(name))) {
        setMoreOpen(true);
      }
    },
  );

  const save = async (formValues: TokenFormData) => {
    const submittedVendorGroups = (formValues.vendor_groups || {}) as Record<
      string,
      'direct' | 'cheap'
    >;
    const allowedVendorGroups = Object.fromEntries(
      Object.entries(submittedVendorGroups).filter(
        ([vendorId, lane]) =>
          lane === 'cheap' && allowedLaneVendorIds.has(vendorId),
      ),
    );
    if (team?.disabled) return;
    form.clearErrors('root');
    try {
      const result = await tokens.saveTokenMutation.mutateAsync({
        editingToken,
        values: {
          ...formValues,
          vendor_groups: allowedVendorGroups,
        },
      });
      if (!editingToken?.id && Array.isArray(result?.data)) {
        onSecretsCreated(result.data as CreatedSecret[]);
      }
      onOpenChange(false);
    } catch (error) {
      form.setError('root', {
        message: errorMessage(
          error,
          t('common.error', { defaultValue: '操作失败，请重试' }),
        ),
      });
    }
  };
  const vendorGroups = (values.vendor_groups || {}) as Record<
    string,
    'direct' | 'cheap'
  >;
  const updateVendorGroup = (vendorId: string, lane: 'direct' | 'cheap') => {
    const nextVendorGroups = { ...vendorGroups };
    if (lane === 'cheap') {
      nextVendorGroups[vendorId] = 'cheap';
    } else {
      delete nextVendorGroups[vendorId];
    }
    form.setValue('vendor_groups', nextVendorGroups, { shouldDirty: true });
  };

  const quotaDisplay = isEdit
    ? buildQuotaDisplay({
        remaining: usdToMicroUSD(Number(values.remain_quota) || 0),
        unlimited: Boolean(values.unlimited_quota),
        used: asMicroUSD(editingToken?.used_quota),
      })
    : null;
  const capSpent = Number(values.period_cap_spent_usd) || 0;

  /** One block per question, each closed until its switch is on. Nine open
   *  sections would be a second page; nine one-line headers is a menu. */
  const advanced = (
    <div className='tok-more'>
      <MoreSection
        title={t('page.console.token.form.modelScope', {
          defaultValue: '模型范围',
        })}
        hint={modelScopeSummary(t, values)}
        control={
          <>
            {values.model_limits_enabled &&
            ((values.model_limits || []).length ||
              (values.model_scope_vendors || []).length) ? (
              <button
                type='button'
                className='tokv2-editlink tok-scope__clear'
                onClick={() => {
                  form.setValue('model_limits', [], { shouldDirty: true });
                  form.setValue('model_scope_vendors', [], {
                    shouldDirty: true,
                  });
                }}
              >
                {t('page.console.token.scope.clear', {
                  defaultValue: '清空',
                })}
              </button>
            ) : null}
            <Switch
              aria-label={t('page.console.token.form.modelScope', {
                defaultValue: '模型范围',
              })}
              checked={Boolean(values.model_limits_enabled)}
              onCheckedChange={(next) => {
                form.setValue('model_limits_enabled', next, {
                  shouldDirty: true,
                });
                if (!next) {
                  form.setValue('model_limits', [], { shouldDirty: true });
                  form.setValue('model_scope_vendors', [], {
                    shouldDirty: true,
                  });
                }
              }}
            />
          </>
        }
        open={Boolean(values.model_limits_enabled)}
      >
        {/* The empty-scope error is spoken by the picker's own notice. */}
        <ModelScopeField form={form} t={t} tokens={tokens} values={values} />
      </MoreSection>

      <MoreSection
        title={t('page.console.token.form.rateLimitLabel', {
          defaultValue: '限流',
        })}
        hint={t('page.console.token.form.rateLimitSectionHint', {
          defaultValue: '按账户档位的上限折算',
        })}
        control={
          <Controller
            control={form.control}
            name='rate_limits_enabled'
            render={({ field }) => (
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            )}
          />
        }
        open={Boolean(values.rate_limits_enabled)}
      >
        <RateLimitFields
          ceiling={tokens.vendorOptions.rateLimitCeiling}
          form={form}
          t={t}
          values={values}
        />
      </MoreSection>

      <MoreSection
        title={t('page.console.token.form.usageAlert', {
          defaultValue: '用量提醒',
        })}
        hint={
          team
            ? t(
                values.unlimited_quota
                  ? 'projects.key.form.alertRequiresCap'
                  : 'projects.key.form.alertHint',
              )
            : t('page.console.token.form.usageAlertHint', {
                defaultValue: '额度用到阈值时发一封邮件',
              })
        }
        control={
          <Switch
            checked={Number(values.usage_alert_percent) > 0}
            disabled={Boolean(team && values.unlimited_quota)}
            onCheckedChange={(next) =>
              form.setValue('usage_alert_percent', next ? 80 : 0, {
                shouldDirty: true,
              })
            }
          />
        }
        open={Number(values.usage_alert_percent) > 0}
      >
        <div className='tokv2-row'>
          <span className='tokv2-f__label'>
            {t('page.console.token.form.usageAlertAt', {
              defaultValue: '已用达到',
            })}
          </span>
          {[50, 80, 90].map((percent) => (
            <button
              key={percent}
              type='button'
              data-owns-chrome='true'
              aria-pressed={Number(values.usage_alert_percent) === percent}
              className={cn(
                'tokv2-chip',
                Number(values.usage_alert_percent) === percent &&
                  'tokv2-chip--evi',
              )}
              onClick={() =>
                form.setValue('usage_alert_percent', percent, {
                  shouldDirty: true,
                })
              }
            >
              {`${percent}%`}
            </button>
          ))}
        </div>
        <span className='tokv2-hint'>
          {team
            ? t('projects.key.form.alertNote')
            : t('page.console.token.form.usageAlertNote', {
                defaultValue: '每个阈值只发一次，加额度后重新计算。',
              })}
        </span>
      </MoreSection>

      <MoreSection
        title={t('page.console.token.form.ipAllowlist', {
          defaultValue: 'IP 白名单',
        })}
        hint={t('page.console.token.form.ipAllowlistHint', {
          defaultValue: '只有这些地址能用',
        })}
        control={
          <Controller
            control={form.control}
            name='ip_whitelist_enabled'
            render={({ field }) => (
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            )}
          />
        }
        open={Boolean(values.ip_whitelist_enabled)}
      >
        <Textarea
          placeholder={t(
            'legacy.i18n.allowed_ips_one_per_line_not_filled_in_means_no_restrictions.5ca008f520',
          )}
          rows={3}
          {...form.register('allow_ips')}
        />
      </MoreSection>

      <MoreSection
        title={t('page.console.token.form.ipBlocklist', {
          defaultValue: 'IP 黑名单',
        })}
        hint={t('page.console.token.form.ipBlocklistHint', {
          defaultValue: '这些地址一律拒绝',
        })}
        control={
          <Controller
            control={form.control}
            name='ip_blacklist_enabled'
            render={({ field }) => (
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            )}
          />
        }
        open={Boolean(values.ip_blacklist_enabled)}
      >
        <Textarea
          placeholder={t('legacy.i18n.banned_ips_one_per_line.49dde1bbd2')}
          rows={3}
          {...form.register('ip_blacklist')}
        />
      </MoreSection>

      <MoreSection
        title={t('page.console.token.search.title')}
        hint={t('page.console.token.form.searchHint', {
          defaultValue: '限制能用的搜索源和域名',
        })}
        control={
          <Controller
            control={form.control}
            name='search_limits_enabled'
            render={({ field }) => (
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            )}
          />
        }
        open={Boolean(values.search_limits_enabled)}
      >
        <Controller
          control={form.control}
          name='search_providers'
          render={({ field }) => (
            <MultiSelect
              options={SEARCH_PROVIDERS.map((provider) => ({
                label: provider.label,
                value: provider.id,
              }))}
              placeholder={t(
                'page.console.token.search.allowedProvidersPlaceholder',
              )}
              value={field.value}
              onValueChange={field.onChange}
            />
          )}
        />
        <Textarea
          placeholder={t(
            'page.console.token.search.domainAllowlistPlaceholder',
          )}
          rows={2}
          {...form.register('search_domain_allowlist')}
        />
        <span className='tokv2-hint'>
          {t('page.console.token.search.allowedProvidersHint')}
        </span>
      </MoreSection>

      <MoreSection
        title={t('page.console.token.decision.generation', {
          defaultValue: 'Automatic decision questions',
        })}
        hint={t('page.console.token.decision.help', {
          defaultValue:
            'Allows model + state requests. Question generation and the decision are billed separately; explicit questions always take precedence.',
        })}
        control={
          <Controller
            name='question_generation_enabled'
            control={form.control}
            render={({ field }) => (
              <Switch
                checked={field.value}
                onCheckedChange={field.onChange}
                aria-label={t('page.console.token.decision.generation', {
                  defaultValue: 'Automatic decision questions',
                })}
              />
            )}
          />
        }
        open={Boolean(values.question_generation_enabled)}
      >
        {values.question_generation_enabled && (
          <Input
            aria-label={t('page.console.token.decision.model', {
              defaultValue: 'Question generation model',
            })}
            placeholder='gpt-6-luna'
            {...form.register('question_generation_model')}
          />
        )}
      </MoreSection>

      <MoreSection
        title={t('page.console.token.form.note', { defaultValue: '备注' })}
        hint={t('page.console.token.form.noteHint', {
          defaultValue: '给同事看的一句话',
        })}
      >
        <Input
          placeholder={t('page.console.token.form.notePlaceholder', {
            defaultValue: '例如：我的 Mac 上的 Claude Code',
          })}
          {...form.register('note')}
        />
      </MoreSection>

      {!isEdit && !team ? (
        <MoreSection
          title={t('legacy.i18n.new_quantity.0859dc5a65')}
          hint={t(
            'legacy.i18n.when_creating_in_batches_a_random_suffix_will_be_automatically_a.97f8747f02',
          )}
        >
          <Input min='1' type='number' {...form.register('tokenCount')} />
          <FieldMessage error>
            {String(form.formState.errors.tokenCount?.message || '')}
          </FieldMessage>
        </MoreSection>
      ) : null}
    </div>
  );

  const formBody = (
    <form className='tok-create' id='token-form' onSubmit={submit}>
      {team?.content}
      <div className='tok-create__field'>
        <label className='tok-create__label' htmlFor='token-name'>
          {t('legacy.i18n.name.1be7ae4fc2')}
        </label>
        <Input
          id='token-name'
          maxLength={30}
          placeholder={
            team
              ? t('projects.key.form.namePlaceholder')
              : t('page.console.token.create.namePlaceholder', {
                  defaultValue: '例如 claude-code · Mac',
                })
          }
          {...form.register('name')}
        />
        <FieldMessage error>
          {form.formState.errors.name
            ? t('legacy.i18n.please_enter_a_name.c2afb255a5')
            : ''}
        </FieldMessage>
      </div>

      <div className='tok-create__quota'>
        <div className='tok-create__head'>
          <label className='tok-create__quota-label' htmlFor='token-quota'>
            {isEdit && !team
              ? t('page.console.token.form.remainingQuota', {
                  defaultValue: '剩余额度',
                })
              : team
                ? t('projects.key.form.monthlyCap')
                : t('page.console.token.create.quota', {
                    defaultValue: '额度',
                  })}
          </label>
          <span className='tok-create__hint'>
            {isEdit && !team && quotaDisplay
              ? t('page.console.token.form.quotaUsedHint', {
                  defaultValue: '已用 {{used}} · 用完自动停用',
                  used: renderQuota(quotaDisplay.used),
                })
              : team
                ? null
                : t('page.console.token.create.quotaHint', {
                    defaultValue: '用完自动停用，之后随时可以加',
                  })}
          </span>
        </div>
        <div className='tok-create__row'>
          <div className='tok-create__money'>
            <span aria-hidden='true'>$</span>
            <Input
              id='token-quota'
              min='0'
              step='any'
              type='number'
              disabled={Boolean(values.unlimited_quota)}
              {...form.register('remain_quota')}
            />
          </div>
          <div className='tok-create__presets'>
            {/* On a new Key the chips are amounts; on an existing one the
                question is how much to add, so they top up what is left. */}
            {quotaPresets.map((amount) => {
              const active =
                (!isEdit || team) &&
                !values.unlimited_quota &&
                Number(values.remain_quota) === amount;
              return (
                <button
                  key={amount}
                  type='button'
                  data-owns-chrome='true'
                  aria-pressed={active}
                  className={cn('tokv2-chip', active && 'tokv2-chip--evi')}
                  onClick={() =>
                    isEdit && !team
                      ? setQuota(
                          Math.round(
                            ((Number(values.remain_quota) || 0) + amount) * 1e6,
                          ) / 1e6,
                        )
                      : setQuota(amount)
                  }
                >
                  {isEdit && !team ? `+$${amount}` : `$${amount}`}
                </button>
              );
            })}
            <button
              type='button'
              data-owns-chrome='true'
              aria-pressed={Boolean(values.unlimited_quota)}
              className={cn(
                'tokv2-chip',
                values.unlimited_quota && 'tokv2-chip--evi',
              )}
              onClick={() => {
                form.setValue('unlimited_quota', !values.unlimited_quota, {
                  shouldDirty: true,
                });
                if (team && !values.unlimited_quota)
                  form.setValue('usage_alert_percent', 0, {
                    shouldDirty: true,
                  });
              }}
            >
              {t('page.console.token.create.unlimited', {
                defaultValue: '不限',
              })}
            </button>
          </div>
        </div>
        {team ? (
          <span className='tok-create__hint'>
            {t('projects.key.form.budgetHint')}
          </span>
        ) : null}
        {isEdit && !team && quotaDisplay?.mode === 'finite' ? (
          <div className='tokv2-meter'>
            <span
              className='tokv2-meter__fill'
              style={{ width: `${quotaDisplay.usedPercent}%` }}
            />
          </div>
        ) : null}

        {/* The total says how much this Key may ever spend; this says how much
            it may spend in a day. A runaway agent is stopped by the second one
            long before it reaches the first. Creation does not ask: a Key with
            no spend behind it has nothing to pace, and the create screen earns
            its "one name is enough" by not asking. */}
        {isEdit ? (
          <div className='tok-create__cap'>
            <label className='tok-create__cap-label' htmlFor='token-cap'>
              {t('page.console.token.form.periodCap', {
                defaultValue: '周期上限',
              })}
            </label>
            <div className='tok-create__money tok-create__cap-money'>
              <span aria-hidden='true'>$</span>
              <Input
                id='token-cap'
                min='0'
                step='any'
                type='number'
                disabled={!values.period_cap_period}
                {...form.register('period_cap_usd')}
              />
            </div>
            <div
              className='tokv2-seg'
              role='radiogroup'
              aria-label={t('page.console.token.form.periodCap', {
                defaultValue: '周期上限',
              })}
            >
              {(
                [
                  [
                    'day',
                    t('page.console.token.form.perDay', {
                      defaultValue: '每天',
                    }),
                  ],
                  [
                    'month',
                    t('page.console.token.form.perMonth', {
                      defaultValue: '每月',
                    }),
                  ],
                  [
                    '',
                    t('page.console.token.create.unlimited', {
                      defaultValue: '不限',
                    }),
                  ],
                ] as const
              ).map(([period, label]) => (
                <button
                  key={period || 'none'}
                  type='button'
                  role='radio'
                  aria-checked={values.period_cap_period === period}
                  data-owns-chrome='true'
                  className={cn(
                    'tokv2-seg__btn',
                    values.period_cap_period === period && 'is-on',
                  )}
                  onClick={() =>
                    form.setValue('period_cap_period', period, {
                      shouldDirty: true,
                    })
                  }
                >
                  {label}
                </button>
              ))}
            </div>
            {values.period_cap_period ? (
              <span className='tok-create__hint tok-create__cap-spent'>
                {values.period_cap_period === 'day'
                  ? t('page.console.token.form.capSpentDay', {
                      defaultValue: '今天已用 ${{spent}}',
                      spent: capSpent.toFixed(2),
                    })
                  : t('page.console.token.form.capSpentMonth', {
                      defaultValue: '本月已用 ${{spent}}',
                      spent: capSpent.toFixed(2),
                    })}
              </span>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className='tok-create__field'>
        <span className='tok-create__label' id='token-expiry-label'>
          {t('page.console.token.create.expiry', { defaultValue: '到期' })}
        </span>
        <div
          className='tokv2-seg tok-create__seg'
          role='radiogroup'
          aria-labelledby='token-expiry-label'
        >
          {(
            [
              ['never', t('legacy.i18n.never_expires.0070ca2601')],
              [
                '30',
                t('page.console.token.create.days', {
                  count: 30,
                  defaultValue: '{{count}} 天',
                }),
              ],
              [
                '90',
                t('page.console.token.create.days', {
                  count: 90,
                  defaultValue: '{{count}} 天',
                }),
              ],
              [
                'custom',
                t('page.console.token.create.custom', {
                  defaultValue: '自定义',
                }),
              ],
            ] as const
          ).map(([choice, label]) => (
            <button
              key={choice}
              type='button'
              role='radio'
              aria-checked={expiryChoice === choice}
              data-owns-chrome='true'
              className={cn(
                'tokv2-seg__btn',
                expiryChoice === choice && 'is-on',
              )}
              onClick={() => chooseExpiry(choice)}
            >
              {label}
            </button>
          ))}
        </div>
        {expiryChoice === 'custom' ? (
          <>
            <Controller
              control={form.control}
              name='expired_time'
              render={({ field }) => (
                <DatePicker
                  value={field.value instanceof Date ? field.value : undefined}
                  onChange={(date?: Date) => {
                    form.clearErrors('expired_time');
                    form.setValue('expires_never', !date);
                    field.onChange(date || null);
                  }}
                />
              )}
            />
            <FieldMessage error>
              {String(form.formState.errors.expired_time?.message || '')}
            </FieldMessage>
          </>
        ) : null}
      </div>

      {tokenLaneVendors.length ? (
        <div className='tok-create__field'>
          <div className='tok-create__head'>
            <span className='tok-create__label'>
              {t('page.console.token.create.lane', { defaultValue: '线路' })}
            </span>
            <span className='tok-create__hint'>
              {team
                ? t('projects.key.form.laneHint')
                : t('page.console.token.create.laneHint', {
                    defaultValue:
                      'Agent 专线由第三方订阅池提供，不是官方 API，会带预设上下文，价格低很多；只建议在对应的 Agent 工具里用：Claude 配 Claude Code，GPT 配 Codex，Gemini 配 Antigravity',
                  })}
            </span>
          </div>
          <div className='tok-create__lanes'>
            {tokenLaneVendors.map((vendor) => {
              const vendorId = String(vendor.id);
              const lane = vendorGroups[vendorId] || 'direct';
              const sample = laneSampleModel(
                tokens.vendorOptions.models.filter(
                  (fact) => fact.vendorID === vendorId,
                ),
              );
              return (
                <div className='tok-create__lane' key={vendorId}>
                  <span className='tok-create__lane-name'>
                    {vendor.laneLabel}
                    {sample ? (
                      <span className='tok-create__hint'>{sample}</span>
                    ) : null}
                  </span>
                  <div
                    className='tokv2-seg'
                    role='radiogroup'
                    aria-label={vendor.laneLabel}
                  >
                    {(['direct', 'cheap'] as const).map((value) => (
                      <button
                        key={value}
                        type='button'
                        role='radio'
                        aria-checked={lane === value}
                        data-owns-chrome='true'
                        className={cn(
                          'tokv2-seg__btn',
                          lane === value && 'is-on',
                        )}
                        onClick={() => updateVendorGroup(vendorId, value)}
                      >
                        {value === 'direct'
                          ? t('page.console.token.form.laneDirect', {
                              defaultValue: '稳定',
                            })
                          : t('page.console.token.form.laneCheap', {
                              defaultValue: 'Agent',
                            })}
                      </button>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      ) : null}

      <div className='tok-create__field'>
        <div className='tok-create__head'>
          <span className='tok-create__label' id='tok-effort-label'>
            {t('page.console.token.create.effort', {
              defaultValue: '思考深度',
            })}
          </span>
          {team &&
          effortRank(team.memberEffortCap) < EFFORT_LEVELS.length - 1 ? (
            <span className='tok-create__hint'>
              {t('page.console.token.create.effortMemberCap', {
                defaultValue: '{{name}} 的上限 ≤ {{cap}}',
                name: team.memberName ?? '',
                cap: team.memberEffortCap,
              })}
            </span>
          ) : null}
        </div>
        <EffortCapControl
          aria-label={t('page.console.token.create.effort', {
            defaultValue: '思考深度',
          })}
          ceiling={team?.memberEffortCap}
          onChange={(next) =>
            form.setValue('reasoning_effort_cap', next, { shouldDirty: true })
          }
          size='lg'
          value={lowerEffortCap(
            String(values.reasoning_effort_cap || ''),
            team?.memberEffortCap,
          )}
        />
        <span className='tok-create__hint tok-create__hint--below'>
          {t('page.console.token.create.effortHint', {
            defaultValue:
              '点一档就是上限：这把 Key 最多用到这一档，写得更高的请求按上限执行并记在日志里。这些词就是 Codex / Claude Code 里的词。',
          })}
        </span>
      </div>

      <button
        type='button'
        data-owns-chrome='true'
        className={cn('tok-create__more', moreOpen && 'is-open')}
        aria-expanded={moreOpen}
        aria-controls='token-create-advanced'
        onClick={() => setMoreOpen((current) => !current)}
      >
        <ChevronRight size={14} aria-hidden='true' />
        {t('page.console.token.create.more', { defaultValue: '更多设置' })}
        {!team && (
          <span className='tok-create__more-hint'>
            {t('page.console.token.create.moreHint', {
              defaultValue: '模型范围 · 限流 · 用量提醒 · IP · 检索 · 备注',
            })}
          </span>
        )}
      </button>
      <FieldMessage error>{form.formState.errors.root?.message}</FieldMessage>
      {moreOpen ? <div id='token-create-advanced'>{advanced}</div> : null}
    </form>
  );

  const createdAt = formatUnixTime(editingToken?.created_time);

  // Edit is the create screen with the values filled in. What an editor cannot
  // change — the Key itself and when it was made — sits under the title rather
  // than taking a section of its own.
  return (
    <Modal
      busy={tokens.saveTokenMutation.isPending}
      open={open}
      onOpenChange={onOpenChange}
      className={cn(
        'tok-create-modal',
        isEdit && 'tok-create-modal--edit',
        team && 'tok-create-modal--team',
      )}
      title={
        isEdit
          ? t('page.console.token.form.editTitle', {
              defaultValue: '编辑密钥 · {{name}}',
              name: String(editingToken?.name || ''),
            })
          : t('page.console.token.form.createTitle', {
              defaultValue: '创建密钥',
            })
      }
      description={
        isEdit && editingToken ? (
          <>
            <TokenKeyCell
              record={editingToken}
              onCopy={async (record) => {
                if (record.id === undefined) return false;
                try {
                  return await tokens.copyText(
                    await tokens.revealKey(record.id),
                  );
                } catch {
                  return false;
                }
              }}
            />
            {createdAt === '-' ? null : (
              <span>
                {t('page.console.token.form.createdAt', {
                  defaultValue: '· {{date}} 创建',
                  date: createdAt,
                })}
              </span>
            )}
          </>
        ) : team ? undefined : (
          t('page.console.token.create.description', {
            defaultValue:
              '一个名字就能建。额度、到期、线路都有默认值，创建后直接进接入。',
          })
        )
      }
      body={
        team && isEdit && !editValues ? (
          <>
            {team.content}
            <PendingBlock height={420} />
          </>
        ) : (
          formBody
        )
      }
      footer={
        <div className='tok-modal-foot'>
          {isEdit ? null : (
            <span className='tok-modal-foot__hint'>
              {team
                ? team.countLabel
                : t('page.console.token.create.footHint', {
                    defaultValue: '创建后直接打开接入窗口，可在里面选工具',
                  })}
            </span>
          )}
          <Button
            type='button'
            variant='secondary'
            disabled={tokens.saveTokenMutation.isPending}
            onClick={() => {
              if (team && !isEdit) {
                form.reset(defaultValues as TokenFormInput);
                setMoreOpen(false);
                setExpiryChoice('never');
              } else onOpenChange(false);
            }}
          >
            {team && !isEdit
              ? t('projects.key.form.reset')
              : t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={team?.disabled}
            form='token-form'
            variant='primary'
            loading={
              tokens.saveTokenMutation.isPending ||
              (isEdit && detailQuery.isFetching)
            }
            type='submit'
          >
            {isEdit
              ? t('page.console.token.form.save', { defaultValue: '保存修改' })
              : team
                ? t('page.console.token.form.createTitle', {
                    defaultValue: '创建密钥',
                  })
                : t('page.console.token.create.submit', {
                    defaultValue: '创建并接入',
                  })}
          </Button>
        </div>
      }
    />
  );
}

export function FluentPrefillModal({
  onOpenChange,
  open,
  prefillKey,
  tokens,
}: OpenChangeProps & { prefillKey: string; tokens: TokensQuery }) {
  const { t } = tokens;
  const [selectedModel, setSelectedModel] = useState('');

  const sendToFluent = () => {
    const container = document.getElementById('fluent-lazu-container');
    if (!container) {
      return;
    }
    if (!selectedModel) {
      return;
    }
    const status = localStorage.getItem('status');
    let serverAddress = '';
    if (status) {
      try {
        const parsed: unknown = JSON.parse(status);
        serverAddress =
          parsed &&
          typeof parsed === 'object' &&
          typeof (parsed as Record<string, unknown>).server_address === 'string'
            ? String((parsed as Record<string, unknown>).server_address)
            : '';
      } catch {
        serverAddress = '';
      }
    }
    if (!serverAddress) {
      serverAddress = window.location.origin;
    }
    const fallback = tokens.selectedRows[0] || tokens.tokens[0];
    const apiKey = prefillKey || fallback?.key;
    if (!apiKey) {
      return;
    }
    container.dispatchEvent(
      new CustomEvent('fluent:prefill', {
        detail: {
          apiKey: formatTokenKey(apiKey),
          baseUrl: serverAddress,
          id: 'lazu',
          model: selectedModel,
        },
      }),
    );
    onOpenChange(false);
  };

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={t('legacy.i18n.fluentread_smooth_reading_detected.c6986ba698')}
      description={t(
        'legacy.i18n.after_selecting_a_model_you_can_fill_the_current_selected_token.3995686dca',
      )}
      body={
        <div className='tokv2-f'>
          <label className='tokv2-f__label'>
            {t('legacy.i18n.please_select_model.7330bf2340')}
          </label>
          <Select value={selectedModel} onValueChange={setSelectedModel}>
            {tokens.modelOptions.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </Select>
        </div>
      }
      footer={
        <div className='flex justify-end gap-2'>
          <Button variant='ghost' onClick={() => onOpenChange(false)}>
            {t('legacy.i18n.close.6c14bd7f6f')}
          </Button>
          <Button disabled={!selectedModel} onClick={sendToFluent}>
            {t('legacy.i18n.one_click_fill_to_fluentread.dc147b69cd')}
          </Button>
        </div>
      }
    />
  );
}
