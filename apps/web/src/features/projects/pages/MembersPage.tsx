/* R3 · 成员与预算. Four-column ledger (allocation drawn inside its own cell),
 * member table, pending invitation rows, suspended rows and removal with
 * undo. A row's 「编辑」 opens the small member dialog (budget, allowed models,
 * reasoning-effort cap); role, suspension and removal live in its ··· menu.
 * Locked 2026-09-28: brand/foundations/team-projects-2026-09.md. */
import { Download, Plus, Search } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import {
  Button,
  EffortCapBadge,
  InlineNotice,
  Input,
  RowActions,
} from '../../../components/ui';
import ConsolePage from '../../../components/ui/console-page';
import { PendingBlock } from '../../../components/ui/pending';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import { isLazuApiError } from '../../../lib/api-error';
import {
  canChangeRole as roleCanChange,
  canEditBudget,
  canManageInvite,
  canRemoveMember,
} from '../access';
import { INVITE_VALID_DAYS } from '../budget';
import {
  BudgetCell,
  Card,
  ActionModal,
  Ledger,
  PjBadge,
  RoleBadge,
  SegControl,
  TableFoot,
  Who,
  formatDayTime,
} from '../components/atoms';
import { InviteExpiry } from '../components/InviteExpiry';
import { InviteModal } from '../components/InviteModal';
import {
  RemoveMemberModal,
  RoleChangeModal,
  UndoRemoveLine,
} from '../components/MemberDialogs';
import { MemberEditModal } from '../components/MemberEditModal';
import { budgetTone, formatUSD, formatUSDShort, percentOf } from '../money';
import { useProject } from '../ProjectContext';
import {
  useInvalidateProject,
  useProjectDetail,
  useProjectInvites,
  useProjectMembers,
  useProjectTokens,
} from '../queries';
import { errorReceipt, receipt } from '../receipt';
import {
  type ProjectInvite,
  type ProjectMember,
  type RemovalReceipt,
} from '../types';
import { useInviteClock } from '../useInviteClock';

type Filter = 'all' | 'managers' | 'members' | 'pending';
type Sort = 'spend' | 'name' | 'joined';

/* One line under a member's name on phones, where the table's other six
   columns cannot fit: role, spend against cap, and today's rejections. */
const memberRowSummary = (
  m: ProjectMember,
  t: (key: string, opts?: Record<string, unknown>) => string,
) => {
  const parts = [
    m.status === 'suspended'
      ? t('projects.status.suspendedBadge')
      : t(`projects.role.${m.role}`),
    `${formatUSD(m.month_spend_microusd)} / ${
      m.budget_microusd === null
        ? t('projects.budget.unlimited')
        : formatUSDShort(m.budget_microusd)
    }`,
  ];
  if (m.rejected_count_today > 0)
    parts.push(
      t('projects.detail.rejectedToday', { count: m.rejected_count_today }),
    );
  return parts.join(' · ');
};

