import {
  ArrowDownWideNarrow,
  KeyRound,
  Link2,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Search,
  Trash2,
  X,
  Copy,
} from 'lucide-react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useDebouncedCallback } from 'use-debounce';
import { AccessAddressStrip } from './access-addresses';
import { ConnectModal } from './connect/ConnectModal';
import { ToolMono } from './connect/ToolMono';
import { findConnectTool, type ConnectToolId } from './connect/tools';
import {
  isExpiringSoon,
  isNearlyOutLive,
  secondsToExpiry,
  useTokenOverview,
} from './token-summary';
import {
  FluentPrefillModal,
  TokenFormDrawer,
  TokenKeyCell,
  isLegacyKey,
  tokenStatus,
  type TokenColumn,
} from './token-support';
import { TokenUsagePanel } from './token-usage-panel';
import {
  areAllSelectableTokensSelected,
  getSelectableTokenIds,
  retainVisibleTokenIds,
} from './tokenSelection';
import { tokenStatusCode } from './tokenStatusCode';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { ControlAppearanceContext } from '../../components/ui/control-appearance';
import { DataTableShell } from '../../components/ui/data-table-shell';
import { EmptyState } from '../../components/ui/empty-state';
import { Input } from '../../components/ui/input';
import { Modal } from '../../components/ui/modal';
import { PendingValue } from '../../components/ui/pending';
import { Select, SelectItem } from '../../components/ui/select';
import {
  ColumnVisibilityMenu,
  ConsoleTableFrame,
  TextStack,
  compactNumber,
  formatUnixTime,
  useColumnVisibility,
} from '../../components/ui/table-tools';
import {
  KeyAudienceModal,
  shouldAskKeyAudience,
} from '../../features/projects/components/KeyAudienceModal';
import { useProject } from '../../features/projects/ProjectContext';
import { buildQuotaDisplay } from '../../helpers/quotaDisplay';
import { renderQuota } from '../../helpers/render';
import { copy, showError, showSuccess } from '../../helpers/utils';
import {
  useTokensQuery,
  type CreatedSecret,
  type TokenRecord,
} from '../../hooks/tokens/useTokensQuery';
import { cn } from '../../lib/cn';
import './rate-limit-slider.css';
import './token-page.css';

const mobileTitle = (title: string) => ({ title }) as Partial<TokenColumn>;

/** An expiry is a day, not a minute: the date alone keeps the column narrow
 *  enough that the row's actions never cover it. */
const formatExpiryDate = (value: unknown) => {
  const seconds = Number(value);
  if (!Number.isFinite(seconds) || seconds <= 0) return '-';
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(
    new Date(seconds * 1000),
  );
};

/** How long a revealed Key stays visible in the list. */
const REVEAL_MS = 60_000;

type ConnectTarget = {
  record: TokenRecord;
  secret?: string;
  tool?: ConnectToolId;
};

