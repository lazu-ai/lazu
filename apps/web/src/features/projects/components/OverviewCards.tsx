/* Cards the overview grows when the projects switch is on:
 * - AttentionCard (S8.2, R3 todo): owner/admin "需要处理", from the backend
 *   aggregate, never summed from loaded pages.
 * - MemberOverview (R6): the member's whole overview in a shared project. */
import { ChevronDown } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  INVITE_VALID_DAYS,
  validateBudget,
  validateBudgetRequestNote,
  type BudgetDraft,
} from '../budget';
import {
  BudgetPicker,
  Card,
  ConfirmPop,
  KeyLimitBadge,
  Ledger,
  Notice,
  PjBadge,
  ReasonChip,
  SectionHead,
  TableFoot,
  budgetErrorText,
  budgetReasonText,
  formatDayTime,
  formatMonthDay,
} from './atoms';
import { KeySecretCell } from './KeySecretCell';
import { LeaveProjectAction } from './LeaveProjectAction';
import { HandleBudgetRequestModal } from './MemberDialogs';
import { Button, Textarea } from '../../../components/ui';
import { canRevealKey } from '../access';
import { canManageInvite, canHandleBudgetRequest } from '../access';
import { formatUSD, formatUSDShort, percentOf } from '../money';
import { useProject } from '../ProjectContext';
import {
  useAttention,
  useBudgetRequests,
  useInvalidateProject,
  useMyBudget,
  useMyProjectTokens,
  useProjectDetail,
  useProjectInvites,
  useProjectMembers,
} from '../queries';
import { errorReceipt, receipt } from '../receipt';
import {
  REJECTION_REASONS,
  rejectionActionKey,
  rejectionEffectKey,
  rejectionLabelKey,
  rejectionTone,
  rejectionUsageHref,
} from '../rejections';
import { useInviteClock } from '../useInviteClock';
import { InviteExpiry } from './InviteExpiry';
import type { BudgetRequest } from '../types';

/* The overview no longer carries a "start a team" card. Its entries moved
 * to where the decision is actually made: the sidebar's 团队 group, the
 * wallet next to the balance that would be shared, and the fork in front of
 * creating a Key. */

/* --------------------------------------------------------- attention card */

