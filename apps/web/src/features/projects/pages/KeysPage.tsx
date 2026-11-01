/* R5 / R5b · API Keys in a shared project. There is one kind of Key: it
 * belongs to the project and to one member, is charged to the project
 * balance and counts against that member's budget. Owner/admin see every
 * Key with its member, the effective limit ("which wall it hits first") and
 * today's rejections; members see their own. Create, rotate, disable and
 * delete follow S1 / S10. */
import { Download, Plus, Search } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import { Button } from '../../../components/ui/button';
import ConsolePage from '../../../components/ui/console-page';
import { RowActions } from '../../../components/ui/data-table-shell';
import {
  EffortCapBadge,
  lowerEffortCap,
} from '../../../components/ui/effort-cap';
import { InlineNotice } from '../../../components/ui/inline-notice';
import { Input } from '../../../components/ui/input';
import {
  deferUndoable,
  useUndoPending,
} from '../../../components/ui/undo-toast';
import { cn } from '../../../lib/cn';
import { canRevealKey } from '../access';
import { MAX_PERSONAL_KEYS } from '../budget';
import {
  Card,
  KeyLimitBadge,
  Kvs,
  PjBadge,
  SegControl,
  TableFoot,
  Who,
  formatDay,
  formatDayTime,
} from '../components/atoms';
import {
  CreateKeyModal,
  EditKeyModal,
  CreatedSecret,
  RotateKeyModal,
} from '../components/KeyDialogs';
import { KeySecretCell } from '../components/KeySecretCell';
import { ProjectKeyConnect } from '../components/ProjectKeyConnect';
import { formatUSD, formatUSDShort } from '../money';
import { useProject } from '../ProjectContext';
import {
  useInvalidateProject,
  useMyBudget,
  useMyProjectTokens,
  useProjectDetail,
  useProjectModels,
  useProjectTokens,
} from '../queries';
import { errorReceipt, receipt, reversibleReceipt } from '../receipt';
import { effortUsageHref, rejectionUsageHref } from '../rejections';
import type { ProjectToken } from '../types';

type Filter = 'all' | 'mine' | 'disabled';
type Sort = 'used' | 'spend' | 'name';

