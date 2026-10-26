/* /console/usage/sessions/:id — one task an agent ran: what it was asked,
 * what it cost against the list price, which models and lanes did the work,
 * and every API call it made, each opening the same receipt as 请求. */
import { ChevronDown } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  SK,
  type SessionLane,
  clockSecondsText,
  clockText,
  durationText,
  officialSaving,
  pct,
  ratio,
  sessionClient,
  sessionLanes,
  shortSessionId,
  shortThreadId,
  spanText,
} from './session-format';
import SessionActivity, {
  type ActivityCall,
  type CallRange,
} from './SessionActivity';
import {
  SessionCost,
  SessionLaneChip,
  sessionStatus,
  sessionTitle,
} from './SessionsView';
import {
  AgentBadge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  ErrorState,
  PendingBlock,
  PendingValue,
  agentName,
  formatCount,
  formatMicroUsd,
} from '../../../components/ui';
import { notify } from '../../../components/ui/toast';
import { copy, showError } from '../../../helpers/utils';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import {
  type AgentSessionDetail,
  type AgentSessionRequest,
  useAgentSessionDetailQuery,
} from '../../../hooks/usage-logs/useAgentSessions';
import { useUsageLogRequestQuery } from '../../../hooks/usage-logs/useUsageLogsQuery';
import { useUsageScope } from '../../../hooks/usage-logs/useUsageScope';
import { LogDetailPanel, type LogRecord } from '../log-support';
import '../log-table.css';
import './sessions.css';
import type { TFunction } from 'i18next';

const CALL_PAGE = 50;
const QUOTE_CLAMP_CHARS = 280;
const QUOTE_CLAMP_LINES = 5;

