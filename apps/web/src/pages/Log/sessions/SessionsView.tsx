/* 用量与日志 · Agent 会话: one row per task an agent ran, with the range's
 * totals above it. Requests that carried no session id are not agent
 * sessions; they live only in the API 调用 tab. */
import { ChevronDown } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  SESSION_RANGES,
  SK,
  type SessionLane,
  durationText,
  officialSaving,
  pct,
  rangeLabel,
  ratio,
  sessionClient,
  sessionLanes,
  sessionState,
  spanText,
  spanTextCompact,
} from './session-format';
import {
  AgentBadge,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  EmptyState,
  ErrorState,
  Pagination,
  PendingValue,
  agentName,
  formatCount,
  formatMicroUsd,
} from '../../../components/ui';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import {
  type AgentSessionListItem,
  type AgentSessionListSummary,
  type AgentSessionRange,
  useAgentSessionsQuery,
} from '../../../hooks/usage-logs/useAgentSessions';
import '../log-table.css';
import './sessions.css';
import type { TFunction } from 'i18next';

const PAGE_SIZE = 20;
const PENDING_ROWS = 8;

export const sessionDetailHref = (id: string, admin: boolean) =>
  `/console/usage/sessions/${encodeURIComponent(id)}?scope=${admin ? 'platform' : 'self'}`;

type Props = {
  admin: boolean;
  t: TFunction;
};

function SearchIcon() {
  return (
    <svg aria-hidden='true' height='14' viewBox='0 0 16 16' width='14'>
      <path
        d='M7 2.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM10.4 10.4l3.1 3.1'
        fill='none'
        stroke='currentColor'
        strokeLinecap='round'
        strokeWidth='1.5'
      />
    </svg>
  );
}

const KIND_NAMES: Record<string, string> = {
  compact: '上下文压缩',
  review: '代码审查',
  subagent: '子代理',
};

/** subagent ×4 / compact ×2 / review ×1 — only the ones that happened. */
export function SessionKindTags({
  session,
  t,
}: {
  session: AgentSessionListItem;
  t: TFunction;
}) {
  const tags: [string, number][] = [
    ['subagent', session.subagent_count],
    ['compact', session.compact_count],
    ['review', session.review_count],
  ];
  return (
    <>
      {tags
        .filter(([, count]) => count > 0)
        .map(([kind, count]) => (
          <span
            className='lz-kind-tag'
            key={kind}
            title={t(`${SK}.kind.${kind}`, { defaultValue: KIND_NAMES[kind] })}
          >
            {kind} ×{count}
          </span>
        ))}
    </>
  );
}

/** 成功 / 重试过 N 次 / 失败, with the tone the row paints it in. */
export function sessionStatus(
  session: { error_count: number; last_at: number; last_error_at?: number },
  t: TFunction,
): { label: string; hint?: string; tone: 'ok' | 'warn' | 'danger' } {
  const state = sessionState(session);
  if (state === 'failed')
    return {
      hint: t(`${SK}.status.failedHint`, {
        defaultValue: '这个会话的最后一次调用失败了',
      }),
      label: t(`${SK}.status.failed`, { defaultValue: '失败' }),
      tone: 'danger',
    };
  if (state === 'retried')
    return {
      hint: t(`${SK}.status.retriedHint`, {
        defaultValue:
          '有 {{n}} 次调用把错误返回给了 agent，它重新发送后接着跑了（网关自己的重试不算在内）',
        n: session.error_count,
      }),
      label: t(`${SK}.status.retried`, {
        defaultValue: '重试过 {{n}} 次',
        n: session.error_count,
      }),
      tone: 'warn',
    };
  return {
    label: t(`${SK}.status.ok`, { defaultValue: '成功' }),
    tone: 'ok',
  };
}

/** The title of a session row: its first message. */
export function sessionTitle(session: { title: string }, t: TFunction) {
  return (
    session.title ||
    t(`${SK}.list.noTitle`, { defaultValue: '（没有记录到首条消息）' })
  );
}