export default function ProjectKeysPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [params] = useSearchParams();
  const manager = project.isManager;
  const all = useProjectTokens({ enabled: manager });
  const mine = useMyProjectTokens({ enabled: !manager });
  const detail = useProjectDetail();
  const models = useProjectModels();
  const myBudget = useMyBudget({ enabled: !manager });
  const tokens = (manager ? all.data : mine.data) ?? [];
  const loading = manager ? all.isLoading : mine.isLoading;
  const [keyword, setKeyword] = useState('');
  const [filter, setFilter] = useState<Filter>(manager ? 'all' : 'mine');
  const [sort, setSort] = useState<Sort>('used');
  const [ownerFilter, setOwnerFilter] = useState('');
  const [expanded, setExpanded] = useState<string | null>(params.get('key'));
  const [createOpen, setCreateOpen] = useState(false);
  const [connecting, setConnecting] = useState<{
    token: ProjectToken;
    secret?: string;
  } | null>(null);
  const [created, setCreated] = useState<{
    id: string;
    name: string;
    key: string;
  } | null>(null);
  const [rotating, setRotating] = useState<ProjectToken | null>(null);
  const [editing, setEditing] = useState<ProjectToken | null>(null);
  const [highlight, setHighlight] = useState<string | null>(null);

  useEffect(() => {
    if (!highlight) return;
    const timer = window.setTimeout(() => setHighlight(null), 6000);
    return () => window.clearTimeout(timer);
  }, [highlight]);

  const owners = useMemo(() => {
    const map = new Map<string, string>();
    tokens.forEach((k) => {
      if (k.owner) map.set(k.owner.id, k.owner.username);
    });
    return [...map.entries()];
  }, [tokens]);

  const rows = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    let out = tokens.filter(
      (k) =>
        !q ||
        k.name.toLowerCase().includes(q) ||
        k.masked_key.toLowerCase().endsWith(q) ||
        (k.owner?.username.toLowerCase().includes(q) ?? false),
    );
    if (filter === 'mine')
      out = out.filter((k) => k.owner?.id === project.viewerId);
    if (filter === 'disabled') out = out.filter((k) => k.status !== 'enabled');
    if (ownerFilter) out = out.filter((k) => k.owner?.id === ownerFilter);
    const sorters: Record<Sort, (a: ProjectToken, b: ProjectToken) => number> =
      {
        used: (a, b) => (b.last_used_at ?? 0) - (a.last_used_at ?? 0),
        spend: (a, b) => b.month_spend_microusd - a.month_spend_microusd,
        name: (a, b) => a.name.localeCompare(b.name),
      };
    return [...out].sort(sorters[sort]);
  }, [filter, keyword, ownerFilter, project.viewerId, sort, tokens]);

  const ownCount = tokens.filter(
    (k) => k.owner?.id === project.viewerId,
  ).length;
  const disabledCount = tokens.filter((k) => k.status !== 'enabled').length;

  /* Disabling is undone by enabling, so neither asks first: the receipt
     carries the opposite action. The latest row is read at click time, since
     a toggle bumps the Key's version. */
  const tokensRef = useRef(tokens);
  tokensRef.current = tokens;
  const toggle = async (key: ProjectToken) => {
    if (!project.client || !project.current) return;
    try {
      await project.client.setTokenEnabled(
        key.id,
        project.current.id,
        key.status !== 'enabled',
        key.version,
      );
      const disabled = key.status === 'enabled';
      reversibleReceipt(
        disabled
          ? t('projects.key.disabledReceipt', { name: key.name })
          : t('projects.key.enabledReceipt', { name: key.name }),
        {
          label: disabled
            ? t('projects.key.enable')
            : t('projects.key.disable'),
          onClick: () => {
            const latest = tokensRef.current.find((k) => k.id === key.id);
            if (latest) void toggle(latest);
          },
        },
      );
      await invalidate('tokens');
    } catch (e) {
      errorReceipt(e);
    }
  };

  /* 删除 runs at once with a 10-second undo; the row reads 删除中 until the
     request goes out. */
  const undoPending = useUndoPending();
  const pendingDelete = (key: ProjectToken) =>
    undoPending.has(`project-token:${key.id}`);
  const remove = (key: ProjectToken) => {
    const client = project.client;
    const current = project.current;
    if (!client || !current) return;
    setExpanded(null);
    deferUndoable({
      keys: [`project-token:${key.id}`],
      message: t('undo.token.deleted', {
        defaultValue: '已删除 {{name}}',
        name: key.name,
      }),
      restored: t('undo.token.restored', {
        defaultValue: '已恢复 {{name}}',
        name: key.name,
      }),
      failed: t('undo.token.failed', {
        defaultValue: '没删成，{{name}} 已放回',
        name: key.name,
      }),
      commit: () => client.deleteToken(key.id, current.id),
      keepalive: {
        method: 'DELETE',
        path: `/api/token/${key.id}?project_id=${encodeURIComponent(current.id)}`,
      },
      onCommitted: () => void invalidate('tokens'),
    });
  };

  const exportCsv = () => {
    const header = [
      'name',
      'member',
      'month_spend_usd',
      'cap_usd',
      'models',
      'status',
      'last_used',
    ];
    const body = tokens.map((k) =>
      [
        k.name,
        k.owner?.username ?? '',
        (k.month_spend_microusd / 1e6).toFixed(2),
        k.cap_microusd === null ? '' : (k.cap_microusd / 1e6).toFixed(2),
        k.model_limits.join('|'),
        k.status,
        k.last_used_at ? new Date(k.last_used_at * 1000).toISOString() : '',
      ].join(','),
    );
    const blob = new Blob([[header.join(','), ...body].join('\n')], {
      type: 'text/csv;charset=utf-8;',
    });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `${project.currentName}-keys.csv`;
    link.click();
    URL.revokeObjectURL(link.href);
  };

  const atLimit = ownCount >= MAX_PERSONAL_KEYS;
  const myBudgetLabel =
    myBudget.data?.budget_microusd === null
      ? t('projects.budget.unlimited')
      : formatUSDShort(myBudget.data?.budget_microusd ?? null);
  const managers = managerNames(
    tokens,
    project.viewerId,
    t('projects.member.managers'),
  );

  const reads = [
    detail,
    manager ? all : mine,
    models,
    ...(!manager ? [myBudget] : []),
  ];
  const failedQuery = reads.find((query) => query.isError);
  const recovery = failedQuery ? (
    <InlineNotice
      tone='danger'
      title={t('projects.common.loadFailed')}
      description={t('projects.common.loadFailedBody')}
      actions={
        <Button
          onClick={() =>
            void Promise.all(reads.map((query) => query.refetch()))
          }
          variant='secondary'
        >
          {t('projects.common.retry')}
        </Button>
      }
    />
  ) : null;
  if (reads.some((query) => query.isError && query.data === undefined)) {
    return (
      <ConsolePage appearance='review' title={t('nav.apiKeys')}>
        {recovery}
      </ConsolePage>
    );
  }

  return (
    <ConsolePage
      appearance='review'
      className='pj-page pj-keys-page'
      title={t('nav.apiKeys', { defaultValue: 'API Keys' })}
      description={
        manager
          ? t('projects.key.ledeManager', {
              project: project.currentName,
              count: tokens.length,
            })
          : t('projects.key.ledeMember', {
              project: project.currentName,
              count: tokens.length,
              budget: myBudgetLabel,
            })
      }
      actions={
        <>
          <Button onClick={exportCsv} size='sm' variant='secondary'>
            <Download aria-hidden='true' size={14} />
            {t('projects.common.export')}
          </Button>
          <Button
            disabled={atLimit || reads.some((query) => query.isPending)}
            onClick={() => setCreateOpen(true)}
            size='sm'
            title={
              atLimit
                ? t('projects.key.limitReached', { max: MAX_PERSONAL_KEYS })
                : undefined
            }
            variant='primary'
          >
            <Plus aria-hidden='true' size={14} />
            {t('projects.key.create')}
          </Button>
          {atLimit ? (
            <span className='pj-sub pj-sub--inline'>
              {t('projects.key.limitReached', { max: MAX_PERSONAL_KEYS })}
            </span>
          ) : null}
        </>
      }
    >
      {recovery}
      {created ? (
        <CreatedSecret
          name={created.name}
          note={t('projects.key.createdNote', { managers })}
          secret={created.key}
        />
      ) : null}

      <div className='pj-tools'>
        <div className='pj-search'>
          <Search aria-hidden='true' size={14} />
          <Input
            aria-label={t('projects.key.search')}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder={t('projects.key.search')}
            value={keyword}
          />
        </div>
        <SegControl<Filter>
          ariaLabel={t('projects.key.filter')}
          onChange={setFilter}
          options={[
            ...(manager
              ? [
                  {
                    value: 'all' as const,
                    label: t('projects.key.filterAll', {
                      count: tokens.length,
                    }),
                  },
                ]
              : []),
            {
              value: 'mine',
              label: t('projects.key.filterMine', { count: ownCount }),
            },
            {
              value: 'disabled',
              label: t('projects.key.filterDisabled', { count: disabledCount }),
            },
          ]}
          value={filter}
        />
        {manager ? (
          <select
            aria-label={t('projects.key.col.owner')}
            className='pj-fld pj-fld--select'
            onChange={(e) => setOwnerFilter(e.target.value)}
            value={ownerFilter}
          >
            <option value=''>{t('projects.key.ownerAny')}</option>
            {owners.map(([id, name]) => (
              <option key={id} value={id}>
                {name}
              </option>
            ))}
          </select>
        ) : null}
        <select
          aria-label={t('projects.members.sort')}
          className='pj-fld pj-fld--select'
          onChange={(e) => setSort(e.target.value as Sort)}
          value={sort}
        >
          <option value='used'>{t('projects.key.sortUsed')}</option>
          <option value='spend'>{t('projects.key.sortSpend')}</option>
          <option value='name'>{t('projects.key.sortName')}</option>
        </select>
      </div>

      <Card className='pj-table-card' pad={false}>
        <table className='pj-table'>
          <thead>
            <tr>
              <th style={{ width: '17%' }}>{t('projects.key.col.name')}</th>
              {manager ? <th>{t('projects.key.col.owner')}</th> : null}
              <th className='pj-r'>{t('projects.key.col.spend')}</th>
              <th>{t('projects.key.col.limit')}</th>
              <th>{t('projects.key.col.scope')}</th>
              <th>{t('projects.key.col.lastUsed')}</th>
              <th />
            </tr>
          </thead>
          <>
            {loading ? (
              <tbody>
                <tr>
                  <td className='pj-muted' colSpan={7}>
                    {t('projects.common.loading')}
                  </td>
                </tr>
              </tbody>
            ) : null}
            {rows.map((k) => {
              const isOwn = k.owner?.id === project.viewerId;
              const stopped = Boolean(k.stopped_by_removal);
              const canReveal =
                !stopped &&
                k.capabilities.can_reveal &&
                canRevealKey(
                  project.role,
                  k.owner?.role ?? 'member',
                  isOwn,
                  true,
                );
              const isOpen = expanded === k.id;
              return (
                <tbody
                  className='pj-rowgroup'
                  data-selected={isOpen || undefined}
                  key={k.id}
                >
                  <tr
                    className={cn(
                      'pj-row',
                      pendingDelete(k) && 'is-undo-pending',
                    )}
                    data-highlight={highlight === k.id || undefined}
                    data-muted={k.status !== 'enabled' || stopped || undefined}
                    data-selected={isOpen || undefined}
                  >
                    <td>
                      <button
                        className='pj-rowbtn'
                        data-owns-chrome=''
                        onClick={() => setExpanded(isOpen ? null : k.id)}
                        type='button'
                      >
                        <span className='pj-nm'>{k.name}</span>
                        <span className='pj-sub pj-mono'>{k.id}</span>
                      </button>
                      {pendingDelete(k) ? (
                        <PjBadge>
                          {t('undo.deleting', { defaultValue: '删除中' })}
                        </PjBadge>
                      ) : stopped ? (
                        <PjBadge tone='bad'>
                          {t('projects.key.stoppedByRemoval')}
                        </PjBadge>
                      ) : k.status !== 'enabled' ? (
                        <PjBadge tone='bad'>
                          {t(`projects.key.status.${k.status}`)}
                        </PjBadge>
                      ) : null}
                    </td>
                    {manager ? (
                      <td>
                        <Who name={k.owner?.username ?? '—'} />
                      </td>
                    ) : null}
                    <td className='pj-r pj-num pj-strong'>
                      {formatUSD(k.month_spend_microusd)}
                    </td>
                    <td>
                      <KeyLimitBadge
                        as='text'
                        projectCapMicro={
                          detail.data?.project_cap_microusd ?? null
                        }
                        projectSpentMicro={
                          detail.data?.month_spend_microusd ?? 0
                        }
                        token={k}
                        viewerId={project.viewerId}
                      />
                      {(() => {
                        const rejected = k.rejected_today.reduce(
                          (sum, r) => sum + r.count,
                          0,
                        );
                        const downed = k.effort_downed_month ?? 0;
                        if ((!rejected && !downed) || !project.current)
                          return null;
                        const top = [...k.rejected_today].sort(
                          (a, b) => b.count - a.count,
                        )[0];
                        return (
                          <span className='pj-limit-sub'>
                            {rejected && top ? (
                              <Link
                                to={rejectionUsageHref(
                                  project.current.id,
                                  top.reason,
                                  k.id,
                                )}
                              >
                                {t('projects.key.rejectedCount', {
                                  count: rejected,
                                })}
                              </Link>
                            ) : null}
                            {rejected && downed ? ' · ' : null}
                            {downed ? (
                              <Link
                                to={effortUsageHref(project.current.id, k.id)}
                              >
                                {t('projects.key.downedCount', {
                                  count: downed,
                                })}
                              </Link>
                            ) : null}
                          </span>
                        );
                      })()}
                    </td>
                    <td>
                      <span className='pj-scope'>
                        {k.model_limits.length ? (
                          <PjBadge tone='evi'>
                            {k.model_limits.length === 1
                              ? t('projects.key.modelsOne', {
                                  model: k.model_limits[0],
                                })
                              : t('projects.key.limitedModels', {
                                  count: k.model_limits.length,
                                })}
                          </PjBadge>
                        ) : (
                          <PjBadge>
                            {t('projects.key.modelsAllCount', {
                              count: models.data?.length ?? 0,
                            })}
                          </PjBadge>
                        )}
                        <EffortCapBadge
                          allLabel={t('projects.effort.all')}
                          cap={lowerEffortCap(
                            k.reasoning_effort_cap,
                            k.member_effort_cap,
                          )}
                        />
                      </span>
                    </td>
                    <td className='pj-mono pj-muted pj-small'>
                      {formatDayTime(k.last_used_at)}
                    </td>
                    <td className='pj-r'>
                      <span className='pj-ra' hidden={pendingDelete(k)}>
                        {!stopped ? (
                          <Button
                            size='sm'
                            variant='ghost'
                            onClick={() => setConnecting({ token: k })}
                          >
                            {t('page.console.token.action.connect', {
                              defaultValue: '接入',
                            })}
                          </Button>
                        ) : null}
                        <RowActions
                          menu={[
                            {
                              hidden: stopped || !k.capabilities.can_update,
                              key: 'edit',
                              label: t('projects.common.edit', {
                                defaultValue: '编辑',
                              }),
                              onSelect: () => setEditing(k),
                            },
                            {
                              hidden: stopped || !k.capabilities.can_disable,
                              key: 'toggle',
                              label:
                                k.status === 'enabled'
                                  ? t('projects.key.disable')
                                  : t('projects.key.enable'),
                              onSelect: () => void toggle(k),
                            },
                            {
                              danger: true,
                              hidden: !k.capabilities.can_delete,
                              key: 'delete',
                              label: t('page.console.tables.delete', {
                                defaultValue: '删除',
                              }),
                              onSelect: () => remove(k),
                            },
                          ]}
                          menuLabel={t('projects.common.moreFor', {
                            name: k.name,
                          })}
                        />
                      </span>
                    </td>
                  </tr>
                  {isOpen ? (
                    <tr className='pj-row--exp'>
                      <td colSpan={7}>
                        <div className='pj-grid2 pj-grid2--exp'>
                          <div>
                            <Kvs
                              rows={[
                                [
                                  t('projects.key.col.secret'),
                                  <KeySecretCell
                                    canReveal={canReveal}
                                    isOwn={isOwn}
                                    key='secret'
                                    onRotate={() => setRotating(k)}
                                    token={k}
                                  />,
                                ],
                                [
                                  t('projects.key.exp.payer'),
                                  isOwn
                                    ? t('projects.key.exp.payerMine', {
                                        project: project.currentName,
                                      })
                                    : t('projects.key.exp.payerPersonal', {
                                        project: project.currentName,
                                        name: k.owner?.username ?? '',
                                      }),
                                ],
                                [
                                  t('projects.key.col.limit'),
                                  t('projects.key.exp.limitChain', {
                                    key:
                                      k.cap_microusd === null
                                        ? t('projects.budget.unlimited')
                                        : `${formatUSDShort(k.cap_microusd)} (${formatUSD(k.month_spend_microusd)})`,
                                    project:
                                      detail.data?.project_cap_microusd == null
                                        ? t('projects.budget.notSet')
                                        : formatUSDShort(
                                            detail.data.project_cap_microusd,
                                          ),
                                    balance: formatUSD(
                                      detail.data?.balance_microusd,
                                    ),
                                  }),
                                ],
                                [
                                  t('projects.key.exp.visible'),
                                  isOwn
                                    ? t('projects.key.exp.visibleMine', {
                                        managers,
                                      })
                                    : t('projects.key.exp.visiblePersonal', {
                                        name: k.owner?.username ?? '',
                                      }),
                                ],
                                [
                                  t('projects.key.exp.models'),
                                  k.model_limits.length
                                    ? k.model_limits.join(', ')
                                    : t('projects.key.exp.modelsAll'),
                                ],
                                [
                                  t('projects.key.exp.created'),
                                  `${formatDay(k.created_at)} · ${t('projects.key.exp.createdPersonal', { name: k.owner?.username ?? '' })}`,
                                ],
                              ]}
                            />
                            <div className='pj-inline pj-inline--gap'>
                              {project.current ? (
                                <Button
                                  render={
                                    <Link
                                      to={`/console/usage?tab=logs&project_id=${project.current.id}&token_id=${k.id}`}
                                    />
                                  }
                                  size='sm'
                                  variant='secondary'
                                >
                                  {t('projects.key.exp.logs')}
                                </Button>
                              ) : null}
                              {!stopped && k.capabilities.can_rotate ? (
                                <Button
                                  onClick={() => setRotating(k)}
                                  size='sm'
                                  variant='ghost'
                                >
                                  {t('projects.key.rotate')}
                                </Button>
                              ) : null}
                              {k.capabilities.can_delete ? (
                                <Button
                                  onClick={() => remove(k)}
                                  size='sm'
                                  variant='ghost-danger'
                                >
                                  {t('page.console.tables.delete', {
                                    defaultValue: '删除',
                                  })}
                                </Button>
                              ) : null}
                            </div>
                          </div>
                        </div>
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              );
            })}
            {!loading && !rows.length ? (
              <tbody>
                <tr>
                  <td className='pj-muted' colSpan={7}>
                    {t('projects.key.empty')}
                  </td>
                </tr>
              </tbody>
            ) : null}
          </>
        </table>
        <TableFoot
          left={
            manager
              ? t('projects.key.footManager')
              : t('projects.key.footMember', { managers })
          }
          right={
            manager
              ? t('projects.key.footRight', {
                  count: tokens.length,
                  project: project.currentName,
                })
              : t('projects.key.footRightMember', {
                  count: tokens.length,
                  max: MAX_PERSONAL_KEYS,
                })
          }
        />
      </Card>

      {connecting ? (
        <ProjectKeyConnect
          token={connecting.token}
          secret={connecting.secret}
          onClose={() => setConnecting(null)}
        />
      ) : null}
      {editing ? (
        <EditKeyModal
          key={editing.id}
          token={editing}
          onClose={() => setEditing(null)}
        />
      ) : null}
      <CreateKeyModal
        onCreated={(c) => {
          setCreated(null);
          setConnecting({ token: c.token, secret: c.key });
          setHighlight(c.token.id);
        }}
        onOpenChange={setCreateOpen}
        open={createOpen}
        ownKeyCount={ownCount}
      />
      {rotating ? (
        <RotateKeyModal
          onOpenChange={(open) => {
            if (!open) setRotating(null);
          }}
          onRotated={(r) => {
            setCreated({ id: rotating.id, name: rotating.name, key: r.key });
            receipt(
              r.old_expires_at
                ? t('projects.key.rotated24h', { name: rotating.name })
                : t('projects.key.rotatedNow', { name: rotating.name }),
            );
          }}
          open
          token={rotating}
        />
      ) : null}
    </ConsolePage>
  );
}

const managerNames = (
  tokens: ProjectToken[],
  viewerId: string,
  fallback = '',
) => {
  const names = new Set<string>();
  tokens.forEach((k) => {
    if (k.owner && k.owner.role !== 'member' && k.owner.id !== viewerId)
      names.add(k.owner.username);
  });
  return [...names].join(' 和 ') || fallback;
};
