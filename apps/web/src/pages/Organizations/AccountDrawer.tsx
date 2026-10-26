import { useMutation, useQuery } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, Link2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  type Account,
  type AccountKey,
  type AccountSignup,
  type AccountSummary,
  accountKeyOperationalState,
  buildDailySpendSeries,
  daysToEmpty,
  formatStorageBytes,
  initials,
  int,
  isFrozen,
  isZeroBalance,
  ROLE_ADMIN,
  usd,
} from './account-model';
import AccountAccessAddresses from './AccountAccessAddresses';
import { Sparkline } from '../../components/charts/mini';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Drawer } from '../../components/ui/drawer';
import { Field, Label } from '../../components/ui/field';
import { Input } from '../../components/ui/input';
import { Modal } from '../../components/ui/modal';
import { PendingValue } from '../../components/ui/pending';
import { Select } from '../../components/ui/select';
import { Tooltip } from '../../components/ui/tooltip';
import { showSuccess, usdToMicroUSD } from '../../helpers';
import { API } from '../../helpers/api';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import AccountSessions from '../Log/sessions/AccountSessions';

type UsageTotals = {
  active_keys?: number;
  amount_microusd?: number;
  cache_hit_ratio?: number;
  error_count?: number;
  p95_latency_ms?: number;
  request_count?: number;
};

type UsageDimension = {
  amount_microusd?: number;
  name?: string;
  request_count?: number;
};

