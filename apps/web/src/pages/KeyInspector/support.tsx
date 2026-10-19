import dayjs from 'dayjs';
import { Boxes, Clock, KeyRound, Layers } from 'lucide-react';
import { z } from 'zod';
import { Badge, DataCard, DataSurface, Tag } from '../../components/ui';
import { formatUnixTime } from '../../components/ui/table-tools';
import type { TFunction } from 'i18next';
import type { LucideIcon } from 'lucide-react';
import type { ComponentProps, ReactNode } from 'react';
import type { DateRange } from 'react-day-picker';

export type UsageRow = {
  amount_microusd?: number;
  completion_tokens?: number;
  content?: string;
  created_at?: number;
  id?: number | string;
  last_used_at?: number;
  model_name?: string;
  prompt_tokens?: number;
  request_count?: number;
  request_id?: string;
  use_time?: number;
};

export type InspectorToken = {
  expires_at?: number;
  group?: string;
  masked_key?: string;
  model_limits?: Record<string, unknown>;
  model_limits_enabled?: boolean;
  name?: string;
  total_available?: number;
  total_quota?: number;
  total_used?: number;
  unlimited_quota?: boolean;
};

export type InspectorLogs = {
  filters?: { log_keyword?: string; log_model_name?: string };
  items?: UsageRow[];
  page?: number;
  page_size?: number;
  total?: number;
};

export type InspectorResult = {
  by_model?: UsageRow[];
  logs?: InspectorLogs;
  summary?: { request_count?: number; total_tokens?: number };
  token?: InspectorToken;
};

export type InspectorFormValues = {
  apiKey: string;
  dateRange: unknown;
};

export type ChartDatum = Record<string, unknown>;

const chartColors: string[] = [
  'rgba(94, 121, 207, 1)',
  'rgba(94, 121, 207, 0.78)',
  'rgba(94, 121, 207, 0.56)',
  'rgba(94, 121, 207, 0.38)',
  'rgba(71, 75, 84, 0.72)',
  'rgba(71, 75, 84, 0.56)',
  'rgba(71, 75, 84, 0.4)',
  'rgba(94, 121, 207, 0.68)',
  'rgba(94, 121, 207, 0.48)',
  'rgba(94, 121, 207, 0.3)',
];

export const inspectorSchema = z.object({
  apiKey: z.string().trim().min(1),
  dateRange: z.unknown(),
});

export const createDefaultRange = () => ({
  from: dayjs().subtract(7, 'day').startOf('day').toDate(),
  to: dayjs().endOf('day').toDate(),
});

export const toRange = (value: unknown): DateRange => {
  if (Array.isArray(value)) {
    return {
      from: value[0] instanceof Date ? value[0] : undefined,
      to: value[1] instanceof Date ? value[1] : undefined,
    };
  }
  if (value && typeof value === 'object') {
    const range = value as { from?: unknown; to?: unknown };
    return {
      from: range.from instanceof Date ? range.from : undefined,
      to: range.to instanceof Date ? range.to : undefined,
    };
  }
  return { from: undefined };
};

export const buildColorMap = (models: string[] = []) =>
  models.reduce<Record<string, string>>((acc, model, index) => {
    acc[model] = chartColors[index % chartColors.length];
    return acc;
  }, {});

export const applyLocalLogFilters = (
  items: UsageRow[] = [],
  modelName: unknown = '',
  keyword: unknown = '',
) => {
  const normalizedModel = String(modelName || '').trim();
  const normalizedKeyword = String(keyword || '')
    .trim()
    .toLowerCase();
  return items.filter((item) => {
    const model = String(item?.model_name || '');
    if (normalizedModel && model !== normalizedModel) {
      return false;
    }
    if (!normalizedKeyword) {
      return true;
    }
    return (
      model.toLowerCase().includes(normalizedKeyword) ||
      String(item?.content || '')
        .toLowerCase()
        .includes(normalizedKeyword) ||
      String(item?.request_id || '')
        .toLowerCase()
        .includes(normalizedKeyword)
    );
  });
};