export function AttentionCard() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const attention = useAttention();
  const requests = useBudgetRequests();
  const invites = useProjectInvites();
  const members = useProjectMembers();
  const detail = useProjectDetail();
  const now = useInviteClock(
    (invites.data ?? []).map((invite) => invite.expires_at),
  );
  const [handling, setHandling] = useState<BudgetRequest | null>(null);
  const [resending, setResending] = useState<string | null>(null);
  if (!project.enabled || !project.current?.isShared || !project.isManager)
    return null;
  const a = attention.data;
  const pendingRequests = (requests.data?.items ?? []).filter(
    (r) => r.status === 'pending',
  );
  const pendingInvites = (invites.data ?? []).filter(
    (i) => i.status === 'pending',
  );
  /* One per line below, so count the rejection GROUPS the backend aggregated,
     not `rejected_count_today`, which counts individual requests and made the
     heading read "· 4" above three rows. */
  const total =
    (a?.rejections.total ?? 0) +
    (a?.near_budget?.total ?? 0) +
    (a?.pending_invites_count ?? 0) +
    (a?.pending_budget_requests_count ?? 0) +
    (a?.balance_days_left !== null &&
    a?.balance_days_left !== undefined &&
    a.balance_days_left <= 14
      ? 1
      : 0);

  const resend = async (id: string, email: string) => {
    if (!project.client || !project.current) return;
    setResending(id);
    try {
      await project.client.resendInvite(project.current.id, id);
      receipt(t('projects.invite.resent', { email }));
      await invalidate('invites');
    } catch (e) {
      errorReceipt(e);
    } finally {
      setResending(null);
    }
  };

  return (
    <Card className='pj-todo'>
      <SectionHead
        sub={t('projects.attention.sub')}
        title={t('projects.attention.title', { count: total })}
      />
      <div className='pj-list'>
        {a?.rejections.items.map((r) => (
          <div key={`${r.actor?.id ?? 'anon'}-${r.token.id}-${r.reason}`}>
            <span className='pj-mono pj-faint pj-list__when'>
              {formatDayTime(r.last_at).slice(6)}
            </span>
            <span className='pj-list__t'>
              {r.actor ? (
                <>
                  <strong>{r.actor.username}</strong> ·{' '}
                </>
              ) : null}
              <span className='pj-mono pj-small'>{r.token.name}</span>
              <small>
                <ReasonChip
                  code={r.reason}
                  count={r.count}
                  href={rejectionUsageHref(
                    project.current!.id,
                    r.reason,
                    r.token.id,
                  )}
                />
              </small>
            </span>
            <Button
              render={
                <Link
                  to={
                    r.reason === 'project_access_suspended' && r.actor
                      ? `/console/project/members?member=${encodeURIComponent(r.actor.id)}`
                      : r.reason === 'member_budget_exceeded' && r.actor
                        ? `/console/project/members?edit=${encodeURIComponent(r.actor.id)}`
                        : r.reason === 'project_budget_exceeded' ||
                            r.reason === 'insufficient_quota'
                          ? '/console/wallet'
                          : `/console/token?key=${r.token.id}`
                  }
                />
              }
              size='sm'
              variant='secondary'
            >
              {t(rejectionActionKey(r.reason, project.role), {
                name: r.actor?.username ?? r.token.name,
              })}
            </Button>
          </div>
        ))}
        {(a?.near_budget?.items ?? []).map((n) => {
          const member = members.data?.find((m) => m.user_id === n.actor.id);
          const canAdjust = Boolean(
            member && canHandleBudgetRequest(project.role, member.role),
          );
          return (
            <div key={`near-${n.actor.id}`}>
              <span className='pj-mono pj-faint pj-list__when'>
                {percentOf(n.spent_microusd, n.budget_microusd)}%
              </span>
              <span className='pj-list__t'>
                <strong>{n.actor.username}</strong>{' '}
                {n.exhausted
                  ? t('projects.attention.usedUpLine')
                  : t('projects.attention.nearLine', {
                      pct: percentOf(n.spent_microusd, n.budget_microusd),
                    })}
                <small>
                  {t('projects.attention.nearSub', {
                    spent: formatUSD(n.spent_microusd),
                    budget: formatUSD(n.budget_microusd),
                    warning: n.warning_pct,
                  })}
                </small>
              </span>
              <Button
                disabled={!canAdjust}
                render={
                  canAdjust ? (
                    <Link
                      to={`/console/project/members?edit=${encodeURIComponent(n.actor.id)}`}
                    />
                  ) : undefined
                }
                size='sm'
                variant='secondary'
              >
                {t('projects.attention.adjustBudget')}
              </Button>
            </div>
          );
        })}
        {pendingRequests.map((r) => {
          const member = members.data?.find((m) => m.user_id === r.user.id);
          return (
            <div key={r.id}>
              <span className='pj-mono pj-faint pj-list__when'>
                {formatDayTime(r.created_at).slice(6)}
              </span>
              <span className='pj-list__t'>
                <strong>{r.user.username}</strong>{' '}
                {t('projects.attention.requestLine', {
                  amount: formatUSDShort(r.requested_microusd),
                })}
                <small>
                  {t('projects.attention.requestSub', {
                    current: formatUSDShort(member?.budget_microusd ?? null),
                    spent: formatUSD(member?.month_spend_microusd ?? 0),
                    reason: r.reason,
                  })}
                </small>
              </span>
              <Button
                disabled={
                  !member ||
                  member.status !== 'active' ||
                  !canHandleBudgetRequest(project.role, member.role)
                }
                onClick={() => setHandling(r)}
                size='sm'
                variant='secondary'
              >
                {t('projects.attention.handle')}
              </Button>
            </div>
          );
        })}
        {pendingInvites.map((i) => (
          <div key={i.id}>
            <span className='pj-mono pj-faint pj-list__when'>
              {formatMonthDay(i.expires_at - INVITE_VALID_DAYS * 86_400)}
            </span>
            <span className='pj-list__t'>
              <strong>{i.email.split('@')[0]}</strong>{' '}
              {t('projects.attention.inviteLine')}
              <small>
                {i.email} · <InviteExpiry expiresAt={i.expires_at} now={now} />
              </small>
            </span>
            <Button
              disabled={!canManageInvite(project.role, i.role)}
              loading={resending === i.id}
              onClick={() => void resend(i.id, i.email)}
              size='sm'
              variant='secondary'
            >
              {t('projects.invite.resend')}
            </Button>
          </div>
        ))}
        {a && a.balance_days_left !== null && a.balance_days_left <= 14 ? (
          <div>
            <span className='pj-mono pj-faint pj-list__when'>
              {t('projects.attention.forecast')}
            </span>
            <span className='pj-list__t'>
              <strong>
                {t('projects.attention.balanceLine', {
                  days: a.balance_days_left,
                })}
              </strong>
              <small>
                {t('projects.attention.balanceSub', {
                  balance: formatUSD(detail.data?.balance_microusd),
                })}
              </small>
            </span>
            <Button
              render={<Link to='/console/wallet' />}
              size='sm'
              variant='secondary'
            >
              {t('projects.wallet.topUp')}
            </Button>
          </div>
        ) : null}
        {a && total === 0 ? (
          <div className='pj-list__empty'>{t('projects.attention.empty')}</div>
        ) : null}
        {!a && attention.isLoading ? (
          <div className='pj-list__empty'>{t('projects.common.loading')}</div>
        ) : null}
      </div>
      {handling ? (
        <HandleBudgetRequestModal
          allocationMicro={
            detail.data
              ? detail.data.allocation.member_caps_microusd
              : undefined
          }
          member={members.data?.find((m) => m.user_id === handling.user.id)}
          onOpenChange={(open) => {
            if (!open) setHandling(null);
          }}
          open
          request={handling}
        />
      ) : null}
    </Card>
  );
}