type Usage = {
  by_model?: UsageDimension[];
  time_series?: { amount_microusd?: number; bucket?: number }[];
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

const SECTIONS = [
  'usage',
  'keys',
  'sessions',
  'wallet',
  'client',
  'ledger',
  'access',
  'actions',
] as const;

const ledgerKind = (kind?: string) =>
  kind === 'topup' || kind === 'bonus'
    ? 'topup'
    : kind === 'refund'
      ? 'refund'
      : 'manage';

export default function AccountDrawer({
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
  const bodyRef = useRef<HTMLDivElement>(null);
  const [activeSection, setActiveSection] = useState<string>('usage');
  const [keyPage, setKeyPage] = useState(1);

  const [draftName, setDraftName] = useState('');
  const [draftPlan, setDraftPlan] = useState('0');
  const [draftRpm, setDraftRpm] = useState('');
  const [draftTpm, setDraftTpm] = useState('');
  const [draftNote, setDraftNote] = useState('');
  const [adjustAmount, setAdjustAmount] = useState('');
  const [adjustReason, setAdjustReason] = useState('');
  const [adjustDirection, setAdjustDirection] = useState('credit');
  const [profileTouched, setProfileTouched] = useState(false);
  const [planTouched, setPlanTouched] = useState(false);
  const [limitsTouched, setLimitsTouched] = useState(false);
  const [confirmAction, setConfirmAction] = useState<
    'disable' | 'enable' | 'promote' | 'demote' | null
  >(null);

  const id = account?.id;
  const username = account?.username || '';
  const enabled = open && id != null;

  // Reset the editable draft whenever a different account opens.
  useEffect(() => {
    if (!account) return;
    setDraftName(account.name);
    setDraftPlan(String(account.price_plan_id || 0));
    setDraftRpm(account.rpm_limit ? String(account.rpm_limit) : '');
    setDraftTpm(account.tpm_limit ? String(account.tpm_limit) : '');
    setDraftNote(account.note || '');
    setAdjustAmount('');
    setAdjustReason('');
    setAdjustDirection('credit');
    setProfileTouched(false);
    setPlanTouched(false);
    setLimitsTouched(false);
    setConfirmAction(null);
    setActiveSection('usage');
    setKeyPage(1);
  }, [account]);

  const summary = useQuery<AccountSummary>({
    enabled,
    queryKey: ['account-summary', id],
    queryFn: async () => {
      const data = unwrap<AccountSummary | null>(
        await API.get(`/api/user/${id}/summary`),
      );
      return data || ({} as AccountSummary);
    },
  });

  useEffect(() => {
    if (
      !summary.data ||
      (summary.data.id != null && String(summary.data.id) !== String(id)) ||
      profileTouched
    )
      return;
    setDraftName(
      summary.data.display_name?.trim() || summary.data.username || '',
    );
    setDraftPlan(String(summary.data.price_plan?.id ?? 0));
    const limits = summary.data.rate_limit;
    setDraftRpm(limits?.rpm_override ? String(limits.rpm_override) : '');
    setDraftTpm(limits?.tpm_override ? String(limits.tpm_override) : '');
    setDraftNote(summary.data.remark || '');
  }, [profileTouched, summary.data]);

  const usage = useQuery<Usage>({
    enabled,
    queryKey: ['account-usage', id],
    queryFn: async () => {
      // Match the "近 30 日" label: pass an explicit 30-day window instead of
      // letting the endpoint fall back to its 7-day default. Whole hours
      // through the current one let the server read hourly rows instead of
      // every request of a heavy account.
      const hourEnd = Math.floor(Date.now() / 1000 / 3600) * 3600 + 3599;
      const end = hourEnd;
      const start = hourEnd + 1 - 30 * 24 * 60 * 60;
      const data = unwrap<Usage | null>(
        await API.get(`/api/user/${id}/dashboard`, {
          params: { start_timestamp: start, end_timestamp: end },
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
      // Keep each request and render bounded while allowing the complete,
      // account-scoped inventory to be paged inside the drawer.
      const data = unwrap<{ items?: unknown; total?: unknown } | null>(
        await API.get(`/api/user/${id}/tokens`, {
          params: { p: keyPage, page_size: 20 },
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
    enabled: enabled && Boolean(account?.internal_id),
    queryKey: ['account-ledger', account?.internal_id],
    queryFn: async () => {
      const data = unwrap<unknown>(
        await API.get(`/api/user/${id}/ledger`, {
          skipErrorHandler: true,
        }),
      );
      return (Array.isArray(data) ? data : []) as LedgerEntry[];
    },
  });

  useEffect(() => {
    const total = keys.data?.total;
    if (total == null) return;
    const pageCount = Math.max(1, Math.ceil(Number(total) / 20));
    setKeyPage((value) => Math.min(value, pageCount));
  }, [keys.data?.total]);

  const invalidateAll = () => {
    summary.refetch();
    usage.refetch();
    keys.refetch();
    ledger.refetch();
    onChanged();
  };

  const saveProfile = useMutation({
    mutationFn: async () => {
      const payload: Record<string, unknown> = {
        id,
        display_name: draftName,
        remark: draftNote,
      };
      // Only what was edited is sent: the server keeps omitted fields, so a
      // stale drawer never rewrites a plan or a limit someone else changed.
      if (planTouched) payload.price_plan_id = Number(draftPlan) || 0;
      if (limitsTouched) {
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
      setProfileTouched(false);
      setPlanTouched(false);
      setLimitsTouched(false);
      invalidateAll();
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
      setAdjustAmount('');
      setAdjustReason('');
      invalidateAll();
    },
    onError: apiErrorHandler,
  });

  const setRoleOrStatus = useMutation({
    mutationFn: async (action: 'disable' | 'enable' | 'promote' | 'demote') => {
      unwrap(await API.post('/api/user/manage', { id, action }));
    },
    onSuccess: async () => {
      showSuccess(d('success.action', '已操作'));
      setConfirmAction(null);
      await summary.refetch();
      onChanged();
    },
    onError: apiErrorHandler,
  });

  if (!account) return null;

  const detail = summary.data;
  const totals = usage.data?.totals || {};
  const series = buildDailySpendSeries(usage.data?.time_series || []);
  const models = (usage.data?.by_model || []).slice(0, 4);
  const modelMax = Math.max(
    1,
    ...models.map((m) => Number(m.amount_microusd || 0)),
  );
  const clients = (detail?.client_breakdown || []).slice(0, 4);
  const clientMax = Math.max(1, ...clients.map((c) => c.count || 0));
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
  const hasLimitOverride = Boolean(
    limits?.rpm_override || limits?.tpm_override,
  );
  const balance = detail?.quota ?? account.balance_microusd ?? 0;
  const balanceHealth =
    balance <= 0
      ? { label: d('balance.empty', '余额耗尽'), variant: 'danger' as const }
      : balance < 5_000_000
        ? { label: d('balance.low', '低余额'), variant: 'warning' as const }
        : {
            label: d('balance.healthy', '余额充足'),
            variant: 'success' as const,
          };
  // Burndown hint under the balance figure, mirroring the list column and
  // reusing its i18n keys (a-v3 shows "≈ N 天" as the strip's sub-line).
  const burndownHint = (() => {
    if (isFrozen(currentAccount)) return null;
    const merged = { ...currentAccount, balance_microusd: balance };
    if (isZeroBalance(merged)) {
      return t('page.console.orgs.burndown.exhausted', {
        defaultValue: '已耗尽',
      });
    }
    const days = daysToEmpty(merged);
    if (days == null) return null;
    return t('page.console.orgs.burndown.days', {
      count: Math.max(1, Math.round(days)),
      defaultValue: '≈ {{count}} 天',
    });
  })();
  const storageUsed = detail?.storage_bytes_used ?? 0;
  const storageQuota = detail?.storage_quota_bytes ?? 0;
  const canManage = (currentAccount.role ?? 1) < callerRole;
  const isSelf = String(account.id) === String(callerId ?? '');
  const canDelete = canManage && !isSelf;
  // One admin role: any 管理员 may grant or revoke it, never on themselves.
  const canToggleAdmin = callerRole >= ROLE_ADMIN && !isSelf;
  const targetIsAdmin = (currentAccount.role ?? 1) >= ROLE_ADMIN;
  const providers = detail?.login_providers || account.login_providers || [];
  const keyTotal = keys.data?.total ?? account.key_count;
  const keyPageCount = Math.max(1, Math.ceil(Number(keyTotal ?? 0) / 20));
  const usageUserID = account.internal_id ?? detail?.internal_id;
  const usageParams = new URLSearchParams({
    scope: 'platform',
    owner: username,
  });
  if (usageUserID) usageParams.set('user_id', String(usageUserID));
  const usageHref = `/console/usage?${usageParams.toString()}`;
  const roleText = (role?: number) =>
    (role ?? 1) >= ROLE_ADMIN
      ? d('role.admin', '管理员')
      : d('role.customer', '客户');
  const providerText = (provider: string) => {
    if (provider === 'email') return d('provider.email', '邮箱验证码');
    if (provider === 'password') return d('provider.password', '密码');
    return provider;
  };
  const dateTime = (unixSeconds?: number) =>
    unixSeconds
      ? new Date(unixSeconds * 1000).toLocaleString(
          i18n.resolvedLanguage || i18n.language || 'en',
          {
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
          },
        )
      : '—';
  const relative = (unixSeconds?: number | null) => {
    if (!unixSeconds) return d('time.never', '从未调用');
    const seconds = Math.max(0, Math.floor(Date.now() / 1000) - unixSeconds);
    if (seconds < 60) return d('time.now', '刚刚');
    if (seconds < 3600)
      return t('page.console.orgs.drawer.time.minutes', {
        count: Math.floor(seconds / 60),
        defaultValue: '{{count}} 分钟前',
      });
    if (seconds < 86400)
      return t('page.console.orgs.drawer.time.hours', {
        count: Math.floor(seconds / 3600),
        defaultValue: '{{count}} 小时前',
      });
    return t('page.console.orgs.drawer.time.days', {
      count: Math.floor(seconds / 86400),
      defaultValue: '{{count}} 天前',
    });
  };
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

  const scrollTo = (section: string) => {
    setActiveSection(section);
    const target = bodyRef.current?.querySelector(
      `[data-section='${section}']`,
    );
    // The scroll container is the drawer popup, not the body wrapper, so scroll
    // it explicitly and offset by the sticky section nav's height.
    const scroller = bodyRef.current?.closest('.lazu-drawer-content');
    if (!target || !scroller) return;
    const navHeight =
      bodyRef.current?.querySelector<HTMLElement>('.acc-nav')?.offsetHeight ??
      44;
    const delta =
      target.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
    const top = scroller.scrollTop + delta - navHeight - 8;
    // Honor reduced-motion: a smooth scrollTo is a no-op there, so jump instead.
    const smooth = !window.matchMedia('(prefers-reduced-motion: reduce)')
      .matches;
    if (smooth) {
      scroller.scrollTo({ top, behavior: 'smooth' });
    } else {
      scroller.scrollTop = top;
    }
  };

  const sectionTitle = (
    title: string,
    jump?: { label: string; to: string },
  ) => (
    <div className='acc-sec__title'>
      {title}
      {jump ? (
        <a
          className='acc-sec__jump'
          href={jump.to}
          target='_blank'
          rel='noreferrer'
        >
          {jump.label} ↗
        </a>
      ) : null}
    </div>
  );

  const title = (
    <div className='acc-head'>
      <div className='acc-head__tools'>
        <Button
          aria-label={d('nav.previousAria', '上一个客户（K）')}
          disabled={!canGoPrevious}
          onClick={onPrevious}
          size='sm'
          variant='ghost'
        >
          <ChevronLeft size={15} />
        </Button>
        <Button
          aria-label={d('nav.nextAria', '下一个客户（J）')}
          disabled={!canGoNext}
          onClick={onNext}
          size='sm'
          variant='ghost'
        >
          <ChevronRight size={15} />
        </Button>
        <Button
          aria-label={d('copy.aria', '复制此客户链接')}
          onClick={async () => {
            await navigator.clipboard.writeText(window.location.href);
            showSuccess(d('copy.success', '客户链接已复制'));
          }}
          size='sm'
          variant='ghost'
        >
          <Link2 size={14} />
          {d('copy.label', '复制链接')}
        </Button>
      </div>
      <div className='acc-head__id'>
        <span className='acc-head__avatar' aria-hidden='true'>
          {initials(currentAccount)}
        </span>
        <div>
          <div className='acc-head__name'>{currentAccount.name}</div>
          <div className='acc-head__meta'>
            {account.username}
            {account.internal_id ? ` · #${account.internal_id}` : ''}
            {account.email ? ` · ${account.email}` : ''}
          </div>
          {summary.isPending ? (
            <div className='acc-ses__signup'>
              <PendingValue chars={28} />
            </div>
          ) : (
            <SignupLine signup={detail?.signup} />
          )}
        </div>
      </div>
      {summary.isLoading ? (
        <div className='acc-head__chips'>
          <Badge>{d('loading', '加载中…')}</Badge>
        </div>
      ) : summary.isError || !detail ? (
        <div className='acc-head__chips'>
          <Badge variant='danger'>
            {d('summary.failed', '账户摘要加载失败')}
          </Badge>
        </div>
      ) : (
        <div className='acc-head__chips'>
          <Badge>{roleText(currentAccount.role)}</Badge>
          <Badge variant={isFrozen(currentAccount) ? 'danger' : 'success'}>
            <i className='acc-chip-dot' aria-hidden='true' />
            {isFrozen(currentAccount)
              ? d('status.frozen', '已冻结')
              : d('status.ok', '正常')}
          </Badge>
          <Badge variant={balanceHealth.variant}>{balanceHealth.label}</Badge>
          {plan ? <Badge variant='info'>{plan.name}</Badge> : null}
          {detail?.last_used_at || account.last_used_at ? (
            <Badge variant='success'>
              {t('page.console.orgs.drawer.status.activeAgo', {
                defaultValue: '{{time}}活跃',
                time: relative(detail?.last_used_at || account.last_used_at),
              })}
            </Badge>
          ) : null}
        </div>
      )}
    </div>
  );

  const adminToggleRow = canToggleAdmin ? (
    <div className='orgs__danger'>
      <div>
        <b>
          {targetIsAdmin
            ? d('actions.demoteTitle', '取消管理员')
            : d('actions.promoteTitle', '设为管理员')}
        </b>
        <span>
          {targetIsAdmin
            ? d('actions.demoteHint', '将无法再进入后台')
            : d('actions.promoteHint', '可进入全部后台')}
        </span>
      </div>
      <Button
        size='sm'
        variant='ghost'
        disabled={setRoleOrStatus.isPending}
        onClick={() => setConfirmAction(targetIsAdmin ? 'demote' : 'promote')}
      >
        {targetIsAdmin
          ? d('actions.demote', '取消管理员')
          : d('actions.promote', '设为管理员')}
      </Button>
    </div>
  ) : null;

  const confirmation = confirmAction
    ? {
        disable: {
          title: d('confirm.disable.title', '确认冻结账户？'),
          description: d(
            'confirm.disable.description',
            '冻结后将禁用登录与全部 API Keys，可随时恢复。',
          ),
          confirm: d('confirm.disable.action', '冻结账户'),
        },
        enable: {
          title: d('confirm.enable.title', '确认解冻账户？'),
          description: d(
            'confirm.enable.description',
            '解冻后登录与仍有效的 API Keys 将恢复使用。',
          ),
          confirm: d('confirm.enable.action', '解冻账户'),
        },
        promote: {
          title: d('confirm.promote.title', '确认设为管理员？'),
          description: d(
            'confirm.promote.description',
            '管理员可进入全部后台：账户、模型运营、内容与设置。',
          ),
          confirm: d('confirm.promote.action', '设为管理员'),
        },
        demote: {
          title: d('confirm.demote.title', '确认取消管理员？'),
          description: d(
            'confirm.demote.description',
            '该账户将无法再进入后台。',
          ),
          confirm: d('confirm.demote.action', '取消管理员'),
        },
      }[confirmAction]
    : null;

  return (
    <>
      <Drawer
        className='acc-drawer'
        overlayClassName='acc-drawer-overlay'
        open={open}
        onOpenChange={onOpenChange}
        title={title}
        body={
          <div ref={bodyRef}>
            {summary.isLoading ? (
              <div className='acc-empty'>{d('loading', '加载中…')}</div>
            ) : summary.isError || !detail ? (
              <div className='acc-empty'>
                {d('summary.failed', '账户摘要加载失败')} ·{' '}
                <button
                  type='button'
                  className='acc-sec__jump'
                  style={{ marginLeft: 0 }}
                  onClick={() => summary.refetch()}
                >
                  {d('retry', '重试')}
                </button>
              </div>
            ) : (
              <>
                <div className='acc-strip'>
                  <div>
                    <span>{d('metric.balance', '余额')}</span>
                    <b>{usd(balance)}</b>
                    {burndownHint ? <em>{burndownHint}</em> : null}
                  </div>
                  <div>
                    <span>{d('metric.spend30d', '近 30 日消费')}</span>
                    <b>
                      {usd(
                        detail?.recent_spend_microusd ??
                          account.recent_spend_microusd,
                      )}
                    </b>
                  </div>
                  <div>
                    <span>{d('metric.requests30d', '近 30 日请求')}</span>
                    <b>
                      {int(
                        detail?.request_count_recent ??
                          account.request_count_recent,
                      )}
                    </b>
                    {totals.request_count ? (
                      <em>
                        {d('metric.errorRate', '错误率')}{' '}
                        {(
                          ((totals.error_count ?? 0) / totals.request_count) *
                          100
                        ).toFixed(1)}
                        %
                      </em>
                    ) : null}
                  </div>
                  <div>
                    <span>{d('metric.lifetimeSpend', '累计消费')}</span>
                    <b>
                      {usd(
                        detail?.used_quota_microusd ??
                          account.used_quota_microusd,
                      )}
                    </b>
                    <em>
                      {d('metric.topupTotal', '累计充值')}{' '}
                      {usd(detail?.topup_total_microusd)}
                    </em>
                  </div>
                </div>

                <div className='acc-nav'>
                  {SECTIONS.map((section) => (
                    <button
                      key={section}
                      type='button'
                      data-active={activeSection === section || undefined}
                      onClick={() => scrollTo(section)}
                    >
                      {section === 'sessions'
                        ? t('page.console.log.session.account.nav', {
                            defaultValue: '会话',
                          })
                        : d(`nav.${section}`, section)}
                    </button>
                  ))}
                </div>

                {/* 用量 */}
                <div className='acc-sec' data-section='usage'>
                  {sectionTitle(d('usage.title', '近 30 日用量'), {
                    label: d('usage.openDaily', '在用量与日志看逐日'),
                    to: usageHref,
                  })}
                  {usage.isLoading ? (
                    <div className='acc-empty'>{d('loading', '加载中…')}</div>
                  ) : usage.isError ? (
                    <div className='acc-empty'>
                      {d('usage.failed', '用量加载失败')} ·{' '}
                      <button
                        type='button'
                        className='acc-sec__jump'
                        style={{ marginLeft: 0 }}
                        onClick={() => usage.refetch()}
                      >
                        {d('retry', '重试')}
                      </button>
                    </div>
                  ) : (
                    <>
                      {series.length ? (
                        <Sparkline
                          className='acc-spark'
                          values={series}
                          width={520}
                          height={60}
                          color='var(--lazu-evidence)'
                        />
                      ) : (
                        <div className='acc-empty'>
                          {d('usage.empty', '暂无用量数据')}
                        </div>
                      )}
                      <div className='acc-two'>
                        <div className='acc-bars'>
                          {models.length ? (
                            models.map((model) => (
                              <div key={model.name}>
                                <span>{model.name}</span>
                                <b>{usd(model.amount_microusd)}</b>
                                <div className='acc-bar'>
                                  <i
                                    style={{
                                      width: `${Math.round(
                                        (Number(model.amount_microusd || 0) /
                                          modelMax) *
                                          100,
                                      )}%`,
                                    }}
                                  />
                                </div>
                              </div>
                            ))
                          ) : (
                            <div className='acc-empty'>
                              {d('usage.noModels', '暂无模型分布')}
                            </div>
                          )}
                        </div>
                        <div className='acc-kv'>
                          <div>
                            <span>{d('usage.p95', 'P95 延迟')}</span>
                            <b>
                              {totals.p95_latency_ms
                                ? `${Math.round(totals.p95_latency_ms)} ms`
                                : '—'}
                            </b>
                          </div>
                          <div>
                            <span>{d('usage.cache', '缓存命中')}</span>
                            <b>
                              {totals.cache_hit_ratio != null
                                ? `${Math.round(totals.cache_hit_ratio * 100)}%`
                                : '—'}
                            </b>
                          </div>
                          <div>
                            <span>{d('usage.errors', '错误')}</span>
                            <b>{int(totals.error_count ?? 0)}</b>
                          </div>
                          <div>
                            <span>{d('usage.activeKeys', '活跃 Keys')}</span>
                            <b>{int(totals.active_keys ?? 0)}</b>
                          </div>
                        </div>
                      </div>
                    </>
                  )}
                </div>

                {/* Keys */}
                <div className='acc-sec' data-section='keys'>
                  {sectionTitle(`API Keys · ${keyTotal ?? '—'}`)}
                  {keys.isLoading ? (
                    <div className='acc-empty'>{d('loading', '加载中…')}</div>
                  ) : keys.isError ? (
                    <div className='acc-empty'>
                      {d('keys.failed', 'Keys 加载失败')} ·{' '}
                      <button
                        type='button'
                        className='acc-sec__jump'
                        style={{ marginLeft: 0 }}
                        onClick={() => keys.refetch()}
                      >
                        {d('retry', '重试')}
                      </button>
                    </div>
                  ) : keys.data &&
                    (keys.data.items.length > 0 ||
                      Number(keyTotal ?? 0) > 0) ? (
                    <>
                      {keys.data.items.length > 0 ? (
                        <div className='acc-keys'>
                          {keys.data.items.map((key) => {
                            const state = accountKeyOperationalState(
                              currentAccount,
                              key,
                            );
                            return (
                              <div key={key.id || key.key_prefix}>
                                <b>
                                  {key.name || d('keys.unnamed', '(未命名)')}
                                </b>
                                <Badge
                                  variant={
                                    state === 'account_disabled' ||
                                    state === 'expired'
                                      ? 'danger'
                                      : state === 'enabled'
                                        ? 'success'
                                        : 'neutral'
                                  }
                                >
                                  {state === 'account_disabled'
                                    ? d('keys.accountDisabled', '账户已冻结')
                                    : state === 'expired'
                                      ? d('keys.expired', '已过期')
                                      : state === 'enabled'
                                        ? d('keys.enabled', '启用')
                                        : d('keys.disabled', '停用')}
                                </Badge>
                                <small>
                                  {key.key_prefix}…{key.key_suffix}
                                  {key.unlimited_quota
                                    ? ` · ${d('keys.unlimited', '无限额')}`
                                    : ` · ${d('keys.remaining', '剩')} ${usd(key.remain_quota)}`}
                                  {key.last_used_at
                                    ? ` · ${relative(key.last_used_at)}`
                                    : ''}
                                </small>
                              </div>
                            );
                          })}
                        </div>
                      ) : (
                        <div className='acc-empty'>
                          {d('keys.emptyPage', '此页暂无 API Key')}
                        </div>
                      )}
                      {keyPageCount > 1 ? (
                        <div className='acc-key-pages'>
                          <Button
                            aria-label={t(
                              'legacy.i18n.previous_page.b41561d807',
                            )}
                            disabled={keyPage <= 1 || keys.isFetching}
                            onClick={() => setKeyPage((value) => value - 1)}
                            size='sm'
                            variant='ghost'
                          >
                            <ChevronLeft size={14} />
                          </Button>
                          <span>
                            {keyPage} / {keyPageCount}
                          </span>
                          <Button
                            aria-label={t('legacy.i18n.next_page.67a246a344')}
                            disabled={
                              keyPage >= keyPageCount || keys.isFetching
                            }
                            onClick={() => setKeyPage((value) => value + 1)}
                            size='sm'
                            variant='ghost'
                          >
                            <ChevronRight size={14} />
                          </Button>
                        </div>
                      ) : null}
                    </>
                  ) : (
                    <div className='acc-empty'>
                      {d('keys.empty', '暂无 API Key')}
                    </div>
                  )}
                </div>

                {/* 会话 */}
                <div className='acc-sec' data-section='sessions'>
                  {sectionTitle(
                    t('page.console.log.session.account.title', {
                      defaultValue: '最近的会话',
                    }),
                  )}
                  <AccountSessions
                    clientBreakdown={detail?.client_breakdown}
                    t={t}
                    userId={usageUserID}
                  />
                </div>

                {/* 钱包 */}
                <div className='acc-sec' data-section='wallet'>
                  {sectionTitle(d('wallet.title', '钱包'), {
                    label: d('wallet.openTopups', '查看充值记录'),
                    to: `/console/wallet?owner=${encodeURIComponent(String(account.id))}`,
                  })}
                  <div className='acc-kv acc-kv--3'>
                    <div>
                      <span>{d('metric.balance', '余额')}</span>
                      <b>{usd(balance)}</b>
                    </div>
                    <div>
                      <span>{d('metric.topupTotal', '累计充值')}</span>
                      <b>{usd(detail?.topup_total_microusd)}</b>
                    </div>
                    <div>
                      <span>{d('metric.lifetimeSpend', '累计消费')}</span>
                      <b>
                        {usd(
                          detail?.used_quota_microusd ??
                            account.used_quota_microusd,
                        )}
                      </b>
                    </div>
                    <div>
                      <span>{d('wallet.settlementFailures', '结算失败')}</span>
                      <b>{int(detail?.settlement_failures?.open ?? 0)}</b>
                    </div>
                  </div>
                  <div className='acc-lines'>
                    <div className='acc-line'>
                      <span>{d('wallet.pricePlan', '专属折扣')}</span>
                      <span className='acc-line__value'>
                        {plan ? (
                          <Badge variant='info'>{plan.name}</Badge>
                        ) : (
                          d('wallet.standardPrice', '标准价')
                        )}
                      </span>
                      {plan ? (
                        <Link to={`/console/price-plans/${plan.id}`}>
                          {d('wallet.openPlan', '打开')}
                        </Link>
                      ) : (
                        <span />
                      )}
                    </div>
                    <div className='acc-line'>
                      <span>{d('wallet.limits', '限额')}</span>
                      <span className='acc-line__value acc-line__mono'>
                        {hasLimitOverride
                          ? `${limits?.rpm ?? '—'} RPM · ${limits?.tpm ?? '—'} TPM`
                          : d('wallet.limitsDefault', '默认')}
                      </span>
                      <Link to='/console/setting?tab=rate-limits'>
                        {d('wallet.limitsSettings', '设置')}
                      </Link>
                    </div>
                  </div>
                  <div>
                    <div className='acc-sec__title' style={{ marginBottom: 6 }}>
                      {d('wallet.storage', '存储')}
                    </div>
                    {/* Storage is free and capped in bytes per paying account. */}
                    <div className='acc-kv'>
                      <div>
                        <span>{d('wallet.storageUsed', '已用')}</span>
                        <b>{formatStorageBytes(storageUsed)}</b>
                      </div>
                      <div>
                        <span>{d('wallet.storageLimit', '上限')}</span>
                        <b>
                          {storageQuota > 0
                            ? formatStorageBytes(storageQuota)
                            : '—'}
                        </b>
                      </div>
                    </div>
                  </div>
                </div>

                {/* 客户端 */}
                <div className='acc-sec' data-section='client'>
                  {sectionTitle(d('client.title', '客户端与 Agent'), {
                    label: d('client.openUsage', '在用量与日志查看'),
                    to: usageHref,
                  })}
                  <div className='acc-two'>
                    <div className='acc-bars'>
                      {clients.length ? (
                        clients.map((client) => (
                          <div key={client.framework}>
                            <span>{client.framework}</span>
                            <b>{int(client.count)}</b>
                            <div className='acc-bar'>
                              <i
                                style={{
                                  width: `${Math.round((client.count / clientMax) * 100)}%`,
                                }}
                              />
                            </div>
                          </div>
                        ))
                      ) : (
                        <div className='acc-empty'>
                          {d('client.empty', '暂无客户端数据')}
                        </div>
                      )}
                    </div>
                    <div className='acc-kv'>
                      <div>
                        <span>{d('client.country', '国家')}</span>
                        <b>
                          {(detail?.countries || [])
                            .slice(0, 2)
                            .map((c) => c.country)
                            .join(' · ') || '—'}
                        </b>
                      </div>
                      <div>
                        <span>{d('client.sessions', 'Agent 会话')}</span>
                        <b>{int(detail?.agent_sessions ?? 0)}</b>
                      </div>
                      <div>
                        <span>{d('client.mediaJobs', '媒体任务')}</span>
                        <b>{int(detail?.media_jobs?.total ?? 0)}</b>
                      </div>
                      <div>
                        <span>{d('client.loginMethods', '登录方式')}</span>
                        <b>
                          {providers.map(providerText).join(' · ') ||
                            d('provider.unavailable', '无可用方式')}
                        </b>
                      </div>
                    </div>
                  </div>
                </div>

                {/* 账本与审计 */}
                <div className='acc-sec' data-section='ledger'>
                  {sectionTitle(d('ledger.title', '账本与审计'))}
                  {ledger.isLoading ? (
                    <div className='acc-empty'>{d('loading', '加载中…')}</div>
                  ) : ledger.isError ? (
                    <div className='acc-empty'>
                      {d('ledger.failed', '账本加载失败')} ·{' '}
                      <button
                        type='button'
                        className='acc-sec__jump'
                        style={{ marginLeft: 0 }}
                        onClick={() => ledger.refetch()}
                      >
                        {d('retry', '重试')}
                      </button>
                    </div>
                  ) : ledger.data && ledger.data.length ? (
                    <div className='acc-tl'>
                      {ledger.data.map((entry) => (
                        <div
                          key={entry.id || `${entry.created_at}-${entry.kind}`}
                        >
                          <i data-kind={ledgerKind(entry.kind)} />
                          <div>
                            <b>{ledgerLabel(entry)}</b>
                            <small>
                              {dateTime(entry.created_at)}
                              {entry.actor_username
                                ? ` · ${entry.actor_username}`
                                : ''}
                            </small>
                          </div>
                          {entry.delta_microusd ? (
                            <span className='acc-tl__amt'>
                              {entry.delta_microusd < 0 ? '−' : '+'}
                              {usd(Math.abs(entry.delta_microusd))}
                            </span>
                          ) : null}
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className='acc-empty'>
                      {d('ledger.empty', '暂无充值 / 管理 / 退款记录')}
                    </div>
                  )}
                </div>

                {/* 接入地址 */}
                <div className='acc-sec' data-section='access'>
                  {sectionTitle(d('access.title', '接入地址'))}
                  <AccountAccessAddresses account={currentAccount} />
                </div>

                {/* 动作 */}
                <div className='acc-sec' data-section='actions'>
                  {sectionTitle(d('actions.title', '动作'))}
                  {canManage ? (
                    <>
                      <div className='orgs__balance'>
                        <div className='orgs__balance-current'>
                          <b>{usd(balance)}</b>
                          <span>
                            {d(
                              'actions.balanceHint',
                              '调整余额 · 写入管理日志',
                            )}
                          </span>
                        </div>
                        <div className='orgs__grid'>
                          <Field>
                            <Label>{d('actions.action', '动作')}</Label>
                            <Select
                              value={adjustDirection}
                              onValueChange={setAdjustDirection}
                              options={[
                                {
                                  label: d('actions.credit', '充值 +'),
                                  value: 'credit',
                                },
                                {
                                  label: d('actions.debit', '扣减 −'),
                                  value: 'debit',
                                },
                              ]}
                            />
                          </Field>
                          <Field>
                            <Label>{d('actions.amount', '金额（USD）')}</Label>
                            <Input
                              placeholder='100.00'
                              value={adjustAmount}
                              onChange={(event) =>
                                setAdjustAmount(event.target.value)
                              }
                            />
                          </Field>
                        </div>
                        <Field>
                          <Label>
                            {d('actions.reason', '原因（写入余额流水）')}
                          </Label>
                          <Input
                            placeholder={d(
                              'actions.reasonPlaceholder',
                              '例如：线下转账 #77',
                            )}
                            value={adjustReason}
                            onChange={(event) =>
                              setAdjustReason(event.target.value)
                            }
                          />
                        </Field>
                        <Button
                          size='sm'
                          variant='primary'
                          style={{ justifySelf: 'start' }}
                          disabled={
                            !adjustAmount.trim() ||
                            !adjustReason.trim() ||
                            adjustBalance.isPending
                          }
                          onClick={() => adjustBalance.mutate()}
                        >
                          {d('actions.apply', '执行调整')}
                        </Button>
                      </div>

                      <div className='orgs__grid'>
                        <Field>
                          <Label>{d('actions.displayName', '显示名称')}</Label>
                          <Input
                            value={draftName}
                            onChange={(event) => {
                              setDraftName(event.target.value);
                              setProfileTouched(true);
                            }}
                          />
                        </Field>
                        <Field>
                          <Label>{d('actions.pricePlan', '专属折扣')}</Label>
                          <Select
                            options={[
                              { label: d('actions.noPlan', '无'), value: '0' },
                              ...planOptions,
                              // A plan the list has not loaded (or a
                              // non-root viewer) still shows by name.
                              ...(plan &&
                              !planOptions.some(
                                (option) => option.value === String(plan.id),
                              )
                                ? [{ label: plan.name, value: String(plan.id) }]
                                : []),
                            ]}
                            value={draftPlan}
                            onValueChange={(value) => {
                              setDraftPlan(value);
                              setPlanTouched(true);
                              setProfileTouched(true);
                            }}
                          />
                        </Field>
                        <Field>
                          <Label>RPM</Label>
                          <Input
                            inputMode='numeric'
                            placeholder={d('actions.limitDefault', '默认')}
                            value={draftRpm}
                            onChange={(event) => {
                              setDraftRpm(event.target.value);
                              setLimitsTouched(true);
                              setProfileTouched(true);
                            }}
                          />
                        </Field>
                        <Field>
                          <Label>TPM</Label>
                          <Input
                            inputMode='numeric'
                            placeholder={d('actions.limitDefault', '默认')}
                            value={draftTpm}
                            onChange={(event) => {
                              setDraftTpm(event.target.value);
                              setLimitsTouched(true);
                              setProfileTouched(true);
                            }}
                          />
                        </Field>
                        <Field>
                          <Label>
                            {d('actions.note', '备注（仅平台可见）')}
                          </Label>
                          <Input
                            value={draftNote}
                            onChange={(event) => {
                              setDraftNote(event.target.value);
                              setProfileTouched(true);
                            }}
                          />
                        </Field>
                      </div>
                      <Button
                        size='sm'
                        variant='secondary'
                        style={{ justifySelf: 'start' }}
                        disabled={!profileTouched || saveProfile.isPending}
                        onClick={() => saveProfile.mutate()}
                      >
                        {d('actions.saveProfile', '保存资料')}
                      </Button>

                      <div className='orgs__danger' style={{ marginTop: 4 }}>
                        <div>
                          <b>
                            {isFrozen(currentAccount)
                              ? d('actions.enableAccount', '解冻账户')
                              : d('actions.disableAccount', '冻结账户')}
                          </b>
                          <span>
                            {d(
                              'actions.disableHint',
                              '禁用登录与全部 Keys，可恢复',
                            )}
                          </span>
                        </div>
                        <Button
                          size='sm'
                          variant='ghost'
                          disabled={setRoleOrStatus.isPending}
                          onClick={() =>
                            setConfirmAction(
                              isFrozen(currentAccount) ? 'enable' : 'disable',
                            )
                          }
                        >
                          {isFrozen(currentAccount)
                            ? d('actions.enable', '解冻')
                            : d('actions.disable', '冻结')}
                        </Button>
                      </div>
                      {adminToggleRow}
                      {canDelete ? (
                        <div className='orgs__danger'>
                          <div>
                            <b className='orgs__danger-title'>
                              {d('actions.deleteTitle', '永久删除')}
                            </b>
                            <span>
                              {d(
                                'actions.deleteHint',
                                '硬删除，登录凭据一并清除',
                              )}
                            </span>
                          </div>
                          <Button
                            size='sm'
                            variant='soft-danger'
                            onClick={() => onRequestDelete(account)}
                          >
                            {d('actions.delete', '删除')}
                          </Button>
                        </div>
                      ) : null}
                    </>
                  ) : (
                    // Another 管理员: balance and freezing stay off, but the
                    // admin role itself can still be revoked.
                    (adminToggleRow ?? (
                      <div className='acc-empty'>
                        {t('page.console.orgs.noPermission', {
                          defaultValue: '无权管理同级或更高等级的账户。',
                        })}
                      </div>
                    ))
                  )}
                </div>
              </>
            )}
          </div>
        }
      />
      <Modal
        busy={setRoleOrStatus.isPending}
        description={confirmation?.description}
        footer={
          <>
            <Button variant='ghost' onClick={() => setConfirmAction(null)}>
              {d('cancel', '取消')}
            </Button>
            <Button
              variant='soft-danger'
              loading={setRoleOrStatus.isPending}
              onClick={() =>
                confirmAction && setRoleOrStatus.mutate(confirmAction)
              }
            >
              {confirmation?.confirm}
            </Button>
          </>
        }
        onOpenChange={(next) => {
          if (!next) setConfirmAction(null);
        }}
        open={Boolean(confirmAction)}
        title={confirmation?.title}
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
    <div className='acc-ses__signup'>
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
    </div>
  );
}
