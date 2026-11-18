import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { Check, ChevronDown, Copy, Plus, Search, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import {
  type Account,
  type PlatformSummary,
  adjacentAccountTarget,
  daysToEmpty,
  initials,
  int,
  isDeleted,
  isFrozen,
  isZeroBalance,
  providerLabel,
  toAccount,
  usd,
} from './account-model';
import AccountDrawer from './AccountDrawer';
import useAuthStatus from '../../components/auth/useAuthStatus';
import { Badge } from '../../components/ui/badge';
import { Button, buttonVariants } from '../../components/ui/button';
import ConsolePage from '../../components/ui/console-page';
import { DataSurface } from '../../components/ui/data-surface';
import { DataTableShell } from '../../components/ui/data-table-shell';
import { EmptyState } from '../../components/ui/empty-state';
import { ErrorState } from '../../components/ui/error-state';
import { Field, Label } from '../../components/ui/field';
import { IconButton } from '../../components/ui/icon-button';
import { Input } from '../../components/ui/input';
import { Modal } from '../../components/ui/modal';
import { Pagination } from '../../components/ui/pagination';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '../../components/ui/popover';
import { GateModal } from '../../components/ui/project-primitives';
import { Select } from '../../components/ui/select';
import {
  DEFAULT_PAGE_SIZE_OPTIONS,
  coerceItemsPayload,
  createSearchParams,
} from '../../components/ui/table-tools';
import { notify } from '../../components/ui/toast';
import { useSession } from '../../context/User';
import { API } from '../../helpers/api';
import {
  copy,
  getUserFromLocalStorage,
  isRoot,
  showError,
} from '../../helpers/utils';
import {
  apiErrorHandler,
  errorMessage,
  isRefusal,
  unwrap,
} from '../../lib/api-error';
import { usePricePlansQuery } from '../ModelOps/usePricePlans';
import type { TFunction } from 'i18next';
import './organizations.css';

type AccountFilters = {
  keyword: string;
  order: string;
  /** 'all' | 'any' | 'none' | a plan id — the 专属折扣 chip. */
  pricePlan: string;
  provider: string;
  /** Server-side cohort filter ('active_30d' | 'topped_up' | 'low_balance_active' | 'zero_topped_up'). */
  quick: string;
  status: string;
  /** Secondary scope: '' | 'admin' | 'deleted'. */
  view: string;
};

type AccountPage = {
  items: Account[];
  page: number;
  pageSize: number;
  total: number;
};

const DEFAULT_PAGE_SIZE = 20;

const emptyFilters: AccountFilters = {
  keyword: '',
  order: 'default',
  pricePlan: 'all',
  provider: 'all',
  quick: '',
  status: 'all',
  view: '',
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);

type CreateForm = {
  display_name: string;
  email: string;
  /** '0' = standard prices. */
  price_plan_id: string;
  password: string;
  quota: string;
  remark: string;
  username: string;
};

const emptyCreateForm: CreateForm = {
  display_name: '',
  email: '',
  price_plan_id: '0',
  password: '',
  quota: '',
  remark: '',
  username: '',
};

/** Balance with its runway underneath. Red is reserved for a paying
 *  customer who has actually run dry; an account that never topped up reads
 *  as quiet grey instead of an alarm. */
function BalanceCell({ account, t }: { account: Account; t: TFunction }) {
  const balance = usd(account.balance_microusd);
  if (isFrozen(account)) {
    return (
      <div className='orgs__cell orgs__cell--end' data-tone='muted'>
        <span className='orgs__num'>{balance}</span>
        <span className='orgs__sub'>
          {t('page.console.orgs.balance.frozen', { defaultValue: '冻结中' })}
        </span>
      </div>
    );
  }
  if (isZeroBalance(account)) {
    const paid =
      account.has_topped_up || (account.topup_total_microusd ?? 0) > 0;
    return (
      <div
        className='orgs__cell orgs__cell--end'
        data-tone={paid ? 'danger' : 'muted'}
      >
        <span className='orgs__num'>{balance}</span>
        <span className='orgs__sub'>
          {paid
            ? t('page.console.orgs.burndown.exhausted', {
                defaultValue: '已耗尽',
              })
            : t('page.console.orgs.balance.neverToppedUp', {
                defaultValue: '未充值',
              })}
        </span>
      </div>
    );
  }
  const days = daysToEmpty(account);
  if (days == null) {
    const spent = account.recent_spend_microusd ?? 0;
    return (
      <div className='orgs__cell orgs__cell--end'>
        <span className='orgs__num'>{balance}</span>
        <span className='orgs__sub'>
          {spent > 0
            ? t('page.console.orgs.burndown.sufficient', {
                defaultValue: '充足',
              })
            : account.request_count_recent
              ? t('page.console.orgs.burndown.trialActive', {
                  defaultValue: '试用中',
                })
              : t('page.console.orgs.burndown.trialIdle', {
                  defaultValue: '试用未动',
                })}
        </span>
      </div>
    );
  }
  const rounded = Math.max(1, Math.round(days));
  return (
    <div
      className='orgs__cell orgs__cell--end'
      data-tone={days <= 7 ? 'warning' : undefined}
    >
      <span className='orgs__num'>{balance}</span>
      <span className='orgs__sub'>
        {t('page.console.orgs.burndown.days', {
          count: rounded,
          defaultValue: '≈ {{count}} 天',
        })}
      </span>
    </div>
  );
}

type FilterOption = { label: string; value: string };

/** One filter as a compact chip: dashed "+ 专属折扣" while unset, the chosen
 *  value once set. Picking applies immediately; there is no search button. */
function FilterChip({
  allLabel,
  label,
  onChange,
  options,
  value,
}: {
  allLabel: string;
  label: string;
  onChange: (value: string) => void;
  options: FilterOption[];
  value: string;
}) {
  const [open, setOpen] = useState(false);
  const selected =
    value === 'all' ? null : options.find((option) => option.value === value);
  const pick = (next: string) => {
    onChange(next);
    setOpen(false);
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger>
        <Button
          className='orgs__chip'
          data-active={selected ? 'true' : undefined}
          data-open={open ? 'true' : undefined}
          size='sm'
          variant='ghost'
        >
          {selected ? (
            <>
              <span className='orgs__chip-key'>{label}</span>
              {selected.label}
              <ChevronDown aria-hidden='true' size={13} />
            </>
          ) : (
            <>
              <Plus aria-hidden='true' size={13} />
              {label}
            </>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align='start' className='orgs__chip-menu'>
        <p className='orgs__chip-menu-title'>{label}</p>
        {[{ label: allLabel, value: 'all' }, ...options].map((option) => (
          <Button
            className='orgs__chip-option'
            data-selected={option.value === value ? 'true' : undefined}
            key={option.value}
            size='sm'
            variant='ghost'
            onClick={() => pick(option.value)}
          >
            <span>{option.label}</span>
            {option.value === value ? (
              <Check aria-hidden='true' size={14} />
            ) : null}
          </Button>
        ))}
      </PopoverContent>
    </Popover>
  );
}

export default function OrganizationsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const userSession = useSession();
  const [searchParams, setSearchParams] = useSearchParams();
  const { status: authStatus } = useAuthStatus();
  const configuredProviderIDs = new Set(
    (authStatus?.providers || []).map((provider) => provider.id),
  );

  const callerRole =
    Number(userSession.user?.role) ||
    Number(getUserFromLocalStorage()?.role) ||
    0;
  const callerId = userSession.user?.id ?? getUserFromLocalStorage()?.id;

  const [filters, setFilters] = useState<AccountFilters>(() => ({
    ...emptyFilters,
    quick: [
      'active_30d',
      'topped_up',
      'low_balance_active',
      'zero_topped_up',
    ].includes(searchParams.get('filter') || '')
      ? searchParams.get('filter')!
      : '',
  }));
  const [keywordDraft, setKeywordDraft] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);

  const [creating, setCreating] = useState(false);
  const [createForm, setCreateForm] = useState<CreateForm>(emptyCreateForm);
  const [deleting, setDeleting] = useState<Account | null>(null);
  const [activeAccount, setActiveAccount] = useState<Account | null>(null);
  const [adjacentLoading, setAdjacentLoading] = useState(false);
  const [drawerDirty, setDrawerDirty] = useState(false);

  const plans = usePricePlansQuery(isRoot());

  const platform = useQuery<PlatformSummary>({
    queryKey: ['organizations-summary'],
    queryFn: async () => {
      const data = unwrap<PlatformSummary | null>(
        await API.get('/api/user/summary', {
          skipErrorHandler: true,
        }),
      );
      return data || ({} as PlatformSummary);
    },
  });

  const params = useMemo(
    () => ({ filters, page, pageSize }),
    [filters, page, pageSize],
  );

  const fetchAccountPage = async (request: typeof params) => {
    const keyword = request.filters.keyword.trim();
    const useSearch = Boolean(keyword);
    const endpoint = useSearch ? '/api/user/search' : '/api/user/';
    const { pricePlan, view } = request.filters;
    const query = createSearchParams({
      p: request.page,
      page_size: request.pageSize,
      keyword: useSearch ? keyword : undefined,
      price_plan: pricePlan !== 'all' ? pricePlan : undefined,
      status:
        request.filters.status === 'all' ? undefined : request.filters.status,
      order:
        request.filters.order === 'default' ? undefined : request.filters.order,
      filter: request.filters.quick || undefined,
      provider:
        request.filters.provider === 'all'
          ? undefined
          : request.filters.provider,
      role:
        pricePlan === 'any' || view === 'deleted'
          ? undefined
          : view === 'admin'
            ? '100'
            : '1',
      exclude_deleted: view === 'deleted' ? undefined : 'true',
      deleted: view === 'deleted' ? 'only' : undefined,
      with_usage: 'true',
    });
    const response = await API.get(`${endpoint}?${query}`);
    unwrap(response);
    const pageData = coerceItemsPayload<Record<string, unknown>>(
      response.data,
      request.page,
      request.pageSize,
    );
    return {
      items: pageData.items.filter(isRecord).map(toAccount),
      page: pageData.page,
      pageSize: pageData.pageSize,
      total: pageData.total,
    };
  };

  const accounts = useQuery<AccountPage>({
    queryKey: ['organizations', params],
    placeholderData: keepPreviousData,
    queryFn: () => fetchAccountPage(params),
  });

  const rows = accounts.isError ? [] : accounts.data?.items || [];
  const total = accounts.isError ? 0 : (accounts.data?.total ?? 0);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['organizations'] });
    void queryClient.invalidateQueries({ queryKey: ['organizations-summary'] });
  };

  // Sync the open modal with ?account=<username>.
  const accountParam = searchParams.get('account');
  useEffect(() => {
    if (!accountParam) {
      setActiveAccount(null);
      return;
    }
    setActiveAccount(null);
    const match = rows.find(
      (row) => row.username === accountParam || String(row.id) === accountParam,
    );
    if (match) {
      setActiveAccount(match);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const data = unwrap<unknown>(
          await API.get('/api/user/lookup', {
            params: { account: accountParam },
            skipErrorHandler: true,
          }),
        );
        if (cancelled || !isRecord(data)) return;
        setActiveAccount(toAccount(data));
      } catch {
        // The list remains usable while the unresolved modal stays closed.
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountParam]);

  const openAccount = (account: Account) => {
    setActiveAccount(account);
    const next = new URLSearchParams(searchParams);
    next.set('account', String(account.id));
    setSearchParams(next, { replace: true });
  };

  const closeAccount = () => {
    setActiveAccount(null);
    setDrawerDirty(false);
    const next = new URLSearchParams(searchParams);
    next.delete('account');
    setSearchParams(next, { replace: true });
  };

  const loadedPage = accounts.data?.page || page;
  const loadedPageSize = accounts.data?.pageSize || pageSize;
  const previousTarget = adjacentAccountTarget(
    rows,
    activeAccount?.id,
    loadedPage,
    loadedPageSize,
    total,
    'previous',
  );
  const nextTarget = adjacentAccountTarget(
    rows,
    activeAccount?.id,
    loadedPage,
    loadedPageSize,
    total,
    'next',
  );
  const openAdjacentAccount = async (direction: 'next' | 'previous') => {
    if (adjacentLoading) return;
    const target = direction === 'next' ? nextTarget : previousTarget;
    if (!target) return;
    if (target.kind === 'row') {
      openAccount(target.account);
      return;
    }
    setAdjacentLoading(true);
    try {
      const nextParams = {
        filters,
        page: target.page,
        pageSize: loadedPageSize,
      };
      const nextPage = await queryClient.fetchQuery({
        queryKey: ['organizations', nextParams],
        queryFn: () => fetchAccountPage(nextParams),
      });
      const neighbor =
        target.pick === 'first'
          ? nextPage.items[0]
          : nextPage.items[nextPage.items.length - 1];
      if (!neighbor) return;
      setPage(target.page);
      openAccount(neighbor);
    } catch (error) {
      showError(
        error,
        t('page.console.orgs.loadFailed', { defaultValue: '加载失败' }),
      );
    } finally {
      setAdjacentLoading(false);
    }
  };
  const openPreviousAccount = () => void openAdjacentAccount('previous');
  const openNextAccount = () => void openAdjacentAccount('next');

  useEffect(() => {
    if (!activeAccount || drawerDirty) return;
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (
        target?.matches(
          'input, textarea, select, button, [contenteditable=true]',
        )
      ) {
        return;
      }
      if (event.key.toLowerCase() === 'j' && nextTarget) {
        event.preventDefault();
        openNextAccount();
      } else if (event.key.toLowerCase() === 'k' && previousTarget) {
        event.preventDefault();
        openPreviousAccount();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  });

  const create = useMutation({
    mutationFn: async (form: CreateForm) => {
      const quota = form.quota.trim();
      const parsedQuota = Number(quota);
      if (quota && (!Number.isFinite(parsedQuota) || parsedQuota < 0)) {
        throw new Error(
          t('page.console.orgs.invalidAmount', {
            defaultValue: '请输入有效金额',
          }),
        );
      }
      unwrap(
        await API.post('/api/user/', {
          username: form.username.trim(),
          password: form.password,
          display_name: form.display_name.trim() || form.username.trim(),
          email: form.email.trim(),
          price_plan_id: Number(form.price_plan_id) || 0,
          remark: form.remark.trim(),
          ...(quota ? { quota: Math.round(parsedQuota * 1_000_000) } : {}),
        }),
      );
    },
    onSuccess: () => {
      notify.success(
        t('page.console.orgs.created', { defaultValue: '客户账户已创建' }),
      );
      setCreating(false);
      setCreateForm(emptyCreateForm);
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const remove = useMutation({
    mutationFn: async (account: Account) => {
      unwrap(
        await API.delete(`/api/user/${account.id}`, {
          skipErrorHandler: true,
        }),
      );
    },
    onSuccess: () => {
      notify.success(
        t('page.console.orgs.deleted', { defaultValue: '已删除' }),
      );
      setDeleting(null);
      closeAccount();
      invalidate();
    },
  });
  const closeDeleting = () => {
    setDeleting(null);
    remove.reset();
  };
  const deleteError = remove.error
    ? isRefusal(remove.error)
      ? t('page.console.orgs.deleteFailed', {
          reason: errorMessage(remove.error),
        })
      : errorMessage(remove.error)
    : undefined;

  const planOptions = useMemo(
    () =>
      (plans.data || []).map((plan) => ({
        label: plan.name,
        value: String(plan.id),
      })),
    [plans.data],
  );

  const updateFilters = (patch: Partial<AccountFilters>) => {
    setFilters((current) => ({ ...current, ...patch }));
    setPage(1);
  };

  // Search applies as you type (debounced); Enter applies at once.
  useEffect(() => {
    const keyword = keywordDraft.trim();
    if (keyword === filters.keyword) return;
    const handle = window.setTimeout(() => updateFilters({ keyword }), 300);
    return () => window.clearTimeout(handle);
  }, [keywordDraft, filters.keyword]);

  // "/" jumps to search from anywhere on the page.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== '/' || event.metaKey || event.ctrlKey) return;
      const target = event.target as HTMLElement | null;
      if (target?.matches('input, textarea, select, [contenteditable=true]')) {
        return;
      }
      event.preventDefault();
      searchRef.current?.focus();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  const scopeChipValue =
    filters.view === 'admin'
      ? 'admin'
      : filters.view === 'deleted'
        ? 'deleted'
        : filters.quick === 'zero_topped_up'
          ? 'zero_topped_up'
          : filters.status;

  const handleScopeChange = (next: string) => {
    if (next === 'admin' || next === 'deleted') {
      updateFilters({ quick: '', status: 'all', view: next });
    } else if (next === 'zero_topped_up') {
      updateFilters({ quick: 'zero_topped_up', status: 'all', view: '' });
    } else {
      updateFilters({
        quick: filters.quick === 'zero_topped_up' ? '' : filters.quick,
        status: next,
        view: '',
      });
    }
  };

  const hasChipFilters =
    filters.pricePlan !== 'all' ||
    filters.provider !== 'all' ||
    scopeChipValue !== 'all' ||
    Boolean(filters.keyword);

  const clearFilters = () => {
    setKeywordDraft('');
    setFilters((current) => ({
      ...emptyFilters,
      quick: current.quick === 'zero_topped_up' ? '' : current.quick,
      order: current.order,
    }));
    setPage(1);
  };

  const copyAccountId = async (value: string) => {
    if (await copy(value)) {
      notify.success(
        t('page.console.orgs.copyIdSuccess', { defaultValue: 'ID 已复制' }),
      );
    }
  };

  const providerText = (provider: string) => {
    if (provider === 'email') {
      return t('page.console.orgs.drawer.provider.email', {
        defaultValue: '邮箱验证码',
      });
    }
    if (provider === 'password') {
      return t('page.console.orgs.drawer.provider.password', {
        defaultValue: '密码',
      });
    }
    return providerLabel(provider);
  };

  const formatRelativeTime = (unixSeconds?: number | null) => {
    if (!unixSeconds) {
      return t('page.console.orgs.time.never', { defaultValue: '从未调用' });
    }
    const seconds = Math.max(0, Math.floor(Date.now() / 1000) - unixSeconds);
    if (seconds < 60) {
      return t('page.console.orgs.time.now', { defaultValue: '刚刚' });
    }
    if (seconds < 3600) {
      return t('page.console.orgs.time.minutes', {
        count: Math.floor(seconds / 60),
        defaultValue: '{{count}} 分钟前',
      });
    }
    if (seconds < 86400) {
      return t('page.console.orgs.time.hours', {
        count: Math.floor(seconds / 3600),
        defaultValue: '{{count}} 小时前',
      });
    }
    const days = Math.floor(seconds / 86400);
    if (days < 30) {
      return t('page.console.orgs.time.days', {
        count: days,
        defaultValue: '{{count}} 天前',
      });
    }
    return t('page.console.orgs.time.months', {
      count: Math.floor(days / 30),
      defaultValue: '{{count}} 个月前',
    });
  };

  const summary = platform.data || {};
  const cohortTabs: {
    key: string;
    label: string;
    quick: string;
    tone?: 'danger' | 'warning';
    value?: number;
  }[] = [
    {
      key: 'all',
      label: t('page.console.orgs.fact.all.label', {
        defaultValue: '全部账户',
      }),
      value: summary.total_customers,
      quick: '',
    },
    {
      key: 'active',
      label: t('page.console.orgs.fact.active.label', {
        defaultValue: '近 30 日活跃',
      }),
      value: summary.active_30d,
      quick: 'active_30d',
    },
    {
      key: 'topped',
      label: t('page.console.orgs.fact.topped.label', {
        defaultValue: '已充值过',
      }),
      value: summary.topped_up,
      quick: 'topped_up',
    },
    {
      key: 'low',
      label: t('page.console.orgs.fact.low.label', {
        defaultValue: '低余额还在用',
      }),
      value: summary.low_balance_active,
      quick: 'low_balance_active',
      tone: 'warning',
    },
  ];

  const activeTab = filters.view
    ? ''
    : (cohortTabs.find((tab) => tab.quick === filters.quick)?.key ?? '');

  const selectTab = (quick: string) => {
    updateFilters({
      quick,
      status: 'all',
      view: '',
    });
  };

  // Numeric columns right-align their header with their figures.
  const endHeader = (label: string) => () => (
    <span className='orgs__th-end'>{label}</span>
  );

  const columns = [
    {
      id: 'internal_id',
      header: t('page.console.orgs.column.id', { defaultValue: 'ID' }),
      cell: ({ row }: { row: { original: Account } }) => {
        const value = String(row.original.internal_id ?? row.original.id);
        return (
          <span className='orgs__uid'>
            <span className='orgs__mono'>#{value}</span>
            <IconButton
              aria-label={t('page.console.orgs.copyId', {
                defaultValue: '复制 ID',
              })}
              className='orgs__uid-copy'
              size='xs'
              onClick={(event) => {
                event.stopPropagation();
                void copyAccountId(value);
              }}
            >
              <Copy size={13} />
            </IconButton>
          </span>
        );
      },
    },
    {
      id: 'name',
      header: t('page.console.orgs.column.account', { defaultValue: '账户' }),
      cell: ({ row }: { row: { original: Account } }) => {
        const account = row.original;
        const frozen = isFrozen(account);
        const secondary = [
          account.email ||
            t('page.console.orgs.identity.unboundEmail', {
              defaultValue: '未绑定邮箱',
            }),
          account.username && account.username !== account.name
            ? account.username
            : '',
        ]
          .filter(Boolean)
          .join(' · ');
        return (
          <div className='orgs__who'>
            <span aria-hidden='true' className='orgs__avatar'>
              {initials(account)}
            </span>
            <span className='orgs__who-copy'>
              <span
                className='orgs__who-name'
                data-frozen={frozen || undefined}
              >
                <button
                  className='orgs__who-open'
                  type='button'
                  onClick={(event) => {
                    event.stopPropagation();
                    openAccount(account);
                  }}
                >
                  <strong>{account.name}</strong>
                </button>
                {isDeleted(account) ? (
                  <Badge variant='neutral'>
                    {t('page.console.orgs.status.deleted', {
                      defaultValue: '已注销',
                    })}
                  </Badge>
                ) : frozen ? (
                  <Badge variant='danger'>
                    {t('page.console.orgs.status.frozen', {
                      defaultValue: '已冻结',
                    })}
                  </Badge>
                ) : null}
              </span>
              <span className='orgs__sub'>{secondary}</span>
            </span>
          </div>
        );
      },
    },
    {
      id: 'plan',
      header: t('page.console.orgs.column.planLogin', {
        defaultValue: '折扣 · 登录',
      }),
      cell: ({ row }: { row: { original: Account } }) => {
        const provider = row.original.login_providers?.[0];
        return (
          <div className='orgs__cell'>
            <span>
              {row.original.price_plan_id ? (
                <Badge variant='info'>
                  {row.original.price_plan_name ||
                    `#${row.original.price_plan_id}`}
                </Badge>
              ) : (
                <span className='orgs__standard'>
                  {t('page.console.orgs.plan.standard', {
                    defaultValue: '标准价',
                  })}
                </span>
              )}
            </span>
            <span className='orgs__sub'>
              {provider
                ? providerText(provider)
                : t('page.console.orgs.identity.providerUnavailable', {
                    defaultValue: '无可用方式',
                  })}
            </span>
          </div>
        );
      },
    },
    {
      id: 'credit',
      header: endHeader(
        t('page.console.orgs.column.credit', { defaultValue: '累计额度' }),
      ),
      cell: ({ row }: { row: { original: Account } }) => {
        const account = row.original;
        const used = account.used_quota_microusd ?? 0;
        const credit = (account.balance_microusd ?? 0) + used;
        if (credit <= 0) {
          return (
            <div className='orgs__cell orgs__cell--end' data-tone='muted'>
              <span className='orgs__num'>—</span>
            </div>
          );
        }
        return (
          <div className='orgs__cell orgs__cell--end'>
            <span className='orgs__num'>{usd(credit)}</span>
            <span className='orgs__sub orgs__num'>
              {t('page.console.orgs.credit.used', {
                amount: usd(used),
                defaultValue: '已用 {{amount}}',
              })}
            </span>
          </div>
        );
      },
    },
    {
      id: 'recent',
      header: endHeader(
        t('page.console.orgs.column.recent', { defaultValue: '近 30 日' }),
      ),
      cell: ({ row }: { row: { original: Account } }) => {
        const account = row.original;
        const idle =
          !account.recent_spend_microusd && !account.request_count_recent;
        if (idle) {
          return (
            <div className='orgs__cell orgs__cell--end' data-tone='muted'>
              <span className='orgs__num'>—</span>
            </div>
          );
        }
        return (
          <div className='orgs__cell orgs__cell--end'>
            <span className='orgs__num'>
              {usd(account.recent_spend_microusd)}
            </span>
            <span className='orgs__sub orgs__num'>
              {t('page.console.orgs.recent.requests', {
                count: int(account.request_count_recent),
                defaultValue: '{{count}} 请求',
              })}
            </span>
          </div>
        );
      },
    },
    {
      id: 'balance',
      header: endHeader(
        t('page.console.orgs.column.balanceRunway', {
          defaultValue: '余额 · 可用',
        }),
      ),
      cell: ({ row }: { row: { original: Account } }) => (
        <BalanceCell account={row.original} t={t} />
      ),
    },
    {
      id: 'active',
      header: t('page.console.orgs.column.lastActiveClient', {
        defaultValue: '最近活跃 · 客户端',
      }),
      cell: ({ row }: { row: { original: Account } }) => {
        const top = row.original.client_top;
        const attribution = [top?.framework, top?.country]
          .filter(Boolean)
          .join(' · ');
        return (
          <div
            className='orgs__cell'
            data-tone={row.original.last_used_at ? undefined : 'muted'}
          >
            <span className='orgs__plain'>
              {formatRelativeTime(row.original.last_used_at)}
            </span>
            <span className='orgs__sub'>{attribution || '—'}</span>
          </div>
        );
      },
    },
  ];

  return (
    <ConsolePage
      appearance='review'
      className='users-page--compact orgs-ledger'
      description={t('page.console.orgs.description', {
        defaultValue: '平台客户的额度、消费、余额与访问状态。',
      })}
      title={t('page.console.orgs.title', { defaultValue: '用户' })}
      tabs={
        <div
          aria-label={t('page.console.orgs.viewsLabel', {
            defaultValue: '视图',
          })}
          className='orgs__tabs'
          role='tablist'
        >
          {cohortTabs.map((tab) => {
            const active = activeTab === tab.key;
            return (
              <button
                aria-selected={active}
                className={`orgs__tab${active ? ' is-active' : ''}`}
                data-owns-chrome=''
                key={tab.key}
                role='tab'
                type='button'
                onClick={() => selectTab(tab.quick)}
              >
                <span>{tab.label}</span>
                {platform.isPending || tab.value == null ? null : (
                  <span className='orgs__view-count' data-tone={tab.tone}>
                    {int(tab.value)}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      }
      actions={
        <>
          <Link
            className={buttonVariants({ variant: 'secondary' })}
            to='/console/price-plans'
          >
            {t('page.console.modelOps.nav.pricePlans', {
              defaultValue: '专属折扣',
            })}
          </Link>
          <Button variant='primary' onClick={() => setCreating(true)}>
            <Plus size={15} />
            {t('page.console.orgs.create', { defaultValue: '新建用户' })}
          </Button>
        </>
      }
    >
      <div className='orgs__toolbar'>
        <label className='orgs__search'>
          <Search aria-hidden='true' size={15} />
          <input
            aria-label={t('page.console.orgs.filter.keywordLabel', {
              defaultValue: '搜索',
            })}
            placeholder={t('page.console.orgs.filter.searchPlaceholder', {
              defaultValue: '搜索名称、用户名、邮箱或 #ID',
            })}
            ref={searchRef}
            value={keywordDraft}
            onChange={(event) => setKeywordDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                updateFilters({ keyword: keywordDraft.trim() });
              } else if (event.key === 'Escape') {
                setKeywordDraft('');
              }
            }}
          />
          {keywordDraft ? null : (
            <kbd aria-hidden='true' className='orgs__kbd'>
              /
            </kbd>
          )}
        </label>

        <FilterChip
          allLabel={t('page.console.orgs.filter.planAll', {
            defaultValue: '全部',
          })}
          label={t('page.console.orgs.filter.planLabel', {
            defaultValue: '专属折扣',
          })}
          options={[
            {
              label: t('page.console.orgs.filter.planAny', {
                defaultValue: '有专属折扣',
              }),
              value: 'any',
            },
            ...planOptions,
            {
              label: t('page.console.orgs.filter.planNone', {
                defaultValue: '无',
              }),
              value: 'none',
            },
          ]}
          value={filters.pricePlan}
          onChange={(pricePlan) => updateFilters({ pricePlan })}
        />

        <FilterChip
          allLabel={t('page.console.orgs.filter.providerAll', {
            defaultValue: '全部方式',
          })}
          label={t('page.console.orgs.filter.providerLabel', {
            defaultValue: '登录方式',
          })}
          options={[
            ...(authStatus?.password_login
              ? [
                  {
                    label: t('page.console.orgs.filter.providerPassword', {
                      defaultValue: '密码',
                    }),
                    value: 'password',
                  },
                ]
              : []),
            ...(authStatus?.email_code_auth
              ? [
                  {
                    label: t('page.console.orgs.filter.providerEmail', {
                      defaultValue: '邮箱验证码',
                    }),
                    value: 'email',
                  },
                ]
              : []),
            ...(configuredProviderIDs.has('github')
              ? [{ label: 'GitHub', value: 'github' }]
              : []),
            ...(configuredProviderIDs.has('google')
              ? [{ label: 'Google', value: 'google' }]
              : []),
            ...(configuredProviderIDs.has('oidc')
              ? [{ label: 'OIDC', value: 'oidc' }]
              : []),
            {
              label: t('page.console.orgs.filter.providerUnbound', {
                defaultValue: '未绑定',
              }),
              value: 'unbound',
            },
          ]}
          value={filters.provider}
          onChange={(provider) => updateFilters({ provider })}
        />

        <FilterChip
          allLabel={t('page.console.orgs.filter.statusAll', {
            defaultValue: '全部状态',
          })}
          label={t('page.console.orgs.filter.statusLabel', {
            defaultValue: '状态',
          })}
          options={[
            {
              label: t('page.console.orgs.drawer.status.ok', {
                defaultValue: '正常',
              }),
              value: 'enabled',
            },
            {
              label: t('page.console.orgs.filter.statusDisabled', {
                defaultValue: '已冻结',
              }),
              value: 'disabled',
            },
            {
              label: t('page.console.orgs.fact.zero.label', {
                defaultValue: '零余额且充过值',
              }),
              value: 'zero_topped_up',
            },
            {
              label: t('page.console.orgs.fact.admin.label', {
                defaultValue: '管理员',
              }),
              value: 'admin',
            },
            {
              label: t('page.console.orgs.fact.deleted.label', {
                defaultValue: '已注销',
              }),
              value: 'deleted',
            },
          ]}
          value={scopeChipValue}
          onChange={handleScopeChange}
        />

        {hasChipFilters ? (
          <Button
            className='orgs__clear'
            size='sm'
            variant='ghost'
            onClick={clearFilters}
          >
            <X aria-hidden='true' size={13} />
            {t('page.console.orgs.filter.clear', {
              defaultValue: '清除筛选',
            })}
          </Button>
        ) : null}

        <div className='orgs__sort'>
          <span className='orgs__sort-label'>
            {t('page.console.orgs.filter.orderLabel', {
              defaultValue: '排序',
            })}
          </span>
          <Select
            className='orgs__sort-select'
            options={[
              {
                label: t('page.console.orgs.filter.orderDefault', {
                  defaultValue: '最新加入',
                }),
                value: 'default',
              },
              {
                label: t('page.console.orgs.filter.orderBalanceAsc', {
                  defaultValue: '余额从低到高',
                }),
                value: 'balance_asc',
              },
              {
                label: t('page.console.orgs.filter.orderSpendDesc', {
                  defaultValue: '累计消费从高到低',
                }),
                value: 'spend_desc',
              },
              {
                label: t('page.console.orgs.filter.orderCreditDesc', {
                  defaultValue: '累计额度从高到低',
                }),
                value: 'credit_desc',
              },
            ]}
            size='sm'
            value={filters.order}
            onValueChange={(order) => updateFilters({ order })}
          />
        </div>
      </div>

      <DataSurface className='lazu-table-page-surface'>
        {accounts.isError ? (
          <ErrorState
            action={
              <Button variant='ghost' onClick={() => void accounts.refetch()}>
                {t('legacy.i18n.retry.e2d53a6d3a')}
              </Button>
            }
            title={t('page.console.orgs.loadFailed', {
              defaultValue: '加载失败',
            })}
          />
        ) : (
          <>
            <DataTableShell
              columns={columns}
              data={rows}
              empty={
                <EmptyState
                  description={t('page.console.orgs.empty.description', {
                    defaultValue: '用户注册后会作为客户账户出现在这里。',
                  })}
                  title={t('page.console.orgs.empty.title', {
                    defaultValue: '还没有客户账户',
                  })}
                />
              }
              loading={accounts.isLoading}
              rowKey='id'
              selectedRowKey={activeAccount?.id ?? null}
              onRowClick={(record) => openAccount(record as Account)}
            />

            <Pagination
              className='mt-4'
              currentPage={accounts.data?.page || page}
              pageSize={accounts.data?.pageSize || pageSize}
              pageSizeOpts={DEFAULT_PAGE_SIZE_OPTIONS}
              pending={accounts.isPending}
              showSizeChanger
              total={total}
              onPageChange={setPage}
              onPageSizeChange={(nextSize) => {
                setPageSize(nextSize);
                setPage(1);
              }}
            />
          </>
        )}
      </DataSurface>

      <AccountDrawer
        account={activeAccount}
        callerId={callerId}
        callerRole={callerRole}
        canGoNext={Boolean(nextTarget) && !adjacentLoading}
        canGoPrevious={Boolean(previousTarget) && !adjacentLoading}
        open={Boolean(activeAccount)}
        planOptions={planOptions}
        onChanged={invalidate}
        onDirtyChange={setDrawerDirty}
        onNext={openNextAccount}
        onOpenChange={(next) => {
          if (!next) closeAccount();
        }}
        onPrevious={openPreviousAccount}
        onRequestDelete={(account) => setDeleting(account)}
      />

      <Modal
        body={
          <div className='orgs__form'>
            <div className='orgs__grid'>
              <Field>
                <Label htmlFor='org-create-username'>
                  {t('page.console.orgs.field.username', {
                    defaultValue: '登录名',
                  })}
                </Label>
                <Input
                  id='org-create-username'
                  value={createForm.username}
                  onChange={(event) =>
                    setCreateForm({
                      ...createForm,
                      username: event.target.value,
                    })
                  }
                />
              </Field>
              <Field>
                <Label htmlFor='org-create-password'>
                  {t('page.console.orgs.field.password', {
                    defaultValue: '初始密码',
                  })}
                </Label>
                <Input
                  autoComplete='new-password'
                  id='org-create-password'
                  type='password'
                  value={createForm.password}
                  onChange={(event) =>
                    setCreateForm({
                      ...createForm,
                      password: event.target.value,
                    })
                  }
                />
              </Field>
            </div>
            <div className='orgs__grid'>
              <Field>
                <Label htmlFor='org-create-name'>
                  {t('page.console.orgs.field.name', {
                    defaultValue: '账户名称',
                  })}
                </Label>
                <Input
                  id='org-create-name'
                  value={createForm.display_name}
                  onChange={(event) =>
                    setCreateForm({
                      ...createForm,
                      display_name: event.target.value,
                    })
                  }
                />
              </Field>
              <Field>
                <Label htmlFor='org-create-email'>
                  {t('page.console.orgs.field.email', {
                    defaultValue: '邮箱',
                  })}
                </Label>
                <Input
                  id='org-create-email'
                  value={createForm.email}
                  onChange={(event) =>
                    setCreateForm({ ...createForm, email: event.target.value })
                  }
                />
              </Field>
            </div>
            <div className='orgs__grid'>
              <Field>
                <Label>
                  {t('page.console.orgs.field.pricePlan', {
                    defaultValue: '专属折扣',
                  })}
                </Label>
                <Select
                  options={[
                    {
                      label: t('page.console.orgs.filter.planNone', {
                        defaultValue: '无',
                      }),
                      value: '0',
                    },
                    ...planOptions,
                  ]}
                  value={createForm.price_plan_id}
                  onValueChange={(value) =>
                    setCreateForm({ ...createForm, price_plan_id: value })
                  }
                />
              </Field>
              <Field>
                <Label htmlFor='org-create-quota'>
                  {t('page.console.orgs.field.initialBalance', {
                    defaultValue: '初始余额（USD，可留空）',
                  })}
                </Label>
                <Input
                  id='org-create-quota'
                  placeholder='0.00'
                  value={createForm.quota}
                  onChange={(event) =>
                    setCreateForm({ ...createForm, quota: event.target.value })
                  }
                />
              </Field>
            </div>
            <Field>
              <Label htmlFor='org-create-note'>
                {t('page.console.orgs.field.note', {
                  defaultValue: '备注（仅平台可见）',
                })}
              </Label>
              <Input
                id='org-create-note'
                value={createForm.remark}
                onChange={(event) =>
                  setCreateForm({ ...createForm, remark: event.target.value })
                }
              />
            </Field>
          </div>
        }
        footer={
          <>
            <Button variant='secondary' onClick={() => setCreating(false)}>
              {t('page.console.orgs.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              disabled={
                create.isPending ||
                !createForm.username.trim() ||
                !createForm.password
              }
              loading={create.isPending}
              variant='primary'
              onClick={() => create.mutate(createForm)}
            >
              {t('page.console.orgs.createSubmit', { defaultValue: '创建' })}
            </Button>
          </>
        }
        open={creating}
        title={t('page.console.orgs.createTitle', {
          defaultValue: '新建用户',
        })}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false);
            setCreateForm(emptyCreateForm);
          }
        }}
      />

      <GateModal
        body={
          <div className='pj-gate__lines'>
            <p>{t('page.console.orgs.deleteWhat')}</p>
            <p>{t('page.console.orgs.deleteSessions')}</p>
            <p>{t('page.console.orgs.deleteFinal')}</p>
          </div>
        }
        busy={remove.isPending}
        confirmLabel={t('page.console.orgs.deleteAction')}
        error={deleteError}
        gate={{
          type: 'typed',
          expected: deleting?.username || '',
          label: t('page.console.orgs.deleteTypeUsername', {
            username: deleting?.username || '',
          }),
        }}
        open={Boolean(deleting)}
        title={t('page.console.orgs.deleteQuestion', {
          name: deleting?.name ?? '',
        })}
        onConfirm={() => {
          if (deleting) remove.mutate(deleting);
        }}
        onOpenChange={(open) => {
          if (!open) closeDeleting();
        }}
      />
    </ConsolePage>
  );
}
