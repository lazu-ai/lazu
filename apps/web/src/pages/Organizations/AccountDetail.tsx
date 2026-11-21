import { useMutation, useQuery } from '@tanstack/react-query';
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Link2,
  X,
} from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  type Account,
  type AccountKey,
  type AccountSignup,
  type AccountSummary,
  accountKeyOperationalState,
  buildDailyUsage,
  initials,
  int,
  isFrozen,
  ROLE_ADMIN,
  usd,
} from './account-model';
import AccountAccessAddresses from './AccountAccessAddresses';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Modal, ModalFrame, ModalTitle } from '../../components/ui/modal';
import { PendingValue } from '../../components/ui/pending';
import { Select } from '../../components/ui/select';
import { Textarea } from '../../components/ui/textarea';
import { notify } from '../../components/ui/toast';
import { Tooltip } from '../../components/ui/tooltip';
import { API } from '../../helpers/api';
import { usdToMicroUSD } from '../../helpers/render';
import { showSuccess } from '../../helpers/utils';
import { useAccessAddressesAdmin } from '../../hooks/access-addresses/useAccessAddressesAdmin';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import AccountSessions from '../Log/sessions/AccountSessions';
import './account-detail.css';

/* 用户详情 — one account in one centred dialog (canvas board Final_Account).
   The left column is who they are and what can be changed, one row per
   setting with its edit opening in place; the right column is the last 30
   days, then their Keys, their ledger and their sessions. */

type UsageTotals = {
  amount_microusd?: number;
  cache_hit_ratio?: number;
  error_count?: number;
  p95_latency_ms?: number;
  request_count?: number;
};

type UsageDimension = {
  amount_microusd?: number;
  name?: string;
};

type UsageDay = {
  amount_microusd?: number;
  bucket?: number;
  error_count?: number;
  request_count?: number;
};

type Usage = {
  by_model?: UsageDimension[];
  time_series?: UsageDay[];
  totals?: UsageTotals;
};

type LedgerEntry = {
  action?: string;
  actor_username?: string;
  delta_microusd?: number;
  created_at?: number;
  id?: number | string;
  kind?: string;
};

type PendingTopUp = {
  completable?: boolean;
  create_time?: number;
  credit_microusd?: number;
  payment_method?: string;
  trade_no?: string;
};

type BalanceAlert = {
  email?: boolean;
  last_sent_at?: number;
  threshold_microusd?: number;
};

type RatePeak = { rpm?: number; tpm?: number };

type AccountKeyPage = {
  items: AccountKey[];
  total: number;
};

type Props = {
  account: Account | null;
  canGoNext: boolean;
  canGoPrevious: boolean;
  callerId?: number | string;
  callerRole: number;
  onChanged: () => void;
  onNext: () => void;
  onOpenChange: (open: boolean) => void;
  onPrevious: () => void;
  onRequestDelete: (account: Account) => void;
  open: boolean;
  /** 专属折扣 to choose from: plan id (as a string) and name. */
  planOptions: { label: string; value: string }[];
};

type Editing = 'balance' | 'plan' | 'rate' | 'access' | 'name' | 'note';
type Tab = 'keys' | 'ledger' | 'sessions';

const DAY = 86_400;
const KEY_PAGE_SIZE = 20;
const LOW_BALANCE_FALLBACK = 5_000_000;

/* 410000 → 410K, 1200000 → 1.2M. */
const shortCount = (value: number) => {
  if (value >= 1_000_000) return `${Math.round(value / 100_000) / 10}M`;
  if (value >= 10_000) return `${Math.round(value / 1000)}K`;
  return value.toLocaleString('en-US');
};

const ledgerKey = (entry: LedgerEntry) =>
  entry.id || `${entry.created_at}-${entry.kind}`;

