/* 内容 › 邮件: the pieces the three mail views share — the sub-nav, the
 * labels for a mail's kind and status, and the time formats. */
import { useTranslation } from 'react-i18next';
import type { MailDelivery } from './mailApi';
import type { TFunction } from 'i18next';

export const MAIL_KINDS = [
  'digest',
  'payment',
  'balance',
  'key_usage',
  'notice',
  'auth',
  'team',
  'ops',
  'copy',
] as const;

export const MAIL_STATUSES = [
  'sent',
  'delivered',
  'delayed',
  'bounced',
  'complained',
  'failed',
  'skipped',
  'generating',
  'draft',
] as const;

const KIND_DEFAULTS: Record<string, string> = {
  digest: '用量回顾',
  weekly_digest: '周报',
  monthly_digest: '月报',
  payment: '付款',
  balance: '余额',
  key_usage: 'Key 用量',
  notice: '通知',
  auth: '账号安全',
  team: '团队',
  ops: '运维',
  copy: '副本',
  other: '其他',
};

const STATUS_DEFAULTS: Record<string, string> = {
  sent: '已交给 Resend',
  delivered: '已送达',
  delayed: '延迟',
  bounced: '退信',
  complained: '被标为垃圾',
  failed: '失败',
  skipped: '跳过',
  generating: '生成中',
  draft: '草稿（未发送）',
};

const SKIP_DEFAULTS: Record<string, string> = {
  opted_out: '已关闭',
  below_minimum: '低于 $1',
  monthly_same_day: '并入月报',
  incomplete_data: '数据不全',
  other: '其他',
};

export const kindLabel = (t: TFunction, kind: string) =>
  KIND_DEFAULTS[kind]
    ? t(`page.console.ops.mail.kind.${kind}`, {
        defaultValue: KIND_DEFAULTS[kind],
      })
    : kind || '—';

export const statusLabel = (t: TFunction, status: string) =>
  STATUS_DEFAULTS[status]
    ? t(`page.console.ops.mail.status.${status}`, {
        defaultValue: STATUS_DEFAULTS[status],
      })
    : status || '—';

export const skipReasonLabel = (t: TFunction, reason: string) =>
  SKIP_DEFAULTS[reason]
    ? t(`page.console.ops.mail.skip.${reason}`, {
        defaultValue: SKIP_DEFAULTS[reason],
      })
    : reason;

/** A mail's status as the list and the detail both say it: 跳过 carries the
 *  reason it was not sent. */
export const mailStatusText = (
  t: TFunction,
  mail: Pick<MailDelivery, 'status' | 'error'>,
) =>
  mail.status === 'skipped' && mail.error
    ? `${statusLabel(t, 'skipped')} · ${skipReasonLabel(t, mail.error)}`
    : statusLabel(t, mail.status);

export const statusTone = (status: string) => {
  if (status === 'delivered') return 'done';
  if (status === 'bounced' || status === 'complained' || status === 'failed')
    return 'danger';
  if (status === 'delayed') return 'warn';
  return 'neutral';
};

export function MailStatus({
  mail,
}: {
  mail: Pick<MailDelivery, 'status' | 'error'>;
}) {
  const { t } = useTranslation();
  return (
    <span className='ops-table__state'>
      <span className={`ops-dot ops-dot--${statusTone(mail.status)}`} />
      {mailStatusText(t, mail)}
    </span>
  );
}

const pad = (n: number) => String(n).padStart(2, '0');

/** Unix seconds → "MM-DD HH:mm:ss" in the reader's clock. */
export const formatMailTime = (unix: number, seconds = true) => {
  if (!unix) return '—';
  const date = new Date(unix * 1000);
  const base = `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
  return seconds ? `${base}:${pad(date.getSeconds())}` : base;
};

/** Parts of a unix instant as a wall clock in `timeZone`. */
export const zonedParts = (unix: number, timeZone: string, locale: string) => {
  const date = new Date(unix * 1000);
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(date);
  const get = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((part) => part.type === type)?.value || '';
  const weekday = new Intl.DateTimeFormat(locale, {
    timeZone,
    weekday: 'short',
  }).format(date);
  return {
    date: `${get('month')}-${get('day')}`,
    time: `${get('hour')}:${get('minute')}`,
    weekday,
  };
};
