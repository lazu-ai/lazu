import { useMutation, useQuery } from '@tanstack/react-query';
import {
  ArrowUpRight,
  ChevronLeft,
  ChevronRight,
  Link2,
  Plus,
  X,
} from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  type Account,
  type AccountKey,
  type AccountSignup,
  type AccountSummary,
  countOperationalKeys,
  daysToEmpty,
  initials,
  int,
  isFrozen,
  isZeroBalance,
  providerLabel,
  ROLE_ADMIN,
  usd,
} from './account-model';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Field, Label } from '../../components/ui/field';
import { IconButton } from '../../components/ui/icon-button';
import { Input } from '../../components/ui/input';
import { Modal, ModalFrame, ModalTitle } from '../../components/ui/modal';
import { PendingValue } from '../../components/ui/pending';
import { Select } from '../../components/ui/select';
import { Switch } from '../../components/ui/switch';
import { notify } from '../../components/ui/toast';
import { Tooltip } from '../../components/ui/tooltip';
import { API } from '../../helpers/api';
import { usdToMicroUSD } from '../../helpers/render';
import { copy, showSuccess } from '../../helpers/utils';
import {
  type AccessAddressKind,
  useAccessAddressesAdmin,
  useSetAccessAddressGrant,
} from '../../hooks/access-addresses/useAccessAddressesAdmin';
import { apiErrorHandler, unwrap } from '../../lib/api-error';

type UsageTotals = {
  active_keys?: number;
  amount_microusd?: number;
  request_count?: number;
};

type UsageDimension = {
  amount_microusd?: number;
  name?: string;
  request_count?: number;
};

type Usage = {
  by_model?: UsageDimension[];
  totals?: UsageTotals;
};

type AccountKeyPage = {
  items: AccountKey[];
  total: number;
};

type ConfirmAction = 'demote' | 'disable' | 'promote';

type Props = {
  account: Account | null;
  callerId?: number | string;
  callerRole: number;
  canGoNext: boolean;
  canGoPrevious: boolean;
  onChanged: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  onNext: () => void;
  onOpenChange: (open: boolean) => void;
  onPrevious: () => void;
  onRequestDelete: (account: Account) => void;
  open: boolean;
  /** 专属折扣 to choose from: plan id (as a string) and name. */
  planOptions: { label: string; value: string }[];
};

/**
 * Centered 600px Customer Card Modal (09-A):
 * Replaces the 8-tab right drawer with a crisp 1-screen card holding identity,
 * 3-cell KPI strip, direct admin controls, deep-link jump bar, and collapsible
 * audit-reason-required balance adjustment.
 */
