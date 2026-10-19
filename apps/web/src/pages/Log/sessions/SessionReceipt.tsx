/* The session a request belongs to: a cell in the 请求 table and a block in
 * the expanded receipt. */
import { useNavigate } from 'react-router-dom';
import {
  SK,
  isUngrouped,
  requestKind,
  sessionSourceLabel,
  shortSessionId,
  shortThreadId,
} from './session-format';
import { sessionDetailHref } from './SessionsView';
import { AgentBadge, PendingValue } from '../../../components/ui';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import {
  useAgentSessionDetailQuery,
  useAgentSessionLookupQuery,
} from '../../../hooks/usage-logs/useAgentSessions';
import './sessions.css';
import type { TFunction } from 'i18next';

type SessionFields = {
  client_framework?: string;
  created_at?: number;
  parent_thread_id?: string;
  request_id?: string;
  request_kind?: string;
  session_id?: string;
  session_source?: string;
  thread_id?: string;
  user_id?: number | string;
};

/** 请求 table cell: badge + short id + kind, or a ghost "未带编号". */
export function SessionLogCell({
  record,
  t,
}: {
  record: SessionFields;
  t: TFunction;
}) {
  if (!record.session_id) return <span className='logv2-faint'>—</span>;
  // Requests grouped by Key carried no session id: they belong to no agent
  // session.
  if (isUngrouped(record.session_source))
    return <span className='logv2-faint'>—</span>;
  const kind = requestKind(record);
  return (
    <span className='ses-logcell'>
      <AgentBadge client={record.client_framework} size='sm' />
      <span className='ses-logcell__id' title={record.session_id}>
        {shortSessionId(record.session_id)}
      </span>
      <span
        className='lz-kind-tag'
        data-fill={kind === 'compact' ? '' : undefined}
      >
        {kind}
      </span>
    </span>
  );
}

/** Receipt block: which session, which thread, where in the session, and
 *  where the id came from. */
export function SessionReceiptBlock({
  isAdminUser,
  record,
  t,
}: {
  isAdminUser: boolean;
  record: SessionFields;
  t: TFunction;
}) {
  const navigate = useNavigate();
  const sessionId = record.session_id || '';
  const lookup = useAgentSessionLookupQuery({
    admin: isAdminUser,
    at: Number(record.created_at) || 0,
    enabled: Boolean(sessionId) && !isUngrouped(record.session_source),
    sessionId,
    userId: isAdminUser ? record.user_id : undefined,
  });
  const session = lookup.data ?? null;
  const detail = useAgentSessionDetailQuery({
    admin: isAdminUser,
    enabled: Boolean(session?.id && record.request_id),
    id: session?.id ?? '',
  });
  const waiting = lookup.isPending || (Boolean(session) && detail.isPending);
  const revealed = useDelayedReveal(waiting);
  if (!sessionId || isUngrouped(record.session_source)) return null;

  const kind = requestKind(record);
  const position = (() => {
    if (!detail.data || !record.request_id) return null;
    const index = detail.data.requests.findIndex(
      (request) => request.request_id === record.request_id,
    );
    return index < 0 ? null : index + 1;
  })();
  const showPosition =
    Boolean(record.request_id) &&
    (lookup.isPending ||
      (Boolean(session) && detail.isPending) ||
      position !== null);
  const openable = Boolean(session?.id);

  return (
    <div
      className='ses-receipt'
      data-hold={waiting && !revealed ? '' : undefined}
    >
      <span className='ses-receipt__head'>
        <strong>
          {t(`${SK}.receipt.title`, { defaultValue: 'Agent 会话' })}
        </strong>
        <span style={{ display: 'inline-flex', gap: 8 }}>
          <button
            data-owns-chrome
            className='ses-btn ses-btn--sm'
            disabled={!openable}
            onClick={(event) => {
              event.stopPropagation();
              if (session)
                navigate(
                  `${sessionDetailHref(session.id, isAdminUser)}#requests`,
                );
            }}
            type='button'
          >
            {t(`${SK}.receipt.onlyThis`, { defaultValue: '只看这个会话' })}
          </button>
          <button
            data-owns-chrome
            className='ses-btn ses-btn--sm'
            disabled={!openable}
            onClick={(event) => {
              event.stopPropagation();
              if (session) navigate(sessionDetailHref(session.id, isAdminUser));
            }}
            type='button'
          >
            {t(`${SK}.receipt.open`, { defaultValue: '打开会话 →' })}
          </button>
        </span>
      </span>
      <span className='ses-receipt__fact'>
        <span className='ses-note'>
          {t(`${SK}.receipt.session`, { defaultValue: '所属会话' })}
        </span>
        <span>
          <AgentBadge client={record.client_framework} size='sm' />
          <span className='ses-mono' title={sessionId}>
            {shortSessionId(sessionId)}
          </span>
        </span>
      </span>
      <span className='ses-receipt__fact'>
        <span className='ses-note'>
          {t(`${SK}.receipt.threadKind`, { defaultValue: '线程 · 类型' })}
        </span>
        <span>
          <span
            className='lz-kind-tag'
            data-fill={record.thread_id ? undefined : ''}
            title={record.thread_id || undefined}
          >
            {record.thread_id ? shortThreadId(record.thread_id) : 'main'}
          </span>
          {kind !== 'main' || record.thread_id ? (
            <span className='lz-kind-tag'>{kind}</span>
          ) : null}
        </span>
      </span>
      {showPosition ? (
        <span className='ses-receipt__fact'>
          <span className='ses-note'>
            {t(`${SK}.receipt.position`, { defaultValue: '在会话里的位置' })}
          </span>
          <span className='ses-num'>
            {position === null || !session ? (
              <PendingValue chars={10} />
            ) : (
              t(`${SK}.receipt.positionValue`, {
                defaultValue: '第 {{n}} / {{total}} 次',
                n: position,
                total: session.request_count,
              })
            )}
          </span>
        </span>
      ) : null}
      <span className='ses-receipt__fact'>
        <span className='ses-note'>
          {t(`${SK}.receipt.source`, { defaultValue: '编号来源' })}
        </span>
        <span className='ses-mono' style={{ fontSize: 11.5 }}>
          {sessionSourceLabel(record.session_source, t)}
        </span>
      </span>
    </div>
  );
}