const Token = () => {
  const [editingToken, setEditingToken] = useState<TokenRecord | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const project = useProject();
  const [audienceOpen, setAudienceOpen] = useState(false);
  /* A solo owner is asked once who the key is for; everyone else, and every
     later key, goes straight to the drawer. */
  const openCreate = () => {
    setEditingToken(null);
    if (project.isSoloOwner && shouldAskKeyAudience(project.viewerId)) {
      setAudienceOpen(true);
      return;
    }
    setFormOpen(true);
  };
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<TokenRecord | null>(null);
  const [connectTarget, setConnectTarget] = useState<ConnectTarget | null>(
    null,
  );
  const [connectOpen, setConnectOpen] = useState(false);
  const [rotateTarget, setRotateTarget] = useState<TokenRecord | null>(null);
  // One key per opened confirmation, so pressing the button again after a
  // timeout asks the server to finish the same rotation, not a new one.
  const rotateOperation = useMemo(
    () =>
      `rotate-${String(rotateTarget?.id ?? '')}-${Date.now().toString(36)}-${Math.random()
        .toString(36)
        .slice(2, 10)}`,
    [rotateTarget?.id],
  );
  const [rotating, setRotating] = useState(false);
  const [revealed, setRevealed] = useState<{ id: string; key: string } | null>(
    null,
  );
  const revealTimer = useRef<number | undefined>(undefined);
  const [fluentOpen, setFluentOpen] = useState(false);
  const [fluentKey, setFluentKey] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');
  /* 快用完 and 7 天内到期 are not statuses the server filters on; those two
     tabs read the full Key list the counts already loaded. */
  const attentionFilter =
    statusFilter === 'nearlyOut' || statusFilter === 'expiring';
  const fluentContainerPresentRef = useRef(false);
  const tokens = useTokensQuery(
    (key) => {
      setFluentKey(key || '');
      setFluentOpen(true);
    },
    attentionFilter ? 'all' : statusFilter,
  );
  const overview = useTokenOverview();
  const vendorNames = useMemo(
    () =>
      new Map(
        tokens.vendorOptions.vendors.map((vendor) => [
          String(vendor.id),
          String(vendor.name || vendor.id),
        ]),
      ),
    [tokens.vendorOptions.vendors],
  );

  /* Typing is the search. 300ms is long enough that a burst of keystrokes
     makes one request and short enough that the list feels live. */
  const runSearch = useDebouncedCallback((value: string) => {
    tokens.setDraftFilters((current) => ({ ...current, keyword: value }));
    tokens.submitFilters();
  }, 300);
  const { t } = tokens;

  useEffect(() => () => window.clearTimeout(revealTimer.current), []);

  const hideRevealed = () => {
    window.clearTimeout(revealTimer.current);
    setRevealed(null);
  };

  const revealNow = (record: TokenRecord, key: string) => {
    window.clearTimeout(revealTimer.current);
    setRevealed({ id: String(record.id), key });
    revealTimer.current = window.setTimeout(() => setRevealed(null), REVEAL_MS);
  };

  /** Copying a masked row has to fetch the value first, and a browser may by
   *  then consider the click too old to allow a clipboard write. Rather than
   *  report a copy that did not happen, the row falls back to showing the
   *  Key, which the next click can copy without any fetch in between. */
  const copyKey = async (record: TokenRecord) => {
    if (record.id === undefined) return false;
    try {
      const known = revealed?.id === String(record.id) ? revealed.key : '';
      const key = known || (await tokens.revealKey(record.id));
      const ok = await copy(key);
      if (ok) return true;
      if (known) {
        showError(t('page.console.tables.copyFailed'));
        return false;
      }
      revealNow(record, key);
      showError(
        t('page.console.token.copyNeedsSecondClick', {
          defaultValue:
            '浏览器拦下了这次复制，密钥已显示出来，再点一次即可复制',
        }),
      );
      return false;
    } catch (error) {
      showError(error);
      return false;
    }
  };

  const openConnect = (target: ConnectTarget) => {
    setConnectTarget(target);
    setConnectOpen(true);
  };

  // A new Key lands in the connect dialog, where the tool is chosen; the
  // list row (and its copy button) is there for later. Batch creation has no
  // single Key to connect, so it only gets the toast.
  const handleCreated = (secrets: CreatedSecret[]) => {
    const [only] = secrets;
    if (!only) return;
    if (secrets.length === 1 && only.id !== undefined) {
      openConnect({
        record: { ...(only.token || {}), id: only.id, name: only.name },
        secret: only.key,
      });
      return;
    }
    showSuccess(
      secrets.length === 1
        ? t('page.console.token.toast.created', {
            defaultValue: '已创建 {{name}}，可在列表中复制',
            name: only.name,
          })
        : t('page.console.token.toast.createdMany', {
            defaultValue: '已创建 {{count}} 把密钥',
            count: secrets.length,
          }),
    );
  };

  const rotate = async () => {
    if (rotateTarget?.id === undefined) return;
    const wasRevealed = revealed?.id === String(rotateTarget.id);
    setRotating(true);
    try {
      const key = await tokens.rotateKey(rotateTarget.id, rotateOperation);
      // The row keeps its id, so a value revealed a moment ago would sit
      // there looking current while being the one this rotation just killed
      // — and the copy button prefers it. Show the new value if the old one
      // was on screen, and otherwise show nothing.
      if (wasRevealed) revealNow(rotateTarget, key);
      else hideRevealed();
      showSuccess(
        t('page.console.token.toast.rotated', {
          defaultValue: '已重新生成 {{name}}，旧值已失效',
          name: String(rotateTarget.name || ''),
        }),
      );
      setRotateTarget(null);
    } catch (error) {
      showError(error);
    } finally {
      setRotating(false);
    }
  };

  const hasFilters = Boolean(tokens.filters.keyword) || statusFilter !== 'all';
  const sortKey = `${tokens.sort.id}:${tokens.sort.desc ? 'desc' : 'asc'}`;
  /* The trigger names the order in two words; the menu keeps the full
     sentences. */
  const sortShortLabel =
    sortKey === 'created_time:asc'
      ? t('page.console.token.sort.short.createdAsc', {
          defaultValue: '最早创建',
        })
      : sortKey === 'name:asc'
        ? t('page.console.token.sort.short.nameAsc', {
            defaultValue: '按名称',
          })
        : sortKey === 'remain_quota:desc'
          ? t('page.console.token.sort.short.quotaDesc', {
              defaultValue: '额度最多',
            })
          : t('page.console.token.sort.short.createdDesc', {
              defaultValue: '最新创建',
            });
  const selectedCount = tokens.selectedIds.length;

  useEffect(() => {
    const selector = '#fluent-lazu-container';
    const onFirstAppeared = () => {
      setFluentKey('');
      setFluentOpen(true);
    };

    const syncContainerPresence = () => {
      const isPresent = Boolean(document.querySelector(selector));
      if (isPresent && !fluentContainerPresentRef.current) {
        fluentContainerPresentRef.current = true;
        onFirstAppeared();
        return;
      }
      if (!isPresent) {
        fluentContainerPresentRef.current = false;
      }
    };

    syncContainerPresence();
    const observer = new MutationObserver(syncContainerPresence);
    observer.observe(document.body || document.documentElement, {
      childList: true,
      subtree: true,
    });
    return () => observer.disconnect();
  }, []);

  const columnMeta = useMemo(
    () => [
      { id: 'select', label: t('page.console.token.column.select') },
      { id: 'name', label: t('page.console.token.column.name') },
      { id: 'key', label: t('page.console.token.column.key') },
      { id: 'status', label: t('page.console.token.column.status') },
      { id: 'quota', label: t('page.console.token.column.quota') },
      {
        id: 'models',
        label: t('page.console.token.column.modelScope', {
          defaultValue: '模型范围',
        }),
      },
      { id: 'expires', label: t('page.console.token.column.expires') },
      {
        id: 'product',
        label: t('page.console.token.column.product'),
        defaultHidden: true,
      },
      {
        id: 'remaining',
        label: t('pricing.remainingBalance', { defaultValue: '剩余 · USD' }),
        defaultHidden: true,
      },
      {
        id: 'ip',
        label: t('page.console.token.column.ip'),
        defaultHidden: true,
      },
      {
        id: 'ratelimit',
        label: t('page.console.token.column.rateLimit'),
        defaultHidden: true,
      },
      {
        id: 'created',
        label: t('page.console.token.column.created'),
        defaultHidden: true,
      },
      { id: 'actions', label: t('page.console.tables.actions') },
    ],
    [t],
  );
  // v4: the connect redesign trimmed the default set to eight columns, so
  // earlier saved choices (which kept "remaining" on) are not carried over.
  const columnVisibility = useColumnVisibility(
    'page-console-token-columns-v4',
    columnMeta,
    [],
    [],
  );

  const attentionTokens = useMemo(() => {
    if (!attentionFilter) return null;
    const keyword = tokens.filters.keyword.trim().toLowerCase();
    return (overview.records || []).filter(
      (token) =>
        (statusFilter === 'nearlyOut'
          ? isNearlyOutLive(token)
          : isExpiringSoon(token)) &&
        (!keyword ||
          String(token.name || '')
            .toLowerCase()
            .includes(keyword)),
    );
  }, [attentionFilter, overview.records, statusFilter, tokens.filters.keyword]);

  const visibleTokens = useMemo(() => {
    if (attentionTokens) {
      const start = (tokens.page - 1) * tokens.pageSize;
      return attentionTokens.slice(start, start + tokens.pageSize);
    }
    return statusFilter === 'all'
      ? tokens.tokens
      : tokens.tokens.filter(
          (token) => String(tokenStatusCode(token)) === statusFilter,
        );
  }, [
    attentionTokens,
    statusFilter,
    tokens.page,
    tokens.pageSize,
    tokens.tokens,
  ]);

  useEffect(() => {
    tokens.setSelectedIds((current) =>
      retainVisibleTokenIds(current, visibleTokens),
    );
  }, [tokens.setSelectedIds, visibleTokens]);

  const selectedIdSet = useMemo(
    () => new Set(tokens.selectedIds.map(String)),
    [tokens.selectedIds],
  );
  const allPageSelected = areAllSelectableTokensSelected(
    visibleTokens,
    tokens.selectedIds,
  );

  const allColumns = useMemo<TokenColumn[]>(() => {
    const togglePage = (checked: boolean) => {
      if (!checked) {
        tokens.setSelectedIds([]);
        return;
      }
      tokens.setSelectedIds(getSelectableTokenIds(visibleTokens));
    };
    const toggleRow = (record: TokenRecord, checked: boolean) => {
      tokens.setSelectedIds((current) => {
        const next = new Set(current.map(String));
        if (checked) {
          next.add(String(record.id));
        } else {
          next.delete(String(record.id));
        }
        return Array.from(next);
      });
    };

    const actionsFor = (record: TokenRecord) => {
      const legacy = isLegacyKey(record);
      const capabilities = (record.capabilities || {}) as {
        can_delete?: boolean;
        can_rotate?: boolean;
        can_update?: boolean;
      };
      // The row ends in three things and no menu. What the "…" used to hold
      // lives where people already look for it: copying on the key cell,
      // disabling on the batch bar, regeneration on the legacy rows that are
      // the only ones with nowhere else to go.
      const deleteLabel = t('page.console.tables.delete');
      const name = String(record.name || '');

      return (
        <div className='tokv2-rowactions'>
          {legacy ? (
            capabilities.can_rotate !== false ? (
              <button
                type='button'
                className='tokv2-act'
                onClick={() => setRotateTarget(record)}
              >
                <RefreshCw aria-hidden='true' size={13} />
                {t('page.console.token.action.regenerate', {
                  defaultValue: '重新生成',
                })}
              </button>
            ) : null
          ) : (
            <button
              type='button'
              className='tokv2-act'
              onClick={() => openConnect({ record })}
            >
              <Link2 aria-hidden='true' size={13} />
              {t('page.console.token.action.connect', {
                defaultValue: '接入',
              })}
            </button>
          )}
          {capabilities.can_update !== false ? (
            <button
              type='button'
              className='tokv2-act'
              onClick={() => {
                setEditingToken(record);
                setFormOpen(true);
              }}
            >
              <Pencil aria-hidden='true' size={13} />
              {t('legacy.i18n.edit.a7f814c0a4')}
            </button>
          ) : null}
          {capabilities.can_delete !== false ? (
            <button
              type='button'
              className='tokv2-act tokv2-act--icon tokv2-act--danger'
              aria-label={`${deleteLabel} ${name}`}
              title={deleteLabel}
              onClick={() => setDeleteTarget(record)}
            >
              <Trash2 aria-hidden='true' size={13} />
            </button>
          ) : null}
        </div>
      );
    };

    const tokenColumns: TokenColumn[] = [
      {
        id: 'select',
        header: () => (
          <Checkbox
            checked={allPageSelected}
            aria-label={t('page.console.token.action.selectPage')}
            onCheckedChange={togglePage}
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={selectedIdSet.has(String(row.original.id))}
            aria-label={t('page.console.token.action.selectRow')}
            onCheckedChange={(checked) => toggleRow(row.original, checked)}
          />
        ),
      },
      {
        id: 'name',
        accessorKey: 'name',
        header: t('page.console.token.column.name'),
        cell: ({ row }) => {
          const tool = findConnectTool(row.original.product);
          return (
            <div className='tok-name' title={tool ? tool.name : undefined}>
              <ToolMono tool={tool} />
              {/* The tool a Key is wired into tells eight Keys apart at a
                  glance; the public id is the fallback for a Key never
                  connected to one. */}
              <TextStack
                title={row.original.name}
                description={
                  tool
                    ? tool.name
                    : t('page.console.tables.idValue', {
                        id: row.original.id,
                      })
                }
              />
            </div>
          );
        },
      },
      {
        id: 'product',
        accessorKey: 'product',
        header: t('page.console.token.column.product'),
        cell: ({ getValue }) => {
          const product = String(getValue() || '').trim();
          return product ? (
            <Badge variant='info'>{product}</Badge>
          ) : (
            <Badge>{t('page.console.tables.none')}</Badge>
          );
        },
      },
      {
        id: 'status',
        accessorKey: 'status',
        header: t('page.console.token.column.status'),
        cell: ({ row }) => {
          const code = tokenStatusCode(row.original);
          return (
            <span className={cn('tok-status', `tok-status--${code}`)}>
              <span aria-hidden='true' className='tok-status__dot' />
              {tokenStatus(code, t).label}
            </span>
          );
        },
      },
      {
        id: 'quota',
        accessorKey: 'remain_quota',
        header: t('page.console.token.column.quota'),
        cell: ({ row }) => {
          const quota = buildQuotaDisplay({
            remaining: row.original.remain_quota,
            unlimited: Boolean(row.original.unlimited_quota),
            used: row.original.used_quota,
          });
          const usedText = renderQuota(quota.used);
          const unlimited = quota.mode === 'unlimited';
          const totalText = unlimited
            ? t('page.console.token.create.unlimited', {
                defaultValue: '不限',
              })
            : renderQuota(quota.total);
          const label = `${usedText} / ${totalText}`;
          const share = unlimited
            ? 0
            : Number(quota.used) / Math.max(1, Number(quota.total));
          return (
            <div
              className={cn(
                'tok-quota',
                unlimited && 'is-unlimited',
                share >= 0.8 && 'is-high',
              )}
              title={label}
            >
              <div className='tok-quota__line'>
                <span className='tok-quota__used'>{usedText}</span>
                <span className='tok-quota__total'>{totalText}</span>
              </div>
              <progress
                aria-label={label}
                className='tok-quota__meter'
                max={1}
                value={Math.min(1, share)}
              />
            </div>
          );
        },
      },
      {
        id: 'remaining',
        header: t('pricing.remainingBalance', { defaultValue: '剩余 · USD' }),
        cell: ({ row }) =>
          row.original.unlimited_quota
            ? '∞'
            : renderQuota(row.original.remain_quota),
      },
      {
        id: 'key',
        accessorKey: 'key',
        header: t('page.console.token.column.key'),
        cell: ({ row }) => (
          <TokenKeyCell
            record={row.original}
            onCopy={copyKey}
            revealed={
              revealed?.id === String(row.original.id)
                ? revealed.key
                : undefined
            }
          />
        ),
      },
      {
        id: 'models',
        accessorKey: 'model_limits',
        header: t('page.console.token.column.modelScope', {
          defaultValue: '模型范围',
        }),
        cell: ({ getValue, row }) => {
          const models = String(getValue() || '')
            .split(',')
            .filter(Boolean);
          const vendors = (
            Array.isArray(row.original.model_scope_vendors)
              ? row.original.model_scope_vendors
              : []
          ).map(String);
          // A Key scoped to a vendor alone stores no model names, so the
          // names are not what decides whether it is scoped.
          if (
            !row.original.model_limits_enabled ||
            (!models.length && !vendors.length)
          ) {
            return (
              <span className='tok-scopecell'>
                <span className='tok-scopecell__chip'>
                  {t('page.console.token.form.allModels', {
                    defaultValue: '全部模型',
                  })}
                </span>
              </span>
            );
          }
          const chips = [
            ...vendors.map((id) => ({
              key: `v:${id}`,
              label: t('page.console.token.scope.vendorChip', {
                defaultValue: '{{vendor}} · 全部',
                vendor: vendorNames.get(id) || id,
              }),
              vendor: true,
            })),
            ...models.map((model) => ({
              key: `m:${model}`,
              label: model,
              vendor: false,
            })),
          ];
          const shown = chips.slice(0, 2);
          const hidden = chips.length - shown.length;
          return (
            <span
              className='tok-scopecell'
              title={chips.map((chip) => chip.label).join(', ')}
            >
              {shown.map((chip) => (
                <span
                  className={cn(
                    'tok-scopecell__chip',
                    chip.vendor ? 'is-vendor' : 'is-model',
                  )}
                  key={chip.key}
                >
                  {chip.label}
                </span>
              ))}
              {hidden > 0 ? (
                <span className='tok-scopecell__chip is-model'>+{hidden}</span>
              ) : null}
            </span>
          );
        },
      },
      {
        id: 'ip',
        accessorKey: 'allow_ips',
        header: t('page.console.token.column.ip'),
        cell: ({ getValue }) => {
          const ips = String(getValue() || '')
            .split('\n')
            .map((ip) => ip.trim())
            .filter(Boolean);
          return ips.length ? (
            <TextStack
              title={ips[0]}
              description={
                ips.length > 1
                  ? t('page.console.token.moreIps', {
                      count: ips.length - 1,
                    })
                  : ''
              }
            />
          ) : (
            <Badge>{t('legacy.i18n.unlimited.7258118a9b')}</Badge>
          );
        },
      },
      {
        id: 'ratelimit',
        accessorKey: 'rpm_limit',
        header: t('page.console.token.column.rateLimit'),
        cell: ({ row }) => {
          const enabled = Boolean(row.original.rate_limits_enabled);
          const rpm = Math.max(
            0,
            Math.trunc(Number(row.original.rpm_limit) || 0),
          );
          const tpm = Math.max(
            0,
            Math.trunc(Number(row.original.tpm_limit) || 0),
          );
          const effective = (
            row.original.effective_rate_limits as
              | { effective?: { rpm?: unknown; tpm?: unknown } }
              | undefined
          )?.effective;
          const effectiveRpm = Math.max(
            0,
            Math.trunc(Number(effective?.rpm) || rpm),
          );
          const effectiveTpm = Math.max(
            0,
            Math.trunc(Number(effective?.tpm) || tpm),
          );
          if (!enabled || (rpm <= 0 && tpm <= 0)) {
            return <Badge>{t('page.console.token.rateLimit.inherit')}</Badge>;
          }
          const parts: string[] = [];
          if (rpm > 0) {
            parts.push(
              t('page.console.token.rateLimit.badgeRpm', {
                value: compactNumber(effectiveRpm),
              }),
            );
          }
          if (tpm > 0) {
            parts.push(
              t('page.console.token.rateLimit.badgeTpm', {
                value: compactNumber(effectiveTpm),
              }),
            );
          }
          return <Badge variant='info'>{parts.join(' · ')}</Badge>;
        },
      },
      {
        id: 'created',
        // The right-aligned header is a render function; phone cards read
        // this plain label instead of falling back to the column id.
        ...mobileTitle(t('page.console.token.column.created')),
        accessorKey: 'created_time',
        header: () => (
          <span className='tokv2-th-r'>
            {t('page.console.token.column.created')}
          </span>
        ),
        cell: ({ getValue }) => (
          <span className='tokv2-cell-r'>{formatUnixTime(getValue())}</span>
        ),
      },
      {
        id: 'expires',
        // The right-aligned header is a render function; phone cards read
        // this plain label instead of falling back to the column id.
        ...mobileTitle(t('page.console.token.column.expires')),
        accessorKey: 'expired_time',
        header: t('page.console.token.column.expires'),
        cell: ({ row }) => {
          const left = secondsToExpiry(row.original);
          if (left === null) {
            return (
              <span className='tok-expiry'>
                {t('legacy.i18n.never_expires.0070ca2601')}
              </span>
            );
          }
          if (left <= 0) {
            return (
              <span className='tok-expiry is-past'>
                {formatExpiryDate(row.original.expired_time)}
              </span>
            );
          }
          const days = Math.ceil(left / 86_400);
          return days <= 7 ? (
            <span
              className='tok-expiry is-soon'
              title={formatExpiryDate(row.original.expired_time)}
            >
              {t('page.console.token.expiry.inDays', {
                count: days,
                defaultValue: '{{count}} 天后',
              })}
            </span>
          ) : (
            <span className='tok-expiry'>
              {formatExpiryDate(row.original.expired_time)}
            </span>
          );
        },
      },
      {
        id: 'actions',
        ...mobileTitle(t('page.console.tables.actions')),
        header: () => (
          <span className='tokv2-th-r'>
            <ColumnVisibilityMenu
              iconOnly
              t={t}
              columns={columnMeta}
              value={columnVisibility.value}
              onChange={columnVisibility.setColumnVisible}
            />
          </span>
        ),
        cell: ({ row }) => actionsFor(row.original),
      },
    ];
    const order = [
      'select',
      'name',
      'key',
      'status',
      'quota',
      'remaining',
      'models',
      'expires',
      'product',
      'ip',
      'ratelimit',
      'created',
      'actions',
    ];
    return tokenColumns.sort(
      (a, b) => order.indexOf(String(a.id)) - order.indexOf(String(b.id)),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    allPageSelected,
    revealed,
    selectedIdSet,
    t,
    tokens,
    vendorNames,
    visibleTokens,
  ]);
  const columns = useMemo(
    () =>
      allColumns.filter((column) =>
        columnVisibility.visibleColumns.some(
          (meta) =>
            meta.id ===
            (column.id ||
              ('accessorKey' in column ? String(column.accessorKey) : '')),
        ),
      ),
    [allColumns, columnVisibility.visibleColumns],
  );

  return (
    <ControlAppearanceContext.Provider value='review'>
      <ConsoleTableFrame
        showRefresh={false}
        compact
        className='token-page-table console-review'
        t={t}
        title={t('page.console.token.title')}
        description={t('page.console.token.subtitle', {
          defaultValue: '一把 Key，接到任何工具',
        })}
        isFetching={tokens.isFetching}
        onRefresh={tokens.refresh}
        summary={<AccessAddressStrip t={t} />}
        toolbar={
          selectedCount ? (
            /* Batch work is rare, so its controls only exist while rows are
               ticked — no permanently greyed-out menu in the header. */
            <div className='tokv2-toolbar tok-selection'>
              <Checkbox
                checked
                aria-label={t('page.console.token.action.clearSelection', {
                  defaultValue: '取消选择',
                })}
                onCheckedChange={() => tokens.setSelectedIds([])}
              />
              <b className='tok-selection__count'>
                {t('page.console.token.selection.count', {
                  count: selectedCount,
                  defaultValue: '已选 {{count}} 把',
                })}
              </b>
              <Button
                size='sm'
                variant='secondary'
                onClick={() => void tokens.copySelected()}
              >
                <Copy size={14} />
                {t('page.console.token.selection.copyNames', {
                  defaultValue: '复制名称',
                })}
              </Button>
              <Button
                size='sm'
                variant='secondary'
                loading={tokens.batchDisableMutation.isPending}
                onClick={() => tokens.batchDisableMutation.mutate()}
              >
                <Power size={14} />
                {t('page.console.tables.disable')}
              </Button>
              <Button
                size='sm'
                variant='soft-danger'
                onClick={() => setDeleteOpen(true)}
              >
                <Trash2 size={14} />
                {t('page.console.tables.delete')}
              </Button>
              <button
                type='button'
                className='tokv2-editlink tok-selection__clear'
                onClick={() => tokens.setSelectedIds([])}
              >
                {t('page.console.token.action.clearSelection', {
                  defaultValue: '取消选择',
                })}
              </button>
            </div>
          ) : (
            <div className='tok-bar'>
              <div
                className='tok-tabs'
                role='radiogroup'
                aria-label={t('page.console.token.column.status')}
              >
                {(
                  ['all', '1', '2', '3', '4', 'nearlyOut', 'expiring'] as const
                ).map((code) => {
                  const count =
                    code === 'nearlyOut'
                      ? overview.overview?.nearlyOut
                      : code === 'expiring'
                        ? overview.overview?.expiringSoon
                        : overview.overview?.counts[code];
                  // A status nobody is in stays out of the row, unless it is
                  // the one being filtered on.
                  if (
                    code !== 'all' &&
                    code !== statusFilter &&
                    overview.overview &&
                    !count
                  ) {
                    return null;
                  }
                  return (
                    <button
                      key={code}
                      type='button'
                      role='radio'
                      aria-checked={statusFilter === code}
                      data-owns-chrome='true'
                      className={cn(
                        'tok-tabs__btn',
                        statusFilter === code && 'is-on',
                      )}
                      onClick={() => {
                        setStatusFilter(code);
                        tokens.setPage(1);
                      }}
                    >
                      {code === 'all'
                        ? t('page.console.token.filter.all', {
                            defaultValue: '全部',
                          })
                        : code === 'nearlyOut'
                          ? t('page.console.token.filter.nearlyOut', {
                              defaultValue: '快用完',
                            })
                          : code === 'expiring'
                            ? t('page.console.token.filter.expiring', {
                                defaultValue: '7 天内到期',
                              })
                            : tokenStatus(Number(code), t).label}
                      <span className='tok-tabs__n'>
                        {overview.overview ? count : <PendingValue chars={1} />}
                      </span>
                    </button>
                  );
                })}
              </div>
              <span className='tok-toolbar__spacer' />
              <label className='tokv2-search'>
                <Search
                  aria-hidden='true'
                  className='tokv2-search__icon'
                  size={15}
                />
                <Input
                  aria-label={t('page.console.token.filter.keyword')}
                  className='tokv2-search__input'
                  placeholder={t('page.console.token.filter.placeholder', {
                    defaultValue: '搜索名称或密钥尾号',
                  })}
                  value={tokens.draftFilters.keyword}
                  onChange={(event) => {
                    const next = event.target.value;
                    tokens.setDraftFilters((current) => ({
                      ...current,
                      keyword: next,
                      token: '',
                    }));
                    runSearch(next);
                  }}
                />
                {tokens.draftFilters.keyword ? (
                  <button
                    aria-label={t('page.console.tables.reset')}
                    className='tokv2-search__clear'
                    onClick={() => {
                      runSearch.cancel();
                      setStatusFilter('all');
                      tokens.resetFilters();
                    }}
                    type='button'
                  >
                    <X aria-hidden='true' size={14} />
                  </button>
                ) : null}
              </label>
              <Select
                size='sm'
                aria-label={t('page.console.token.filter.sort')}
                className='tok-sort'
                value={sortKey}
                triggerContent={
                  <span className='tok-sort__value'>
                    <ArrowDownWideNarrow aria-hidden='true' size={15} />
                    {sortShortLabel}
                  </span>
                }
                onValueChange={(value) => {
                  const [id, order] = value.split(':');
                  tokens.setSort({ desc: order === 'desc', id });
                }}
              >
                <SelectItem value='created_time:desc'>
                  {t('page.console.token.sort.createdDesc')}
                </SelectItem>
                <SelectItem value='created_time:asc'>
                  {t('page.console.token.sort.createdAsc')}
                </SelectItem>
                <SelectItem value='name:asc'>
                  {t('page.console.token.sort.nameAsc')}
                </SelectItem>
                <SelectItem value='remain_quota:desc'>
                  {t('page.console.token.sort.quotaDesc')}
                </SelectItem>
              </Select>
              <Button
                variant='primary'
                className='tok-create-cta'
                onClick={openCreate}
              >
                <Plus size={15} />
                {t('page.console.token.form.createTitle', {
                  defaultValue: '创建密钥',
                })}
              </Button>
            </div>
          )
        }
        pagination={{
          page: tokens.page,
          pageSize: tokens.pageSize,
          total: attentionTokens ? attentionTokens.length : tokens.total,
          onPageChange: tokens.setPage,
          onPageSizeChange: tokens.setPageSize,
        }}
      >
        <DataTableShell
          data={visibleTokens}
          columns={columns}
          mobileColumns={allColumns}
          loading={tokens.isLoading}
          rowKey='id'
          /* Clicking a row asks "how much has this Key used", which is what
             /console/key used to answer on its own page. Editing stays on the
             row's own action button. */
          renderExpanded={(record) => <TokenUsagePanel record={record} />}
          empty={
            hasFilters ? (
              <EmptyState
                icon={<KeyRound size={24} />}
                title={t('legacy.i18n.no_results_found.3f5746269e')}
                description={t('page.console.token.emptyDescription')}
              />
            ) : (
              <EmptyState
                icon={<KeyRound size={24} />}
                title={t('page.console.token.empty.title', {
                  defaultValue: '还没有密钥',
                })}
                description={t('page.console.token.empty.description', {
                  defaultValue:
                    '创建第一把，然后接到 Claude Code 或任何工具里。',
                })}
                action={
                  <Button variant='primary' onClick={openCreate}>
                    <Plus size={15} />
                    {t('page.console.token.form.createTitle', {
                      defaultValue: '创建密钥',
                    })}
                  </Button>
                }
              />
            )
          }
        />
      </ConsoleTableFrame>
      <Modal
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        busy={tokens.manageTokenMutation.isPending}
        title={t('page.console.tables.delete')}
        body={
          <p>
            {t('secret.deleteConfirm', {
              defaultValue: '删除 {{name}}？使用此密钥的应用将无法继续访问。',
              name: deleteTarget?.name,
            })}
          </p>
        }
        footer={
          <>
            <Button
              disabled={tokens.manageTokenMutation.isPending}
              variant='secondary'
              onClick={() => setDeleteTarget(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              variant='danger'
              disabled={tokens.manageTokenMutation.isPending}
              onClick={() => {
                if (deleteTarget?.id !== undefined)
                  tokens.manageTokenMutation.mutate(
                    { action: 'delete', id: deleteTarget.id },
                    { onSuccess: () => setDeleteTarget(null) },
                  );
              }}
            >
              {t('page.console.tables.delete')}
            </Button>
          </>
        }
      />
      <TokenFormDrawer
        editingToken={editingToken}
        open={formOpen}
        onOpenChange={setFormOpen}
        tokens={tokens}
        onSecretsCreated={handleCreated}
      />
      <KeyAudienceModal
        onOpenChange={setAudienceOpen}
        onSelf={() => setFormOpen(true)}
        open={audienceOpen}
      />
      <ConnectModal
        onEditKey={(record) => {
          setEditingToken(record);
          setFormOpen(true);
        }}
        open={connectOpen}
        onOpenChange={(open) => {
          setConnectOpen(open);
          if (!open) setConnectTarget(null);
        }}
        record={connectTarget?.record ?? null}
        initialSecret={connectTarget?.secret}
        initialTool={connectTarget?.tool}
        tokens={tokens}
      />
      <Modal
        open={rotateTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRotateTarget(null);
        }}
        busy={rotating}
        title={t('page.console.token.rotate.title', {
          defaultValue: '重新生成 {{name}}？',
          name: String(rotateTarget?.name || ''),
        })}
        body={
          <p>
            {t('page.console.token.rotate.body', {
              defaultValue:
                '旧值立即失效，用它的应用需要换成新值。额度、模型范围和其他设置保持不变。',
            })}
          </p>
        }
        footer={
          <>
            <Button
              variant='secondary'
              disabled={rotating}
              onClick={() => setRotateTarget(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button variant='primary' loading={rotating} onClick={rotate}>
              {t('page.console.token.action.regenerate', {
                defaultValue: '重新生成',
              })}
            </Button>
          </>
        }
      />
      <Modal
        busy={tokens.batchDeleteMutation.isPending}
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t('legacy.i18n.batch_delete_token.5e5d9d5031')}
        description={t(
          'legacy.i18n.are_you_sure_you_want_to_delete_the_selected_tokens.350223edf9',
          {
            count: tokens.selectedRows.length,
          },
        )}
        footer={
          <div className='flex justify-end gap-2'>
            <Button
              variant='ghost'
              disabled={tokens.batchDeleteMutation.isPending}
              onClick={() => setDeleteOpen(false)}
            >
              {t('legacy.i18n.cancel.4d0b4688c7')}
            </Button>
            <Button
              variant='danger'
              loading={tokens.batchDeleteMutation.isPending}
              onClick={async () => {
                try {
                  await tokens.batchDeleteMutation.mutateAsync();
                  setDeleteOpen(false);
                } catch {
                  // Keep the stateful dialog open so the user can recover or
                  // retry without losing the selected API Keys.
                }
              }}
            >
              {t('page.console.tables.delete')}
            </Button>
          </div>
        }
      />
      <FluentPrefillModal
        open={fluentOpen}
        onOpenChange={setFluentOpen}
        prefillKey={fluentKey}
        tokens={tokens}
      />
    </ControlAppearanceContext.Provider>
  );
};

export default Token;