function downloadFile(name: string, type: string, content: string) {
  const blob = new Blob([content], { type });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = name;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

const csvCell = (value: unknown) => {
  const text = String(value ?? '');
  return /[",\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
};

function requestsCsv(detail: AgentSessionDetail) {
  const header = [
    'created_at',
    'request_id',
    'thread_id',
    'kind',
    'model',
    'lane',
    'prompt_tokens',
    'completion_tokens',
    'cached_tokens',
    'amount_usd',
    'official_usd',
    'duration_seconds',
    'failed',
    'error',
  ];
  const rows = detail.requests.map((request) =>
    [
      new Date(request.created_at * 1000).toISOString(),
      request.request_id,
      request.thread_id,
      request.kind,
      request.model_name,
      request.lane ?? '',
      request.prompt_tokens,
      request.completion_tokens,
      request.cached_tokens,
      (request.amount_microusd / 1_000_000).toFixed(6),
      request.official_microusd
        ? (request.official_microusd / 1_000_000).toFixed(6)
        : '',
      request.duration_seconds,
      request.failed ? 'true' : 'false',
      request.error ?? '',
    ]
      .map(csvCell)
      .join(','),
  );
  return [header.join(','), ...rows].join('\n');
}

export default function SessionDetailPage() {
  const { t } = useTranslation();
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const { admin } = useUsageScope();
  const usageHref = `/console/usage?scope=${admin ? 'platform' : 'self'}`;
  const sessionsHref = `${usageHref}&view=sessions`;
  const query = useAgentSessionDetailQuery({ admin, id });
  const pending = query.isPending;
  const revealed = useDelayedReveal(pending);
  const detail = query.data;

  const copyText = async (value: unknown) => {
    if (await copy(String(value ?? ''))) {
      notify.success(t('page.console.tables.copied'));
    } else {
      showError(t('page.console.tables.copyFailed'));
    }
  };

  return (
    <div
      className='ses ses-detail'
      data-hold={pending && !revealed ? '' : undefined}
    >
      <nav
        aria-label={t(`${SK}.detail.breadcrumbAria`, { defaultValue: '位置' })}
        className='ses-crumbs'
      >
        <Link className='ses-link' to={usageHref}>
          {t(`${SK}.detail.crumbUsage`, { defaultValue: '用量与日志' })}
        </Link>
        <span aria-hidden='true'>/</span>
        <Link className='ses-link' to={sessionsHref}>
          {t(`${SK}.tabs.sessions`, { defaultValue: 'Agent 会话' })}
        </Link>
        <span aria-hidden='true'>/</span>
        <span className='sd-crumb-current'>
          {pending || !detail ? (
            <PendingValue chars={16} />
          ) : (
            sessionTitle(detail.session, t)
          )}
        </span>
      </nav>

      {query.isError && !detail ? (
        <div className='ses-panel ses-state'>
          <ErrorState
            action={
              <span style={{ display: 'inline-flex', gap: 8 }}>
                <button
                  data-owns-chrome
                  className='ses-btn ses-btn--sm'
                  onClick={() => query.refetch()}
                  type='button'
                >
                  {t(`${SK}.retry`, { defaultValue: '重试' })}
                </button>
                <button
                  data-owns-chrome
                  className='ses-btn ses-btn--sm'
                  onClick={() => navigate(sessionsHref)}
                  type='button'
                >
                  {t(`${SK}.detail.backToList`, {
                    defaultValue: '回到会话列表',
                  })}
                </button>
              </span>
            }
            error={query.error}
            title={t(`${SK}.detail.loadFailed`, {
              defaultValue: '没能打开这个会话',
            })}
          />
        </div>
      ) : (
        <>
          <DetailHead admin={admin} copyText={copyText} detail={detail} t={t} />
          <DetailKpis detail={detail} t={t} />
          <div className='sd-mid'>
            <FirstMessage copyText={copyText} detail={detail} t={t} />
            <ModelsPanel detail={detail} t={t} />
          </div>
          {!detail ? (
            <>
              <PendingBlock height={210} />
              <PendingBlock height={480} />
            </>
          ) : detail.requests_retained ? (
            <>
              {!detail.requests_complete ? (
                <div className='ses-panel ses-state'>
                  <p>
                    {t(`${SK}.detail.coverage`, {
                      defaultValue:
                        '已加载 {{loaded}} 次调用，可读取 {{available}} 次，会话合计 {{total}} 次。缺失或未加载的明细不用于全程分析。',
                      loaded: detail.requests.length,
                      available: detail.requests_available,
                      total: detail.session.request_count,
                    })}
                  </p>
                  {query.hasNextPage ? (
                    <Button
                      disabled={query.isFetchingNextPage}
                      onClick={() => void query.fetchNextPage()}
                      variant='secondary'
                    >
                      {t(`${SK}.detail.loadMore`, { defaultValue: '加载更多' })}
                    </Button>
                  ) : null}
                  {query.isFetchNextPageError ? (
                    <p role='alert'>
                      {t(`${SK}.detail.pageFailed`, {
                        defaultValue: '未能加载下一批，请重试。',
                      })}
                    </p>
                  ) : null}
                </div>
              ) : null}
              <ActivityAndCalls
                admin={admin}
                copyText={copyText}
                detail={detail}
                t={t}
              />
            </>
          ) : (
            <div className='ses-panel ses-aged'>
              {t(`${SK}.detail.aged`, {
                defaultValue:
                  '当前没有可读取的请求明细，可能未记录或已超过保留期；上方合计来自保留的会话汇总。',
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function DetailHead({
  admin,
  copyText,
  detail,
  t,
}: {
  admin: boolean;
  copyText: (value: unknown) => void;
  detail?: AgentSessionDetail;
  t: TFunction;
}) {
  if (!detail) {
    return (
      <header className='sd-head'>
        <div className='sd-head__text'>
          <h1 className='sd-title'>
            <PendingValue chars={30} />
          </h1>
          <span className='sd-head__meta'>
            <PendingValue chars={60} />
          </span>
        </div>
      </header>
    );
  }
  const session = detail.session;
  const client = sessionClient(session);
  const status = sessionStatus(session, t);
  const title = detail.first_message
    ? detail.first_message.split('\n').find((line) => line.trim()) ||
      detail.first_message
    : sessionTitle(session, t);
  const exportName = `session-${shortSessionId(session.session_id) || session.id}`;
  const facts = [
    admin && detail.username ? (
      <strong key='user'>{detail.username}</strong>
    ) : null,
    client ? (
      <span className='sd-head__agent' key='agent'>
        <AgentBadge client={client} size='sm' />
        {agentName(client)}
        {session.client_version ? ` ${session.client_version}` : ''}
      </span>
    ) : null,
    <span key='span'>
      <span className='sd-mono'>
        {spanText(session.started_at, session.last_at)}
      </span>
      {t(`${SK}.detail.durationParen`, {
        defaultValue: '（{{duration}}）',
        duration: durationText(session.last_at - session.started_at, t),
      })}
    </span>,
    session.token_name ? <span key='key'>Key {session.token_name}</span> : null,
    <span
      className='sv-status'
      data-tone={status.tone}
      key='status'
      title={status.hint}
    >
      {status.label}
    </span>,
  ].filter(Boolean);
  return (
    <header className='sd-head'>
      <div className='sd-head__text'>
        <h1 className='sd-title' title={title}>
          {title}
        </h1>
        <span className='sd-head__meta'>
          {facts.map((fact, index) => (
            <span className='sd-head__fact' key={index}>
              {index > 0 ? <span aria-hidden='true'>·</span> : null}
              {fact}
            </span>
          ))}
        </span>
      </div>
      <div className='sd-head__actions'>
        <Button
          onClick={() => copyText(session.session_id)}
          variant='secondary'
        >
          {t(`${SK}.detail.copyId`, { defaultValue: '复制会话编号' })}
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger>
            <Button variant='secondary'>
              {t(`${SK}.detail.export`, { defaultValue: '导出' })}
              <ChevronDown aria-hidden='true' size={13} />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end'>
            <DropdownMenuItem
              onSelect={() =>
                downloadFile(
                  `${exportName}.json`,
                  'application/json',
                  JSON.stringify(detail, null, 2),
                )
              }
            >
              {t(`${SK}.detail.exportJson`, {
                defaultValue: '会话与已加载明细（JSON）',
              })}
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={!detail.requests_complete}
              onSelect={() =>
                downloadFile(
                  `${exportName}-calls.csv`,
                  'text/csv;charset=utf-8',
                  requestsCsv(detail),
                )
              }
            >
              {t(`${SK}.detail.exportCsv`, { defaultValue: '调用明细（CSV）' })}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}

function DetailKpis({
  detail,
  t,
}: {
  detail?: AgentSessionDetail;
  t: TFunction;
}) {
  const session = detail?.session;
  const saving = session ? officialSaving(session) : null;
  const paidShare = saving ? Math.max(0, 100 - saving.pct) : null;
  const status = session ? sessionStatus(session, t) : null;
  const hit = session
    ? ratio(session.cached_tokens, session.prompt_tokens)
    : null;
  const subagentAmount = (detail?.threads ?? [])
    .filter((thread) => thread.thread_id)
    .reduce((sum, thread) => sum + thread.amount_microusd, 0);
  const subagentShare =
    session && session.amount_microusd > 0
      ? subagentAmount / session.amount_microusd
      : null;
  const value = (chars: number, render: () => string) =>
    session ? render() : <PendingValue chars={chars} />;
  return (
    <div className='sv-summary'>
      <div className='sv-kpi sv-kpi--spend'>
        <span className='sv-kpi__top'>
          <span className='sv-kpi__label'>
            {t(`${SK}.kpi.spend`, { defaultValue: '花费' })}
          </span>
          {saving ? (
            <b className='sv-kpi__saved'>
              {t(`${SK}.kpi.saved`, {
                amount: formatMicroUsd(saving.saved),
                defaultValue: '比官方价省 {{amount}}（{{pct}}%）',
                pct: saving.pct,
              })}
            </b>
          ) : null}
        </span>
        <span className='sv-kpi__lead'>
          <strong className='sv-kpi__value'>
            {value(6, () => formatMicroUsd(session?.amount_microusd))}
          </strong>
          {saving ? (
            <span className='sv-kpi__official'>
              {t(`${SK}.kpi.official`, { defaultValue: '官方价' })}{' '}
              <s>{formatMicroUsd(saving.official)}</s>
            </span>
          ) : null}
        </span>
        {paidShare !== null ? (
          <span
            aria-hidden='true'
            className='sv-savebar'
            style={{
              gridTemplateColumns: `${paidShare}fr ${100 - paidShare}fr`,
            }}
          >
            <i />
            <i />
          </span>
        ) : null}
      </div>
      <div className='sv-kpi'>
        <span className='sv-kpi__label'>
          {t(`${SK}.list.col.usage`, { defaultValue: '用量' })}
        </span>
        <strong className='sv-kpi__value'>
          {value(5, () =>
            formatCount(
              (session?.prompt_tokens ?? 0) + (session?.completion_tokens ?? 0),
            ),
          )}
        </strong>
        <span className='sv-kpi__note'>
          {value(14, () =>
            t(`${SK}.list.callsCache`, {
              defaultValue: '{{n}} 次调用 · 缓存 {{pct}}',
              n: formatCount(session?.request_count),
              pct: pct(hit),
            }),
          )}
        </span>
      </div>
      <div className='sv-kpi'>
        <span className='sv-kpi__label'>
          {t(`${SK}.detail.result`, { defaultValue: '结果' })}
        </span>
        <strong className='sv-kpi__value' data-tone={status?.tone}>
          {status ? status.label : <PendingValue chars={4} />}
        </strong>
        <span className='sv-kpi__note'>
          {value(12, () =>
            t(`${SK}.detail.resultNote`, {
              defaultValue: '{{ok}} 次成功 · {{failed}} 次失败',
              failed: session?.error_count ?? 0,
              ok: formatCount(
                (session?.request_count ?? 0) - (session?.error_count ?? 0),
              ),
            }),
          )}
        </span>
      </div>
      <div className='sv-kpi'>
        <span className='sv-kpi__label'>
          {t(`${SK}.detail.subagents`, { defaultValue: '子代理' })}
        </span>
        <strong className='sv-kpi__value'>
          {value(3, () =>
            t(`${SK}.detail.subagentCount`, {
              defaultValue: '{{n}} 个',
              n: session?.subagent_count ?? 0,
            }),
          )}
        </strong>
        <span className='sv-kpi__note'>
          {value(10, () =>
            subagentShare && subagentShare > 0
              ? t(`${SK}.detail.subagentShare`, {
                  defaultValue: '花掉这个会话的 {{pct}}',
                  pct: pct(subagentShare),
                })
              : t(`${SK}.detail.noSubagent`, {
                  defaultValue: '没有用到子代理',
                }),
          )}
        </span>
      </div>
    </div>
  );
}

function FirstMessage({
  copyText,
  detail,
  t,
}: {
  copyText: (value: unknown) => void;
  detail?: AgentSessionDetail;
  t: TFunction;
}) {
  const [expanded, setExpanded] = useState(false);
  const text = detail?.first_message ?? '';
  const long =
    text.length > QUOTE_CLAMP_CHARS ||
    text.split('\n').length > QUOTE_CLAMP_LINES;
  return (
    <section className='sd-card'>
      <div className='sd-card__head'>
        <h2 className='sd-card__title'>
          {t(`${SK}.detail.firstMessage`, { defaultValue: '首条消息' })}
        </h2>
        <Button
          disabled={!text}
          onClick={() => copyText(text)}
          size='sm'
          variant='secondary'
        >
          {t(`${SK}.detail.copy`, { defaultValue: '复制' })}
        </Button>
      </div>
      {!detail ? (
        <PendingBlock height={108} />
      ) : text ? (
        <blockquote
          className='ses-quote'
          data-clamped={long && !expanded ? '' : undefined}
        >
          {text}
        </blockquote>
      ) : (
        <blockquote className='ses-quote ses-dim'>
          {t(`${SK}.detail.noFirstMessage`, {
            defaultValue: '这个会话没有记录到首条消息。',
          })}
        </blockquote>
      )}
      <span className='ses-note'>
        {t(`${SK}.detail.firstMessageNote`, {
          defaultValue:
            '首条消息仅在已采集且未超过保留期时可用，仅你本人和平台管理员可见。',
        })}
        {long ? (
          <button
            data-owns-chrome
            className='ses-textlink'
            onClick={() => setExpanded((value) => !value)}
            type='button'
          >
            {expanded
              ? t(`${SK}.detail.collapse`, { defaultValue: '收起' })
              : t(`${SK}.detail.expand`, { defaultValue: '展开全文' })}
          </button>
        ) : null}
      </span>
    </section>
  );
}

function ModelsPanel({
  detail,
  t,
}: {
  detail?: AgentSessionDetail;
  t: TFunction;
}) {
  const models = detail?.models ?? [];
  const total = models.reduce((sum, model) => sum + model.amount_microusd, 0);
  return (
    <section className='sd-card'>
      <div className='sd-card__head'>
        <h2 className='sd-card__title'>
          {t(`${SK}.detail.byModel`, { defaultValue: '按模型' })}
        </h2>
      </div>
      {!detail ? (
        <PendingBlock height={108} />
      ) : models.length === 0 ? (
        <span className='ses-note'>
          {t(`${SK}.detail.noModels`, {
            defaultValue: '还没有成功的调用，没有模型用量。',
          })}
        </span>
      ) : (
        <div className='sd-models'>
          {models.map((model) => (
            <div className='sd-model' key={model.model_name}>
              <div className='sd-model__name'>
                <span className='sd-model__label'>
                  <span className='sv-mono sv-ellipsis'>
                    {model.model_name}
                  </span>
                  {sessionLanes([model]).map(({ count, lane }) => (
                    <SessionLaneChip
                      count={lane === 'fallback' ? count : undefined}
                      key={lane}
                      lane={lane}
                      t={t}
                    />
                  ))}
                </span>
                <span aria-hidden='true' className='sd-model__bar'>
                  <i
                    style={{
                      width: `${total > 0 ? (model.amount_microusd / total) * 100 : 0}%`,
                    }}
                  />
                </span>
              </div>
              <span className='sv-sub sv-right'>
                {t(`${SK}.detail.modelCalls`, {
                  defaultValue: '{{n}} 次调用',
                  n: formatCount(model.request_count),
                })}
              </span>
              <SessionCost
                amount={model.amount_microusd}
                compared={model.compared_microusd}
                official={model.official_microusd}
                t={t}
              />
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

type CallFilter = 'all' | 'failed';

const threadName = (request: { thread_id: string }) =>
  request.thread_id ? shortThreadId(request.thread_id) : 'main';

/** The activity view above and the call table below share one selection:
 *  a bar opens its call's receipt, a work segment narrows the table. */
function ActivityAndCalls({
  admin,
  copyText,
  detail,
  t,
}: {
  admin: boolean;
  copyText: (value: unknown) => void;
  detail: AgentSessionDetail;
  t: TFunction;
}) {
  const calls = useMemo<ActivityCall[]>(
    () =>
      detail.requests.map((request, index) => ({
        key: request.request_id || `${request.created_at}-${index}`,
        request,
      })),
    [detail.requests],
  );
  const [filter, setFilter] = useState<CallFilter>('all');
  const [thread, setThread] = useState<string | null>(null);
  const [range, setRange] = useState<CallRange | null>(null);
  const [shown, setShown] = useState(CALL_PAGE);
  const [open, setOpen] = useState<string | null>(null);
  const [scrollTo, setScrollTo] = useState<string | null>(null);

  const newestFirst = useMemo(() => [...calls].reverse(), [calls]);
  const filtered = newestFirst.filter(
    ({ request }) =>
      (filter === 'all' || request.failed) &&
      (thread === null || request.thread_id === thread) &&
      (range === null ||
        (request.created_at >= range.from && request.created_at <= range.to)),
  );

  useEffect(() => {
    if (!scrollTo) return;
    document
      .getElementById(`call-${scrollTo}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    setScrollTo(null);
  }, [scrollTo]);

  const pickCall = (key: string) => {
    setFilter('all');
    setThread(null);
    setRange(null);
    const index = newestFirst.findIndex((call) => call.key === key);
    if (index >= 0) setShown((value) => Math.max(value, index + 1));
    setOpen(key);
    setScrollTo(key);
  };
  const pickRange = (next: CallRange) => {
    setRange(next);
    setShown(CALL_PAGE);
    document
      .getElementById('requests')
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  return (
    <>
      <SessionActivity
        calls={calls}
        detail={detail}
        onPickCall={pickCall}
        onPickRange={pickRange}
        t={t}
      />
      <CallsTable
        admin={admin}
        copyText={copyText}
        detail={detail}
        filter={filter}
        filtered={filtered}
        open={open}
        range={range}
        setFilter={setFilter}
        setOpen={setOpen}
        setRange={setRange}
        setShown={setShown}
        setThread={setThread}
        shown={shown}
        t={t}
        thread={thread}
      />
    </>
  );
}

function CallsTable({
  admin,
  copyText,
  detail,
  filter,
  filtered,
  open,
  range,
  setFilter,
  setOpen,
  setRange,
  setShown,
  setThread,
  shown,
  t,
  thread,
}: {
  admin: boolean;
  copyText: (value: unknown) => void;
  detail: AgentSessionDetail;
  filter: CallFilter;
  filtered: ActivityCall[];
  open: string | null;
  range: CallRange | null;
  setFilter: (value: CallFilter) => void;
  setOpen: (value: string | null) => void;
  setRange: (value: CallRange | null) => void;
  setShown: (update: (value: number) => number) => void;
  setThread: (value: string | null) => void;
  shown: number;
  t: TFunction;
  thread: string | null;
}) {
  const threads = detail.threads;
  const total = detail.session.request_count;

  return (
    <section className='sv-table' id='requests'>
      <div className='sd-calls__head'>
        <h2 className='sd-card__title'>
          {t(`${SK}.detail.callsTitle`, {
            defaultValue: '{{n}} 次调用',
            n: formatCount(total),
          })}
        </h2>
        <span className='sd-calls__tools'>
          {range ? (
            <button
              data-owns-chrome
              aria-pressed
              className='ses-btn'
              onClick={() => setRange(null)}
              title={t(`${SK}.activity.clearRange`, {
                defaultValue: '显示全部调用',
              })}
              type='button'
            >
              {t(`${SK}.activity.onlyRange`, {
                defaultValue: '只看{{label}}',
                label: range.label,
              })}
              <span aria-hidden='true'>×</span>
            </button>
          ) : null}
          <span className='ses-seg' role='group'>
            {(['all', 'failed'] as CallFilter[]).map((value) => (
              <button
                data-owns-chrome
                aria-pressed={filter === value}
                key={value}
                onClick={() => setFilter(value)}
                type='button'
              >
                {value === 'all'
                  ? t(`${SK}.detail.filterAll`, { defaultValue: '全部' })
                  : t(`${SK}.detail.filterFailed`, {
                      defaultValue: '只看失败',
                    })}
              </button>
            ))}
          </span>
          {threads.length > 1 ? (
            <DropdownMenu>
              <DropdownMenuTrigger>
                <button
                  data-owns-chrome
                  aria-pressed={thread !== null}
                  className='ses-btn'
                  type='button'
                >
                  {thread === null
                    ? t(`${SK}.detail.allThreads`, {
                        defaultValue: '全部线程',
                      })
                    : threadName({ thread_id: thread })}
                  <ChevronDown aria-hidden='true' size={13} />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align='end'>
                <DropdownMenuItem onSelect={() => setThread(null)}>
                  {t(`${SK}.detail.allThreads`, { defaultValue: '全部线程' })}
                </DropdownMenuItem>
                {threads.map((item) => (
                  <DropdownMenuItem
                    key={item.thread_id || 'main'}
                    onSelect={() => setThread(item.thread_id)}
                  >
                    {threadName(item)}
                    {item.thread_id ? ` · ${item.kind || 'subagent'}` : ''}
                    {' · '}
                    {t(`${SK}.detail.modelCalls`, {
                      defaultValue: '{{n}} 次调用',
                      n: formatCount(item.request_count),
                    })}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
        </span>
      </div>
      <div className='sv-scroll'>
        <div className='sd-call sd-call--head'>
          <span>{t(`${SK}.list.col.time`, { defaultValue: '时间' })}</span>
          <span>{t(`${SK}.detail.col.thread`, { defaultValue: '线程' })}</span>
          <span>
            {t(`${SK}.list.col.modelLane`, { defaultValue: '模型 · 线路' })}
          </span>
          <span className='sv-right'>
            {t(`${SK}.detail.col.io`, { defaultValue: '输入 → 输出' })}
          </span>
          <span className='sv-right'>
            {t(`${SK}.detail.col.duration`, { defaultValue: '耗时' })}
          </span>
          <span>{t(`${SK}.list.col.status`, { defaultValue: '状态' })}</span>
          <span className='sv-right'>
            {t(`${SK}.kpi.spend`, { defaultValue: '花费' })}
          </span>
        </div>
        {filtered.length === 0 ? (
          <div className='ses-state ses-note'>
            {t(`${SK}.detail.noRequests`, {
              defaultValue: '没有符合条件的调用。',
            })}
          </div>
        ) : (
          filtered.slice(0, shown).map(({ key, request }) => {
            const isOpen = open === key;
            return (
              <CallRow
                admin={admin}
                copyText={copyText}
                id={`call-${key}`}
                isOpen={isOpen}
                key={key}
                onToggle={() => setOpen(isOpen ? null : key)}
                request={request}
                t={t}
              />
            );
          })
        )}
      </div>
      <div className='sv-foot'>
        <span>
          {t(`${SK}.detail.shownCount`, {
            defaultValue: '显示 {{shown}} / {{total}} 次 · 点任意一行展开回执',
            shown: Math.min(shown, filtered.length),
            total: filtered.length,
          })}
          {detail.requests.length < total
            ? ` ${t(`${SK}.detail.truncated`, {
                defaultValue: '（已加载 {{n}} 次，明细可能不完整）',
                n: detail.requests.length,
              })}`
            : ''}
        </span>
        {shown < filtered.length ? (
          <button
            data-owns-chrome
            className='ses-btn ses-btn--sm'
            onClick={() => setShown((value) => value + CALL_PAGE)}
            type='button'
          >
            {t(`${SK}.detail.loadMore`, { defaultValue: '加载更多' })}
          </button>
        ) : null}
      </div>
    </section>
  );
}

function CallRow({
  admin,
  copyText,
  id,
  isOpen,
  onToggle,
  request,
  t,
}: {
  admin: boolean;
  copyText: (value: unknown) => void;
  id: string;
  isOpen: boolean;
  onToggle: () => void;
  request: AgentSessionRequest;
  t: TFunction;
}) {
  const openable = Boolean(request.request_id);
  const kind = request.kind && request.kind !== 'main' ? request.kind : '';
  return (
    <>
      {/* The row is a pointer shortcut; the time button is its keyboard
          handle. */}
      {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions */}
      <div
        className='sd-call'
        data-failed={request.failed || undefined}
        id={id}
        data-open={isOpen || undefined}
        onClick={openable ? onToggle : undefined}
      >
        {openable ? (
          <button
            data-owns-chrome
            aria-expanded={isOpen}
            className='sd-call__time'
            onClick={(event) => {
              event.stopPropagation();
              onToggle();
            }}
            type='button'
          >
            {clockSecondsText(request.created_at)}
          </button>
        ) : (
          <span className='sd-call__time'>
            {clockSecondsText(request.created_at)}
          </span>
        )}
        <span className='sd-call__thread'>
          <span className='sv-ellipsis' title={request.thread_id || undefined}>
            {threadName(request)}
          </span>
          {kind ? <span className='lz-kind-tag'>{kind}</span> : null}
        </span>
        <span className='sd-call__model'>
          <span className='sv-mono sv-ellipsis' title={request.model_name}>
            {request.model_name || '—'}
          </span>
          {request.lane ? (
            <SessionLaneChip lane={request.lane as SessionLane} t={t} />
          ) : null}
        </span>
        <span className='sv-mono sv-right sv-muted'>
          {request.failed
            ? '—'
            : `${formatCount(request.prompt_tokens)} → ${formatCount(request.completion_tokens)}`}
        </span>
        <span className='sv-mono sv-right sv-muted'>
          {`${request.duration_seconds}s`}
        </span>
        <span>
          {request.failed ? (
            <span
              className='sv-status sd-call__error'
              data-tone='danger'
              title={request.error || undefined}
            >
              {request.error
                ? t(`${SK}.detail.failedWith`, {
                    defaultValue: '失败 · {{error}}',
                    error: request.error,
                  })
                : t(`${SK}.detail.failed`, { defaultValue: '失败' })}
            </span>
          ) : (
            <span className='sv-status' data-tone='ok'>
              {t(`${SK}.status.ok`, { defaultValue: '成功' })}
            </span>
          )}
        </span>
        {request.failed && request.amount_microusd <= 0 ? (
          <span className='sv-two sv-right'>
            <span className='sv-money'>{formatMicroUsd(0)}</span>
            <span className='sv-sub'>
              {t(`${SK}.detail.notCharged`, { defaultValue: '未扣费' })}
            </span>
          </span>
        ) : (
          <SessionCost
            amount={request.amount_microusd}
            compared={request.official_microusd ? request.amount_microusd : 0}
            official={request.official_microusd}
            quiet
            t={t}
          />
        )}
      </div>
      {isOpen && request.request_id ? (
        <CallReceipt
          admin={admin}
          copyText={copyText}
          requestId={request.request_id}
          t={t}
        />
      ) : null}
    </>
  );
}

/** The same receipt a row opens in 请求, fetched on demand. */
function CallReceipt({
  admin,
  copyText,
  requestId,
  t,
}: {
  admin: boolean;
  copyText: (value: unknown) => void;
  requestId: string;
  t: TFunction;
}) {
  const query = useUsageLogRequestQuery({ admin, requestId });
  const revealed = useDelayedReveal(query.isPending);
  return (
    <div className='sd-receipt'>
      {query.isPending ? (
        <div data-hold={revealed ? undefined : ''}>
          <PendingBlock height={180} />
        </div>
      ) : query.data ? (
        <div className='logv2-receipt'>
          <LogDetailPanel
            copyText={copyText}
            hideSession
            isAdminUser={admin}
            record={query.data as LogRecord}
            t={t}
          />
        </div>
      ) : (
        <span className='ses-note'>
          {t(`${SK}.detail.receiptMissing`, {
            defaultValue: '这次调用的回执已经过期。',
          })}
        </span>
      )}
    </div>
  );
}