/* ------------------------------------------------------- member overview */

export function MemberOverview() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const budget = useMyBudget();
  const tokens = useMyProjectTokens();
  const requests = useBudgetRequests();
  const invalidate = useInvalidateProject();
  const b = budget.data;
  const keys = tokens.data ?? [];
  const managers = t('projects.member.managers');
  const remaining = b?.remaining_microusd ?? null;
  const pct = b ? percentOf(b.spent_microusd, b.budget_microusd) : null;
  const dailyDays =
    b && remaining !== null && b.daily_avg_microusd > 0
      ? Math.round(remaining / b.daily_avg_microusd)
      : null;
  const requestsList = requests.data?.items ?? [];
  const pending = requestsList.find((r) => r.status === 'pending');
  const [draft, setDraft] = useState<BudgetDraft>({
    mode: 'amount',
    input: '',
  });
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const validation = validateBudget(draft);
  const rangeError = budgetErrorText(draft, t);
  const ownerName = project.current?.ownerName ?? '';

  const send = async () => {
    if (
      !project.client ||
      !project.current ||
      !validation.ok ||
      validation.micro === null
    )
      return;
    setBusy(true);
    try {
      await project.client.createBudgetRequest(project.current.id, {
        requested_microusd: validation.micro,
        reason: reason.trim(),
      });
      receipt(t('projects.request.sentReceipt', { owner: ownerName }));
      setDraft({ mode: 'amount', input: '' });
      setReason('');
      await invalidate('budget');
    } catch (e) {
      errorReceipt(e);
    } finally {
      setBusy(false);
    }
  };
  const withdraw = async (id: string) => {
    if (!project.client || !project.current) return;
    try {
      await project.client.withdrawBudgetRequest(project.current.id, id);
      receipt(t('projects.request.withdrawn'));
      await invalidate('budget');
    } catch (e) {
      errorReceipt(e);
    }
  };

  const warn = pct !== null && b && pct >= b.warning_pct;
  const resetMonth = b ? new Date(b.resets_at * 1000).getUTCMonth() + 1 : 0;

  return (
    <div className='pj-member-overview'>
      <LeaveProjectAction />
      {b && warn ? (
        <Notice
          action={
            <Button
              onClick={() =>
                document
                  .getElementById('pj-request-card')
                  ?.scrollIntoView({ behavior: 'smooth' })
              }
              size='sm'
              variant='secondary'
            >
              {pending
                ? t('projects.member.requestPending', {
                    amount: formatUSDShort(pending.requested_microusd),
                  })
                : t('projects.member.requestFrom', { owner: ownerName })}
            </Button>
          }
          body={t(
            pct !== null && pct >= 100
              ? 'projects.member.noticeBodyOut'
              : dailyDays === null
                ? 'projects.member.noticeBodyNoForecast'
                : 'projects.member.noticeBody',
            {
              daily: formatUSD(b.daily_avg_microusd),
              days: dailyDays ?? 0,
              month: resetMonth,
            },
          )}
          title={t('projects.member.noticeTitle', {
            project: project.currentName,
            budget: formatUSDShort(b.budget_microusd),
            left: formatUSD(remaining),
          })}
          tone={pct !== null && pct >= 100 ? 'bad' : 'warn'}
        />
      ) : null}
      <Ledger
        items={[
          {
            key: 'left',
            label: t('projects.member.canSpend'),
            value: b
              ? b.budget_microusd === null
                ? t('projects.budget.unlimited')
                : formatUSD(remaining)
              : '—',
            hint:
              b && b.budget_microusd !== null
                ? t('projects.member.canSpendHint', {
                    budget: formatUSD(b.budget_microusd),
                    spent: formatUSD(b.spent_microusd),
                  })
                : t('projects.member.noBudgetHint'),
            tone:
              pct !== null && pct >= 100 ? 'bad' : warn ? 'warn' : undefined,
          },
          {
            key: 'spent',
            label: t('projects.member.spent'),
            value: formatUSD(b?.spent_microusd ?? 0),
            hint: b
              ? t('projects.member.spentHint', {
                  pct: pct ?? 0,
                  daily: formatUSD(b.daily_avg_microusd),
                })
              : '',
          },
          {
            key: 'requests',
            label: t('projects.member.requests'),
            value: keys.length ? '—' : '0',
            hint: t('projects.member.requestsHint'),
          },
          {
            key: 'keys',
            label: t('projects.member.myKeys'),
            value: String(keys.length),
            hint:
              keys.map((k) => k.name).join(' · ') ||
              t('projects.member.noKeys'),
          },
        ]}
      />
      <div className='pj-grid2'>
        <div>
          <SectionHead
            sub={t('projects.member.keysSub', { count: keys.length })}
            title={t('projects.member.keysTitle', {
              project: project.currentName,
            })}
          />
          <Card className='pj-table-card' pad={false}>
            <table className='pj-table'>
              <thead>
                <tr>
                  <th style={{ width: '20%' }}>{t('projects.key.col.name')}</th>
                  <th>{t('projects.key.col.secret')}</th>
                  <th className='pj-r'>{t('projects.key.col.spend')}</th>
                  <th>{t('projects.key.col.limit')}</th>
                  <th>{t('projects.key.col.models')}</th>
                  <th>{t('projects.key.col.lastUsed')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr className='pj-row' key={k.id}>
                    <td>
                      <span className='pj-nm'>{k.name}</span>
                      <span className='pj-sub pj-mono'>{k.id}</span>
                    </td>
                    <td>
                      <KeySecretCell
                        canReveal={canRevealKey(
                          project.role,
                          'member',
                          true,
                          true,
                        )}
                        isOwn
                        token={k}
                      />
                    </td>
                    <td className='pj-r pj-num pj-strong'>
                      {formatUSD(k.month_spend_microusd)}
                    </td>
                    <td>
                      <KeyLimitBadge
                        projectCapMicro={b?.project_cap?.cap_microusd ?? null}
                        projectSpentMicro={b?.project_cap?.spent_microusd ?? 0}
                        token={k}
                        viewerId={project.viewerId}
                      />
                    </td>
                    <td>
                      {k.model_limits.length ? (
                        <PjBadge>
                          {t('projects.key.limitedModels', {
                            count: k.model_limits.length,
                          })}
                        </PjBadge>
                      ) : (
                        <PjBadge>{t('projects.key.allModels')}</PjBadge>
                      )}
                    </td>
                    <td className='pj-mono pj-muted pj-small'>
                      {formatDayTime(k.last_used_at)}
                    </td>
                    <td className='pj-r'>
                      <span className='pj-ra'>
                        <Button
                          render={<Link to='/console/token' />}
                          size='sm'
                          variant='ghost'
                        >
                          {t('legacy.i18n.edit.a7f814c0a4', {
                            defaultValue: '编辑',
                          })}
                        </Button>
                      </span>
                    </td>
                  </tr>
                ))}
                {!keys.length ? (
                  <tr>
                    <td className='pj-muted' colSpan={7}>
                      {t('projects.member.noKeysRow')}
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
            <TableFoot
              left={t('projects.member.keysFootLeft', { managers })}
              right={t('projects.member.keysFootRight')}
            />
          </Card>
          <div className='pj-mt'>
            <ReasonsCard budget={b} />
          </div>
        </div>
        <div className='pj-stackcol'>
          <Card id='pj-request-card'>
            <SectionHead
              sub={t('projects.request.cardSub', { owner: ownerName })}
              title={t('projects.request.cardTitle')}
            />
            {pending ? (
              <div className='pj-list'>
                <div>
                  <span className='pj-list__t'>
                    <strong>
                      {t('projects.request.requestedTo', {
                        amount: formatUSDShort(pending.requested_microusd),
                      })}
                    </strong>
                    <small>
                      {formatDayTime(pending.created_at)} ·{' '}
                      {t('projects.request.sentTo', { owner: ownerName })} ·{' '}
                      {pending.reason}
                    </small>
                  </span>
                  <PjBadge tone='warn'>{t('projects.request.pending')}</PjBadge>
                  <ConfirmPop
                    confirmLabel={t('projects.request.withdraw')}
                    onConfirm={() => withdraw(pending.id)}
                    text={t('projects.request.withdrawConfirm')}
                    trigger={
                      <Button size='sm' variant='ghost'>
                        {t('projects.request.withdraw')}
                      </Button>
                    }
                  />
                </div>
                <p className='pj-sub'>{t('projects.request.onlyOne')}</p>
              </div>
            ) : (
              <>
                <div
                  className='pj-fs pj-fs--tight'
                  style={{ gridTemplateColumns: '70px 1fr' }}
                >
                  <span className='pj-fs__l'>
                    {t('projects.request.wantTo')}
                  </span>
                  <div className='pj-fs__v'>
                    <BudgetPicker
                      allowUnlimited={false}
                      disabled={busy}
                      draft={draft}
                      onChange={setDraft}
                      quick={[]}
                      size='sm'
                    />
                    {rangeError ? (
                      <span className='pj-errt'>{rangeError}</span>
                    ) : null}
                  </div>
                  <span className='pj-fs__l'>
                    {t('projects.request.reason')}
                  </span>
                  <div className='pj-fs__v'>
                    <Textarea
                      disabled={busy}
                      onChange={(e) => setReason(e.target.value)}
                      placeholder={t('projects.request.reasonPlaceholder')}
                      rows={2}
                      value={reason}
                    />
                  </div>
                </div>
                <div className='pj-mt-s'>
                  <Button
                    disabled={
                      !validation.ok || !validateBudgetRequestNote(reason)
                    }
                    fullWidth
                    loading={busy}
                    onClick={() => void send()}
                    variant='primary'
                  >
                    {t('projects.request.send')}
                  </Button>
                </div>
              </>
            )}
          </Card>
          <Card>
            <SectionHead title={t('projects.member.historyTitle')} />
            <div className='pj-list'>
              {requestsList
                .filter((r) => r.status !== 'pending')
                .slice(0, 3)
                .map((r) => (
                  <div key={r.id}>
                    <span className='pj-list__t'>
                      <strong>
                        {t('projects.request.requestedTo', {
                          amount: formatUSDShort(r.requested_microusd),
                        })}
                      </strong>
                      <small>
                        {r.status === 'approved'
                          ? t('projects.request.approvedLine', {
                              who: r.handled_by?.username ?? '',
                              when: formatDayTime(r.handled_at),
                              budget: formatUSDShort(
                                r.approved_budget_microusd === undefined
                                  ? r.requested_microusd
                                  : r.approved_budget_microusd,
                              ),
                            })
                          : r.status === 'rejected'
                            ? t('projects.request.rejectedLine', {
                                who: r.handled_by?.username ?? '',
                                when: formatDayTime(r.handled_at),
                                note: r.handled_note ?? '',
                              })
                            : t('projects.request.withdrawnLine')}
                      </small>
                    </span>
                    <PjBadge tone={r.status === 'rejected' ? 'bad' : 'neutral'}>
                      {t(`projects.request.status.${r.status}`)}
                    </PjBadge>
                  </div>
                ))}
              {(b?.history ?? []).map((h) => (
                <div key={h.id}>
                  <span className='pj-mono pj-faint pj-list__when'>
                    {formatMonthDay(h.created_at)}
                  </span>
                  <span className='pj-list__t'>
                    {h.old_budget_microusd === null &&
                    h.new_budget_microusd !== null &&
                    !h.changed_by
                      ? t('projects.member.historyAccepted', {
                          budget: formatUSDShort(h.new_budget_microusd),
                        })
                      : t('projects.member.historyChanged', {
                          who: h.changed_by?.username ?? '',
                          from: formatUSDShort(h.old_budget_microusd),
                          to: formatUSDShort(h.new_budget_microusd),
                        })}
                    <small>{budgetReasonText(h.reason, t)}</small>
                  </span>
                </div>
              ))}
              {!requestsList.length && !(b?.history ?? []).length ? (
                <div className='pj-list__empty'>
                  {t('projects.member.historyEmpty')}
                </div>
              ) : null}
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}

function ReasonsCard({
  budget,
}: {
  budget: ReturnType<typeof useMyBudget>['data'];
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const remaining = budget?.remaining_microusd ?? null;
  const cap = budget?.project_cap;
  const capPct = cap ? percentOf(cap.spent_microusd, cap.cap_microusd) : null;
  const nowFor = (code: string) => {
    switch (code) {
      case 'member_budget_exceeded':
        return budget
          ? budget.budget_microusd === null
            ? t('projects.budget.unlimited')
            : t('projects.member.reasonNowBudget', {
                budget: formatUSDShort(budget.budget_microusd),
                left: formatUSD(remaining),
              })
          : '—';
      case 'key_budget_exceeded':
        return t('projects.member.reasonNowKey');
      case 'project_budget_exceeded':
        return cap && cap.cap_microusd !== null
          ? t('projects.member.reasonNowProject', {
              cap: formatUSDShort(cap.cap_microusd),
              pct: capPct ?? 0,
            })
          : t('projects.budget.notSet');
      case 'model_not_allowed':
        return t('projects.member.reasonNowModel');
      case 'insufficient_quota':
        return budget?.balance_days_left == null
          ? t('projects.common.noForecast')
          : t('projects.member.reasonNowBalance', {
              days: budget.balance_days_left,
            });
      default:
        return t('projects.member.reasonNowAccess');
    }
  };
  return (
    <details className='pj-fold'>
      <summary>
        <span className='pj-fold__t'>{t('projects.member.reasonsTitle')}</span>
        <span className='pj-fold__s'>{t('projects.member.reasonsSub')}</span>
        <ChevronDown aria-hidden='true' className='pj-fold__chev' size={16} />
      </summary>
      <div className='pj-fold__body'>
        <table className='pj-table'>
          <thead>
            <tr>
              <th>{t('projects.member.reasonCol.reason')}</th>
              <th>{t('projects.member.reasonCol.now')}</th>
              <th>{t('projects.member.reasonCol.effect')}</th>
              <th>{t('projects.member.reasonCol.action')}</th>
            </tr>
          </thead>
          <tbody>
            {REJECTION_REASONS.map((code) => (
              <tr key={code}>
                <td>
                  <span className='pj-rsn' data-tone={rejectionTone(code)}>
                    {t(rejectionLabelKey(code))}
                  </span>
                </td>
                <td className='pj-strong'>{nowFor(code)}</td>
                <td className='pj-muted'>{t(rejectionEffectKey(code))}</td>
                <td>
                  {t(rejectionActionKey(code, project.role), {
                    name: project.current?.ownerName ?? '',
                  })}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}