/** A lane chip in the 请求 table's look; a fallback also says how often. */
export function SessionLaneChip({
  count,
  lane,
  t,
}: {
  count?: number;
  lane: SessionLane;
  t: TFunction;
}) {
  if (lane === 'fallback') {
    return (
      <span
        className='logv2-lane logv2-lane--fallback'
        title={t('page.console.log.lane.fallbackHint')}
      >
        {count
          ? t(`${SK}.lane.fallbackCount`, {
              defaultValue: '改走稳定 {{n}} 次',
              n: count,
            })
          : t('page.console.log.lane.fallback')}
      </span>
    );
  }
  return (
    <span className={`logv2-lane logv2-lane--${lane}`}>
      <span aria-hidden='true' className='logv2-lane__dot' />
      {t(`page.console.log.lane.${lane}`)}
    </span>
  );
}

/** 实扣 over 官方价 + 省 N%, or a plain note when there is no comparison. */
export function SessionCost({
  amount,
  compared,
  official,
  quiet = false,
  t,
}: {
  amount: number;
  compared?: number;
  official?: number;
  /** One API call without a list price says nothing rather than a note. */
  quiet?: boolean;
  t: TFunction;
}) {
  const saving = officialSaving({
    compared_microusd: compared,
    official_microusd: official,
  });
  return (
    <span className='sv-two sv-right'>
      <span className='sv-money'>{formatMicroUsd(amount)}</span>
      {saving ? (
        <span className='sv-saving'>
          <s>{formatMicroUsd(saving.official)}</s>{' '}
          <b>
            {t(`${SK}.list.savedPct`, {
              defaultValue: '省 {{pct}}%',
              pct: saving.pct,
            })}
          </b>
        </span>
      ) : !official && !quiet ? (
        <span className='sv-sub'>
          {t(`${SK}.list.noOfficial`, { defaultValue: '无官方价' })}
        </span>
      ) : null}
    </span>
  );
}

function SpendCell({
  pending,
  summary,
  t,
}: {
  pending: boolean;
  summary?: AgentSessionListSummary;
  t: TFunction;
}) {
  const saving = summary ? officialSaving(summary) : null;
  const paidShare = saving
    ? Math.max(0, Math.min(100, 100 - saving.pct))
    : null;
  return (
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
          {pending || !summary ? (
            <PendingValue chars={6} />
          ) : (
            formatMicroUsd(summary.amount_microusd)
          )}
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
  );
}

function PendingRow({ admin }: { admin: boolean }) {
  return (
    <div aria-hidden='true' className='sv-row' data-admin={admin || undefined}>
      <span className='sv-two'>
        <PendingValue chars={17} />
        <span className='sv-sub'>
          <PendingValue chars={6} />
        </span>
      </span>
      <span className='sv-two'>
        <PendingValue chars={6} />
        {admin ? (
          <span className='sv-sub'>
            <PendingValue chars={6} />
          </span>
        ) : null}
      </span>
      <span className='sv-agent'>
        <span className='lz-agent-badge' data-ghost data-size='sm'>
          &nbsp;
        </span>
        <PendingValue chars={8} />
      </span>
      <span className='sv-two'>
        <PendingValue chars={26} />
        <span className='sv-sub'>
          <PendingValue chars={10} />
        </span>
      </span>
      <span className='sv-two'>
        <PendingValue chars={15} />
        <span className='sv-sub'>
          <PendingValue chars={4} />
        </span>
      </span>
      <span className='sv-two sv-right'>
        <PendingValue chars={11} />
        <span className='sv-sub'>
          <PendingValue chars={14} />
        </span>
      </span>
      <PendingValue chars={4} />
      <span className='sv-two sv-right'>
        <PendingValue chars={5} />
        <span className='sv-sub'>
          <PendingValue chars={10} />
        </span>
      </span>
    </div>
  );
}