export default function AccountDetail({
  account,
  canGoNext,
  canGoPrevious,
  callerId,
  callerRole,
  onChanged,
  onNext,
  onOpenChange,
  onPrevious,
  onRequestDelete,
  open,
  planOptions,
}: Props) {
  const { t, i18n } = useTranslation();
  const d = (key: string, defaultValue: string) =>
    t(`page.console.orgs.drawer.${key}`, { defaultValue });
  const [editing, setEditing] = useState<Editing | null>(null);
  const editingRow = useRef<HTMLDivElement>(null);
  const [tab, setTab] = useState<Tab>('keys');
  const [keyPage, setKeyPage] = useState(1);

  const [draftName, setDraftName] = useState('');
  const [draftPlan, setDraftPlan] = useState('0');
  const [draftRpm, setDraftRpm] = useState('');
  const [draftTpm, setDraftTpm] = useState('');
  const [draftNote, setDraftNote] = useState('');
  const [adjustAmount, setAdjustAmount] = useState('');
  const [adjustReason, setAdjustReason] = useState('');
  const [adjustDirection, setAdjustDirection] = useState<'credit' | 'debit'>(
    'credit',
  );
  const [confirmAction, setConfirmAction] = useState<
    'promote' | 'demote' | null
  >(null);
  const [completing, setCompleting] = useState<PendingTopUp | null>(null);

  const id = account?.id;
  const enabled = open && id != null;

  const summary = useQuery<AccountSummary & { balance_alert?: BalanceAlert }>({
    enabled,
    queryKey: ['account-summary', id],
    queryFn: async () => {
      const data = unwrap<AccountSummary | null>(
        await API.get(`/api/user/${id}/summary`),
      );
      return data || ({} as AccountSummary);
    },
  });

  // The window starts at a UTC midnight so the daily bars, which the
  // dashboard buckets by UTC day, add up to the figures above them.
  const nowSeconds = Math.floor(Date.now() / 1000);
  const todayStart = Math.floor(nowSeconds / DAY) * DAY;
  const windowStart = todayStart - 29 * DAY;
  const usage = useQuery<Usage>({
    enabled,
    queryKey: ['account-usage', id, windowStart],
    queryFn: async () => {
      const data = unwrap<Usage | null>(
        await API.get(`/api/user/${id}/dashboard`, {
          params: {
            start_timestamp: windowStart,
            end_timestamp: todayStart + DAY - 1,
          },
          skipErrorHandler: true,
        }),
      );
      return data || ({} as Usage);
    },
  });

  const keys = useQuery<AccountKeyPage>({
    enabled,
    queryKey: ['account-keys', id, keyPage],
    queryFn: async () => {
      const data = unwrap<{ items?: unknown; total?: unknown } | null>(
        await API.get(`/api/user/${id}/tokens`, {
          params: { p: keyPage, page_size: KEY_PAGE_SIZE },
          skipErrorHandler: true,
        }),
      );
      const items = Array.isArray(data) ? data : data?.items;
      const pageItems = (Array.isArray(items) ? items : []) as AccountKey[];
      return {
        items: pageItems,
        total: Number(data?.total ?? pageItems.length),
      };
    },
  });

  const ledger = useQuery<LedgerEntry[]>({
    enabled,
    queryKey: ['account-ledger', id],
    queryFn: async () => {
      const data = unwrap<unknown>(
        await API.get(`/api/user/${id}/ledger`, { skipErrorHandler: true }),
      );
      return (Array.isArray(data) ? data : []) as LedgerEntry[];
    },
  });

  const pendingTopUps = useQuery<PendingTopUp[]>({
    enabled,
    queryKey: ['account-pending-topups', id],
    queryFn: async () => {
      const data = unwrap<unknown>(
        await API.get(`/api/user/${id}/pending_topups`, {
          skipErrorHandler: true,
        }),
      );
      return (Array.isArray(data) ? data : []) as PendingTopUp[];
    },
  });

  // The peak scans every request of the window, so it is read on its own.
  const ratePeak = useQuery<RatePeak>({
    enabled,
    queryKey: ['account-rate-peak', id],
    queryFn: async () =>
      unwrap<RatePeak | null>(
        await API.get(`/api/user/${id}/rate_peak`, { skipErrorHandler: true }),
      ) || {},
  });

  const addresses = useAccessAddressesAdmin(enabled);

  /* The drafts follow the server until a row is opened for editing; an
     edit that is cancelled goes back to what the server holds. */
  const resetDrafts = () => {
    const detail = summary.data;
    setDraftName(
      detail?.display_name?.trim() || detail?.username || account?.name || '',
    );
    setDraftPlan(String(detail?.price_plan?.id ?? account?.price_plan_id ?? 0));
    const limits = detail?.rate_limit;
    const rpm = limits?.rpm_override ?? account?.rpm_limit;
    const tpm = limits?.tpm_override ?? account?.tpm_limit;
    setDraftRpm(rpm ? String(rpm) : '');
    setDraftTpm(tpm ? String(tpm) : '');
    setDraftNote(detail?.remark ?? account?.note ?? '');
    setAdjustAmount('');
    setAdjustReason('');
    setAdjustDirection('credit');
  };

  useEffect(() => {
    setEditing(null);
    setConfirmAction(null);
    setCompleting(null);
    setTab('keys');
    setKeyPage(1);
  }, [account?.id]);

  // An opened row takes the focus on its first field, or its first control.
  useEffect(() => {
    const box = editingRow.current;
    if (!editing || !box) return;
    (
      box.querySelector<HTMLElement>('.acd-inline :is(input, textarea)') ||
      box.querySelector<HTMLElement>(
        '.acd-inline :is(button, [role="switch"], [tabindex="0"])',
      )
    )?.focus();
  }, [editing]);

  useEffect(() => {
    if (editing) return;
    resetDrafts();
    // resetDrafts reads only what these name.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [account, summary.data, editing]);

  useEffect(() => {
    const total = keys.data?.total;
    if (total == null) return;
    const pageCount = Math.max(1, Math.ceil(Number(total) / KEY_PAGE_SIZE));
    setKeyPage((value) => Math.min(value, pageCount));
  }, [keys.data?.total]);

  const refreshAll = () => {
    summary.refetch();
    usage.refetch();
    keys.refetch();
    ledger.refetch();
    pendingTopUps.refetch();
    onChanged();
  };

  /* One row saves only its own field: the server keeps what is omitted, so
     a stale dialog never rewrites a plan or a limit someone else changed. */
  const saveProfile = useMutation({
    mutationFn: async (field: Editing) => {
      const payload: Record<string, unknown> = {
        id,
        display_name: draftName,
        remark: draftNote,
      };
      if (field === 'plan') payload.price_plan_id = Number(draftPlan) || 0;
      if (field === 'rate') {
        const limit = (value: string) => {
          const trimmed = value.replace(/,/g, '').trim();
          if (!trimmed) return 0;
          const number = Number(trimmed);
          if (!Number.isSafeInteger(number) || number < 0) {
            throw new Error(d('error.limit', 'RPM / TPM 须为非负整数'));
          }
          return number;
        };
        payload.rpm_limit = limit(draftRpm);
        payload.tpm_limit = limit(draftTpm);
      }
      unwrap(await API.put('/api/user/', payload));
    },
    onSuccess: () => {
      showSuccess(d('success.saved', '已保存'));
      setEditing(null);
      refreshAll();
    },
    onError: apiErrorHandler,
  });

  const adjustBalance = useMutation({
    mutationFn: async () => {
      const parsed = Number(adjustAmount);
      if (!Number.isFinite(parsed) || parsed <= 0) {
        throw new Error(d('error.amount', '请输入有效金额'));
      }
      const amountMicrousd = Number(usdToMicroUSD(parsed));
      if (!Number.isSafeInteger(amountMicrousd) || amountMicrousd <= 0) {
        throw new Error(d('error.amount', '请输入有效金额'));
      }
      unwrap(
        await API.post(`/api/user/${id}/quota/adjust`, {
          delta_microusd:
            adjustDirection === 'debit' ? -amountMicrousd : amountMicrousd,
          reason: adjustReason.trim(),
        }),
      );
    },
    onSuccess: () => {
      showSuccess(d('success.balance', '余额已调整'));
      setEditing(null);
      refreshAll();
    },
    onError: apiErrorHandler,
  });

  const completeTopUp = useMutation({
    mutationFn: async (tradeNo: string) => {
      unwrap(await API.post('/api/user/topup/complete', { trade_no: tradeNo }));
    },
    onSuccess: () => {
      showSuccess(d('pending.completed', '已按到账处理'));
      setCompleting(null);
      refreshAll();
    },
    onError: apiErrorHandler,
  });

  /* Freezing and unfreezing run at once: each takes the other back, so the
     receipt carries the opposite action instead of a dialog asking first.
     The target travels with the call, so a receipt clicked after another
     account opened still acts on the one it names. */
  const setRoleOrStatus = useMutation({
    mutationFn: async ({
      action,
      target,
    }: {
      action: 'disable' | 'enable' | 'promote' | 'demote' | 'restore';
      target: { id: number | string; name: string };
    }) => {
      unwrap(await API.post('/api/user/manage', { id: target.id, action }));
    },
    onSuccess: async (_, { action, target }) => {
      if (action === 'disable' || action === 'enable') {
        const reverse = action === 'disable' ? 'enable' : 'disable';
        notify.success(
          t(
            action === 'disable'
              ? 'page.console.orgs.drawer.receipt.frozen'
              : 'page.console.orgs.drawer.receipt.unfrozen',
            { name: target.name },
          ),
          {
            action: {
              label:
                reverse === 'enable'
                  ? d('actions.enable', '解冻')
                  : d('actions.disable', '冻结'),
              onClick: () =>
                setRoleOrStatus.mutate({ action: reverse, target }),
            },
          },
        );
      } else if (action === 'restore') {
        showSuccess(
          d(
            'deleted.restored',
            '账户已恢复，它的 API Key 停用中，需本人重新启用',
          ),
        );
      } else {
        showSuccess(d('success.action', '已操作'));
        setConfirmAction(null);
      }
      await summary.refetch();
      onChanged();
    },
    onError: apiErrorHandler,
  });

  if (!account) return null;

  const detail = summary.data;
  const currentAccount: Account = {
    ...account,
    name: detail
      ? detail.display_name?.trim() ||
        detail.username ||
        account.username ||
        account.email ||
        account.name
      : account.name,
    role: detail?.role ?? account.role,
    status: detail?.status ?? account.status,
  };
  const plan = detail ? detail.price_plan : null;
  const limits = detail?.rate_limit;
  const balance = detail?.quota ?? account.balance_microusd ?? 0;
  const alert = detail?.balance_alert;
  const lowLine = Number(alert?.threshold_microusd) || LOW_BALANCE_FALLBACK;
  const canManage = (currentAccount.role ?? 1) < callerRole;
  const isSelf = String(account.id) === String(callerId ?? '');
  const canDelete = canManage && !isSelf;
  const deleted = Boolean(detail?.deleted_at);
  const deletion = detail?.deletion;
  const purged = Boolean(deletion?.purged_at);
  const canEdit = canManage && !deleted;
  // One admin role: any 管理员 may grant or revoke it, never on themselves.
  const canToggleAdmin = callerRole >= ROLE_ADMIN && !isSelf && !deleted;
  const targetIsAdmin = (currentAccount.role ?? 1) >= ROLE_ADMIN;
  const keyTotal = keys.data?.total ?? account.key_count;
  const keyPageCount = Math.max(
    1,
    Math.ceil(Number(keyTotal ?? 0) / KEY_PAGE_SIZE),
  );
  const usageUserID = account.internal_id ?? detail?.internal_id;
  const usageParams = new URLSearchParams({
    scope: 'platform',
    owner: account.username || '',
  });
  if (usageUserID) usageParams.set('user_id', String(usageUserID));
  const usageHref = `/console/usage?${usageParams.toString()}`;
  const locale = i18n.resolvedLanguage || i18n.language || 'en';

  const formatMoment = (unixSeconds?: number) =>
    unixSeconds
      ? new Date(unixSeconds * 1000).toLocaleString(locale, {
          dateStyle: 'medium',
          timeStyle: 'short',
        })
      : '';
  const monthDay = (unixSeconds: number, utc = false) => {
    const date = new Date(unixSeconds * 1000);
    const month = utc ? date.getUTCMonth() : date.getMonth();
    const day = utc ? date.getUTCDate() : date.getDate();
    return `${String(month + 1).padStart(2, '0')}.${String(day).padStart(2, '0')}`;
  };
  const clock = (unixSeconds: number) =>
    new Date(unixSeconds * 1000).toLocaleTimeString(locale, {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  const isToday = (unixSeconds: number) =>
    new Date(unixSeconds * 1000).toDateString() === new Date().toDateString();
  /* Today 09:12, or the date when it was another day. */
  const dayAndTime = (unixSeconds: number) =>
    isToday(unixSeconds)
      ? t('page.console.orgs.drawer.time.todayAt', {
          defaultValue: '今天 {{time}}',
          time: clock(unixSeconds),
        })
      : `${monthDay(unixSeconds)} ${clock(unixSeconds)}`;
  const relative = (unixSeconds?: number | null) => {
    if (!unixSeconds) return d('time.never', '从未调用');
    const seconds = Math.max(0, nowSeconds - unixSeconds);
    if (seconds < 60) return d('time.now', '刚刚');
    if (seconds < 3600)
      return t('page.console.orgs.drawer.time.minutes', {
        count: Math.floor(seconds / 60),
        defaultValue: '{{count}} 分钟前',
      });
    if (seconds < DAY)
      return t('page.console.orgs.drawer.time.hours', {
        count: Math.floor(seconds / 3600),
        defaultValue: '{{count}} 小时前',
      });
    if (seconds < 2 * DAY) return d('time.yesterday', '昨天');
    return monthDay(unixSeconds);
  };
  const roleText = (role?: number) =>
    (role ?? 1) >= ROLE_ADMIN
      ? d('role.admin', '管理员')
      : d('role.customer', '客户');
  const methodText = (method?: string) =>
    method
      ? t(`page.console.wallet.record.method.${method}`, {
          defaultValue: method,
        })
      : '—';
  const ledgerLabel = (entry: LedgerEntry) => {
    if (entry.action === 'user.create')
      return d('ledger.accountCreated', '账户已创建');
    if (entry.action === 'user.update')
      return d('ledger.accountUpdated', '账户资料已更新');
    if (entry.action === 'user.manage.disable')
      return d('ledger.accountDisabled', '账户已冻结');
    if (entry.action === 'user.manage.enable')
      return d('ledger.accountEnabled', '账户已解冻');
    if (entry.action === 'user.manage.promote')
      return d('ledger.accountPromoted', '已设为管理员');
    if (entry.action === 'user.manage.demote')
      return d('ledger.accountDemoted', '已取消管理员');
    if (entry.action === 'user.has_topped_up.update')
      return d('ledger.topupStatusUpdated', '充值状态已更新');
    if (entry.kind === 'audit') return d('ledger.manage', '管理员账户操作');
    if (entry.kind === 'topup') return d('ledger.topup', '充值到账');
    if (entry.kind === 'bonus') return d('ledger.bonus', '奖励到账');
    if (entry.kind === 'refund') return d('ledger.refund', '退款入账');
    if (entry.kind === 'payment_reversal')
      return d('ledger.paymentReversal', '支付退款冲回');
    if (Number(entry.delta_microusd || 0) === 0)
      return d('ledger.manage', '管理员账户操作');
    return Number(entry.delta_microusd || 0) < 0
      ? d('ledger.debit', '管理员扣减余额')
      : d('ledger.credit', '管理员增加余额');
  };

  /* ---- left column ------------------------------------------------------ */

  const lastActive = detail?.last_used_at || account.last_used_at;
  const badges = (
    <div className='acd-badges'>
      <Badge variant='neutral'>{roleText(currentAccount.role)}</Badge>
      {deleted ? (
        <Badge variant='danger'>{d('deleted.title', '账户已注销')}</Badge>
      ) : (
        <Badge variant={isFrozen(currentAccount) ? 'danger' : 'success'}>
          {isFrozen(currentAccount)
            ? d('status.frozen', '已冻结')
            : d('status.ok', '正常')}
        </Badge>
      )}
      {!deleted && balance <= 0 ? (
        <Badge variant='danger'>{d('balance.empty', '余额耗尽')}</Badge>
      ) : !deleted && balance < lowLine ? (
        <Badge variant='warning'>{d('balance.low', '低余额')}</Badge>
      ) : null}
    </div>
  );

  /* An opened row edits in place: its controls take the value's spot, with
     one 取消 / 保存 pair under them. It is a form, so Enter saves, except
     where enterSaves is off (moving money): there the save button is not a
     submit button, and a form of two fields with none does not submit on
     Enter. */
  const editBox = (
    body: ReactNode,
    onSave: () => void,
    pending: boolean,
    saveLabel = d('save', '保存'),
    disabled = false,
    enterSaves = true,
  ) => (
    <form
      className='acd-inline'
      onSubmit={(event) => {
        event.preventDefault();
        if (!disabled && !pending) onSave();
      }}
    >
      {body}
      <div className='acd-inline__foot'>
        <Button
          size='sm'
          type='button'
          variant='ghost'
          onClick={() => setEditing(null)}
        >
          {d('cancel', '取消')}
        </Button>
        <Button
          disabled={disabled || pending}
          loading={pending}
          size='sm'
          type={enterSaves ? 'submit' : 'button'}
          variant='primary'
          onClick={
            enterSaves
              ? undefined
              : () => {
                  if (!disabled && !pending) onSave();
                }
          }
        >
          {saveLabel}
        </Button>
      </div>
    </form>
  );

  const row = (
    key: Editing,
    label: string,
    value: ReactNode,
    caption: ReactNode,
    actionLabel: string,
    editor: ReactNode,
  ) => {
    const open = canEdit && editing === key;
    return (
      <div
        className='acd-row'
        data-editing={open || undefined}
        key={key}
        ref={open ? editingRow : undefined}
      >
        <div className='acd-row__line'>
          <span className='acd-row__label'>{label}</span>
          {open ? (
            editor
          ) : (
            <>
              <span className='acd-row__value'>
                {value}
                {caption}
              </span>
              {canEdit ? (
                <button
                  className='acd-link'
                  type='button'
                  onClick={() => {
                    resetDrafts();
                    setEditing(key);
                  }}
                >
                  {actionLabel}
                </button>
              ) : (
                <span />
              )}
            </>
          )}
        </div>
      </div>
    );
  };

  const adjustValue = Number(adjustAmount);
  const adjustShown =
    Number.isFinite(adjustValue) && adjustValue > 0
      ? ` $${adjustValue.toFixed(2)}`
      : '';
  const balanceRow = row(
    'balance',
    d('metric.balance', '余额'),
    <span className='acd-row__big'>{usd(balance)}</span>,
    <span className='acd-cap acd-row__lines'>
      <span>
        {d('metric.topupTotal', '累计充值')} {usd(detail?.topup_total_microusd)}
      </span>
      <span>
        {d('metric.lifetimeSpend', '累计消费')}{' '}
        {usd(detail?.used_quota_microusd ?? account.used_quota_microusd)}
      </span>
      {alert?.threshold_microusd ? (
        <span>
          {t('page.console.orgs.drawer.alert.threshold', {
            defaultValue: '余额提醒 低于 {{amount}}',
            amount: usd(alert.threshold_microusd),
          })}
          {alert.email ? ` · ${d('alert.email', '邮件')}` : ''}
        </span>
      ) : null}
      {alert?.last_sent_at ? (
        <span className={balance < lowLine ? 'acd-warn' : undefined}>
          {t('page.console.orgs.drawer.alert.sent', {
            defaultValue: '{{when}} 已发提醒',
            when: dayAndTime(alert.last_sent_at),
          })}
        </span>
      ) : null}
    </span>,
    d('balance.adjust', '调整余额'),
    editBox(
      <>
        <div className='acd-inline__head'>
          <span className='acd-row__big'>{usd(balance)}</span>
          <div
            aria-label={d('balance.adjust', '调整余额')}
            className='acd-seg'
            role='group'
          >
            {(['credit', 'debit'] as const).map((direction) => (
              <button
                aria-pressed={adjustDirection === direction}
                key={direction}
                type='button'
                onClick={() => setAdjustDirection(direction)}
              >
                {direction === 'credit'
                  ? d('balance.credit', '加款')
                  : d('balance.debit', '扣款')}
              </button>
            ))}
          </div>
        </div>
        <div className='acd-inline__pair'>
          <Input
            aria-label={d('actions.amount', '金额（USD）')}
            inputMode='decimal'
            placeholder='100.00'
            value={adjustAmount}
            onChange={(event) => setAdjustAmount(event.target.value)}
          />
          <Input
            aria-label={d('actions.reasonShort', '原因')}
            placeholder={d('actions.reasonPlaceholder', '例如：线下转账 #77')}
            value={adjustReason}
            onChange={(event) => setAdjustReason(event.target.value)}
          />
        </div>
      </>,
      () => adjustBalance.mutate(),
      adjustBalance.isPending,
      (adjustDirection === 'credit'
        ? d('balance.confirmCredit', '确认加款')
        : d('balance.confirmDebit', '确认扣款')) + adjustShown,
      !adjustAmount.trim() || !adjustReason.trim(),
      false,
    ),
  );

  const planPct = Number(plan?.default_pct);
  const planRow = row(
    'plan',
    d('wallet.pricePlan', '专属折扣'),
    <span className='acd-row__text'>
      {!plan
        ? d('wallet.standardPrice', '标准价')
        : planPct > 0 && planPct < 100
          ? t('page.console.orgs.drawer.plan.discount', {
              defaultValue: '{{value}} 折',
              value: planPct % 10 === 0 ? planPct / 10 : planPct,
              off: 100 - planPct,
            })
          : plan.name}
    </span>,
    plan ? (
      <Link className='acd-cap' to={`/console/price-plans/${plan.id}`}>
        {plan.name}
      </Link>
    ) : null,
    d('edit', '修改'),
    editBox(
      <Select
        aria-label={d('wallet.pricePlan', '专属折扣')}
        options={[
          { label: d('actions.noPlan', '无'), value: '0' },
          ...planOptions,
          // A plan the list has not loaded still shows by name.
          ...(plan &&
          !planOptions.some((option) => option.value === String(plan.id))
            ? [{ label: plan.name, value: String(plan.id) }]
            : []),
        ]}
        value={draftPlan}
        onValueChange={setDraftPlan}
      />,
      () => saveProfile.mutate('plan'),
      saveProfile.isPending,
    ),
  );

  const peak = ratePeak.data;
  const limitText = (override?: number) =>
    override ? shortCount(override) : d('actions.limitDefault', '默认');
  const peakText = (value?: number) =>
    peak ? shortCount(Number(value || 0)) : '—';
  const peakLine = ratePeak.isPending ? (
    <PendingValue chars={20} />
  ) : (
    t('page.console.orgs.drawer.rate.peak', {
      defaultValue: '30 日峰值 RPM {{rpm}} · TPM {{tpm}}',
      rpm: peakText(peak?.rpm),
      tpm: peakText(peak?.tpm),
    })
  );
  const rateRow = row(
    'rate',
    d('rate.title', '速率'),
    <span className='acd-row__text'>
      RPM {limitText(limits?.rpm_override)} · TPM{' '}
      {limitText(limits?.tpm_override)}
    </span>,
    <span className='acd-cap'>{peakLine}</span>,
    d('edit', '修改'),
    editBox(
      <>
        <div className='acd-inline__pair acd-inline__pair--even'>
          <span className='acd-inline__unit'>
            <span aria-hidden='true'>RPM</span>
            <Input
              aria-label='RPM'
              inputMode='numeric'
              placeholder={d('actions.limitDefault', '默认')}
              value={draftRpm}
              onChange={(event) => setDraftRpm(event.target.value)}
            />
          </span>
          <span className='acd-inline__unit'>
            <span aria-hidden='true'>TPM</span>
            <Input
              aria-label='TPM'
              inputMode='numeric'
              placeholder={d('actions.limitDefault', '默认')}
              value={draftTpm}
              onChange={(event) => setDraftTpm(event.target.value)}
            />
          </span>
        </div>
        <span className='acd-cap'>{peakLine}</span>
      </>,
      () => saveProfile.mutate('rate'),
      saveProfile.isPending,
    ),
  );

  const internalId = Number(account.internal_id ?? detail?.internal_id ?? 0);
  const kindLabel = (kind: string) =>
    kind === 'cn_direct'
      ? t('page.console.token.address.kind.cnDirect', {
          defaultValue: '国内直连',
        })
      : t('page.console.token.address.kind.global', {
          defaultValue: '全球加速',
        });
  const openAddresses = [
    'global',
    ...(addresses.data || [])
      .filter((item) => item.user_ids.includes(internalId))
      .map((item) => item.kind),
  ];
  const accessRow = row(
    'access',
    d('access.title', '接入地址'),
    <span className='acd-row__text'>
      {addresses.isPending ? (
        <PendingValue chars={12} />
      ) : (
        Array.from(new Set(openAddresses)).map(kindLabel).join(' · ')
      )}
    </span>,
    null,
    d('access.set', '设置'),
    <div className='acd-inline'>
      <AccountAccessAddresses account={currentAccount} />
      <div className='acd-inline__foot'>
        <Button size='sm' variant='secondary' onClick={() => setEditing(null)}>
          {d('done', '完成')}
        </Button>
      </div>
    </div>,
  );

  const nameRow = row(
    'name',
    d('actions.displayName', '显示名称'),
    <span className='acd-row__text'>{currentAccount.name}</span>,
    null,
    d('edit', '修改'),
    editBox(
      <Input
        aria-label={d('actions.displayName', '显示名称')}
        value={draftName}
        onChange={(event) => setDraftName(event.target.value)}
      />,
      () => saveProfile.mutate('name'),
      saveProfile.isPending,
    ),
  );

  const noteRow = row(
    'note',
    d('note.title', '备注'),
    <span className='acd-row__text'>
      {detail?.remark || account.note || '—'}
    </span>,
    null,
    d('edit', '修改'),
    editBox(
      <Textarea
        aria-label={d('note.title', '备注')}
        rows={2}
        value={draftNote}
        onChange={(event) => setDraftNote(event.target.value)}
      />,
      () => saveProfile.mutate('note'),
      saveProfile.isPending,
    ),
  );

  const actions = (
    <div className='acd-actions'>
      {canEdit || canToggleAdmin ? (
        <div className='acd-actions__pair'>
          {canEdit ? (
            <Button
              disabled={setRoleOrStatus.isPending}
              variant='secondary'
              onClick={() =>
                setRoleOrStatus.mutate({
                  action: isFrozen(currentAccount) ? 'enable' : 'disable',
                  target: { id: account.id, name: currentAccount.name },
                })
              }
            >
              {isFrozen(currentAccount)
                ? d('actions.enableAccount', '解冻账户')
                : d('actions.disableAccount', '冻结账户')}
            </Button>
          ) : null}
          {canToggleAdmin ? (
            <Button
              disabled={setRoleOrStatus.isPending}
              variant='secondary'
              onClick={() =>
                setConfirmAction(targetIsAdmin ? 'demote' : 'promote')
              }
            >
              {targetIsAdmin
                ? d('actions.demote', '取消管理员')
                : d('actions.promote', '设为管理员')}
            </Button>
          ) : null}
        </div>
      ) : null}
      {canDelete ? (
        <Button
          variant='soft-danger'
          onClick={() =>
            onRequestDelete({
              ...currentAccount,
              username: detail?.username || account.username,
            })
          }
        >
          {d('actions.deleteAccount', '删除账户')}
        </Button>
      ) : null}
      {!canManage && !canToggleAdmin ? (
        <p className='acd-cap'>
          {t('page.console.orgs.noPermission', {
            defaultValue: '无权管理同级或更高等级的账户。',
          })}
        </p>
      ) : null}
    </div>
  );

  const deletedNotice = deleted ? (
    <div className='acd-deleted' role='status'>
      <p>
        {purged
          ? t('page.console.orgs.drawer.deleted.purged', {
              at: formatMoment(detail?.deleted_at),
              purgedAt: formatMoment(deletion?.purged_at),
              username: deletion?.username || '—',
              email: deletion?.email || '—',
              amount: usd(deletion?.written_off_microusd ?? 0),
              defaultValue:
                '注销于 {{at}}，{{purgedAt}} 已清除。原用户名 {{username}}，邮箱 {{email}}，作废余额 {{amount}}。',
            })
          : detail?.restore_until
            ? t('page.console.orgs.drawer.deleted.restorable', {
                at: formatMoment(detail?.deleted_at),
                until: formatMoment(detail?.restore_until),
                defaultValue:
                  '注销于 {{at}}。{{until}} 前本人重新登录会自动恢复。',
              })
            : t('page.console.orgs.drawer.deleted.expired', {
                at: formatMoment(detail?.deleted_at),
                defaultValue: '注销于 {{at}}，已过恢复期，本人不能再恢复。',
              })}
      </p>
      {canManage && !purged ? (
        <Button
          loading={setRoleOrStatus.isPending}
          size='sm'
          variant='secondary'
          onClick={() =>
            setRoleOrStatus.mutate({
              action: 'restore',
              target: { id: account.id, name: currentAccount.name },
            })
          }
        >
          {d('deleted.restore', '恢复账户')}
        </Button>
      ) : null}
    </div>
  ) : null;

  const side = (
    <aside className='acd-side'>
      <div className='acd-side__top'>
        <span aria-hidden='true' className='acd-avatar'>
          {initials(currentAccount).slice(0, 1)}
        </span>
        <span className='acd-tools'>
          <button
            aria-label={d('nav.previousAria', '上一位（K）')}
            className='acd-icon acd-icon--step'
            disabled={!canGoPrevious}
            title={d('nav.previousAria', '上一位（K）')}
            type='button'
            onClick={onPrevious}
          >
            <ChevronUp size={16} />
          </button>
          <button
            aria-label={d('nav.nextAria', '下一位（J）')}
            className='acd-icon acd-icon--step'
            disabled={!canGoNext}
            title={d('nav.nextAria', '下一位（J）')}
            type='button'
            onClick={onNext}
          >
            <ChevronDown size={16} />
          </button>
          <button
            aria-label={d('copy.label', '复制链接')}
            className='acd-icon'
            title={d('copy.label', '复制链接')}
            type='button'
            onClick={async () => {
              await navigator.clipboard.writeText(window.location.href);
              showSuccess(d('copy.success', '客户链接已复制'));
            }}
          >
            <Link2 size={15} />
          </button>
        </span>
      </div>
      <div className='acd-id'>
        <ModalTitle className='acd-id__name'>{currentAccount.name}</ModalTitle>
        <p className='acd-cap'>
          {[
            account.email || detail?.email || account.username,
            lastActive
              ? t('page.console.orgs.drawer.status.activeAgo', {
                  defaultValue: '{{time}}活跃',
                  time: relative(lastActive),
                })
              : d('time.never', '从未调用'),
          ]
            .filter(Boolean)
            .join(' · ')}
        </p>
        {summary.isPending ? (
          <div className='acd-badges'>
            <PendingValue chars={14} />
          </div>
        ) : (
          badges
        )}
        <SignupLine signup={detail?.signup} />
      </div>
      {deletedNotice}
      {summary.isError ? (
        <p className='acd-cap'>
          {d('summary.failed', '账户摘要加载失败')} ·{' '}
          <button
            className='acd-link'
            type='button'
            onClick={() => summary.refetch()}
          >
            {d('retry', '重试')}
          </button>
        </p>
      ) : (
        <div className='acd-rows'>
          {balanceRow}
          {planRow}
          {rateRow}
          {accessRow}
          {nameRow}
          {noteRow}
        </div>
      )}
      {actions}
    </aside>
  );

  /* ---- right column ----------------------------------------------------- */

  const totals = usage.data?.totals || {};
  const days = buildDailyUsage(usage.data?.time_series || [], nowSeconds);
  const dayMax = Math.max(
    1,
    ...days.map((day) => Number(day.amount_microusd || 0)),
  );
  const spend = Number(totals.amount_microusd || 0);
  const requests = Number(totals.request_count || 0);
  const errorRate = requests
    ? `${((Number(totals.error_count || 0) / requests) * 100).toFixed(1)}%`
    : '—';
  const models = usage.data?.by_model || [];
  const shownModels = models.slice(0, 3);
  const otherModels = models.slice(3);
  const weekday = (start: number) =>
    new Date(start * 1000).toLocaleDateString(locale, {
      weekday: 'short',
      timeZone: 'UTC',
    });

  const stat = (label: string, value: string, caption: string) => (
    <div className='acd-stat'>
      <p className='acd-cap'>{label}</p>
      <p className='acd-stat__value'>{value}</p>
      <p className='acd-cap'>{caption}</p>
    </div>
  );

  const usageSection = (
    <section className='acd-usage' aria-labelledby='acd-usage-title'>
      <div className='acd-usage__head'>
        <h3 className='acd-sec' id='acd-usage-title'>
          {d('usage.window', '近 30 日')}
        </h3>
        <Link
          className='acd-cap acd-usage__range'
          title={d('usage.openDaily', '在用量与日志看逐日')}
          to={usageHref}
        >
          {monthDay(windowStart, true)} – {monthDay(todayStart, true)}
        </Link>
      </div>
      {usage.isError ? (
        <p className='acd-cap'>
          {d('usage.failed', '用量加载失败')} ·{' '}
          <button
            className='acd-link'
            type='button'
            onClick={() => usage.refetch()}
          >
            {d('retry', '重试')}
          </button>
        </p>
      ) : (
        <>
          <div className='acd-stats'>
            {stat(
              d('usage.spend', '消费'),
              usage.isPending ? '—' : usd(spend),
              t('page.console.orgs.drawer.usage.daily', {
                defaultValue: '日均 {{amount}}',
                amount: usd(spend / 30),
              }),
            )}
            {stat(
              d('usage.requests', '请求'),
              usage.isPending ? '—' : int(requests),
              t('page.console.orgs.drawer.usage.cacheHit', {
                defaultValue: '缓存命中 {{value}}',
                value:
                  totals.cache_hit_ratio != null
                    ? `${Math.round(totals.cache_hit_ratio * 100)}%`
                    : '—',
              }),
            )}
            {stat(
              d('metric.errorRate', '错误率'),
              usage.isPending ? '—' : errorRate,
              `P95 ${
                totals.p95_latency_ms
                  ? `${(totals.p95_latency_ms / 1000).toFixed(1)} s`
                  : '—'
              }`,
            )}
          </div>
          <div className='acd-chart'>
            <div
              aria-label={d('usage.dailySpend', '每日消费')}
              className='acd-bars'
              role='group'
            >
              {days.map((day, index) => {
                const today = index === days.length - 1;
                const label = today
                  ? t('page.console.orgs.drawer.usage.todayUntil', {
                      defaultValue: '今天 · 截至 {{time}}',
                      time: clock(nowSeconds),
                    })
                  : `${monthDay(day.start, true)} ${weekday(day.start)}`;
                const amount = usd(Number(day.amount_microusd || 0));
                const height = Math.round(
                  (Number(day.amount_microusd || 0) / dayMax) * 100,
                );
                return (
                  <button
                    aria-label={`${label} ${amount}`}
                    className={today ? 'acd-bar is-today' : 'acd-bar'}
                    key={day.start}
                    type='button'
                  >
                    <span
                      className='acd-bar__fill'
                      style={{ height: `${height}%` }}
                    />
                    <span
                      className={
                        index < 4
                          ? 'acd-tip acd-tip--l'
                          : index > 25
                            ? 'acd-tip acd-tip--r'
                            : 'acd-tip'
                      }
                    >
                      <span className='acd-tip__head'>
                        <b>{label}</b>
                        <span>{amount}</span>
                      </span>
                      <span className='acd-cap'>
                        {t('page.console.orgs.drawer.usage.dayLine', {
                          defaultValue: '{{requests}} 次 · 错误 {{errors}}',
                          requests: int(Number(day.request_count || 0)),
                          errors: int(Number(day.error_count || 0)),
                        })}
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
            <div className='acd-cap acd-chart__axis'>
              <span>{monthDay(windowStart, true)}</span>
              <span>{monthDay(windowStart + 15 * DAY, true)}</span>
              <span>{d('usage.today', '今天')}</span>
            </div>
          </div>
          {models.length ? (
            <div className='acd-models'>
              <span className='acd-cap'>{d('usage.byModel', '按模型')}</span>
              {shownModels.map((model) => (
                <span className='acd-models__item' key={model.name}>
                  {model.name}
                  <span>{usd(model.amount_microusd)}</span>
                </span>
              ))}
              {otherModels.length ? (
                <span className='acd-models__item'>
                  {t('page.console.orgs.drawer.usage.otherModels', {
                    defaultValue: '其他 {{count}} 个',
                    count: otherModels.length,
                  })}
                  <span>
                    {usd(
                      otherModels.reduce(
                        (sum, model) =>
                          sum + Number(model.amount_microusd || 0),
                        0,
                      ),
                    )}
                  </span>
                </span>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </section>
  );

  const pending = pendingTopUps.data || [];
  const tabs: { id: Tab; label: string }[] = [
    { id: 'keys', label: `API Keys · ${keyTotal ?? '—'}` },
    {
      id: 'ledger',
      label: pending.length
        ? t('page.console.orgs.drawer.ledger.tabPending', {
            defaultValue: '账本与审计 · {{count}} 待确认',
            count: pending.length,
          })
        : d('ledger.title', '账本与审计'),
    },
    {
      id: 'sessions',
      label: t('page.console.log.session.account.nav', {
        defaultValue: '会话',
      }),
    },
  ];

  const retryLine = (text: string, onRetry: () => void) => (
    <p className='acd-cap acd-empty'>
      {text} ·{' '}
      <button className='acd-link' type='button' onClick={onRetry}>
        {d('retry', '重试')}
      </button>
    </p>
  );

  const keysPanel = keys.isError ? (
    retryLine(d('keys.failed', 'Keys 加载失败'), () => keys.refetch())
  ) : keys.isPending ? (
    <p className='acd-cap acd-empty'>{d('loading', '加载中…')}</p>
  ) : keys.data.items.length === 0 ? (
    <p className='acd-cap acd-empty'>
      {Number(keyTotal ?? 0) > 0
        ? d('keys.emptyPage', '此页暂无 API Key')
        : d('keys.empty', '暂无 API Key')}
    </p>
  ) : (
    <>
      <div className='acd-table acd-table--keys'>
        <div className='acd-table__head'>
          <span>{d('keys.name', '名称')}</span>
          <span>{d('keys.state', '状态')}</span>
          <span>{d('keys.quota', '额度')}</span>
          <span>{d('keys.models', '模型')}</span>
          <span>{d('keys.lastUsed', '最近')}</span>
        </div>
        {keys.data.items.map((key) => {
          const state = accountKeyOperationalState(currentAccount, key);
          const remain = Number(key.remain_quota || 0);
          const total = remain + Number(key.used_quota || 0);
          const modelCount = (key.model_limits || '')
            .split(',')
            .map((value) => value.trim())
            .filter(Boolean).length;
          return (
            <div className='acd-table__row' key={key.id || key.key_prefix}>
              <span className='acd-key'>
                <span>{key.name || d('keys.unnamed', '未命名')}</span>
                <span className='acd-mono'>
                  {key.key_prefix}…{key.key_suffix}
                </span>
              </span>
              <span className='acd-state'>
                <span
                  aria-hidden='true'
                  className={`acd-dot acd-dot--${state === 'enabled' ? 'ok' : state === 'account_disabled' ? 'bad' : 'idle'}`}
                />
                {state === 'account_disabled'
                  ? d('keys.accountDisabled', '账户已冻结')
                  : state === 'expired'
                    ? d('keys.expired', '已过期')
                    : state === 'enabled'
                      ? d('keys.enabled', '启用')
                      : d('keys.disabled', '停用')}
              </span>
              <span className='acd-quota'>
                {key.unlimited_quota ? (
                  d('keys.unlimited', '无限额')
                ) : state === 'expired' ? (
                  '—'
                ) : (
                  <>
                    <span>
                      {t('page.console.orgs.drawer.keys.left', {
                        defaultValue: '剩 {{remain}} / {{total}}',
                        remain: usd(remain),
                        total: usd(total),
                      })}
                    </span>
                    {total > 0 ? (
                      <span aria-hidden='true' className='acd-gauge'>
                        <span
                          style={{
                            width: `${Math.round((remain / total) * 100)}%`,
                          }}
                        />
                      </span>
                    ) : null}
                  </>
                )}
              </span>
              <span>
                {modelCount
                  ? t('page.console.orgs.drawer.keys.onlyModels', {
                      defaultValue: '只允许 {{count}} 个',
                      count: modelCount,
                    })
                  : d('keys.allModels', '全部')}
              </span>
              <span className='acd-cap'>
                {key.last_used_at ? relative(key.last_used_at) : '—'}
              </span>
            </div>
          );
        })}
      </div>
      {keyPageCount > 1 ? (
        <div className='acd-pages'>
          <Button
            aria-label={t('legacy.i18n.previous_page.b41561d807')}
            disabled={keyPage <= 1 || keys.isFetching}
            size='sm'
            variant='ghost'
            onClick={() => setKeyPage((value) => value - 1)}
          >
            <ChevronLeft size={14} />
          </Button>
          <span className='acd-cap'>
            {keyPage} / {keyPageCount}
          </span>
          <Button
            aria-label={t('legacy.i18n.next_page.67a246a344')}
            disabled={keyPage >= keyPageCount || keys.isFetching}
            size='sm'
            variant='ghost'
            onClick={() => setKeyPage((value) => value + 1)}
          >
            <ChevronRight size={14} />
          </Button>
        </div>
      ) : null}
    </>
  );

  const signedAmount = (micro?: number) =>
    micro
      ? `${micro < 0 ? '−' : '+'}${(Math.abs(micro) / 1e6).toFixed(2)}`
      : '';

  const ledgerPanel = (
    <>
      {pending.map((order) => (
        <div className='acd-pending' key={order.trade_no}>
          <span className='acd-cap'>
            {order.create_time && isToday(order.create_time)
              ? d('usage.today', '今天')
              : order.create_time
                ? monthDay(order.create_time)
                : '—'}
          </span>
          <span>{d('pending.title', '充值待确认')}</span>
          <span className='acd-pending__how'>
            {methodText(order.payment_method)}
            {order.create_time
              ? ` · ${t('page.console.orgs.drawer.pending.ordered', {
                  defaultValue: '{{time}} 下单',
                  time: clock(order.create_time),
                })}`
              : ''}
          </span>
          <span className='acd-num'>{usd(order.credit_microusd)}</span>
          {order.completable && canEdit ? (
            <button
              className='acd-link'
              type='button'
              onClick={() => setCompleting(order)}
            >
              {d('pending.complete', '补单')}
            </button>
          ) : (
            <span />
          )}
        </div>
      ))}
      {ledger.isError ? (
        retryLine(d('ledger.failed', '账本加载失败'), () => ledger.refetch())
      ) : ledger.isPending ? (
        <p className='acd-cap acd-empty'>{d('loading', '加载中…')}</p>
      ) : (ledger.data || []).length === 0 ? (
        <p className='acd-cap acd-empty'>
          {d('ledger.empty', '暂无充值 / 管理 / 退款记录')}
        </p>
      ) : (
        <>
          <div className='acd-table acd-table--ledger'>
            <div className='acd-table__head'>
              <span>{d('ledger.date', '日期')}</span>
              <span>{d('ledger.type', '类型')}</span>
              <span>{d('ledger.actor', '操作人')}</span>
              <span className='acd-num'>{d('ledger.amount', '金额')}</span>
            </div>
            {(ledger.data || []).map((entry) => (
              <div className='acd-table__row' key={ledgerKey(entry)}>
                <span className='acd-cap'>
                  {entry.created_at ? monthDay(entry.created_at) : '—'}
                </span>
                <span>{ledgerLabel(entry)}</span>
                <span className='acd-muted'>{entry.actor_username || ''}</span>
                <span
                  className={
                    Number(entry.delta_microusd || 0) > 0
                      ? 'acd-num acd-ok'
                      : 'acd-num'
                  }
                >
                  {signedAmount(entry.delta_microusd)}
                </span>
              </div>
            ))}
          </div>
          <p className='acd-cap acd-table__note'>
            {t('page.console.orgs.drawer.ledger.recent', {
              defaultValue: '最近 {{count}} 条',
              count: (ledger.data || []).length,
            })}
          </p>
        </>
      )}
    </>
  );

  const main = (
    <div className='acd-main'>
      <div className='acd-main__close'>
        <button
          aria-label={t('common.close', { defaultValue: '关闭' })}
          className='acd-icon'
          title={t('common.close', { defaultValue: '关闭' })}
          type='button'
          onClick={() => onOpenChange(false)}
        >
          <X size={17} />
        </button>
      </div>
      {usageSection}
      <div className='acd-tabs' role='tablist'>
        {tabs.map((item) => (
          <button
            aria-controls={`acd-panel-${item.id}`}
            aria-selected={tab === item.id}
            className='acd-tab'
            id={`acd-tab-${item.id}`}
            key={item.id}
            role='tab'
            tabIndex={tab === item.id ? 0 : -1}
            type='button'
            onClick={() => setTab(item.id)}
            onKeyDown={(event) => {
              if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft')
                return;
              const index = tabs.findIndex((entry) => entry.id === item.id);
              const next =
                tabs[
                  (index +
                    (event.key === 'ArrowRight' ? 1 : -1) +
                    tabs.length) %
                    tabs.length
                ];
              setTab(next.id);
              document.getElementById(`acd-tab-${next.id}`)?.focus();
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
      <div
        aria-labelledby={`acd-tab-${tab}`}
        className='acd-panel'
        id={`acd-panel-${tab}`}
        role='tabpanel'
      >
        {tab === 'keys' ? keysPanel : null}
        {tab === 'ledger' ? ledgerPanel : null}
        {tab === 'sessions' ? (
          <AccountSessions
            clientBreakdown={detail?.client_breakdown}
            t={t}
            userId={usageUserID}
          />
        ) : null}
      </div>
    </div>
  );

  const confirmation = confirmAction
    ? {
        promote: {
          title: t('page.console.orgs.drawer.confirm.promote.titleNamed', {
            defaultValue: '把 {{name}} 设为管理员？',
            name: currentAccount.name,
          }),
          description: d('actions.promoteHint', '可进入全部后台'),
          confirm: d('confirm.promote.action', '设为管理员'),
        },
        demote: {
          title: t('page.console.orgs.drawer.confirm.demote.titleNamed', {
            defaultValue: '取消 {{name}} 的管理员？',
            name: currentAccount.name,
          }),
          description: d('actions.demoteHint', '将无法再进入后台'),
          confirm: d('confirm.demote.action', '取消管理员'),
        },
      }[confirmAction]
    : null;

  return (
    <>
      <ModalFrame
        className='acd-dialog lazu-modal-content--xwide'
        hideClose
        open={open}
        onOpenChange={onOpenChange}
      >
        <div className='acd-layout'>
          {side}
          {main}
        </div>
      </ModalFrame>
      <Modal
        busy={setRoleOrStatus.isPending}
        className='acd-confirm'
        description={confirmation?.description}
        footer={
          <>
            <Button variant='secondary' onClick={() => setConfirmAction(null)}>
              {d('cancel', '取消')}
            </Button>
            <Button
              loading={setRoleOrStatus.isPending}
              variant='primary'
              onClick={() =>
                confirmAction &&
                setRoleOrStatus.mutate({
                  action: confirmAction,
                  target: { id: account.id, name: currentAccount.name },
                })
              }
            >
              {confirmation?.confirm}
            </Button>
          </>
        }
        open={Boolean(confirmAction)}
        title={confirmation?.title}
        onOpenChange={(next) => {
          if (!next) setConfirmAction(null);
        }}
      />
      <Modal
        body={
          completing ? (
            <dl className='acd-facts'>
              <dt>{d('ledger.amount', '金额')}</dt>
              <dd className='acd-num'>{usd(completing.credit_microusd)}</dd>
              <dt>{d('pending.method', '方式')}</dt>
              <dd>
                {methodText(completing.payment_method)}
                {completing.create_time
                  ? ` · ${t('page.console.orgs.drawer.pending.ordered', {
                      defaultValue: '{{time}} 下单',
                      time: dayAndTime(completing.create_time),
                    })}`
                  : ''}
              </dd>
              <dt>{d('pending.tradeNo', '订单号')}</dt>
              <dd className='acd-mono'>{completing.trade_no}</dd>
            </dl>
          ) : null
        }
        busy={completeTopUp.isPending}
        className='acd-confirm'
        footer={
          <>
            <Button variant='secondary' onClick={() => setCompleting(null)}>
              {d('cancel', '取消')}
            </Button>
            <Button
              loading={completeTopUp.isPending}
              variant='primary'
              onClick={() =>
                completing?.trade_no &&
                completeTopUp.mutate(completing.trade_no)
              }
            >
              {d('pending.complete', '补单')}
            </Button>
          </>
        }
        open={Boolean(completing)}
        title={d('pending.confirmTitle', '按到账处理这笔充值？')}
        onOpenChange={(next) => {
          if (!next) setCompleting(null);
        }}
      />
    </>
  );
}

const SIGNUP_METHOD_FALLBACK: Record<string, string> = {
  email_code: '邮箱验证码',
  github: 'GitHub',
  google: 'Google',
  oauth: 'OAuth',
  oidc: 'OIDC',
  password: '密码',
};

/** 注册于 7月14日（来自 github · utm_campaign=launch-week）. An inferred
 *  registration date reads "约" and says why on hover. */
function SignupLine({ signup }: { signup?: AccountSignup }) {
  const { t } = useTranslation();
  const createdAt = Number(signup?.created_at || 0);
  if (!signup || createdAt <= 0) return null;
  const date = new Date(createdAt * 1000);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  const dateText = sameYear
    ? t('page.console.log.session.account.signupDate', {
        day: date.getDate(),
        defaultValue: '{{month}}月{{day}}日',
        month: date.getMonth() + 1,
      })
    : t('page.console.log.session.account.signupDateYear', {
        day: date.getDate(),
        defaultValue: '{{year}}年{{month}}月{{day}}日',
        month: date.getMonth() + 1,
        year: date.getFullYear(),
      });
  const inferred = signup.created_at_source !== 'exact';
  const attribution = signup.attribution;
  const method = (attribution?.method || '').trim();
  const origin = [
    method && method !== 'other'
      ? t(`page.console.log.session.account.method.${method}`, {
          defaultValue: SIGNUP_METHOD_FALLBACK[method] || method,
        })
      : '',
    attribution?.source ? `utm_source=${attribution.source}` : '',
    attribution?.campaign ? `utm_campaign=${attribution.campaign}` : '',
    attribution?.invite_code
      ? t('page.console.log.session.account.invite', {
          code: attribution.invite_code,
          defaultValue: '邀请码 {{code}}',
        })
      : '',
  ].filter(Boolean);
  const dateNode = inferred ? (
    <Tooltip
      content={t('page.console.log.session.account.inferredTip', {
        defaultValue:
          '这个账户注册时还没有记录注册时间，这是按最早的活动记录推算的，实际注册不晚于这一天。',
      })}
    >
      <span style={{ cursor: 'help', textDecoration: 'underline dotted' }}>
        {t('page.console.log.session.account.approx', {
          date: dateText,
          defaultValue: '约 {{date}}',
        })}
      </span>
    </Tooltip>
  ) : (
    <span>{dateText}</span>
  );
  return (
    <p className='acd-cap'>
      {t('page.console.log.session.account.signupPrefix', {
        defaultValue: '注册于',
      })}{' '}
      {dateNode}
      {origin.length
        ? t('page.console.log.session.account.signupOrigin', {
            defaultValue: '（来自 {{origin}}）',
            origin: origin.join(' · '),
          })
        : ''}
    </p>
  );
}
