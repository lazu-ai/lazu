/* 平台管理 · 客户账户 → 会话: what this customer's agents have been doing,
 * so a complaint can be read against the actual tasks. Operator view. */
import { Link } from 'react-router-dom';
import { SK, sessionClient, whenText } from './session-format';
import { sessionDetailHref, sessionStatus, sessionTitle } from './SessionsView';
import {
  AgentBadge,
  PendingValue,
  agentName,
  formatCount,
  formatMicroUsd,
} from '../../../components/ui';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import { useAgentSessionsQuery } from '../../../hooks/usage-logs/useAgentSessions';
import './sessions.css';
import type { TFunction } from 'i18next';

const ROWS = 8;

type Props = {
  clientBreakdown?: { count: number; framework: string }[];
  /** Internal user id the admin endpoint narrows by. */
  userId?: number;
  t: TFunction;
};

export default function AccountSessions({ clientBreakdown, t, userId }: Props) {
  const query = useAgentSessionsQuery({
    admin: true,
    enabled: Boolean(userId),
    page: 1,
    pageSize: ROWS,
    range: '7d',
    userId,
  });
  const pending = query.isPending;
  const revealed = useDelayedReveal(pending);
  const summary = query.data?.summary;
  const items = query.data?.items ?? [];

  const clientTotal = (clientBreakdown ?? []).reduce(
    (sum, entry) => sum + (entry.count || 0),
    0,
  );
  const clients = [...(clientBreakdown ?? [])]
    .filter((entry) => entry.framework && entry.count > 0)
    .sort((a, b) => b.count - a.count)
    .slice(0, 3);

  return (
    <div className='acc-ses' data-hold={pending && !revealed ? '' : undefined}>
      <div className='acc-ses__clients'>
        <span className='ses-note'>
          {t(`${SK}.account.clients`, { defaultValue: '常用客户端' })}
        </span>
        {clients.length ? (
          clients.map((entry) => (
            <span className='lz-kind-tag' key={entry.framework}>
              {agentName(entry.framework)}{' '}
              {clientTotal > 0
                ? `${Math.round((entry.count / clientTotal) * 100)}%`
                : ''}
            </span>
          ))
        ) : (
          <span className='ses-note'>—</span>
        )}
        <span className='ses-note'>
          {pending || !summary ? (
            <PendingValue chars={30} />
          ) : (
            t(`${SK}.account.summary`, {
              amount: formatMicroUsd(summary.amount_microusd),
              defaultValue:
                '最近 7 天 · {{sessions}} 个会话 · {{tokens}} tokens · {{amount}}',
              sessions: summary.sessions,
              tokens: formatCount(summary.tokens),
            })
          )}
        </span>
      </div>
      <div className='acc-ses__list'>
        {pending ? (
          Array.from({ length: 4 }, (_, index) => (
            <div aria-hidden='true' className='acc-ses__row' key={index}>
              <span className='lz-agent-badge' data-ghost>
                &nbsp;
              </span>
              <span className='ses-stack'>
                <PendingValue chars={24} />
                <PendingValue chars={10} />
              </span>
              <PendingValue chars={8} />
              <PendingValue chars={10} />
              <PendingValue chars={5} />
              <PendingValue chars={5} />
            </div>
          ))
        ) : query.isError ? (
          <div className='acc-empty'>
            {t(`${SK}.account.failed`, { defaultValue: '会话加载失败' })} ·{' '}
            <button
              data-owns-chrome
              className='acc-sec__jump'
              onClick={() => query.refetch()}
              style={{ marginLeft: 0 }}
              type='button'
            >
              {t(`${SK}.retry`, { defaultValue: '重试' })}
            </button>
          </div>
        ) : items.length === 0 ? (
          <div className='acc-empty'>
            {t(`${SK}.account.empty`, { defaultValue: '最近 7 天没有会话' })}
          </div>
        ) : (
          items.map((session) => {
            const status = sessionStatus(session, t);
            return (
              <div className='acc-ses__row' key={session.id}>
                <AgentBadge client={sessionClient(session)} />
                <span className='ses-stack' style={{ gap: 2 }}>
                  <Link
                    className='ses-rowlink'
                    style={{ fontSize: 13 }}
                    title={sessionTitle(session, t)}
                    to={sessionDetailHref(session.id, true)}
                  >
                    {sessionTitle(session, t)}
                  </Link>
                  <span className='ses-note ses-ellipsis'>
                    {session.token_name || '—'}
                  </span>
                </span>
                <span className='ses-num ses-dim'>
                  {whenText(session.last_at, t)}
                </span>
                <span className='ses-num'>
                  <strong style={{ fontWeight: 600 }}>
                    {formatCount(session.tokens)}
                  </strong>
                  {' · '}
                  {t(`${SK}.kpi.times`, {
                    defaultValue: '{{value}} 次',
                    value: formatCount(session.request_count),
                  })}
                </span>
                <span className='ses-cost' style={{ fontSize: 12.5 }}>
                  {formatMicroUsd(session.amount_microusd)}
                </span>
                <span className='ses-status' data-tone={status.tone}>
                  {status.label}
                </span>
              </div>
            );
          })
        )}
      </div>
      <span className='ses-note'>
        {t(`${SK}.account.auditNote`, {
          defaultValue: '管理员能看到首条消息；打开会话详情会写一条访问审计。',
        })}
      </span>
    </div>
  );
}
