import { zodResolver } from '@hookform/resolvers/zod';
import { Search, SlidersHorizontal, X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { useDebouncedCallback } from 'use-debounce';
import { z } from 'zod';
import { SessionLogCell, SessionReceiptBlock } from './sessions/SessionReceipt';
import { Button } from '../../components/ui/button';
import { DateRangePicker } from '../../components/ui/date-picker';
import { submitFilterFormFromSelect } from '../../components/ui/filter-bar';
import { Input } from '../../components/ui/input';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '../../components/ui/popover';
import { Select, SelectItem } from '../../components/ui/select';
import { compactNumber } from '../../components/ui/table-tools';
import { ReasonChip } from '../../features/projects/components/atoms';
import {
  isProjectRejection,
  rejectionEffectKey,
  rejectionLabelKey,
} from '../../features/projects/rejections';
import { API } from '../../helpers/api';
import { asMicroUSD } from '../../helpers/money';
import { renderQuota } from '../../helpers/render';
import {
  LOG_COLUMN_KEYS,
  getDefaultUsageLogFilters,
  usageDashboardScopeParam,
  useUsageLogsQuery,
} from '../../hooks/usage-logs/useUsageLogsQuery';
import { LazuApiError, readApiError } from '../../lib/api-error';
import './log-table.css';
import type { UsageLogFilters } from '../../hooks/usage-logs/useUsageLogsQuery';
import type { TFunction } from 'i18next';
import type { MouseEvent, ReactNode } from 'react';

const filterSchema = z.object({
  channel: z.string(),
  dateRange: z.custom<UsageLogFilters['dateRange']>(),
  group: z.string(),
  logType: z.string(),
  model_name: z.string(),
  token_name: z.string(),
  user_id: z.string(),
  username: z.string(),
});

type FilterValues = z.infer<typeof filterSchema>;
type ExportFilters = FilterValues;
type DateRangeRecord = {
  from?: unknown;
  to?: unknown;
};
export type UsageRequestRecord = Record<string, unknown>;
type UsageLogsState = ReturnType<typeof useUsageLogsQuery>;
export type LogRecord = UsageLogsState['usageLogs'][number];
export type LogBadgeVariant =
  | 'danger'
  | 'info'
  | 'neutral'
  | 'success'
  | 'warning';
type BillingLineItem = Record<string, unknown> & {
  amount_microusd?: unknown;
  dimension?: unknown;
  quantity?: unknown;
  unit_microusd?: unknown;
};
type QuoteBreakdown = Record<string, unknown> & {
  /** Set when the request's input passed the model's long-context
   *  threshold and the whole request was billed at the long-context price. */
  context_tier?: { input_tokens?: number; threshold?: number };
  discount?: Record<string, unknown>;
  line_items?: BillingLineItem[];
  /** The same usage at the vendor's list price, recorded at billing time. */
  official?: { total_microusd?: number };
  /** Set when the request was priced inside the model's peak hours. */
  peak?: { multiplier?: number };
  subtotal_microusd?: unknown;
  total_microusd?: unknown;
};

const isDateRangeRecord = (value: unknown): value is DateRangeRecord =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const isUnknownRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const toDateRangePickerValue = (
  value: UsageLogFilters['dateRange'],
): [Date | number | string | undefined, Date | number | string | undefined] =>
  Array.isArray(value) ? value : [value.from, value.to];

export const requestIdentity = (value: unknown) => {
  if (!value || typeof value !== 'object') return '';
  const record = value as UsageRequestRecord;
  return String(record.request_id || record.id || record.key || '');
};

const toExportDate = (value: unknown) => {
  if (value instanceof Date) return value;
  if (value === undefined || value === null || value === '') return new Date();
  if (typeof value !== 'string' && typeof value !== 'number') {
    return new Date();
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? new Date() : date;
};

const getExportWindow = (filters: ExportFilters) => {
  const range = filters.dateRange;
  const from = Array.isArray(range)
    ? range[0]
    : isDateRangeRecord(range)
      ? range.from
      : undefined;
  const to = Array.isArray(range)
    ? range[1]
    : isDateRangeRecord(range)
      ? range.to
      : undefined;
  const start = toExportDate(from);
  const end = toExportDate(to);
  start.setHours(0, 0, 0, 0);
  end.setHours(23, 59, 59, 999);
  return {
    endTimestamp: Math.ceil(end.getTime() / 1000),
    startTimestamp: Math.floor(start.getTime() / 1000),
  };
};

/** A download requested as a blob also receives its failure as a Blob.
 *  Read the error object out of it, so the reader gets the copy for its code
 *  instead of a generic sentence. Anything unreadable is returned as is. */
export const readBlobDownloadError = async (
  error: unknown,
): Promise<unknown> => {
  const response =
    error && typeof error === 'object' && 'response' in error
      ? (error as { response?: Record<string, unknown> }).response
      : undefined;
  const data = response?.data;
  if (!(data instanceof Blob)) return error;
  try {
    const body: unknown = JSON.parse(await data.text());
    const info = readApiError({ response: { ...response, data: body } });
    return info.code ? new LazuApiError(info) : error;
  } catch {
    return error;
  }
};

export const downloadUsageLogs = async (
  filters: ExportFilters,
  format: 'csv' | 'xlsx',
  admin: boolean,
) => {
  const window = getExportWindow(filters);
  const response = await API.get('/api/data/export', {
    params: {
      channel: admin ? filters.channel : undefined,
      dataset: 'logs',
      end_timestamp: window.endTimestamp,
      format,
      group: filters.group,
      model_name: filters.model_name,
      // Without an explicit scope the export falls back to the session role,
      // so a self-scoped operator used to download every user's rows.
      scope: usageDashboardScopeParam(admin),
      start_timestamp: window.startTimestamp,
      token_name: filters.token_name,
      type: filters.logType || '0',
      user_id: admin ? filters.user_id : undefined,
      username: admin ? filters.username : undefined,
    },
    responseType: 'blob',
    skipErrorHandler: true,
  });
  const blob = new Blob([response.data]);
  const href = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = href;
  link.download = `usage-logs-${window.startTimestamp}-${window.endTimestamp}.${format}`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(href);
};

const logTypeLabel = (
  type: unknown,
  t: TFunction,
): { label: string; variant: LogBadgeVariant } => {
  if (type === 1) {
    return { label: t('legacy.i18n.top_up.fa03c46253'), variant: 'info' };
  }
  if (type === 2) {
    return { label: t('legacy.i18n.consume.71d6df4130'), variant: 'success' };
  }
  if (type === 3) {
    return { label: t('legacy.i18n.manage.4989b5cf94'), variant: 'warning' };
  }
  if (type === 4) {
    return { label: t('legacy.i18n.system.1a1f6dff78'), variant: 'neutral' };
  }
  if (type === 5) {
    return { label: t('legacy.i18n.error.b859c7be75'), variant: 'danger' };
  }
  if (type === 6) {
    return { label: t('legacy.i18n.refund.b82ef83b7f'), variant: 'danger' };
  }
  return { label: t('legacy.i18n.unknown.d9c32a4c3d'), variant: 'neutral' };
};

/** Amounts on the log read to four decimals ("$0.0213"); a sub-tenth-of-a-
 *  cent request keeps six so it never rounds up to a cost it did not have. */
export const formatCost = (microusd: unknown) => {
  const value = Number(microusd) || 0;
  if (value === 0) return '$0';
  if (Math.abs(value) < 1000) return renderQuota(asMicroUSD(value), 6);
  const usd = (Math.abs(value) / 1_000_000).toFixed(4);
  return `${value < 0 ? '-' : ''}$${usd}`;
};

/** "$1.25", "$10.00", "$0.125": at least cents, more only when the price has them. */
const formatUnitPrice = (microusd: number) => {
  const usd = microusd / 1_000_000;
  const [whole, fraction = ''] = usd.toFixed(6).split('.');
  const trimmed = fraction.replace(/0+$/, '');
  return `$${whole}.${trimmed.padEnd(2, '0')}`;
};

/** "2026-09-29 14:32:08" → "09-29 14:32:08" within the current year; the
 *  full stamp stays in the detail and the cell's title. */
const shortTimestamp = (value: unknown) => {
  const text = String(value ?? '');
  const year = String(new Date().getFullYear());
  return text.startsWith(`${year}-`) ? text.slice(year.length + 1) : text;
};

/** "折扣 6 折" / "40% off". The rule's source is an internal account key
 *  (account_1000015) and never reaches the customer. */
const discountLabel = (pct: unknown, t: TFunction) => {
  const off = Number(pct);
  if (!Number.isFinite(off) || off <= 0) {
    return t('page.console.log.detail.discount');
  }
  return t('page.console.log.m3.discountRate', {
    off: Number(off.toFixed(2)),
    rate: Number(((100 - off) / 10).toFixed(2)),
  });
};

export const EMPTY_CELL = '—';

export const NUMERIC_LOG_COLUMNS = new Set<string>([
  LOG_COLUMN_KEYS.PROMPT,
  LOG_COLUMN_KEYS.COMPLETION,
  LOG_COLUMN_KEYS.COST,
  LOG_COLUMN_KEYS.USE_TIME,
  LOG_COLUMN_KEYS.RETRY,
]);

const userInitials = (name: unknown) => {
  const value = String(name ?? '').trim();
  if (!value) return '?';
  const parts = value.split(/[\s_.@-]+/).filter(Boolean);
  if (parts.length > 1) {
    return `${parts[0][0]}${parts[1][0]}`.toUpperCase();
  }
  return value.slice(0, 2).toUpperCase();
};

export function UserIdentity({
  name,
  onSelect,
  uid,
}: {
  name: string;
  onSelect?: (event: MouseEvent<HTMLButtonElement>) => void;
  uid?: number | string | null;
}) {
  const idLabel =
    uid === null || uid === undefined || uid === '' || Number(uid) === 0
      ? ''
      : `#${uid}`;
  return (
    <span className='logv2-who'>
      <span aria-hidden='true' className='logv2-avatar'>
        {userInitials(name)}
      </span>
      <span className='logv2-who__copy'>
        {onSelect ? (
          <button
            type='button'
            className='logv2-link logv2-who__name'
            onClick={onSelect}
          >
            {name}
          </button>
        ) : (
          <span className='logv2-who__name'>{name}</span>
        )}
        {idLabel ? <span className='logv2-who__uid'>{idLabel}</span> : null}
      </span>
    </span>
  );
}

const logChipClass = (variant: LogBadgeVariant) => {
  if (variant === 'success') return 'logv2-chip logv2-chip--ok';
  if (variant === 'danger') return 'logv2-chip logv2-chip--bad';
  if (variant === 'warning') return 'logv2-chip logv2-chip--warn';
  return 'logv2-chip';
};

const errorUsageUnknown = (record: LogRecord) =>
  Number(record.type) === 5 && record.otherParsed?.usage_unavailable !== false;
const recordedSeconds = (record: LogRecord): number | null => {
  const ms = record.otherParsed?.duration_ms;
  if (typeof ms === 'number' && Number.isFinite(ms)) return ms / 1000;
  if (Number(record.type) === 5 && !record.use_time) return null;
  return Number(record.use_time) || 0;
};

/** Time to the first output token: ttft_ms since 2026-10, before that the
 *  first frame (frt), which older rows store as -1000 when nothing streamed. */
const firstTokenMsOf = (
  other: { frt?: unknown; ttft_ms?: unknown } | undefined,
) => Math.max(Number(other?.ttft_ms) || Number(other?.frt) || 0, 0);

/** "6.8s", with the first token when the response streamed: "6.8s · 0.9s". */
const latencyLabel = (record: LogRecord) => {
  const duration = recordedSeconds(record);
  if (duration === null) return EMPTY_CELL;
  const total = formatSeconds(duration);
  const firstToken = firstTokenMsOf(record.otherParsed);
  if (!record.is_stream || firstToken <= 0) return total;
  return `${total} · ${formatSeconds(firstToken / 1000)}`;
};

const formatSeconds = (seconds: number) =>
  `${seconds >= 100 ? Math.round(seconds) : seconds.toFixed(1)}s`;

const compactTokenFormat = new Intl.NumberFormat('en', {
  maximumFractionDigits: 1,
  notation: 'compact',
});

/** 18400 → "18.4K". Latin K/M on purpose: 万 would change the column width. */
export const shortTokens = (value: unknown) =>
  compactTokenFormat.format(Number(value) || 0);

const retryCount = (record: LogRecord) => {
  const useChannel = record.otherParsed?.admin_info?.use_channel;
  return Array.isArray(useChannel) ? Math.max(0, useChannel.length - 1) : 0;
};

export type LogLane = 'stable' | 'eco' | 'fallback';

/** The lane a request ran on. The log's `group` is the lane slug; anything
 *  else (an old user-tier group, a top-up row) is not a lane and gets no tag. */
export const logLane = (record: LogRecord | null): LogLane | null => {
  if (!record || ![2, 5].includes(Number(record.type))) return null;
  // A request refused before routing never reached a lane; its group is only
  // the default and would claim a lane it did not use.
  if (Number(record.type) === 5) {
    const other = record.otherParsed;
    const tried =
      other?.any_provider_attempted === true ||
      Number(other?.channel_id) > 0 ||
      (Array.isArray(other?.admin_info?.use_channel) &&
        other.admin_info.use_channel.length > 0);
    if (!tried) return null;
  }
  const group = String(record.group || record.otherParsed?.group || '')
    .trim()
    .toLowerCase();
  if (group === 'cheap') return 'eco';
  if (group !== 'direct') return null;
  // The gateway records the lane a Key asked for only when it had to serve
  // the request on the stable lane instead.
  const requested = String(record.otherParsed?.lane_requested || '');
  return requested && requested !== 'direct' ? 'fallback' : 'stable';
};

export function LaneChip({ lane, t }: { lane: LogLane; t: TFunction }) {
  if (lane === 'fallback') {
    return (
      <span
        className='logv2-lane logv2-lane--fallback'
        title={t('page.console.log.lane.fallbackHint')}
      >
        {t('page.console.log.lane.fallback')}
      </span>
    );
  }
  return (
    <span className={`logv2-lane logv2-lane--${lane}`}>
      <span aria-hidden='true' className='logv2-lane__dot' />
      {t(`page.console.log.lane.${lane}`)}
    </span>
  );
}

/** Consumes and errors read as an outcome (a dot and a word); the other
 *  record kinds keep their type name. */
/** A video or image job not settled yet: its row is written at submission
 *  and completed in place when the job finishes. */
export const isPendingMediaLog = (record: LogRecord | null) =>
  Number(record?.type) === 2 && record?.otherParsed?.pending === true;

export function LogStatus({ record, t }: { record: LogRecord; t: TFunction }) {
  const type = Number(record.type);
  if (isPendingMediaLog(record)) {
    return (
      <span className='logv2-status logv2-status--pending'>
        <span aria-hidden='true' className='logv2-status__dot' />
        {t('page.console.log.status.pending')}
      </span>
    );
  }
  if (type === 2 || type === 5) {
    const failed = type === 5;
    return (
      <span className={`logv2-status${failed ? ' logv2-status--bad' : ''}`}>
        <span aria-hidden='true' className='logv2-status__dot' />
        {failed
          ? t('page.console.log.status.failed')
          : t('page.console.log.status.ok')}
      </span>
    );
  }
  const label = logTypeLabel(record.type, t);
  return <span className={logChipClass(label.variant)}>{label.label}</span>;
}

export type LogCellContext = {
  copyText: (value: unknown) => void | Promise<void>;
  isAdminUser: boolean;
  /** Whether this record's detail is expanded under its row. */
  isOpen?: (record: LogRecord) => boolean;
  onOpen: (record: LogRecord) => void;
  showUserInfo: (userId: number | string | null | undefined) => void;
  t: TFunction;
};

export function renderLogCell(
  columnId: string,
  record: LogRecord,
  ctx: LogCellContext,
): ReactNode {
  const { copyText, isAdminUser, isOpen, onOpen, showUserInfo, t } = ctx;
  const type = Number(record.type);
  const isTokenLog = [0, 2, 5].includes(type);
  const isConsume = [2, 5].includes(type);

  if (columnId === LOG_COLUMN_KEYS.TIME) {
    // The whole row opens the detail by pointer; the time is its keyboard
    // handle, so it says whether the detail below is open.
    return (
      <button
        type='button'
        aria-expanded={isOpen ? isOpen(record) : undefined}
        aria-label={t('page.console.log.detail.toggle', {
          time: String(record.timestamp2string ?? ''),
        })}
        className='logv2-link logv2-time'
        title={String(record.timestamp2string ?? '')}
        onClick={(event: MouseEvent<HTMLButtonElement>) => {
          event.stopPropagation();
          onOpen(record);
        }}
      >
        {shortTimestamp(record.timestamp2string)}
      </button>
    );
  }
  if (columnId === LOG_COLUMN_KEYS.SESSION) {
    return <SessionLogCell record={record} t={t} />;
  }
  if (columnId === LOG_COLUMN_KEYS.TYPE) {
    return <LogStatus record={record} t={t} />;
  }
  if (columnId === LOG_COLUMN_KEYS.REASON) {
    const reason = typeof record.reason === 'string' ? record.reason : '';
    if (!reason) return EMPTY_CELL;
    return <ReasonChip code={reason} compact />;
  }
  if (columnId === LOG_COLUMN_KEYS.MODEL) {
    if (!record.model_name) return EMPTY_CELL;
    // The upstream model is operator information; customers see the model
    // they asked for and the lane it ran on.
    const upstream = isAdminUser
      ? record.otherParsed?.upstream_model_name
      : undefined;
    const lane = logLane(record);
    return (
      <span className='logv2-modelcell'>
        <span
          className='logv2-model'
          title={
            upstream && upstream !== record.model_name
              ? t('page.console.log.upstreamModel', { model: upstream })
              : String(record.model_name)
          }
        >
          {String(record.model_name)}
        </span>
        {lane ? <LaneChip lane={lane} t={t} /> : null}
      </span>
    );
  }
  if (columnId === LOG_COLUMN_KEYS.TOKEN) {
    return record.token_name ? (
      <button
        type='button'
        className='logv2-link'
        title={String(record.token_name)}
        onClick={(event: MouseEvent<HTMLButtonElement>) => {
          event.stopPropagation();
          copyText(record.token_name);
        }}
      >
        {String(record.token_name)}
      </button>
    ) : (
      EMPTY_CELL
    );
  }
  if (columnId === LOG_COLUMN_KEYS.CHANNEL) {
    if (!isAdminUser || !isTokenLog) return EMPTY_CELL;
    // channel_id stays operator-internal (json:"-"); admin listings resolve
    // the readable channel_name server side.
    return (
      <span
        className='logv2-soft'
        title={record.channel_name ? String(record.channel_name) : undefined}
      >
        {record.channel_name || t('page.console.log.detail.unknownChannel')}
      </span>
    );
  }
  if (columnId === LOG_COLUMN_KEYS.PROMPT) {
    if (isPendingMediaLog(record)) return EMPTY_CELL;
    return isTokenLog && !errorUsageUnknown(record)
      ? `${shortTokens(record.prompt_tokens)} → ${shortTokens(record.completion_tokens)}`
      : EMPTY_CELL;
  }
  if (columnId === LOG_COLUMN_KEYS.COMPLETION) {
    return isTokenLog && !errorUsageUnknown(record)
      ? compactNumber(record.completion_tokens || 0)
      : EMPTY_CELL;
  }
  if (columnId === LOG_COLUMN_KEYS.COST) {
    if (isPendingMediaLog(record)) {
      return (
        <span className='logv2-soft'>
          {t('page.console.log.cost.held', {
            amount: formatCost(Number(record.otherParsed?.hold_microusd) || 0),
          })}
        </span>
      );
    }
    if (record.isRefund) {
      return (
        <span className='logv2-chip logv2-chip--bad'>
          {record.refundCostDisplay ||
            formatCost(-Math.abs(record.amount_microusd || 0))}
        </span>
      );
    }
    if (type === 1) {
      return (
        <span className='logv2-credit'>
          +{formatCost(record.amount_microusd)}
        </span>
      );
    }
    const tier = logContextTier(record);
    const onPlan = Boolean(parseQuoteBreakdown(record)?.discount?.plan);
    return tier || onPlan ? (
      <span className='logv2-cost'>
        {formatCost(record.amount_microusd)}
        {onPlan ? (
          <span className='logv2-chip logv2-chip--plan'>
            {t('page.console.log.m3.planTag', { defaultValue: '专属' })}
          </span>
        ) : null}
        {tier ? (
          <span className='logv2-chip logv2-chip--tier'>
            {t('page.console.log.contextTier.tag', {
              defaultValue: '长上下文',
            })}
          </span>
        ) : null}
      </span>
    ) : (
      formatCost(record.amount_microusd)
    );
  }
  if (columnId === LOG_COLUMN_KEYS.USE_TIME) {
    if (isPendingMediaLog(record)) {
      const waited = Math.max(
        0,
        Math.floor(Date.now() / 1000) - Number(record.created_at || 0),
      );
      return (
        <span className='logv2-soft'>
          {t('page.console.log.pendingFor', {
            time: `${Math.floor(waited / 60)}:${String(waited % 60).padStart(2, '0')}`,
          })}
        </span>
      );
    }
    return isConsume && !errorUsageUnknown(record) ? (
      <span className='logv2-soft'>{latencyLabel(record)}</span>
    ) : (
      EMPTY_CELL
    );
  }
  if (columnId === LOG_COLUMN_KEYS.RETRY) {
    if (!isAdminUser || !isTokenLog) return EMPTY_CELL;
    const count = retryCount(record);
    const useChannel = record.otherParsed?.admin_info?.use_channel;
    return (
      <span
        className={count > 0 ? undefined : 'logv2-faint'}
        title={Array.isArray(useChannel) ? useChannel.join(' → ') : undefined}
      >
        {count}
      </span>
    );
  }
  if (columnId === LOG_COLUMN_KEYS.GROUP) {
    const group = record.group || record.otherParsed?.group;
    return group ? (
      <span className='logv2-soft'>{String(group)}</span>
    ) : (
      EMPTY_CELL
    );
  }
  if (columnId === LOG_COLUMN_KEYS.IP) {
    return record.ip ? (
      <button
        type='button'
        className='logv2-link logv2-model'
        onClick={(event: MouseEvent<HTMLButtonElement>) => {
          event.stopPropagation();
          copyText(record.ip);
        }}
      >
        {String(record.ip)}
      </button>
    ) : (
      EMPTY_CELL
    );
  }
  if (columnId === LOG_COLUMN_KEYS.USERNAME) {
    if (!isAdminUser || !record.username) return EMPTY_CELL;
    return (
      <UserIdentity
        name={String(record.username)}
        uid={record.user_id}
        onSelect={(event) => {
          event.stopPropagation();
          showUserInfo(record.user_id);
        }}
      />
    );
  }
  return EMPTY_CELL;
}

const parseQuoteBreakdown = (record: LogRecord): QuoteBreakdown | null => {
  const raw = record?.line_items;
  if (!raw) return null;
  if (typeof raw === 'object' && raw !== null) {
    return raw as QuoteBreakdown;
  }
  try {
    const parsed: unknown = JSON.parse(String(raw));
    return typeof parsed === 'object' && parsed !== null
      ? (parsed as QuoteBreakdown)
      : null;
  } catch {
    return null;
  }
};

/** The long-context tier a consume record was billed at, if any. */
export const logContextTier = (record: LogRecord | null) => {
  const tier = record ? parseQuoteBreakdown(record)?.context_tier : undefined;
  const threshold = Number(tier?.threshold) || 0;
  if (threshold <= 0) return null;
  return { inputTokens: Number(tier?.input_tokens) || 0, threshold };
};

/** "272K", "1M". */
const formatTokenThreshold = (tokens: number) =>
  tokens >= 1_000_000 && tokens % 100_000 === 0
    ? `${tokens / 1_000_000}M`
    : `${Math.round(tokens / 1000)}K`;

const lineItemLabel = (dimension: unknown, t: TFunction) => {
  const labels: Record<string, string> = {
    audio_input: t('page.console.log.detail.audioInput'),
    audio_output: t('page.console.log.detail.audioOutput'),
    cache_read: t('page.console.log.detail.cacheRead'),
    cache_write: t('page.console.log.detail.cacheWrite'),
    cache_write_5m: t('page.console.log.detail.cacheWrite5m'),
    cache_write_1h: t('page.console.log.detail.cacheWrite1h'),
    image_input: t('page.console.log.detail.imageInput'),
    image_output: t('page.console.log.detail.imageOutput'),
    input: t('page.console.log.detail.input'),
    output: t('page.console.log.detail.output'),
    request: t('page.console.log.detail.perCall'),
    web_search: t('page.console.log.detail.webSearch'),
    claude_web_search: t('page.console.log.detail.webSearch'),
    file_search: t('page.console.log.detail.fileSearch'),
    image_generation_call: t('page.console.log.detail.imageCall'),
    generated_image: t('pricing.images.generatedImage'),
    generated_image_high_resolution: t('pricing.images.highResolutionImage'),
    image_layer: t('pricing.images.imageLayer'),
    image_layer_high_resolution: t('pricing.images.highResolutionLayer'),
    image_reference: t('pricing.images.imageReference'),

    audio_input_separate: t('page.console.log.detail.audioInput'),
    minimum_charge: t('page.console.log.detail.minimumCharge'),
    second: t('page.console.log.detail.duration'),
  };
  return labels[String(dimension)] || String(dimension || '-');
};

export function CardSection({
  title,
  action,
  children,
}: {
  action?: ReactNode;
  children?: ReactNode;
  title: ReactNode;
}) {
  return (
    <section className='overflow-hidden rounded-[var(--ui-radius-lg)] border border-[var(--ui-border)] bg-[var(--ui-bg-raised)]'>
      <header className='flex items-center justify-between gap-3 border-b border-[var(--ui-border)] px-4 py-2.5'>
        <span className='text-sm font-semibold text-[var(--ui-text)]'>
          {title}
        </span>
        {action}
      </header>
      <div className='px-4 py-3'>{children}</div>
    </section>
  );
}

type LedgerTone =
  | 'input'
  | 'cacheRead'
  | 'cacheWrite'
  | 'cacheWrite1h'
  | 'output'
  | 'image'
  | 'audio'
  | 'tool'
  | 'min';

type LedgerRow = {
  amountMicrousd: number;
  dimension: string;
  label: string;
  /** "96,512", "3 次"; empty when the line has only an amount. */
  quantity: string;
  tone: LedgerTone;
  /** "$2.00/M", "$10.00/千次"; empty when the line has only an amount. */
  unit: string;
};

/* Reading order of a bill: what went in, what came out, then the per-call
   extras and the minimum charge. */
const LEDGER_ORDER: Record<string, number> = {
  input: 0,
  image_input: 1,
  cache_read: 2,
  cache_write: 3,
  cache_write_5m: 3,
  cache_write_1h: 4,
  audio_input: 5,
  output: 6,
  image_output: 7,
  audio_output: 8,
  minimum_charge: 99,
};

const LEDGER_TONE: Record<string, LedgerTone> = {
  input: 'input',
  output: 'output',
  cache_read: 'cacheRead',
  cache_write: 'cacheWrite',
  cache_write_5m: 'cacheWrite',
  cache_write_1h: 'cacheWrite1h',
  image_input: 'image',
  image_output: 'image',
  audio_input: 'audio',
  audio_output: 'audio',
  minimum_charge: 'min',
};

const TOKEN_LINE_DIMENSIONS = new Set(
  Object.keys(LEDGER_TONE).filter((key) => key !== 'minimum_charge'),
);

const usdToMicro = (usd: unknown) => Math.round((Number(usd) || 0) * 1e6);

/** Every line of a consume record's bill, with the quantity and unit price a
 *  reader can multiply by hand. Per-call tools keep only their total in the
 *  line item, so their count and price come from the request record. */
function buildLedgerRows(record: LogRecord, t: TFunction): LedgerRow[] {
  const quote = parseQuoteBreakdown(record);
  const other = record.otherParsed || {};
  const lineItems = Array.isArray(quote?.line_items) ? quote.line_items : [];
  const perThousand = (count: unknown, priceUSD: unknown) => ({
    quantity:
      Number(count) > 0 ? t('page.console.log.m3.times', { n: count }) : '',
    unit:
      Number(priceUSD) > 0
        ? `${formatUnitPrice(usdToMicro(priceUSD))}${t('page.console.log.m3.perThousand')}`
        : '',
  });
  return lineItems
    .map((item): LedgerRow => {
      const dimension = String(item?.dimension || '');
      const quantity = Number(item?.quantity) || 0;
      const unitMicrousd = Number(item?.unit_microusd) || 0;
      let shown = { quantity: '', unit: '' };
      if (TOKEN_LINE_DIMENSIONS.has(dimension)) {
        shown = {
          quantity: quantity.toLocaleString(),
          unit: `${formatUnitPrice(unitMicrousd)}/M`,
        };
      } else if (dimension === 'request') {
        shown = {
          quantity: t('page.console.log.m3.times', { n: quantity }),
          unit: `${formatUnitPrice(unitMicrousd)}${t('page.console.log.m3.perCall')}`,
        };
      } else if (
        [
          'generated_image',
          'generated_image_high_resolution',
          'image_layer',
          'image_layer_high_resolution',
          'image_reference',
        ].includes(dimension)
      ) {
        shown = {
          quantity: t('pricing.images.imagesQty', { n: quantity }),
          unit: `${formatUnitPrice(unitMicrousd)}${t('pricing.images.perImage')}`,
        };
      } else if (dimension === 'second') {
        shown = {
          quantity: t('page.console.log.m3.secondsQty', { n: quantity }),
          unit: `${formatUnitPrice(unitMicrousd)}${t('page.console.log.m3.perSecond')}`,
        };
      } else if (
        dimension === 'web_search' ||
        dimension === 'claude_web_search'
      ) {
        shown = perThousand(
          other.web_search_call_count,
          other.web_search_price,
        );
      } else if (dimension === 'file_search') {
        shown = perThousand(
          other.file_search_call_count,
          other.file_search_price,
        );
      } else if (dimension === 'image_generation_call') {
        const price = Number(other.image_generation_call_price) || 0;
        shown = {
          quantity: t('page.console.log.m3.times', { n: 1 }),
          unit:
            price > 0
              ? `${formatUnitPrice(usdToMicro(price))}${t('page.console.log.m3.perCall')}`
              : '',
        };
      }
      return {
        amountMicrousd: Number(item?.amount_microusd) || 0,
        dimension,
        label: ledgerLabel(dimension, t),
        tone: LEDGER_TONE[dimension] ?? 'tool',
        ...shown,
      };
    })
    .sort(
      (a, b) =>
        (LEDGER_ORDER[a.dimension] ?? 50) - (LEDGER_ORDER[b.dimension] ?? 50),
    );
}

const ledgerLabel = (dimension: string, t: TFunction) => {
  if (dimension === 'cache_write_5m')
    return t('page.console.log.m3.cacheWrite5m');
  if (dimension === 'cache_write_1h')
    return t('page.console.log.m3.cacheWrite1h');
  if (dimension === 'second') return t('page.console.log.m3.perSecondLine');
  return lineItemLabel(dimension, t);
};

/** What the same usage would have cost at the vendor's list price, recorded
 *  when the request was billed. Shown only when it is a real saving: no
 *  "省 0%", never a negative number. */
const officialSaving = (record: LogRecord) => {
  if (record.isRefund) return null;
  const quote = parseQuoteBreakdown(record);
  const minimumCharge = (quote?.line_items ?? []).some(
    (item) => String(item?.dimension) === 'minimum_charge',
  );
  if (minimumCharge) return null;
  const official = Number(quote?.official?.total_microusd) || 0;
  const charged = Number(record.amount_microusd) || 0;
  if (official <= 0 || charged <= 0) return null;
  // More than 20× the charge is not a discount anyone sells; it is a
  // catalogue price in the wrong unit, and the page must not advertise it.
  if (official > charged * 20) return null;
  const saved = official - charged;
  const pct = Math.round((saved / official) * 100);
  if (pct < 1) return null;
  return { official, pct, saved };
};

/** The bill as a column sum: 数量 × 单价 = 金额 per line, then the subtotal,
 *  the whole-order discount or surcharge, and what was charged. */
function PriceLedger({
  record,
  rows,
  t,
}: {
  record: LogRecord;
  rows: LedgerRow[];
  t: TFunction;
}) {
  const quote = parseQuoteBreakdown(record);
  const tier = logContextTier(record);
  const peak = Number(quote?.peak?.multiplier) || 0;
  const pct = Number(quote?.discount?.pct) || 0;
  const adjustMicrousd = Number(quote?.discount?.amount_microusd) || 0;
  // A 专属折扣 is taken off the official price, so its bill reads from there:
  // 官方价, the discount, what was charged. Without an official price to show
  // it falls back to the lane subtotal like any other discount.
  const onPlan = Boolean(quote?.discount?.plan);
  const officialTotal = Number(quote?.official?.total_microusd) || 0;
  const planBase =
    onPlan && officialTotal > Number(record.amount_microusd)
      ? officialTotal
      : 0;
  const subtotal =
    Number(quote?.subtotal_microusd) ||
    rows.reduce((sum, row) => sum + row.amountMicrousd, 0);
  const tagsFor = (row: LedgerRow) => [
    ...(tier && TOKEN_LINE_DIMENSIONS.has(row.dimension)
      ? [t('page.console.log.m3.tagLongContext')]
      : []),
    ...(peak > 1 && row.tone !== 'min'
      ? [t('page.console.log.m3.tagPeak', { x: Number(peak.toFixed(2)) })]
      : []),
  ];
  return (
    <div className='logv2-ledger'>
      <div className='logv2-ledger__row logv2-ledger__row--head'>
        <span />
        <span>{t('page.console.log.m3.qty')}</span>
        <span>{t('page.console.log.m3.unitPrice')}</span>
        <span>{t('page.console.log.m3.amount')}</span>
      </div>
      {rows.map((row) => (
        <div
          className='logv2-ledger__row logv2-ledger__row--item'
          key={row.dimension}
        >
          <span className='logv2-ledger__label'>
            <i aria-hidden='true' data-tone={row.tone} />
            {row.label}
            {tagsFor(row).map((tag) => (
              <span className='logv2-ledger__tag' key={tag}>
                {tag}
              </span>
            ))}
          </span>
          <span>{row.quantity}</span>
          <span className='logv2-ledger__unit'>{row.unit}</span>
          <span>{formatCost(row.amountMicrousd)}</span>
        </div>
      ))}
      {planBase > 0 ? (
        <>
          <div className='logv2-ledger__rule' />
          <div className='logv2-ledger__row'>
            <span className='logv2-ledger__label logv2-ledger__label--sum'>
              {t('page.console.log.m3.official')}
            </span>
            <span />
            <span />
            <span>{formatCost(planBase)}</span>
          </div>
          <div className='logv2-ledger__row'>
            <span className='logv2-ledger__label logv2-ledger__label--sum'>
              {t('page.console.log.m3.planDiscount')}
            </span>
            <span />
            <span className='logv2-ledger__unit'>
              {t('page.console.log.m3.planRate', {
                off: Number(pct.toFixed(2)),
                rate: Number(((100 - pct) / 10).toFixed(2)),
              })}
            </span>
            <span className='logv2-ledger__off'>
              −{formatCost(planBase - Number(record.amount_microusd))}
            </span>
          </div>
        </>
      ) : adjustMicrousd !== 0 ? (
        <>
          <div className='logv2-ledger__rule' />
          <div className='logv2-ledger__row'>
            <span className='logv2-ledger__label logv2-ledger__label--sum'>
              {t('page.console.log.m3.subtotal')}
            </span>
            <span />
            <span />
            <span>{formatCost(subtotal)}</span>
          </div>
          <div className='logv2-ledger__row'>
            <span className='logv2-ledger__label logv2-ledger__label--sum'>
              {onPlan
                ? t('page.console.log.m3.planDiscount')
                : pct > 0
                  ? discountLabel(pct, t)
                  : t('page.console.log.m3.surcharge', {
                      pct: Number(Math.abs(pct).toFixed(2)),
                    })}
            </span>
            <span />
            <span className='logv2-ledger__unit'>
              {pct > 0 ? '−' : '+'}
              {Number(Math.abs(pct).toFixed(2))}%
            </span>
            <span
              className={
                adjustMicrousd > 0
                  ? 'logv2-ledger__off'
                  : 'logv2-ledger__surcharge'
              }
            >
              {adjustMicrousd > 0 ? '−' : '+'}
              {formatCost(Math.abs(adjustMicrousd))}
            </span>
          </div>
        </>
      ) : null}
      <div className='logv2-ledger__rule logv2-ledger__rule--strong' />
      <div className='logv2-ledger__row logv2-ledger__row--total'>
        <span className='logv2-ledger__label logv2-ledger__label--sum'>
          {t('page.console.log.m3.charged')}
        </span>
        <span />
        <span />
        <span>{formatCost(record.amount_microusd)}</span>
      </div>
      {tier ? (
        <p className='logv2-ledger__foot'>
          {t('page.console.log.m3.footTier', {
            input: tier.inputTokens.toLocaleString(),
            threshold: formatTokenThreshold(tier.threshold),
          })}
        </p>
      ) : null}
      {peak > 1 ? (
        <p className='logv2-ledger__foot'>
          {t('page.console.log.m3.footPeak', { x: Number(peak.toFixed(2)) })}
        </p>
      ) : null}
    </div>
  );
}

function DetailFact({
  label,
  children,
}: {
  children: ReactNode;
  label: ReactNode;
}) {
  return (
    <span className='logv2-m3__fact'>
      {label} <b>{children}</b>
    </span>
  );
}

export function LogDetailPanel({
  record,
  t,
  copyText,
  isAdminUser,
  standalone = false,
  hideSession = false,
}: {
  copyText: (value: unknown) => void | Promise<void>;
  isAdminUser: boolean;
  record: LogRecord | null;
  /** A deep-linked request that is not on the page has no row above it, so
   *  the panel names the request itself. */
  standalone?: boolean;
  /** Inside a session's own page the "which session" block says nothing. */
  hideSession?: boolean;
  t: TFunction;
}) {
  const rows = useMemo(
    () => (record ? buildLedgerRows(record, t) : []),
    [record, t],
  );

  if (!record) return null;

  const other = record.otherParsed || {};
  const type = Number(record.type);
  const isConsume = type === 2;
  const isFailed = type === 5;
  const isTokenLog = [0, 2, 5].includes(type);
  const lane = logLane(record);
  const quote = parseQuoteBreakdown(record);
  const saving = officialSaving(record);

  const duration = recordedSeconds(record);
  const firstTokenMs = firstTokenMsOf(other);
  const isStream = Boolean(record.is_stream);
  const outputTokens = Number(record.completion_tokens) || 0;
  const showTiming =
    isConsume &&
    duration !== null &&
    duration > 0 &&
    !errorUsageUnknown(record);
  const streamed =
    isStream && firstTokenMs > 0 && firstTokenMs / 1000 < (duration ?? 0);
  const firstFraction = streamed
    ? Math.min(Math.max(firstTokenMs / 1000 / (duration || 1), 0.08), 0.6)
    : 0;
  const generationSeconds = streamed
    ? (duration ?? 0) - firstTokenMs / 1000
    : (duration ?? 0);
  const tokensPerSecond =
    generationSeconds > 0 && outputTokens > 0
      ? Math.round(outputTokens / generationSeconds)
      : 0;

  const barRows = rows.filter((row) => row.amountMicrousd > 0);
  const barTotal = barRows.reduce((sum, row) => sum + row.amountMicrousd, 0);

  const effortClamped =
    other.effort_clamped === true && other.effort_applied
      ? t('page.console.log.detail.effortClamped', {
          requested: String(other.effort_requested ?? ''),
          applied: String(other.effort_applied),
          source:
            other.effort_cap_source === 'member'
              ? t('page.console.log.detail.effortSourceMember')
              : t('page.console.log.detail.effortSourceKey'),
        })
      : '';
  const reasoning = effortClamped || String(other.reasoning_effort || '');
  const path = String(other.request_path || '');
  const requestId = String(record.request_id || '');
  // A video or image job settles minutes after its request; its own page has
  // the file, the timeline and the hold.
  const mediaJobId = String(other.media_job_id || '');
  const decisionMeta = isUnknownRecord(other.decision) ? other.decision : null;
  const generationId = String(decisionMeta?.generation_request_id || '');
  const parentId = String(other.parent_request_id || '');
  const statusCode = Number(other.status_code) || 0;
  const errorCode = String(other.error_code || other.error_type || '');
  const reason = typeof record.reason === 'string' ? record.reason : '';
  const useChannel = Array.isArray(other?.admin_info?.use_channel)
    ? (other.admin_info.use_channel as unknown[]).map(String)
    : [];
  const upstream = String(other.upstream_model_name || '');
  const routing = isUnknownRecord(other.routing) ? other.routing : null;
  const chargeLabel = record.isRefund
    ? record.refundCostDisplay ||
      formatCost(-Math.abs(record.amount_microusd || 0))
    : formatCost(record.amount_microusd);

  return (
    <div className='logv2-m3'>
      {standalone ? (
        <div className='logv2-m3__standalone'>
          <span className='logv2-time'>{record.timestamp2string}</span>
          <LogStatus record={record} t={t} />
          {record.model_name ? (
            <span className='logv2-modelcell'>
              <span className='logv2-model'>{String(record.model_name)}</span>
              {lane ? <LaneChip lane={lane} t={t} /> : null}
            </span>
          ) : null}
        </div>
      ) : null}

      {isFailed ? (
        <FailureSummary
          amountMicrousd={Number(record.amount_microusd) || 0}
          content={String(record.content || '')}
          reason={reason}
          t={t}
        />
      ) : null}

      {isConsume ? (
        <div className='logv2-m3__main'>
          <div className='logv2-m3__charge'>
            <span className='logv2-m3__eyebrow'>
              {t('page.console.log.m3.charged')}
            </span>
            <b className='logv2-m3__amount'>{chargeLabel}</b>
            {saving ? (
              <>
                <span className='logv2-m3__official'>
                  {t('page.console.log.m3.official')}{' '}
                  <s>{formatCost(saving.official)}</s>
                </span>
                <b className='logv2-m3__saved'>
                  {t('page.console.log.m3.saved', {
                    amount: formatCost(saving.saved),
                    pct: saving.pct,
                  })}
                </b>
              </>
            ) : null}
            {lane === 'fallback' ? (
              <span className='logv2-m3__fallback'>
                {t('page.console.log.lane.fallbackHint')}
              </span>
            ) : null}
          </div>

          <div className='logv2-m3__bars'>
            {showTiming ? (
              <div className='logv2-m3__bar'>
                <span className='logv2-m3__eyebrow'>
                  {t('page.console.log.m3.time')}
                </span>
                <div
                  className='logv2-m3__track'
                  style={{
                    gridTemplateColumns: streamed
                      ? `minmax(64px, ${firstFraction}fr) minmax(0, ${1 - firstFraction}fr)`
                      : 'minmax(0, 1fr)',
                  }}
                >
                  {streamed ? (
                    <div className='logv2-m3__seg'>
                      <i data-tone='wait' />
                      <span>
                        {t('page.console.log.m3.firstToken')}{' '}
                        <b>{formatSeconds(firstTokenMs / 1000)}</b>
                      </span>
                    </div>
                  ) : null}
                  <div className='logv2-m3__seg'>
                    <i data-tone='output' />
                    <span>
                      {streamed
                        ? t('page.console.log.m3.generateShort')
                        : t('page.console.log.m3.notStream')}
                      {tokensPerSecond > 0 ? (
                        <>
                          {' · '}
                          <b>{tokensPerSecond.toLocaleString()} tok/s</b>
                        </>
                      ) : null}
                      {' · '}
                      <b>{formatSeconds(duration ?? 0)}</b>
                    </span>
                  </div>
                </div>
              </div>
            ) : null}

            {barRows.length > 0 ? (
              <div className='logv2-m3__bar'>
                <span className='logv2-m3__eyebrow'>
                  {t('page.console.log.m3.cost')}
                </span>
                <div
                  className='logv2-m3__track'
                  style={{
                    gridTemplateColumns: barRows
                      .map(
                        (row) =>
                          `minmax(0, ${row.amountMicrousd / barTotal}fr)`,
                      )
                      .join(' '),
                  }}
                >
                  {barRows.map((row) => (
                    <div className='logv2-m3__seg' key={row.dimension}>
                      <i data-tone={row.tone} />
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
          </div>

          {rows.length > 0 ? (
            <PriceLedger record={record} rows={rows} t={t} />
          ) : (
            <p className='logv2-m3__note'>
              {t('page.console.log.m3.noCharge')}
            </p>
          )}
        </div>
      ) : null}

      {!isTokenLog && record.content ? (
        <p className='logv2-m3__content'>{String(record.content)}</p>
      ) : null}

      <div className='logv2-m3__facts'>
        {parentId && (
          <Link
            className='logv2-m3__fact'
            to={`/console/usage?scope=${isAdminUser ? 'platform' : 'self'}&request=${encodeURIComponent(parentId)}`}
          >
            {t('page.console.log.decision.parent')}
          </Link>
        )}
        {generationId && (
          <Link
            className='logv2-m3__fact'
            to={`/console/usage?scope=${isAdminUser ? 'platform' : 'self'}&request=${encodeURIComponent(generationId)}`}
          >
            {t('page.console.log.decision.generator')}
          </Link>
        )}
        {isFailed && statusCode > 0 ? (
          <DetailFact label={t('page.console.log.m3.statusCode')}>
            {errorCode ? `${statusCode} · ${errorCode}` : statusCode}
          </DetailFact>
        ) : null}
        {reasoning ? (
          <DetailFact label={t('page.console.log.detail.reasoningEffort')}>
            {reasoning}
          </DetailFact>
        ) : null}
        {path ? (
          <DetailFact label={t('page.console.log.m3.endpoint')}>
            <span className='logv2-mono'>{path}</span>
          </DetailFact>
        ) : null}
        {record.ip ? (
          <DetailFact label={t('page.console.log.column.ip')}>
            <span className='logv2-mono'>{String(record.ip)}</span>
          </DetailFact>
        ) : null}
        {isAdminUser && record.channel_name ? (
          <DetailFact label={t('page.console.log.column.channel')}>
            {String(record.channel_name)}
          </DetailFact>
        ) : null}
        {isAdminUser && useChannel.length > 1 ? (
          <DetailFact label={t('page.console.log.column.retry')}>
            <span className='logv2-mono'>{useChannel.join(' → ')}</span>
          </DetailFact>
        ) : null}
        {isAdminUser && upstream && upstream !== record.model_name ? (
          <DetailFact label={t('page.console.log.m3.upstream')}>
            <span className='logv2-mono'>{upstream}</span>
          </DetailFact>
        ) : null}
        {isAdminUser &&
        routing &&
        (routing.selected_backend || routing.chose_provider) ? (
          <DetailFact label={t('page.console.log.detail.routeSelected')}>
            <span className='logv2-mono'>
              {String(routing.selected_backend || routing.chose_provider)}
            </span>
          </DetailFact>
        ) : null}
        <span className='logv2-m3__spacer' />
        {mediaJobId ? (
          <Link
            className='logv2-m3__fact logv2-link'
            to={`/console/usage?tab=tasks&days=30&task=${encodeURIComponent(mediaJobId)}`}
            onClick={(event) => event.stopPropagation()}
          >
            {t('page.console.log.detail.viewTask')}
          </Link>
        ) : null}
        {requestId ? (
          <span className='logv2-m3__fact logv2-m3__rid'>
            {t('page.console.log.m3.requestId')}{' '}
            <b className='logv2-mono'>{requestId}</b>
            <Button
              size='xs'
              variant='secondary'
              onClick={(event) => {
                event.stopPropagation();
                void copyText(requestId);
              }}
            >
              {t('page.console.tables.copy')}
            </Button>
          </span>
        ) : null}
      </div>

      {record.session_id && !hideSession ? (
        <SessionReceiptBlock isAdminUser={isAdminUser} record={record} t={t} />
      ) : null}
    </div>
  );
}

/** A failed request leads with what went wrong and whether it cost anything,
 *  then the upstream's own words. */
function FailureSummary({
  amountMicrousd,
  content,
  reason,
  t,
}: {
  amountMicrousd: number;
  content: string;
  reason: string;
  t: TFunction;
}) {
  // The rejection copy lives in the lazily loaded `projects` bundle.
  const { t: tProjects } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const known = Boolean(reason) && isProjectRejection(reason);
  return (
    <div className='logv2-m3__fail'>
      <span className='logv2-m3__eyebrow'>
        {t('page.console.log.m3.whatHappened')}
      </span>
      <b className='logv2-m3__failtitle'>
        {known
          ? tProjects(rejectionLabelKey(reason))
          : t('page.console.log.m3.failed')}
      </b>
      <span className='logv2-m3__failbody'>
        {known ? `${tProjects(rejectionEffectKey(reason))} ` : ''}
        {amountMicrousd <= 0 ? t('page.console.log.m3.notCharged') : ''}
      </span>
      {content ? <pre className='logv2-m3__error'>{content}</pre> : null}
    </div>
  );
}

export function UsageLogFilters({
  logs,
  viewSwitcher,
  scope,
  scopeChip,
}: {
  logs: UsageLogsState;
  viewSwitcher?: ReactNode;
  /* The operator's 平台全局 / 仅我自己 switch; it lives in the 筛选 popover. */
  scope?: ReactNode;
  /* A removable chip naming the current scope when it is not the default. */
  scopeChip?: ReactNode;
}) {
  const { t } = logs;
  const [moreOpen, setMoreOpen] = useState(false);
  const form = useForm({
    defaultValues: logs.filters,
    resolver: zodResolver(filterSchema),
  });
  const values = form.watch();
  const { reset: resetForm } = form;

  // The 用户 view drills back into 明细 by pushing a username into the shared
  // filter state, so the form has to follow filters it did not submit itself.
  useEffect(() => {
    resetForm(logs.filters);
  }, [logs.filters, resetForm]);

  const submit = form.handleSubmit((values) => {
    logs.submitFilters({
      ...values,
      // An operator typing a different username intentionally leaves the
      // immutable deep-link scope and starts a fresh username search.
      user_id:
        values.username === logs.filters.username ? logs.filters.user_id : '',
    });
  });

  const applyFilters = (patch: Partial<UsageLogFilters>) => {
    logs.submitFilters({ ...logs.filters, ...patch });
  };

  const reset = () => {
    const next = getDefaultUsageLogFilters();
    form.reset(next);
    logs.resetFilters();
  };

  // The log's group is the lane, filtered by the 线路 switch; the customer's
  // free-text box searches models instead.
  const searchField = logs.isAdminUser ? 'username' : 'model_name';
  const searchLabel = logs.isAdminUser
    ? t('legacy.i18n.user_name.a311ed747b')
    : t('legacy.i18n.model_name.38719c9968');
  const searchValue = values[searchField];

  /* Search as you type: the box applies itself a beat after the last key, so
     there is no separate 搜索 button to find. */
  const searchDebounced = useDebouncedCallback(() => void submit(), 400);
  useEffect(() => {
    if ((searchValue ?? '') === (logs.filters[searchField] ?? '')) return;
    searchDebounced();
  }, [logs.filters, searchDebounced, searchField, searchValue]);

  const logTypeLabels: Record<string, string> = {
    '0': t('legacy.i18n.all.778fc8f994'),
    '1': t('legacy.i18n.top_up.fa03c46253'),
    '2': t('legacy.i18n.consume.71d6df4130'),
    '3': t('legacy.i18n.manage.4989b5cf94'),
    '4': t('legacy.i18n.system.1a1f6dff78'),
    '5': t('legacy.i18n.error.b859c7be75'),
  };

  /* What the 筛选 button counts and the chip row names: the filters that live
     behind the popover and are set to something other than their default. */
  const applied = logs.filters;
  const chips: Array<{ key: string; label: string; clear: () => void }> = [];
  if (applied.logType && applied.logType !== '0') {
    chips.push({
      key: 'logType',
      label: t('page.console.log.filter.chipLogType', {
        defaultValue: '日志类型：{{value}}',
        value: logTypeLabels[applied.logType] ?? applied.logType,
      }),
      clear: () => applyFilters({ logType: '0' }),
    });
  }
  if (logs.isAdminUser && applied.model_name) {
    chips.push({
      key: 'model',
      label: t('page.console.log.filter.chipModel', {
        defaultValue: '模型：{{value}}',
        value: applied.model_name,
      }),
      clear: () => applyFilters({ model_name: '' }),
    });
  }
  if (applied.token_name) {
    chips.push({
      key: 'token',
      label: t('page.console.log.filter.chipKey', {
        defaultValue: 'Key：{{value}}',
        value: applied.token_name,
      }),
      clear: () => applyFilters({ token_name: '' }),
    });
  }
  if (logs.isAdminUser && applied.channel) {
    chips.push({
      key: 'channel',
      label: t('page.console.log.filter.chipChannel', {
        defaultValue: '来源 ID：{{value}}',
        value: applied.channel,
      }),
      clear: () => applyFilters({ channel: '' }),
    });
  }
  if (logs.isAdminUser && applied.username) {
    chips.push({
      key: 'user',
      label: t('page.console.log.filter.userChip', {
        defaultValue: '用户：{{name}}',
        name: applied.username,
      }),
      clear: () => applyFilters({ user_id: '', username: '' }),
    });
  }
  const moreCount = chips.filter((chip) => chip.key !== 'user').length;
  const popoverDirty = () =>
    (['logType', 'model_name', 'token_name', 'channel'] as const).some(
      (key) => (values[key] ?? '') !== (logs.filters[key] ?? ''),
    );

  const laneOptions: Array<{ label: string; value: string }> = [
    { label: t('page.console.log.lane.all'), value: '' },
    { label: t('page.console.log.lane.stable'), value: 'direct' },
    { label: t('page.console.log.lane.eco'), value: 'cheap' },
  ];
  const pickLane = (value: string) => {
    form.setValue('group', value);
    void submit();
  };

  return (
    <form
      className='logv2-filters'
      onKeyDownCapture={submitFilterFormFromSelect}
      onSubmit={submit}
    >
      <div className='logv2-filters__row'>
        <Controller
          control={form.control}
          name='dateRange'
          render={({ field }) => (
            <DateRangePicker
              className='logv2-filters__date'
              value={toDateRangePickerValue(field.value)}
              onChange={(next) => {
                field.onChange(next);
                void submit();
              }}
            />
          )}
        />
        <span
          className='logv2-seg'
          role='group'
          aria-label={t('page.console.log.lane.label')}
        >
          {laneOptions.map((option) => (
            <button
              type='button'
              key={option.value || 'all'}
              aria-pressed={values.group === option.value}
              className='logv2-seg__button'
              data-active={values.group === option.value ? 'true' : undefined}
              onClick={() => pickLane(option.value)}
            >
              {option.label}
            </button>
          ))}
        </span>
        <label className='logv2-search'>
          <Search aria-hidden='true' size={14} />
          <input
            aria-label={searchLabel}
            placeholder={t('page.console.log.filter.searchPlaceholder', {
              defaultValue: '搜索{{field}}…',
              field: searchLabel,
            })}
            type='search'
            {...form.register(searchField)}
          />
        </label>
        <Popover
          open={moreOpen}
          onOpenChange={(open) => {
            setMoreOpen(open);
            // Changes made in the popover apply when it closes.
            if (!open && popoverDirty()) void submit();
          }}
        >
          <PopoverTrigger>
            <Button type='button' size='sm' variant='secondary'>
              <SlidersHorizontal aria-hidden='true' size={14} />
              {t('page.console.log.filter.more', { defaultValue: '筛选' })}
              {moreCount > 0 ? (
                <span className='logv2-filter-count'>{moreCount}</span>
              ) : null}
            </Button>
          </PopoverTrigger>
          <PopoverContent align='start' className='logv2-filter-pop'>
            {scope ? (
              <div className='logv2-filter-pop__field'>
                <span className='logv2-filter-pop__label'>
                  {t('page.console.log.scope.label', { defaultValue: '范围' })}
                </span>
                {scope}
              </div>
            ) : null}
            <div className='logv2-filter-pop__field'>
              <span className='logv2-filter-pop__label'>
                {t('legacy.i18n.log_type.693090ce10')}
              </span>
              <Controller
                control={form.control}
                name='logType'
                render={({ field }) => (
                  <Select value={field.value} onValueChange={field.onChange}>
                    {Object.entries(logTypeLabels).map(([value, label]) => (
                      <SelectItem key={value} value={value}>
                        {label}
                      </SelectItem>
                    ))}
                  </Select>
                )}
              />
            </div>
            {logs.isAdminUser ? (
              <div className='logv2-filter-pop__field'>
                <label
                  className='logv2-filter-pop__label'
                  htmlFor='log-model-name'
                >
                  {t('legacy.i18n.model_name.38719c9968')}
                </label>
                <Input
                  id='log-model-name'
                  placeholder={t('legacy.i18n.all.778fc8f994')}
                  {...form.register('model_name')}
                />
              </div>
            ) : null}
            <div className='logv2-filter-pop__field'>
              <label
                className='logv2-filter-pop__label'
                htmlFor='log-token-name'
              >
                {t('legacy.i18n.token_name.25a130fc5b')}
              </label>
              <Input
                id='log-token-name'
                placeholder={t('legacy.i18n.all.778fc8f994')}
                {...form.register('token_name')}
              />
            </div>
            {logs.isAdminUser ? (
              <div className='logv2-filter-pop__field'>
                <label
                  className='logv2-filter-pop__label'
                  htmlFor='log-channel'
                >
                  {t('legacy.i18n.channel_id.8a01559567')}
                </label>
                <Input
                  id='log-channel'
                  placeholder={t('legacy.i18n.channel_id.8a01559567')}
                  {...form.register('channel')}
                />
              </div>
            ) : null}
            <div className='logv2-filter-pop__foot'>
              <Button
                type='button'
                size='sm'
                variant='ghost'
                onClick={() => {
                  reset();
                  setMoreOpen(false);
                }}
              >
                {t('page.console.log.filter.clearAll', {
                  defaultValue: '清除全部',
                })}
              </Button>
              <Button
                type='button'
                size='sm'
                variant='primary'
                onClick={() => {
                  setMoreOpen(false);
                  void submit();
                }}
              >
                {t('page.console.log.filter.done', { defaultValue: '完成' })}
              </Button>
            </div>
          </PopoverContent>
        </Popover>
        {viewSwitcher ? (
          <span className='logv2-filters__view'>{viewSwitcher}</span>
        ) : null}
      </div>
      {chips.length || scopeChip ? (
        <div className='logv2-filter-chips'>
          {scopeChip}
          {chips.map((chip) => (
            <span className='logv2-filter-chip' key={chip.key}>
              {chip.label}
              <button
                type='button'
                aria-label={t('page.console.log.filter.removeChip', {
                  defaultValue: '移除 {{label}}',
                  label: chip.label,
                })}
                onClick={chip.clear}
              >
                <X aria-hidden='true' size={12} />
              </button>
            </span>
          ))}
          <button type='button' className='logv2-filter-clear' onClick={reset}>
            {t('page.console.log.filter.clearAll', {
              defaultValue: '清除全部',
            })}
          </button>
        </div>
      ) : null}
    </form>
  );
}