export default function MembersPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const navigate = useNavigate();
  const project = useProject();
  const invalidate = useInvalidateProject();
  const detail = useProjectDetail();
  const members = useProjectMembers();
  const invites = useProjectInvites();
  const tokens = useProjectTokens();
  const [keyword, setKeyword] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [sort, setSort] = useState<Sort>('spend');
  /* ?edit=<user id> opens the member dialog; old member-detail links land
     here through that parameter. */
  const [search, setSearch] = useSearchParams();
  const editing = search.get('edit');
  const focusedMember = search.get('member');
  const setEditing = (id: string | null) => {
    const next = new URLSearchParams(search);
    if (id) next.set('edit', id);
    else next.delete('edit');
    setSearch(next, { replace: true });
  };
  const [roleChange, setRoleChange] = useState<{
    member: ProjectMember;
    role: 'admin' | 'member';
  } | null>(null);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [removing, setRemoving] = useState<ProjectMember | null>(null);
  /* Suspend and revoke confirmed in popovers hung off their own row buttons.
     Those buttons are in the overflow menu now, and the menu closes on
     select, so each confirms in a dialog. */
  const [suspending, setSuspending] = useState<ProjectMember | null>(null);
  const [revoking, setRevoking] = useState<ProjectInvite | null>(null);
  const [undo, setUndo] = useState<{
    member: ProjectMember;
    receipt: RemovalReceipt;
  } | null>(null);
  const [resendCooldown, setResendCooldown] = useState<Record<string, number>>(
    {},
  );
  const [busyInvite, setBusyInvite] = useState<string | null>(null);

  useEffect(() => {
    const keys = Object.keys(resendCooldown);
    if (!keys.length) return;
    const timer = window.setInterval(() => {
      const now = Math.floor(Date.now() / 1000);
      setResendCooldown((current) => {
        const next: Record<string, number> = {};
        for (const [id, until] of Object.entries(current))
          if (until > now) next[id] = until;
        return next;
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, [resendCooldown]);

  const now = useInviteClock(
    (invites.data ?? []).map((invite) => invite.expires_at),
  );
  const d = detail.data;
  const list = members.data ?? [];
  const revealMembers = useDelayedReveal(members.isPending);
  const pending = (invites.data ?? []).filter(
    (i) => i.status === 'pending' || i.status === 'expired',
  );
  const allKeys = tokens.data ?? [];

  const rows = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    let out = list.filter(
      (m) =>
        !q ||
        m.username.toLowerCase().includes(q) ||
        m.email.toLowerCase().includes(q),
    );
    if (filter === 'managers') out = out.filter((m) => m.role !== 'member');
    if (filter === 'members') out = out.filter((m) => m.role === 'member');
    if (filter === 'pending') out = [];
    const sorters: Record<
      Sort,
      (a: ProjectMember, b: ProjectMember) => number
    > = {
      spend: (a, b) => b.month_spend_microusd - a.month_spend_microusd,
      name: (a, b) => a.username.localeCompare(b.username),
      joined: (a, b) => b.joined_at - a.joined_at,
    };
    return [...out].sort(sorters[sort]);
  }, [filter, keyword, list, sort]);

  const pendingRows =
    filter === 'all' || filter === 'pending'
      ? pending.filter(
          (i) =>
            !keyword.trim() ||
            i.email.toLowerCase().includes(keyword.trim().toLowerCase()),
        )
      : [];

  const allocation = d?.allocation;
  const allocationTotal = allocation?.member_caps_microusd ?? 0;
  const cap = d?.project_cap_microusd ?? null;
  const capPct = percentOf(d?.month_spend_microusd ?? 0, cap);
  const balanceDays =
    d && d.balance_microusd !== null && d.month_spend_microusd > 0
      ? Math.round(
          d.balance_microusd /
            Math.max(
              1,
              d.month_spend_microusd / Math.max(1, new Date().getUTCDate()),
            ),
        )
      : null;
  const dailyAvg = d
    ? Math.round(d.month_spend_microusd / Math.max(1, new Date().getUTCDate()))
    : 0;

  const setStatus = async (
    member: ProjectMember,
    status: 'active' | 'suspended',
  ) => {
    if (!project.client || !project.current) return;
    try {
      await project.client.setMemberStatus(
        project.current.id,
        member.user_id,
        status,
      );
      receipt(
        status === 'suspended'
          ? t('projects.status.suspended', { name: member.username })
          : t('projects.status.restored', { name: member.username }),
      );
      await invalidate('members');
      await invalidate('tokens');
    } catch (error) {
      errorReceipt(error);
    }
  };

  const resend = async (invite: ProjectInvite) => {
    if (!project.client || !project.current) return;
    setBusyInvite(invite.id);
    try {
      await project.client.resendInvite(project.current.id, invite.id);
      receipt(t('projects.invite.resent', { email: invite.email }));
      setResendCooldown((c) => ({
        ...c,
        [invite.id]: Math.floor(Date.now() / 1000) + 60,
      }));
      await invalidate('invites');
    } catch (error) {
      if (isLazuApiError(error) && error.code === 'resend_too_soon') {
        const seconds = Number(error.details.retry_after_seconds ?? 60);
        setResendCooldown((c) => ({
          ...c,
          [invite.id]: Math.floor(Date.now() / 1000) + seconds,
        }));
      } else errorReceipt(error);
    } finally {
      setBusyInvite(null);
    }
  };

  const revoke = async (invite: ProjectInvite) => {
    if (!project.client || !project.current) return;
    setBusyInvite(invite.id);
    try {
      await project.client.revokeInvite(project.current.id, invite.id);
      receipt(t('projects.invite.revoked', { email: invite.email }));
      await invalidate('invites');
    } catch (error) {
      errorReceipt(error);
    } finally {
      setBusyInvite(null);
    }
  };

  const exportCsv = () => {
    const header = [
      'name',
      'email',
      'role',
      'status',
      'month_spend_usd',
      'budget_usd',
      'keys',
      'last_active',
    ];
    const body = list.map((m) =>
      [
        m.username,
        m.email,
        m.role,
        m.status,
        (m.month_spend_microusd / 1e6).toFixed(2),
        m.budget_microusd === null ? '' : (m.budget_microusd / 1e6).toFixed(2),
        m.key_count,
        m.last_active_at ? new Date(m.last_active_at * 1000).toISOString() : '',
      ].join(','),
    );
    const blob = new Blob([[header.join(','), ...body].join('\n')], {
      type: 'text/csv;charset=utf-8;',
    });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `${project.currentName}-members.csv`;
    link.click();
    URL.revokeObjectURL(link.href);
  };

  const memberCount = list.length;
  const countBy = (role: 'managers' | 'members') =>
    list.filter((m) =>
      role === 'managers' ? m.role !== 'member' : m.role === 'member',
    ).length;

  const reads = [detail, members, invites, tokens];
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
  const memberActions = (m: ProjectMember, narrow = false) => {
    const isSelf = m.user_id === project.viewerId;
    const suspended = m.status === 'suspended';
    const editable =
      canEditBudget(project.role, m.role, isSelf) && m.status === 'active';
    const removable = canRemoveMember(project.role, m.role, isSelf);
    const canChangeRole = roleCanChange(project.role, m.role) && !isSelf;
    return (
      <span className={narrow ? 'pj-ra' : 'pj-ra pj-ra--hover'}>
        {suspended && removable ? (
          <Button
            onClick={() => void setStatus(m, 'active')}
            size='sm'
            variant='secondary'
          >
            {t('projects.status.enable')}
          </Button>
        ) : editable ? (
          <Button
            aria-haspopup='dialog'
            onClick={() => setEditing(m.user_id)}
            size='sm'
            variant='ghost'
          >
            {t('projects.memberEdit.button')}
          </Button>
        ) : (
          <span
            className='pj-ra__dash'
            title={
              isSelf
                ? t('projects.budget.cannotEditOwn')
                : t('projects.budget.cannotEdit')
            }
          >
            —
          </span>
        )}
        <RowActions
          menu={[
            {
              hidden: !canChangeRole || suspended,
              key: 'role',
              label:
                m.role === 'admin'
                  ? t('projects.memberEdit.makeMember')
                  : t('projects.memberEdit.makeAdmin'),
              onSelect: () =>
                setRoleChange({
                  member: m,
                  role: m.role === 'admin' ? 'member' : 'admin',
                }),
            },
            {
              hidden: !removable || suspended,
              key: 'suspend',
              label: t('projects.status.suspend'),
              onSelect: () => setSuspending(m),
            },
            {
              danger: true,
              hidden: !removable,
              key: 'remove',
              label: t('projects.remove.button'),
              onSelect: () => setRemoving(m),
            },
          ]}
          menuLabel={t('projects.common.moreFor', {
            name: m.username,
          })}
        />
      </span>
    );
  };

  if (reads.some((query) => query.isError && query.data === undefined)) {
    return (
      <ConsolePage appearance='review' title={t('projects.members.title')}>
        {recovery}
      </ConsolePage>
    );
  }

  return (
    <ConsolePage
      appearance='review'
      className='pj-page pj-members-page'
      title={t('projects.members.title')}
      description={t('projects.members.lede', {
        project: project.currentName,
        count: memberCount,
        pending: pending.filter((i) => i.status === 'pending').length,
      })}
      actions={
        <>
          <Button onClick={exportCsv} size='sm' variant='secondary'>
            <Download aria-hidden='true' size={14} />
            {t('projects.common.export')}
          </Button>
          <Button
            onClick={() => setInviteOpen(true)}
            size='sm'
            variant='primary'
          >
            <Plus aria-hidden='true' size={14} />
            {t('projects.members.invite')}
          </Button>
        </>
      }
    >
      {recovery}
      <Ledger
        size='ledger'
        items={[
          {
            key: 'balance',
            label: t('projects.members.ledger.balance'),
            value: formatUSD(d?.balance_microusd),
            hint:
              balanceDays !== null
                ? t('projects.members.ledger.balanceHint', {
                    daily: formatUSD(dailyAvg),
                    days: balanceDays,
                  })
                : t('projects.members.ledger.balanceHintOwner'),
          },
          {
            key: 'spent',
            label: t('projects.members.ledger.spent'),
            value: formatUSD(d?.month_spend_microusd ?? 0),
            hint: t('projects.members.ledger.spentHint', {
              people: memberCount,
              keys: allKeys.length,
            }),
          },
          {
            key: 'allocation',
            label: t('projects.members.ledger.allocation'),
            value: formatUSD(allocationTotal),
            hint: members.data ? (
              <AllocationStack members={members.data} cap={cap} />
            ) : (
              <span
                className='pj-allocstack'
                data-testid='allocation-pending'
                style={{ visibility: revealMembers ? 'visible' : 'hidden' }}
              >
                <span className='pj-stack pj-stack--thin'>
                  <PendingBlock height={6} />
                </span>
                <span className='pj-allocstack__legend'>
                  <PendingBlock height={18} />
                </span>
              </span>
            ),
          },
          {
            key: 'cap',
            label: t('projects.members.ledger.cap'),
            value: cap === null ? t('projects.budget.notSet') : formatUSD(cap),
            hint:
              cap === null
                ? t('projects.members.ledger.capNone')
                : t('projects.members.ledger.capHintLeft', {
                    pct: capPct ?? 0,
                    left: formatUSDShort(
                      Math.max(0, cap - (d?.month_spend_microusd ?? 0)),
                    ),
                  }),
          },
        ]}
      />

      <div className='pj-tools'>
        <div className='pj-search'>
          <Search aria-hidden='true' size={14} />
          <Input
            aria-label={t('projects.members.search')}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder={t('projects.members.search')}
            value={keyword}
          />
        </div>
        <SegControl<Filter>
          ariaLabel={t('projects.members.filter')}
          onChange={setFilter}
          options={[
            {
              value: 'all',
              label: t('projects.members.filterAll', {
                count: memberCount + pending.length,
              }),
            },
            {
              value: 'managers',
              label: t('projects.members.filterManagers', {
                count: countBy('managers'),
              }),
            },
            {
              value: 'members',
              label: t('projects.members.filterMembers', {
                count: countBy('members'),
              }),
            },
            {
              value: 'pending',
              label: t('projects.members.filterPending', {
                count: pending.length,
              }),
            },
          ]}
          value={filter}
        />
        <select
          aria-label={t('projects.members.sort')}
          className='pj-fld pj-fld--select'
          onChange={(e) => setSort(e.target.value as Sort)}
          value={sort}
        >
          <option value='spend'>{t('projects.members.sortSpend')}</option>
          <option value='name'>{t('projects.members.sortName')}</option>
          <option value='joined'>{t('projects.members.sortJoined')}</option>
        </select>
      </div>

      {undo ? (
        <UndoRemoveLine
          member={undo.member}
          receiptData={undo.receipt}
          onUndone={() => setUndo(null)}
        />
      ) : null}

      <Card className='pj-table-card' pad={false}>
        <div className='pj-wide'>
          <table className='pj-table'>
            <thead>
              <tr>
                <th style={{ width: '24%' }}>
                  {t('projects.members.col.member')}
                </th>
                <th>{t('projects.members.col.role')}</th>
                <th className='pj-r'>{t('projects.members.col.spent')}</th>
                <th style={{ width: 200 }}>
                  {t('projects.members.col.budget')}
                </th>
                <th>{t('projects.members.col.effort')}</th>
                <th className='pj-r'>{t('projects.members.col.keys')}</th>
                <th>{t('projects.members.col.lastActive')}</th>
                <th />
              </tr>
            </thead>
            <>
              {members.isLoading ? (
                <tbody>
                  <tr>
                    <td className='pj-muted' colSpan={8}>
                      {t('projects.common.loading')}
                    </td>
                  </tr>
                </tbody>
              ) : null}
              {rows.map((m) => {
                const pct = percentOf(
                  m.month_spend_microusd,
                  m.budget_microusd,
                );
                const tone = budgetTone(
                  m.month_spend_microusd,
                  m.budget_microusd,
                  m.warning_pct,
                );
                const suspended = m.status === 'suspended';
                const isEditing = editing === m.user_id;
                const selected = isEditing || focusedMember === m.user_id;
                return (
                  <RowGroup key={m.user_id} selected={selected}>
                    <tr
                      className='pj-row'
                      data-muted={suspended || undefined}
                      data-selected={selected || undefined}
                    >
                      <td>
                        <Who
                          muted={suspended}
                          name={m.username}
                          sub={
                            <>
                              {m.email}
                              {m.role === 'owner'
                                ? ` · ${t('projects.members.createdProject')}`
                                : ''}
                            </>
                          }
                        />
                      </td>
                      <td>
                        <span className='pj-inline-badges'>
                          <RoleBadge role={m.role} />
                          {suspended ? (
                            <PjBadge tone='bad'>
                              {t('projects.status.suspendedBadge')}
                            </PjBadge>
                          ) : null}
                        </span>
                      </td>
                      <td className='pj-r pj-num pj-strong'>
                        {formatUSD(m.month_spend_microusd)}
                        {m.rejected_count_today > 0 && project.current ? (
                          <>
                            {' '}
                            <Link
                              className='pj-evlink'
                              to={`/console/usage?tab=logs&project_id=${encodeURIComponent(project.current.id)}&user_id=${encodeURIComponent(m.user_id)}&result=rejected`}
                            >
                              {t('projects.detail.rejectedToday', {
                                count: m.rejected_count_today,
                              })}
                            </Link>
                          </>
                        ) : null}
                      </td>
                      <td>
                        {suspended ? (
                          <span className='pj-sub'>
                            {t('projects.status.keysStopped', {
                              count: m.key_count,
                            })}
                          </span>
                        ) : (
                          <BudgetCell
                            cap={m.budget_microusd}
                            pct={pct}
                            spent={m.month_spend_microusd}
                            tone={tone}
                          />
                        )}
                      </td>
                      <td>
                        <EffortCapBadge
                          allLabel={t('projects.effort.all')}
                          cap={m.reasoning_effort_cap}
                        />
                      </td>
                      <td className='pj-r pj-num'>{m.key_count}</td>
                      <td className='pj-mono pj-muted pj-small'>
                        {formatDayTime(m.last_active_at)}
                      </td>
                      <td className='pj-r'>{memberActions(m)}</td>
                    </tr>
                  </RowGroup>
                );
              })}
              {pendingRows.map((invite) => {
                const expired =
                  invite.status === 'expired' ||
                  invite.expires_at <= Math.floor(now / 1000);
                const cooldown = resendCooldown[invite.id];
                const left = cooldown
                  ? Math.max(0, cooldown - Math.floor(Date.now() / 1000))
                  : 0;
                const manageable = canManageInvite(project.role, invite.role);
                return (
                  <tbody className='pj-rowgroup' key={invite.id}>
                    <tr className='pj-row pj-row--pending'>
                      <td>
                        <Who
                          name={invite.email.split('@')[0]}
                          sub={
                            <>
                              {invite.email} ·{' '}
                              {t('projects.invite.invitedBy', {
                                date: formatDayTime(
                                  invite.expires_at -
                                    INVITE_VALID_DAYS * 86_400,
                                ).slice(0, 5),
                                name: invite.invited_by?.username ?? '',
                              })}
                              {invite.resent_at ? (
                                <>
                                  {' · '}
                                  {t('projects.invite.resentAt', {
                                    date: formatDayTime(invite.resent_at).slice(
                                      0,
                                      5,
                                    ),
                                  })}
                                </>
                              ) : null}
                            </>
                          }
                        />
                      </td>
                      <td>
                        {expired ? (
                          <PjBadge>{t('projects.invite.expiredBadge')}</PjBadge>
                        ) : (
                          <PjBadge tone='warn'>
                            {t('projects.invite.pendingBadge')}
                          </PjBadge>
                        )}
                      </td>
                      <td className='pj-r pj-faint'>—</td>
                      <td>
                        <span className='pj-sub'>
                          {t('projects.invite.budgetAfterAccept', {
                            budget: formatUSDShort(invite.budget_microusd),
                          })}
                        </span>
                      </td>
                      <td className='pj-faint'>—</td>
                      <td className='pj-r pj-faint'>—</td>
                      <td className='pj-mono pj-muted pj-small'>
                        <InviteExpiry
                          compact
                          expiresAt={invite.expires_at}
                          now={now}
                          expired={expired}
                        />
                      </td>
                      <td className='pj-r'>
                        <span className='pj-ra pj-ra--hover'>
                          <Button
                            disabled={!manageable || (!expired && left > 0)}
                            loading={busyInvite === invite.id}
                            onClick={() => void resend(invite)}
                            size='sm'
                            variant={expired ? 'secondary' : 'ghost'}
                          >
                            {expired
                              ? t('projects.invite.reinvite')
                              : left > 0
                                ? t('projects.invite.justSent', {
                                    seconds: left,
                                  })
                                : t('projects.invite.resend')}
                          </Button>
                          <RowActions
                            menu={[
                              {
                                key: 'revoke',
                                danger: true,
                                disabled: !manageable,
                                label: expired
                                  ? t('projects.invite.remove')
                                  : t('projects.invite.revoke'),
                                onSelect: () =>
                                  expired
                                    ? void revoke(invite)
                                    : setRevoking(invite),
                              },
                            ]}
                            menuLabel={t('projects.common.moreFor', {
                              name: invite.email,
                            })}
                          />
                        </span>
                      </td>
                    </tr>
                  </tbody>
                );
              })}
              {!members.isLoading && !rows.length && !pendingRows.length ? (
                <tbody>
                  <tr>
                    <td className='pj-muted' colSpan={8}>
                      {t('projects.members.empty')}
                    </td>
                  </tr>
                </tbody>
              ) : null}
            </>
          </table>
        </div>
        {/* Narrow rows share the table's permission-aware member controls. */}
        <div className='pj-narrow'>
          <div className='pj-mlist'>
            {rows.map((m) => (
              <div
                className='pj-mlist__row pj-mlist__row--actions'
                data-selected={
                  editing === m.user_id ||
                  focusedMember === m.user_id ||
                  undefined
                }
                key={m.user_id}
              >
                <Who
                  muted={m.status === 'suspended'}
                  name={m.username}
                  sub={memberRowSummary(m, t)}
                />
                {memberActions(m, true)}
              </div>
            ))}
            {pendingRows.map((invite) => {
              const expired =
                invite.status === 'expired' ||
                invite.expires_at <= Math.floor(now / 1000);
              const cooldown = resendCooldown[invite.id];
              const left = cooldown
                ? Math.max(0, cooldown - Math.floor(Date.now() / 1000))
                : 0;
              const manageable = canManageInvite(project.role, invite.role);
              return (
                <div
                  className='pj-mlist__row pj-mlist__row--invite'
                  key={invite.id}
                >
                  <Who
                    muted
                    name={invite.email.split('@')[0]}
                    sub={
                      <>
                        {invite.email}
                        <InviteExpiry
                          expiresAt={invite.expires_at}
                          now={now}
                          expired={expired}
                        />
                      </>
                    }
                  />
                  {expired ? (
                    <PjBadge>{t('projects.invite.expiredBadge')}</PjBadge>
                  ) : (
                    <PjBadge tone='warn'>
                      {t('projects.invite.pendingBadge')}
                    </PjBadge>
                  )}
                  <span className='pj-ra'>
                    <Button
                      disabled={!manageable || (!expired && left > 0)}
                      loading={busyInvite === invite.id}
                      onClick={() => void resend(invite)}
                      size='sm'
                      variant={expired ? 'secondary' : 'ghost'}
                    >
                      {expired
                        ? t('projects.invite.reinvite')
                        : left > 0
                          ? t('projects.invite.justSent', { seconds: left })
                          : t('projects.invite.resend')}
                    </Button>
                    <RowActions
                      menu={[
                        {
                          key: 'revoke',
                          danger: true,
                          disabled: !manageable,
                          label: expired
                            ? t('projects.invite.remove')
                            : t('projects.invite.revoke'),
                          onSelect: () =>
                            expired ? void revoke(invite) : setRevoking(invite),
                        },
                      ]}
                      menuLabel={t('projects.common.moreFor', {
                        name: invite.email,
                      })}
                    />
                  </span>
                </div>
              );
            })}
            {!members.isLoading && !rows.length && !pendingRows.length ? (
              <div className='pj-mlist__row pj-muted'>
                {t('projects.members.empty')}
              </div>
            ) : null}
          </div>
        </div>
        <TableFoot
          left={t('projects.members.footLeft')}
          right={
            <Link to='/console/token'>{t('projects.members.footRight')}</Link>
          }
        />
      </Card>

      <InviteModal
        balanceMicro={d?.balance_microusd}
        detail={d}
        onOpenChange={setInviteOpen}
        open={inviteOpen}
      />
      <ActionModal
        confirmLabel={t('projects.status.suspend')}
        danger
        onConfirm={() => {
          if (suspending) void setStatus(suspending, 'suspended');
          setSuspending(null);
        }}
        onOpenChange={(open) => {
          if (!open) setSuspending(null);
        }}
        open={suspending !== null}
        title={t('projects.status.suspendConfirm', {
          name: suspending?.username ?? '',
          project: project.currentName,
        })}
      />
      <ActionModal
        confirmLabel={t('projects.invite.revoke')}
        danger
        onConfirm={() => {
          if (revoking) void revoke(revoking);
          setRevoking(null);
        }}
        onOpenChange={(open) => {
          if (!open) setRevoking(null);
        }}
        open={revoking !== null}
        title={t('projects.invite.revokeConfirm', {
          name: (revoking?.email ?? '').split('@')[0],
        })}
      />
      {(() => {
        const target = list.find((m) => m.user_id === editing);
        return target &&
          target.status === 'active' &&
          canEditBudget(
            project.role,
            target.role,
            target.user_id === project.viewerId,
          ) ? (
          <MemberEditModal
            member={target}
            onOpenChange={(open) => {
              if (!open) setEditing(null);
            }}
            open
          />
        ) : null;
      })()}
      {roleChange ? (
        <RoleChangeModal
          member={roleChange.member}
          nextRole={roleChange.role}
          onOpenChange={(open) => {
            if (!open) setRoleChange(null);
          }}
          onDone={() => setRoleChange(null)}
          open
        />
      ) : null}
      {removing ? (
        <RemoveMemberModal
          keys={allKeys.filter((k) => k.owner?.id === removing.user_id)}
          member={removing}
          onOpenChange={(open) => {
            if (!open) setRemoving(null);
          }}
          onRemoved={(r) => {
            setUndo({ member: removing, receipt: r });
            setRemoving(null);
            navigate('/console/project/members');
          }}
          open
        />
      ) : null}
    </ConsolePage>
  );
}

function RowGroup({
  children,
  selected,
}: {
  children: React.ReactNode;
  selected: boolean;
}) {
  return (
    <tbody className='pj-rowgroup' data-selected={selected || undefined}>
      {children}
    </tbody>
  );
}

/* The allocation, drawn inside the ledger's 「预算分配」 cell: one bar per
   capped member in ink and neutrals, ink for anyone who used theirs up —
   no red series. The words say who is out; the colour only groups. */
const PALETTE = [
  'var(--lazu-ink)',
  'var(--lazu-chart-3)',
  'var(--lazu-chart-2)',
];

export function AllocationStack({
  members,
  cap,
}: {
  members: ProjectMember[];
  cap: number | null;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const capped = members.filter(
    (m) =>
      m.role !== 'owner' &&
      m.budget_microusd !== null &&
      m.status !== 'removed',
  );
  const unlimited = members.filter(
    (m) => m.budget_microusd === null && m.status !== 'removed',
  ).length;
  const usedUp = (m: ProjectMember) =>
    m.budget_microusd !== null && m.month_spend_microusd >= m.budget_microusd;
  const total = capped.reduce((sum, m) => sum + (m.budget_microusd ?? 0), 0);
  const denominator = cap !== null ? Math.max(cap, total) : Math.max(1, total);
  return (
    <span className='pj-allocstack'>
      <span aria-hidden='true' className='pj-stack pj-stack--thin'>
        {capped.map((m, i) => (
          <i
            key={m.user_id}
            style={{
              width: `${((m.budget_microusd ?? 0) / denominator) * 100}%`,
              background: usedUp(m)
                ? 'var(--lazu-ink)'
                : PALETTE[i % PALETTE.length],
            }}
          />
        ))}
      </span>
      <span className='pj-allocstack__legend'>
        {capped
          .map(
            (m) =>
              `${m.username} ${formatUSDShort(m.budget_microusd)}${usedUp(m) ? ` ${t('projects.members.usedUp')}` : ''}`,
          )
          .concat(
            unlimited
              ? [t('projects.members.unlimitedPeople', { count: unlimited })]
              : [],
          )
          .join(' · ')}
      </span>
    </span>
  );
}