function FilterMenu({
  allLabel,
  label,
  onPick,
  options,
  value,
}: {
  allLabel: string;
  label: string;
  onPick: (value: string) => void;
  options: { label: string; value: string }[];
  value: string;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger>
        <button
          data-owns-chrome
          aria-pressed={Boolean(value)}
          className='ses-btn'
          type='button'
        >
          {value ? label : allLabel}
          <ChevronDown aria-hidden='true' size={13} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='start'>
        <DropdownMenuItem onSelect={() => onPick('')}>
          {allLabel}
        </DropdownMenuItem>
        {options.map((option) => (
          <DropdownMenuItem
            key={option.value}
            onSelect={() => onPick(option.value)}
          >
            {option.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export default function SessionsView({ admin, t }: Props) {
  const [range, setRange] = useState<AgentSessionRange>('24h');
  const [client, setClient] = useState('');
  const [tokenName, setTokenName] = useState('');
  const [userId, setUserId] = useState('');
  const [failed, setFailed] = useState(false);
  const [draft, setDraft] = useState('');
  const [keyword, setKeyword] = useState('');
  const [page, setPage] = useState(1);

  useEffect(() => {
    const timer = setTimeout(() => setKeyword(draft.trim()), 300);
    return () => clearTimeout(timer);
  }, [draft]);

  useEffect(() => {
    setPage(1);
  }, [range, client, tokenName, userId, failed, keyword, admin]);

  const list = useAgentSessionsQuery({
    admin,
    client,
    failed,
    keyword,
    page,
    pageSize: PAGE_SIZE,
    range,
    tokenName,
    userId: admin && userId ? userId : undefined,
  });
  /* The menus list what the range contains, not what the current filters
     left over, so picking one value never hides the others. */
  const options = useAgentSessionsQuery({
    admin,
    page: 1,
    pageSize: 100,
    range,
  });
  const optionItems = options.data?.items;
  const clientOptions = useMemo(() => {
    const seen = new Set<string>();
    for (const item of optionItems ?? []) {
      const value = sessionClient(item);
      if (value) seen.add(value);
    }
    if (client) seen.add(client);
    return [...seen]
      .sort()
      .map((value) => ({ label: agentName(value), value }));
  }, [client, optionItems]);
  const keyOptions = useMemo(() => {
    const seen = new Set<string>();
    for (const item of optionItems ?? []) {
      if (item.token_name) seen.add(item.token_name);
    }
    if (tokenName) seen.add(tokenName);
    return [...seen].sort().map((value) => ({ label: value, value }));
  }, [optionItems, tokenName]);
  const userOptions = useMemo(() => {
    const seen = new Map<string, string>();
    for (const item of optionItems ?? []) {
      if (item.user_id)
        seen.set(String(item.user_id), item.username || `#${item.user_id}`);
    }
    return [...seen.entries()]
      .map(([value, label]) => ({ label, value }))
      .sort((a, b) => a.label.localeCompare(b.label));
  }, [optionItems]);

  const pending = list.isPending;
  const revealed = useDelayedReveal(pending);
  const summary = list.data?.summary;
  const items = list.data?.items ?? [];
  const total = list.data?.total ?? 0;
  const hit = summary
    ? ratio(summary.cached_tokens, summary.prompt_tokens)
    : null;

  const kpiValue = (chars: number, value: () => string) =>
    pending || !summary ? <PendingValue chars={chars} /> : value();

  return (
    <div className='ses' data-hold={pending && !revealed ? '' : undefined}>
      <div className='sv-summary'>
        <SpendCell pending={pending} summary={summary} t={t} />
        <div className='sv-kpi'>
          <span className='sv-kpi__label'>
            {t(`${SK}.kpi.sessions`, { defaultValue: 'Agent 会话' })}
          </span>
          <strong className='sv-kpi__value'>
            {kpiValue(2, () => formatCount(summary?.sessions))}
          </strong>
          <span className='sv-kpi__note'>
            {kpiValue(8, () =>
              t(`${SK}.kpi.subagents`, {
                defaultValue: '子代理 {{n}} 个',
                n: summary?.subagents ?? 0,
              }),
            )}
          </span>
        </div>
        <div className='sv-kpi'>
          <span className='sv-kpi__label'>
            {t(`${SK}.kpi.calls`, { defaultValue: 'API 调用' })}
          </span>
          <strong className='sv-kpi__value'>
            {kpiValue(5, () => formatCount(summary?.request_count))}
          </strong>
          <span className='sv-kpi__note'>
            {kpiValue(10, () =>
              summary?.median_requests == null
                ? '—'
                : t(`${SK}.kpi.medianCalls`, {
                    defaultValue: '每个会话中位 {{n}} 次',
                    n: formatCount(summary.median_requests),
                  }),
            )}
          </span>
        </div>
        <div className='sv-kpi'>
          <span className='sv-kpi__label'>Tokens</span>
          <strong className='sv-kpi__value'>
            {kpiValue(5, () => formatCount(summary?.tokens))}
          </strong>
          <span className='sv-kpi__note'>
            {kpiValue(8, () =>
              hit === null
                ? '—'
                : t(`${SK}.list.cacheHit`, {
                    defaultValue: '缓存命中 {{pct}}',
                    pct: pct(hit),
                  }),
            )}
          </span>
        </div>
      </div>

      <div className='ses-filters'>
        <div className='ses-seg' role='group'>
          {SESSION_RANGES.map((value) => (
            <button
              data-owns-chrome
              aria-pressed={range === value}
              key={value}
              onClick={() => setRange(value)}
              type='button'
            >
              {rangeLabel(value, t)}
            </button>
          ))}
        </div>
        <FilterMenu
          allLabel={t(`${SK}.list.allClients`, { defaultValue: '全部 Agent' })}
          label={agentName(client)}
          onPick={setClient}
          options={clientOptions}
          value={client}
        />
        {admin ? (
          <FilterMenu
            allLabel={t(`${SK}.list.allUsers`, { defaultValue: '全部用户' })}
            label={
              userOptions.find((option) => option.value === userId)?.label ||
              `#${userId}`
            }
            onPick={setUserId}
            options={userOptions}
            value={userId}
          />
        ) : null}
        <FilterMenu
          allLabel={t(`${SK}.list.allKeys`, { defaultValue: '全部 Key' })}
          label={tokenName}
          onPick={setTokenName}
          options={keyOptions}
          value={tokenName}
        />
        <button
          data-owns-chrome
          aria-pressed={failed}
          className='ses-btn'
          onClick={() => setFailed((value) => !value)}
          type='button'
        >
          {t(`${SK}.list.onlyFailed`, { defaultValue: '只看失败' })}
        </button>
        <label className='ses-search'>
          <SearchIcon />
          <input
            aria-label={t(`${SK}.list.searchAria`, {
              defaultValue: '搜索会话',
            })}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={t(`${SK}.list.searchPlaceholder`, {
              defaultValue: '搜索首条消息、会话编号',
            })}
            type='search'
            value={draft}
          />
        </label>
      </div>

      <div className='sv-table'>
        <div className='sv-scroll'>
          <div className='sv-row sv-row--head' data-admin={admin || undefined}>
            <span>
              {t(`${SK}.list.col.span`, { defaultValue: '开始 → 结束' })}
            </span>
            <span>
              {admin
                ? t(`${SK}.list.col.userKey`, { defaultValue: '用户 · Key' })
                : 'Key'}
            </span>
            <span>Agent</span>
            <span
              title={t(`${SK}.list.col.sessionHint`, {
                defaultValue: '取这个会话的第一条消息',
              })}
            >
              {t(`${SK}.list.col.session`, { defaultValue: '会话' })}
            </span>
            <span>
              {t(`${SK}.list.col.modelLane`, { defaultValue: '模型 · 线路' })}
            </span>
            <span className='sv-right'>
              {t(`${SK}.list.col.usage`, { defaultValue: '用量' })}
            </span>
            <span>{t(`${SK}.list.col.status`, { defaultValue: '状态' })}</span>
            <span className='sv-right'>
              {t(`${SK}.kpi.spend`, { defaultValue: '花费' })}
            </span>
          </div>
          {pending ? (
            Array.from({ length: PENDING_ROWS }, (_, index) => (
              <PendingRow admin={admin} key={index} />
            ))
          ) : list.isError ? (
            <div className='ses-state'>
              <ErrorState
                action={
                  <button
                    data-owns-chrome
                    className='ses-btn ses-btn--sm'
                    onClick={() => list.refetch()}
                    type='button'
                  >
                    {t(`${SK}.retry`, { defaultValue: '重试' })}
                  </button>
                }
                error={list.error}
              />
            </div>
          ) : items.length === 0 ? (
            <div className='ses-state'>
              <EmptyState
                description={t(`${SK}.list.emptyDescription`, {
                  defaultValue:
                    '用 Claude Code、Codex、OpenCode 这类 agent 调用时，每个会话会在这里成为一行。',
                })}
                title={t(`${SK}.list.emptyTitle`, {
                  defaultValue: '这段时间没有 Agent 会话',
                })}
              />
            </div>
          ) : (
            items.map((session) => (
              <SessionRow
                admin={admin}
                key={session.id}
                session={session}
                t={t}
              />
            ))
          )}
        </div>
        <div className='sv-foot'>
          <span>
            {pending || !summary ? (
              <PendingValue chars={16} />
            ) : (
              t(`${SK}.list.footer`, {
                defaultValue: '最近 {{range}} · {{sessions}} 个会话',
                range: rangeLabel(range, t),
                sessions: summary.sessions,
              })
            )}
          </span>
          {total > PAGE_SIZE ? (
            <Pagination
              currentPage={page}
              onPageChange={setPage}
              pageSize={PAGE_SIZE}
              showTotal={false}
              total={total}
            />
          ) : null}
        </div>
      </div>
    </div>
  );
}

function SessionRow({
  admin,
  session,
  t,
}: {
  admin: boolean;
  session: AgentSessionListItem;
  t: TFunction;
}) {
  const navigate = useNavigate();
  const client = sessionClient(session);
  const status = sessionStatus(session, t);
  const hit = ratio(session.cached_tokens, session.prompt_tokens);
  const href = sessionDetailHref(session.id, admin);
  const lanes = sessionLanes(session.models);
  const [firstModel, ...otherModels] = session.models;
  return (
    // The whole row is a pointer shortcut; the time link is its keyboard
    // handle, as in the 请求 table.
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions
    <div
      className='sv-row'
      data-admin={admin || undefined}
      onClick={() => navigate(href)}
    >
      <span className='sv-two'>
        <Link
          className='sv-time'
          onClick={(event) => event.stopPropagation()}
          title={spanText(session.started_at, session.last_at)}
          to={href}
        >
          {spanTextCompact(session.started_at, session.last_at)}
        </Link>
        <span className='sv-sub'>
          {durationText(session.last_at - session.started_at, t)}
        </span>
      </span>
      <span className='sv-two'>
        {admin ? (
          <>
            <span className='sv-ellipsis'>{session.username || '—'}</span>
            <span className='sv-sub sv-ellipsis'>
              {session.token_name || '—'}
            </span>
          </>
        ) : (
          <span className='sv-ellipsis sv-muted'>
            {session.token_name || '—'}
          </span>
        )}
      </span>
      <span className='sv-agent'>
        <AgentBadge client={client} size='sm' />
        <span className='sv-ellipsis'>{client ? agentName(client) : '—'}</span>
      </span>
      <span className='sv-two'>
        <span className='sv-ellipsis' title={sessionTitle(session, t)}>
          {sessionTitle(session, t)}
        </span>
        {session.subagent_count + session.compact_count + session.review_count >
        0 ? (
          <span className='sv-tags'>
            <SessionKindTags session={session} t={t} />
          </span>
        ) : null}
      </span>
      <span className='sv-two'>
        <span
          className='sv-mono sv-ellipsis'
          title={session.models.map((model) => model.model_name).join(', ')}
        >
          {firstModel ? firstModel.model_name : '—'}
          {otherModels.length > 0 ? ` +${otherModels.length}` : ''}
        </span>
        {lanes.length > 0 ? (
          <span className='sv-lanes'>
            {lanes.map(({ count, lane }) => (
              <SessionLaneChip
                count={lane === 'fallback' ? count : undefined}
                key={lane}
                lane={lane}
                t={t}
              />
            ))}
          </span>
        ) : null}
      </span>
      <span className='sv-two sv-right'>
        <span className='sv-mono'>
          {formatCount(session.prompt_tokens)} →{' '}
          {formatCount(session.completion_tokens)}
        </span>
        <span className='sv-sub'>
          {t(`${SK}.list.callsCache`, {
            defaultValue: '{{n}} 次调用 · 缓存 {{pct}}',
            n: formatCount(session.request_count),
            pct: pct(hit),
          })}
        </span>
      </span>
      <span>
        <span className='sv-status' data-tone={status.tone} title={status.hint}>
          {status.label}
        </span>
      </span>
      <SessionCost
        amount={session.amount_microusd}
        compared={session.compared_microusd}
        official={session.official_microusd}
        t={t}
      />
    </div>
  );
}