export default function AccountDrawer({
  account,
  callerId,
  callerRole,
  canGoNext,
  canGoPrevious,
  onChanged,
  onDirtyChange,
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

  const [draftPlan, setDraftPlan] = useState('0');
  const [draftRpm, setDraftRpm] = useState('');
  const [draftTpm, setDraftTpm] = useState('');
  const [draftNote, setDraftNote] = useState('');
  const [adjustOpen, setAdjustOpen] = useState(false);
  const [adjustAmount, setAdjustAmount] = useState('');
  const [adjustReason, setAdjustReason] = useState('');
  const [adjustDirection, setAdjustDirection] = useState('credit');
  const [profileTouched, setProfileTouched] = useState(false);
  const [planTouched, setPlanTouched] = useState(false);
  const [limitsTouched, setLimitsTouched] = useState(false);
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | null>(
    null,
  );
  const [pendingNav, setPendingNav] = useState<
    'close' | 'next' | 'prev' | null
  >(null);

  const id = account?.id;
  const username = account?.username || '';
  const enabled = open && id != null;
  const isDirty = profileTouched || Boolean(adjustAmount.trim());

  useEffect(() => {
    onDirtyChange?.(isDirty);
  }, [isDirty, onDirtyChange]);

  // Reset the editable draft whenever a different account opens.
  useEffect(() => {
    if (!account) return;
    setDraftPlan(String(account.price_plan_id || 0));
    setDraftRpm(account.rpm_limit ? String(account.rpm_limit) : '');
    setDraftTpm(account.tpm_limit ? String(account.tpm_limit) : '');
    setDraftNote(account.note || '');
    setAdjustOpen(false);
    setAdjustAmount('');
    setAdjustReason('');
    setAdjustDirection('credit');
    setProfileTouched(false);
    setPlanTouched(false);
    setLimitsTouched(false);
    setConfirmAction(null);
    setPendingNav(null);
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
  const accessAddresses = useAccessAddressesAdmin(enabled);
  const accessGrant = useSetAccessAddressGrant();
  const extraAccessAddresses = accessAddresses.data || [];
  const internalUserId = Number(account?.internal_id ?? account?.id ?? 0);
  const accessKindLabel = (kind: AccessAddressKind) =>
    kind === 'cn_direct'
      ? t('page.console.token.address.kind.cnDirect', {
          defaultValue: '国内直连',
        })
      : t('page.console.token.address.kind.global', {
          defaultValue: '全球加速',
        });

  useEffect(() => {
    if (
      !summary.data ||
      (summary.data.id != null && String(summary.data.id) !== String(id)) ||
      profileTouched
    )
      return;
    setDraftPlan(String(summary.data.price_plan?.id ?? 0));
    const limits = summary.data.rate_limit;
    setDraftRpm(limits?.rpm_override ? String(limits.rpm_override) : '');
    setDraftTpm(limits?.tpm_override ? String(limits.tpm_override) : '');
    setDraftNote(summary.data.remark || '');
  }, [id, profileTouched, summary.data]);

  const usage = useQuery<Usage>({
    enabled,
    queryKey: ['account-usage', id],
    queryFn: async () => {
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
    queryKey: ['account-keys', id],
    queryFn: async () => {
      const data = unwrap<{ items?: unknown; total?: unknown } | null>(
        await API.get(`/api/user/${id}/tokens`, {
          params: { p: 1, page_size: 50 },
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

  const invalidateAll = () => {
    void summary.refetch();
    void usage.refetch();
    void keys.refetch();
    onChanged();
  };

  const saveProfile = useMutation({
    mutationFn: async () => {
      const payload: Record<string, unknown> = {
        id,
        remark: draftNote,
      };
      // Only send touched fields so stale viewers never clobber concurrent changes.
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
      setAdjustOpen(false);
      invalidateAll();
    },
    onError: apiErrorHandler,
  });

  const setRoleOrStatus = useMutation({
    mutationFn: async ({
      action,
      target,
    }: {
      action: 'demote' | 'disable' | 'enable' | 'promote' | 'restore';
      target: { id: number | string; name: string };
    }) => {
      unwrap(await API.post('/api/user/manage', { id: target.id, action }));
    },
    onSuccess: async (_, { action, target }) => {
      setConfirmAction(null);
      if (action === 'disable' || action === 'enable') {
        const reverse = action === 'disable' ? 'enable' : 'disable';
        notify.success(
          t(
            action === 'disable'
              ? 'page.console.orgs.drawer.receipt.frozen'
              : 'page.console.orgs.drawer.receipt.unfrozen',
            {
              defaultValue:
                action === 'disable' ? '已冻结 {{name}}' : '已解冻 {{name}}',
              name: target.name,
            },
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
      }
      await summary.refetch();
      onChanged();
    },
    onError: apiErrorHandler,
  });

  if (!account) return null;

  const guardAction = (action: 'close' | 'next' | 'prev', run: () => void) => {
    if (isDirty) {
      setPendingNav(action);
      return;
    }
    run();
  };

  const detail = summary.data;
  const totals = usage.data?.totals || {};
  const topModel = (usage.data?.by_model || [])[0];
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
  const balance = detail?.quota ?? account.balance_microusd ?? 0;
  const topupTotal =
    detail?.topup_total_microusd ?? account.topup_total_microusd ?? 0;
  const spend30d =
    detail?.recent_spend_microusd ?? account.recent_spend_microusd ?? 0;
  const reqCount =
    detail?.request_count_recent ??
    totals.request_count ??
    account.request_count_recent ??
    0;

  const burndownHint = (() => {
    if (isFrozen(currentAccount)) {
      return {
        text: t('page.console.orgs.balance.frozen', { defaultValue: '冻结中' }),
        tone: 'muted' as const,
      };
    }
    const merged = { ...currentAccount, balance_microusd: balance };
    if (isZeroBalance(merged)) {
      const paid = account.has_topped_up || topupTotal > 0;
      return {
        text: paid
          ? t('page.console.orgs.burndown.exhausted', {
              defaultValue: '已耗尽',
            })
          : t('page.console.orgs.balance.neverToppedUp', {
              defaultValue: '未充值',
            }),
        tone: paid ? ('danger' as const) : ('muted' as const),
      };
    }
    const days = daysToEmpty(merged);
    if (days == null) {
      return {
        text:
          spend30d > 0
            ? t('page.console.orgs.burndown.sufficient', {
                defaultValue: '充足',
              })
            : t('page.console.orgs.burndown.trialIdle', {
                defaultValue: '近期无消耗',
              }),
        tone: 'muted' as const,
      };
    }
    const rounded = Math.max(1, Math.round(days));
    return {
      text: t('page.console.orgs.drawer.burndownEstimate', {
        count: rounded,
        defaultValue: '预计还可用 ≈ {{count}} 天',
      }),
      tone: days <= 7 ? ('warning' as const) : ('ok' as const),
    };
  })();

  const operationalKeyCount = keys.data
    ? countOperationalKeys(currentAccount, keys.data.items)
    : (totals.active_keys ?? account.key_count ?? 0);
  const keyTotal = keys.data?.total ?? account.key_count ?? 0;
  const primaryFramework =
    detail?.client_breakdown?.[0]?.framework ||
    account.client_top?.framework ||
    '';

  const canManage = (currentAccount.role ?? 1) < callerRole;
  const isSelf = String(account.id) === String(callerId ?? '');
  const canDelete = canManage && !isSelf;
  const deleted = Boolean(detail?.deleted_at || account.deleted_at);
  const deletion = detail?.deletion;
  const purged = Boolean(deletion?.purged_at);
  const canToggleAdmin = callerRole >= ROLE_ADMIN && !isSelf;
  const targetIsAdmin = (currentAccount.role ?? 1) >= ROLE_ADMIN;

  const formatMoment = (unixSeconds?: number) =>
    unixSeconds
      ? new Date(unixSeconds * 1000).toLocaleString(i18n.language, {
          dateStyle: 'medium',
          timeStyle: 'short',
        })
      : '';

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

  const providers = detail?.login_providers || account.login_providers || [];
  const primaryProvider = providers[0]
    ? providers[0] === 'email'
      ? d('provider.email', '邮箱登录')
      : providers[0] === 'password'
        ? d('provider.password', '密码登录')
        : `${providerLabel(providers[0])} 登录`
    : '';
  const lastActiveTs = detail?.last_used_at || account.last_used_at;
  const activeText = lastActiveTs
    ? t('page.console.orgs.drawer.status.activeAgo', {
        defaultValue: '{{time}}活跃',
        time: relative(lastActiveTs),
      })
    : d('time.never', '从未调用');

  const usageUserID = account.internal_id ?? detail?.internal_id;
  const usageParams = new URLSearchParams({
    scope: 'platform',
    owner: username,
  });
  if (usageUserID) usageParams.set('user_id', String(usageUserID));
  const usageHref = `/console/usage?${usageParams.toString()}`;

  const sessionsParams = new URLSearchParams({
    tab: 'logs',
    view: 'sessions',
    scope: 'platform',
    owner: username,
  });
  if (usageUserID) sessionsParams.set('user_id', String(usageUserID));
  const sessionsHref = `/console/usage?${sessionsParams.toString()}`;

  const confirmation = confirmAction
    ? {
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
        disable: {
          title: d('confirm.disable.title', '确认冻结该客户账户？'),
          description: d(
            'confirm.disable.description',
            '冻结后该账户将无法登录控制台，名下所有 API Keys 也会立即停用（后续可随时解冻）。',
          ),
          confirm: d('confirm.disable.action', '确认冻结'),
        },
      }[confirmAction]
    : null;

  const emailDisplay = detail?.email || account.email || username;

  return (
    <>
      <ModalFrame
        busy={saveProfile.isPending || adjustBalance.isPending}
        className='acc-drawer acc-modal'
        hideClose
        open={open}
        onOpenChange={(next) => {
          if (!next) guardAction('close', () => onOpenChange(false));
        }}
      >
        {/* 1. Pinned Identity Header */}
        <div className='acc-card__head'>
          <div className='acc-card__identity'>
            <span aria-hidden='true' className='acc-card__avatar'>
              {initials(currentAccount)}
            </span>
            <div className='acc-card__meta-wrap'>
              <ModalTitle className='acc-card__title'>
                <span>{currentAccount.name}</span>
                {deleted ? (
                  <Badge variant='neutral'>
                    {t('page.console.orgs.status.deleted', {
                      defaultValue: '已注销',
                    })}
                  </Badge>
                ) : isFrozen(currentAccount) ? (
                  <Badge variant='danger'>
                    <i aria-hidden='true' className='acc-chip-dot' />
                    {d('status.frozen', '已冻结')}
                  </Badge>
                ) : (
                  <Badge variant='success'>
                    <i aria-hidden='true' className='acc-chip-dot' />
                    {d('status.ok', '正常')}
                  </Badge>
                )}
                {targetIsAdmin ? (
                  <Badge variant='neutral'>{d('role.admin', '管理员')}</Badge>
                ) : null}
                <span className='acc-card__uid'>
                  #{account.internal_id ?? account.id}
                </span>
              </ModalTitle>
              <div className='acc-card__sub'>
                <span>{emailDisplay}</span>
                {primaryProvider ? (
                  <>
                    <span aria-hidden='true'>·</span>
                    <span>{primaryProvider}</span>
                  </>
                ) : null}
                {summary.isPending ? (
                  <>
                    <span aria-hidden='true'>·</span>
                    <PendingValue chars={10} />
                  </>
                ) : detail?.signup?.created_at ? (
                  <>
                    <span aria-hidden='true'>·</span>
                    <SignupInline signup={detail.signup} />
                  </>
                ) : null}
                <span aria-hidden='true'>·</span>
                <span>{activeText}</span>
              </div>
            </div>
          </div>

          <div className='acc-card__tools'>
            <IconButton
              aria-label={d('copy.aria', '复制此客户链接')}
              size='sm'
              variant='ghost'
              onClick={async () => {
                if (await copy(window.location.href)) {
                  showSuccess(d('copy.success', '客户链接已复制'));
                }
              }}
            >
              <Link2 size={14} />
            </IconButton>
            <IconButton
              aria-label={d('nav.previousAria', '上一个客户（K）')}
              disabled={!canGoPrevious}
              size='sm'
              variant='ghost'
              onClick={() => guardAction('prev', onPrevious)}
            >
              <ChevronLeft size={15} />
            </IconButton>
            <IconButton
              aria-label={d('nav.nextAria', '下一个客户（J）')}
              disabled={!canGoNext}
              size='sm'
              variant='ghost'
              onClick={() => guardAction('next', onNext)}
            >
              <ChevronRight size={15} />
            </IconButton>
            <IconButton
              aria-label={t('common.close', { defaultValue: '关闭' })}
              className='lazu-drawer-close'
              size='sm'
              variant='ghost'
              onClick={() => guardAction('close', () => onOpenChange(false))}
            >
              <X size={16} />
            </IconButton>
          </div>
        </div>

        {/* 2–5. Crisp Single-Screen Body */}
        <div className='acc-card__body'>
          {summary.isError && !detail ? (
            <div className='acc-notice' role='alert'>
              <span>{d('summary.failed', '账户摘要加载失败')}</span>
              <Button
                size='xs'
                variant='secondary'
                onClick={() => void summary.refetch()}
              >
                {d('retry', '重试')}
              </Button>
            </div>
          ) : null}

          {deleted ? (
            <div className='acc-notice' role='status'>
              <div className='acc-notice__copy'>
                <b>{d('deleted.title', '账户已注销')}</b>
                <span>
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
                          at: formatMoment(
                            detail?.deleted_at ?? account.deleted_at,
                          ),
                          defaultValue:
                            '注销于 {{at}}，已过恢复期，本人不能再恢复。',
                        })}
                </span>
              </div>
              {canManage && !purged ? (
                <Button
                  loading={setRoleOrStatus.isPending}
                  size='sm'
                  variant='secondary'
                  onClick={() =>
                    setRoleOrStatus.mutate({
                      action: 'restore',
                      target: {
                        id: account.id,
                        name: currentAccount.name,
                      },
                    })
                  }
                >
                  {d('deleted.restore', '恢复账户')}
                </Button>
              ) : null}
            </div>
          ) : null}

          {/* 3-Cell KPI Strip */}
          <div className='acc-kpi-strip'>
            <div className='acc-kpi'>
              <div className='acc-kpi__label'>
                {topupTotal > 0
                  ? t('page.console.orgs.drawer.metric.balanceWithTopup', {
                      amount: usd(topupTotal),
                      defaultValue: '当前余额（累计充 {{amount}}）',
                    })
                  : d('metric.balanceCurrent', '当前余额')}
              </div>
              <div className='acc-kpi__value'>
                {summary.isPending && account.balance_microusd == null ? (
                  <PendingValue chars={6} />
                ) : (
                  usd(balance)
                )}
              </div>
              <div className='acc-kpi__sub' data-tone={burndownHint.tone}>
                {burndownHint.text}
              </div>
            </div>

            <div className='acc-kpi'>
              <div className='acc-kpi__label'>
                {t('page.console.orgs.drawer.metric.spendWithRequests', {
                  count: int(reqCount),
                  defaultValue: '近 30 日消费 · {{count}} 次',
                })}
              </div>
              <div className='acc-kpi__value'>
                {summary.isPending && account.recent_spend_microusd == null ? (
                  <PendingValue chars={6} />
                ) : (
                  usd(spend30d)
                )}
              </div>
              <div className='acc-kpi__sub'>
                {usage.isPending ? (
                  <PendingValue chars={14} />
                ) : topModel?.name ? (
                  t('page.console.orgs.drawer.metric.topModel', {
                    amount: usd(topModel.amount_microusd),
                    model: topModel.name,
                    defaultValue: '主用 {{model}} ({{amount}})',
                  })
                ) : (
                  d('usage.emptyShort', '近 30 日暂无模型消耗')
                )}
              </div>
            </div>

            <div className='acc-kpi'>
              <div className='acc-kpi__label'>
                {d('usage.activeKeysLabel', '活跃 API Keys')}
              </div>
              <div className='acc-kpi__value'>
                {keys.isPending && account.key_count == null ? (
                  <PendingValue chars={3} />
                ) : (
                  <>
                    {int(operationalKeyCount)}{' '}
                    <span className='acc-kpi__unit'>
                      {d('keys.unit', '把')}
                    </span>
                  </>
                )}
              </div>
              <div className='acc-kpi__sub'>
                {summary.isPending && !primaryFramework ? (
                  <PendingValue chars={12} />
                ) : primaryFramework ? (
                  t('page.console.orgs.drawer.client.primary', {
                    client: primaryFramework,
                    defaultValue: '客户端 {{client}}',
                  })
                ) : keyTotal > 0 ? (
                  t('page.console.orgs.drawer.keys.totalHint', {
                    count: int(keyTotal),
                    defaultValue: '共 {{count}} 把密钥',
                  })
                ) : (
                  d('keys.empty', '暂无 API Key')
                )}
              </div>
            </div>
          </div>

          {/* Direct Admin Controls (2-col + 1-col) */}
          {canManage && !deleted ? (
            <div className='acc-controls'>
              <div className='acc-controls__row'>
                <Field>
                  <Label>{d('actions.pricePlanLabel', '专属折扣方案')}</Label>
                  <Select
                    options={[
                      {
                        label: d('actions.standardPlan', '标准价 (无额外折扣)'),
                        value: '0',
                      },
                      ...planOptions,
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
                  <Label>
                    {d('actions.rateLimitLabel', '并发与速率覆写 (RPM / TPM)')}
                  </Label>
                  <div className='acc-limit-pair'>
                    <Input
                      aria-label='RPM'
                      className='orgs__mono'
                      inputMode='numeric'
                      placeholder={d('actions.rpmPlaceholder', 'RPM 默认')}
                      value={draftRpm}
                      onChange={(event) => {
                        setDraftRpm(event.target.value);
                        setLimitsTouched(true);
                        setProfileTouched(true);
                      }}
                    />
                    <Input
                      aria-label='TPM'
                      className='orgs__mono'
                      inputMode='numeric'
                      placeholder={d('actions.tpmPlaceholder', 'TPM 默认')}
                      value={draftTpm}
                      onChange={(event) => {
                        setDraftTpm(event.target.value);
                        setLimitsTouched(true);
                        setProfileTouched(true);
                      }}
                    />
                  </div>
                </Field>
              </div>

              <Field>
                <Label>
                  {d('actions.noteLabel', '内部备注（仅管理员可见）')}
                </Label>
                <Input
                  placeholder={d(
                    'actions.notePlaceholder',
                    '记录商务沟通、渠道来源或特殊约定...',
                  )}
                  value={draftNote}
                  onChange={(event) => {
                    setDraftNote(event.target.value);
                    setProfileTouched(true);
                  }}
                />
              </Field>

              {extraAccessAddresses.length > 0 ? (
                <div className='acc-addr-strip'>
                  <span className='acc-addr-strip__label'>
                    {d('access.title', '专用接入地址')}
                  </span>
                  <div className='acc-addr-strip__list'>
                    {extraAccessAddresses.map((item) => {
                      const checked = item.user_ids.includes(internalUserId);
                      const label = accessKindLabel(item.kind);
                      return (
                        <div className='acc-addr-strip__row' key={item.url}>
                          <span className='acc-addr-strip__kind'>{label}</span>
                          <code className='acc-addr-strip__url'>
                            {item.url}
                          </code>
                          <Switch
                            aria-label={`${label} ${item.url}`}
                            checked={checked}
                            disabled={accessGrant.isPending || id == null}
                            onCheckedChange={(next) => {
                              if (id == null) return;
                              accessGrant.mutate({
                                enabled: next,
                                url: item.url,
                                userId: id,
                              });
                            }}
                          />
                        </div>
                      );
                    })}
                  </div>
                </div>
              ) : null}
            </div>
          ) : !deleted ? (
            <div className='acc-readonly-note'>
              {t('page.console.orgs.noPermission', {
                defaultValue: '无权编辑同级或更高等级账户的折扣与限流。',
              })}
            </div>
          ) : null}

          {/* Deep-Link Jump Bar + Calm Governance Actions */}
          <div className='acc-jumpbar'>
            <div className='acc-jumpbar__links'>
              <Link className='acc-jumpbar__link' to={usageHref}>
                {reqCount > 0
                  ? t('page.console.orgs.drawer.jump.logsCount', {
                      count: int(reqCount),
                      defaultValue: '查他请求日志 ({{count}})',
                    })
                  : d('jump.logs', '查他请求日志')}
                <ArrowUpRight aria-hidden='true' size={12} />
              </Link>
              <Link className='acc-jumpbar__link' to={sessionsHref}>
                {(detail?.agent_sessions ?? 0) > 0
                  ? t('page.console.orgs.drawer.jump.sessionsCount', {
                      count: int(detail?.agent_sessions),
                      defaultValue: '查 Agent 会话 ({{count}})',
                    })
                  : d('jump.sessions', '查 Agent 会话')}
                <ArrowUpRight aria-hidden='true' size={12} />
              </Link>
            </div>

            <div className='acc-jumpbar__actions'>
              {canToggleAdmin && !deleted ? (
                <Button
                  disabled={setRoleOrStatus.isPending}
                  size='xs'
                  type='button'
                  variant='ghost'
                  onClick={() =>
                    setConfirmAction(targetIsAdmin ? 'demote' : 'promote')
                  }
                >
                  {targetIsAdmin
                    ? d('actions.demote', '取消管理员')
                    : d('actions.promote', '设为管理员')}
                </Button>
              ) : null}
              {canManage && !deleted ? (
                <Button
                  className={
                    isFrozen(currentAccount) ? undefined : 'acc-action--danger'
                  }
                  disabled={setRoleOrStatus.isPending}
                  size='xs'
                  type='button'
                  variant='ghost'
                  onClick={() => {
                    if (isFrozen(currentAccount)) {
                      setRoleOrStatus.mutate({
                        action: 'enable',
                        target: { id: account.id, name: currentAccount.name },
                      });
                    } else {
                      setConfirmAction('disable');
                    }
                  }}
                >
                  {isFrozen(currentAccount)
                    ? d('actions.enable', '解冻')
                    : d('actions.disableMenu', '冻结...')}
                </Button>
              ) : null}
              {canDelete ? (
                <Button
                  className='acc-action--danger'
                  size='xs'
                  type='button'
                  variant='ghost'
                  onClick={() =>
                    onRequestDelete({
                      ...currentAccount,
                      username: detail?.username || account.username,
                    })
                  }
                >
                  {d('actions.delete', '删除')}
                </Button>
              ) : null}
            </div>
          </div>

          {/* Collapsible Credit / Debit Form */}
          {canManage && !deleted && adjustOpen ? (
            <div className='orgs__balance'>
              <div className='orgs__balance-head'>
                <b>{d('actions.adjustTitle', '手工充值 / 扣减余额')}</b>
                <span>{d('actions.balanceHint', '原子写入管理审计流水')}</span>
              </div>
              <div className='acc-adjust-grid'>
                <Field>
                  <Label>{d('actions.action', '动作')}</Label>
                  <Select
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
                    value={adjustDirection}
                    onValueChange={setAdjustDirection}
                  />
                </Field>
                <Field>
                  <Label>{d('actions.amount', '金额（USD）')}</Label>
                  <Input
                    className='orgs__mono'
                    inputMode='decimal'
                    placeholder='100.00'
                    value={adjustAmount}
                    onChange={(event) => setAdjustAmount(event.target.value)}
                  />
                </Field>
                <Field className='acc-adjust-grid__reason'>
                  <Label>{d('actions.reason', '原因（写入余额流水）')}</Label>
                  <Input
                    placeholder={d(
                      'actions.reasonPlaceholder',
                      '例如：线下转账 #77',
                    )}
                    value={adjustReason}
                    onChange={(event) => setAdjustReason(event.target.value)}
                  />
                </Field>
              </div>
              <div className='orgs__balance-foot'>
                <Button
                  disabled={
                    !adjustAmount.trim() ||
                    !adjustReason.trim() ||
                    adjustBalance.isPending
                  }
                  loading={adjustBalance.isPending}
                  size='sm'
                  type='button'
                  variant='primary'
                  onClick={() => adjustBalance.mutate()}
                >
                  {d('actions.apply', '执行调整')}
                </Button>
              </div>
            </div>
          ) : null}
        </div>

        {/* 6. Pinned Footer */}
        <div className='acc-card__foot'>
          <div>
            {canManage && !deleted ? (
              <Button
                aria-expanded={adjustOpen}
                size='sm'
                type='button'
                variant='secondary'
                onClick={() => setAdjustOpen((value) => !value)}
              >
                {adjustOpen ? null : <Plus size={14} />}
                {adjustOpen
                  ? d('actions.collapseAdjust', '收起调账')
                  : d('actions.openAdjust', '手工充值 / 扣减')}
              </Button>
            ) : null}
          </div>
          <div className='acc-card__foot-right'>
            <Button
              size='sm'
              type='button'
              variant='secondary'
              onClick={() => guardAction('close', () => onOpenChange(false))}
            >
              {t('common.close', { defaultValue: '关闭' })}
            </Button>
            {canManage && !deleted ? (
              <Button
                disabled={!profileTouched || saveProfile.isPending}
                loading={saveProfile.isPending}
                size='sm'
                type='button'
                variant='primary'
                onClick={() => saveProfile.mutate()}
              >
                {d('actions.saveChanges', '保存修改')}
              </Button>
            ) : null}
          </div>
        </div>
      </ModalFrame>

      {/* Explicit Confirmation Modal for Admin Role & Account Freeze */}
      <Modal
        busy={setRoleOrStatus.isPending}
        description={confirmation?.description}
        footer={
          <>
            <Button
              size='sm'
              variant='secondary'
              onClick={() => setConfirmAction(null)}
            >
              {d('cancel', '取消')}
            </Button>
            <Button
              loading={setRoleOrStatus.isPending}
              size='sm'
              variant='soft-danger'
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

      {/* Dirty-State Guard Modal before closing or switching users */}
      <Modal
        description={d(
          'discard.description',
          '当前客户有未保存的折扣、限流或备注修改，离开后改动将丢失。',
        )}
        footer={
          <>
            <Button
              size='sm'
              variant='secondary'
              onClick={() => setPendingNav(null)}
            >
              {d('discard.keep', '继续编辑')}
            </Button>
            <Button
              size='sm'
              variant='primary'
              onClick={() => {
                const nav = pendingNav;
                setPendingNav(null);
                setProfileTouched(false);
                setAdjustAmount('');
                if (nav === 'prev') onPrevious();
                else if (nav === 'next') onNext();
                else onOpenChange(false);
              }}
            >
              {d('discard.confirm', '放弃修改')}
            </Button>
          </>
        }
        open={pendingNav !== null}
        title={d('discard.title', '放弃未保存的修改？')}
        onOpenChange={(next) => {
          if (!next) setPendingNav(null);
        }}
      />
    </>
  );
}

function SignupInline({ signup }: { signup: AccountSignup }) {
  const { t } = useTranslation();
  const createdAt = Number(signup.created_at || 0);
  if (createdAt <= 0) return null;
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
  const label = t('page.console.orgs.drawer.signupShort', {
    date: inferred ? `约 ${dateText}` : dateText,
    defaultValue: '注册于 {{date}}',
  });
  if (!inferred) return <span>{label}</span>;
  return (
    <Tooltip
      content={t('page.console.log.session.account.inferredTip', {
        defaultValue:
          '这个账户注册时还没有记录注册时间，这是按最早的活动记录推算的，实际注册不晚于这一天。',
      })}
    >
      <span className='acc-inferred-date'>{label}</span>
    </Tooltip>
  );
}