export function StatCard({
  icon: Icon,
  label,
  value,
  subtitle,
  variant = 'neutral',
  progress,
}: {
  icon: LucideIcon;
  label: ReactNode;
  progress?: number;
  subtitle?: ReactNode;
  value: ReactNode;
  variant?: ComponentProps<typeof Badge>['variant'];
}) {
  const hasProgress = typeof progress === 'number';
  return (
    <DataCard>
      <div className='flex items-start gap-3'>
        <div className='rounded border border-[var(--ui-border)] bg-[var(--ui-bg-muted)] p-2 text-[var(--ui-text)]'>
          <Icon size={18} />
        </div>
        <div className='min-w-0 flex-1'>
          <div className='text-xs font-medium text-[var(--ui-text-tertiary)]'>
            {label}
          </div>
          <div className='mt-1 text-xl font-semibold text-[var(--ui-text)]'>
            {value}
          </div>
          {hasProgress ? (
            <div className='mt-2 h-1.5 w-full overflow-hidden rounded-full bg-[var(--ui-bg-muted)]'>
              <div
                className='h-full rounded-full bg-[var(--lazu-ink)]'
                style={{
                  width: `${Math.min(100, Math.max(0, progress))}%`,
                }}
              />
            </div>
          ) : null}
          {subtitle ? (
            <Badge className='mt-2' variant={variant}>
              {subtitle}
            </Badge>
          ) : null}
        </div>
      </div>
    </DataCard>
  );
}

function IdentityField({
  icon: Icon,
  label,
  children,
}: {
  children: ReactNode;
  icon: LucideIcon;
  label: ReactNode;
}) {
  return (
    <div className='flex flex-col gap-1.5'>
      <div className='flex items-center gap-1.5 text-xs font-medium text-[var(--ui-text-tertiary)]'>
        <Icon size={13} />
        {label}
      </div>
      <div className='text-sm text-[var(--ui-text)]'>{children}</div>
    </div>
  );
}

export function KeyIdentityCard({
  token,
  t,
}: {
  t: TFunction;
  token: InspectorToken;
}) {
  const modelLimitsEnabled = Boolean(token.model_limits_enabled);
  const allowedModels = modelLimitsEnabled
    ? Object.keys(token.model_limits || {})
    : [];
  const now = dayjs().unix();
  const expired = Boolean(token.expires_at) && Number(token.expires_at) < now;
  const depleted =
    !token.unlimited_quota && Number(token.total_available || 0) <= 0;
  const statusLabel = expired
    ? t('page.keyInspector.status.expired', { defaultValue: '已过期' })
    : depleted
      ? t('page.keyInspector.status.depleted', { defaultValue: '额度耗尽' })
      : t('page.keyInspector.status.active', { defaultValue: '正常' });
  const statusVariant = expired || depleted ? 'warning' : 'success';

  return (
    <DataSurface
      title={t('page.keyInspector.identityTitle', { defaultValue: 'Key 信息' })}
      actions={<Badge variant={statusVariant}>{statusLabel}</Badge>}
    >
      <div className='mb-4 flex flex-wrap items-center gap-2'>
        <div className='flex items-center gap-2 text-sm font-semibold text-[var(--ui-text)]'>
          <KeyRound size={16} />
          {token.name || t('legacy.i18n.unnamed_key.888ea520a8')}
        </div>
        <Tag>{token.masked_key || '-'}</Tag>
      </div>
      <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
        <IdentityField
          icon={Layers}
          label={t('page.keyInspector.group', { defaultValue: '分组' })}
        >
          <Tag>{token.group || 'default'}</Tag>
        </IdentityField>
        <IdentityField
          icon={Clock}
          label={t('legacy.i18n.expired.7cf7bfff3c', {
            defaultValue: '有效期',
          })}
        >
          {token.expires_at
            ? formatUnixTime(token.expires_at)
            : t('page.keyInspector.neverExpires', {
                defaultValue: '永不过期',
              })}
        </IdentityField>
        <IdentityField
          icon={Boxes}
          label={t('page.keyInspector.allowedModels', {
            defaultValue: '可调用模型',
          })}
        >
          {!modelLimitsEnabled ? (
            <Badge variant='info'>
              {t('page.keyInspector.allModels', {
                defaultValue: '不限 · 全部模型',
              })}
            </Badge>
          ) : allowedModels.length ? (
            <div className='flex flex-wrap gap-1'>
              {allowedModels.map((model) => (
                <Tag key={model}>{model}</Tag>
              ))}
            </div>
          ) : (
            <span className='text-[var(--ui-text-tertiary)]'>
              {t('page.keyInspector.noModels', {
                defaultValue: '无可用模型',
              })}
            </span>
          )}
        </IdentityField>
      </div>
    </DataSurface>
  );
}
