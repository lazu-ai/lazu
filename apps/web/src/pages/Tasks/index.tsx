import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Fragment, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { createSearchParams } from '../../components/ui/table-tools';
import { API } from '../../helpers/api';
import { copy } from '../../helpers/clipboard';
import { showError, showSuccess } from '../../helpers/utils';
import { getUsageProjectScope } from '../../hooks/usage-logs/usageProjectScope';
import { useUsageScope } from '../../hooks/usage-logs/useUsageScope';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import '../Log/log-table.css';
import './tasks.css';
import type { TFunction } from 'i18next';

type TaskStatus = 'pending' | 'running' | 'succeeded' | 'failed';

export type ConsoleTask = {
  id: string;
  kind: string;
  model: string;
  status: TaskStatus;
  source?: string;
  created_at: number;
  started_at?: number;
  completed_at?: number;
  request_id?: string;
  token_name?: string;
  prompt?: string;
  spec: {
    seconds?: number;
    resolution?: string;
    aspect_ratio?: string;
    audio?: boolean;
    reference_images?: number;
    reference_videos?: number;
    reference_audios?: number;
    frames?: number;
  };
  hold_microusd: number;
  charge_microusd: number;
  refund_microusd: number;
  file_expired?: boolean;
  key_deleted?: boolean;
  artifact?: {
    file_id?: string;
    url?: string;
    url_expires_at?: number;
    mime_type?: string;
    bytes?: number;
    expires_at?: number;
  };
  error?: { code?: string; message: string };
  usage?: { seconds?: number; resolution?: string };
};

type TaskSummary = {
  tasks: number;
  spend_microusd: number;
  active: number;
  active_hold_microusd: number;
  failed: number;
  refunded_microusd: number;
  expiring_soon: number;
};

type TaskPage = {
  items: ConsoleTask[];
  total: number;
  summary: TaskSummary;
};

const PAGE_SIZE = 20;
const ACTIVE_POLL_MS = 10_000;
const DAY = 86_400;
const STATUSES = ['', 'active', 'succeeded', 'failed'] as const;
const RANGES = [1, 7, 30] as const;

/** "$0.4115": the log's cost format. */
export const formatTaskCost = (microusd: number) => {
  if (!microusd) return '$0';
  return `$${(microusd / 1_000_000).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')}`;
};

/** "10-20 12:05:16" in local time, as the log shows times this year. */
export const formatTaskTime = (unix?: number) => {
  if (!unix) return '—';
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, '0');
  const date = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  return d.getFullYear() === new Date().getFullYear()
    ? `${date} ${time}`
    : `${d.getFullYear()}-${date} ${time}`;
};

/** "2:48". */
export const formatTaskDuration = (seconds: number) => {
  const s = Math.max(0, Math.round(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
};

/** "2 天 23 小时" / "18 小时" / "40 分钟" until a file is deleted. */
export const formatTaskRemaining = (
  expiresAt: number,
  nowSec: number,
  t: TFunction,
) => {
  const left = expiresAt - nowSec;
  if (left <= 0) return t('page.console.tasks.keep.gone');
  const days = Math.floor(left / DAY);
  const hours = Math.floor((left % DAY) / 3600);
  if (days > 0) return t('page.console.tasks.keep.days', { days, hours });
  if (hours > 0) return t('page.console.tasks.keep.hours', { hours });
  return t('page.console.tasks.keep.minutes', {
    minutes: Math.max(1, Math.floor(left / 60)),
  });
};

export const taskSpecText = (task: ConsoleTask, t: TFunction) => {
  const spec = task.spec || {};
  const parts: string[] = [];
  if (spec.resolution) parts.push(spec.resolution);
  if (spec.seconds)
    parts.push(t('page.console.tasks.spec.seconds', { n: spec.seconds }));
  if (spec.reference_videos)
    parts.push(
      t('page.console.tasks.spec.refVideo', { n: spec.reference_videos }),
    );
  if (spec.reference_images)
    parts.push(
      t('page.console.tasks.spec.refImage', { n: spec.reference_images }),
    );
  if (spec.reference_audios)
    parts.push(
      t('page.console.tasks.spec.refAudio', { n: spec.reference_audios }),
    );
  if (spec.frames)
    parts.push(t('page.console.tasks.spec.frames', { n: spec.frames }));
  return parts.join(' · ') || '—';
};

const isActive = (task: ConsoleTask) =>
  task.status === 'pending' || task.status === 'running';

function TaskStatusCell({ task, t }: { task: ConsoleTask; t: TFunction }) {
  if (task.status === 'failed') {
    return (
      <span className='logv2-status logv2-status--bad'>
        <span aria-hidden='true' className='logv2-status__dot' />
        {t('page.console.tasks.status.failed')}
      </span>
    );
  }
  if (task.status === 'succeeded') {
    return task.file_expired ? (
      <span className='logv2-status tasks-status--expired'>
        <span aria-hidden='true' className='logv2-status__dot' />
        {t('page.console.tasks.status.expired')}
      </span>
    ) : (
      <span className='logv2-status'>
        <span aria-hidden='true' className='logv2-status__dot' />
        {t('page.console.tasks.status.succeeded')}
      </span>
    );
  }
  return (
    <span className={`logv2-status tasks-status--${task.status}`}>
      <span aria-hidden='true' className='logv2-status__dot' />
      {task.status === 'running'
        ? t('page.console.tasks.status.running')
        : t('page.console.tasks.status.pending')}
    </span>
  );
}

function TaskDetail({
  task,
  nowSec,
  t,
}: {
  task: ConsoleTask;
  nowSec: number;
  t: TFunction;
}) {
  const url = task.artifact?.url;
  const mime = task.artifact?.mime_type || '';
  const copyValue = async (value: string) => {
    if (await copy(value)) showSuccess(t('page.console.tables.copied'));
    else showError(t('page.console.tables.copyFailed'));
  };
  const timeline: { at?: number; label: string; tone?: string }[] = [
    {
      at: task.created_at,
      label: task.hold_microusd
        ? t('page.console.tasks.timeline.submittedHold', {
            amount: formatTaskCost(task.hold_microusd),
          })
        : t('page.console.tasks.timeline.submitted'),
    },
  ];
  if (task.started_at)
    timeline.push({
      at: task.started_at,
      label: t('page.console.tasks.timeline.started'),
    });
  if (task.status === 'succeeded' && task.completed_at)
    timeline.push({
      at: task.completed_at,
      tone: 'ok',
      label: task.refund_microusd
        ? t('page.console.tasks.timeline.settledRefund', {
            amount: formatTaskCost(task.charge_microusd),
            refund: formatTaskCost(task.refund_microusd),
          })
        : t('page.console.tasks.timeline.settled', {
            amount: formatTaskCost(task.charge_microusd),
          }),
    });
  if (task.status === 'failed' && task.completed_at)
    timeline.push({
      at: task.completed_at,
      tone: 'bad',
      label: t('page.console.tasks.timeline.failed', {
        refund: formatTaskCost(task.refund_microusd),
      }),
    });
  if (task.artifact?.expires_at)
    timeline.push({
      at: task.artifact.expires_at,
      tone: task.artifact.expires_at - nowSec < DAY ? 'warn' : 'soft',
      label:
        task.artifact.expires_at > nowSec
          ? t('page.console.tasks.timeline.deletes', {
              left: formatTaskRemaining(task.artifact.expires_at, nowSec, t),
            })
          : t('page.console.tasks.timeline.deleted'),
    });

  return (
    <div className='tasks-detail'>
      <div className='tasks-detail__preview'>
        {url && mime.startsWith('video/') ? (
          // A generated clip has no caption track to offer; its prompt is
          // shown beside it.
          // eslint-disable-next-line jsx-a11y/media-has-caption
          <video
            aria-label={task.prompt || task.id}
            controls
            preload='metadata'
            src={url}
          />
        ) : url && mime.startsWith('image/') ? (
          <img alt={task.prompt || task.id} src={url} />
        ) : (
          <div className='tasks-detail__placeholder'>
            {isActive(task)
              ? t('page.console.tasks.detail.inProgress', {
                  time: formatTaskDuration(nowSec - task.created_at),
                })
              : task.status === 'failed'
                ? t('page.console.tasks.detail.noFile')
                : t('page.console.tasks.detail.deleted')}
          </div>
        )}
        {url ? (
          <div className='tasks-detail__actions'>
            <a
              className='tasks-button tasks-button--primary'
              download
              href={url}
            >
              {t('page.console.tasks.detail.download')}
            </a>
            <button
              className='tasks-button'
              type='button'
              onClick={() => copyValue(url)}
            >
              {t('page.console.tasks.detail.copyLink')}
            </button>
          </div>
        ) : null}
      </div>

      <div className='tasks-detail__col tasks-detail__col--wide'>
        <h3>{t('page.console.tasks.detail.timeline')}</h3>
        <ol className='tasks-timeline'>
          {timeline.map((event) => (
            <li key={`${event.at}-${event.label}`}>
              <span className='logv2-time'>{formatTaskTime(event.at)}</span>
              <span className={event.tone ? `tasks-tone--${event.tone}` : ''}>
                {event.label}
              </span>
            </li>
          ))}
        </ol>
        {task.error?.message ? (
          <p className='tasks-detail__error'>
            <code>{task.error.code}</code> {task.error.message}
          </p>
        ) : null}
      </div>

      <div className='tasks-detail__col'>
        <h3>{t('page.console.tasks.detail.cost')}</h3>
        <dl className='tasks-bill'>
          <dt>{t('page.console.tasks.detail.hold')}</dt>
          <dd>{formatTaskCost(task.hold_microusd)}</dd>
          <dt>{t('page.console.tasks.detail.charge')}</dt>
          <dd>
            {isActive(task)
              ? t('page.console.tasks.detail.afterFinish')
              : formatTaskCost(task.charge_microusd)}
          </dd>
          {task.refund_microusd ? (
            <>
              <dt>{t('page.console.tasks.detail.refund')}</dt>
              <dd className='tasks-tone--ok'>
                {formatTaskCost(task.refund_microusd)}
              </dd>
            </>
          ) : null}
        </dl>
      </div>

      <div className='tasks-detail__col'>
        <h3>{t('page.console.tasks.detail.request')}</h3>
        <dl className='tasks-ids'>
          <dt>{t('page.console.tasks.detail.taskId')}</dt>
          <dd>
            <button
              className='logv2-link logv2-model'
              type='button'
              onClick={() => copyValue(task.id)}
            >
              {task.id}
            </button>
          </dd>
          {task.request_id ? (
            <>
              <dt>{t('page.console.tasks.detail.requestId')}</dt>
              <dd>
                <button
                  className='logv2-link logv2-model'
                  type='button'
                  onClick={() => copyValue(task.request_id || '')}
                >
                  {task.request_id}
                </button>
              </dd>
            </>
          ) : null}
          <dt>{t('page.console.tasks.detail.spec')}</dt>
          <dd>
            {[
              taskSpecText(task, t),
              task.spec?.aspect_ratio,
              task.spec?.audio === false
                ? t('page.console.tasks.spec.silent')
                : '',
            ]
              .filter(Boolean)
              .join(' · ')}
          </dd>
        </dl>
        {task.prompt ? (
          <p className='tasks-detail__prompt'>{task.prompt}</p>
        ) : null}
      </div>
    </div>
  );
}

export default function TasksPage() {
  const { t } = useTranslation();
  const { activeScope } = useUsageScope();
  const [searchParams, setSearchParams] = useSearchParams();
  const status = searchParams.get('status') || '';
  const days = Number(searchParams.get('days')) || 7;
  const openId = searchParams.get('task') || '';
  const [page, setPage] = useState(1);
  const [draft, setDraft] = useState(searchParams.get('q') || '');
  const q = searchParams.get('q') || '';
  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000));

  const activeProjectId =
    activeScope.plane === 'customer' && activeScope.project?.isShared
      ? activeScope.project.id
      : undefined;
  const project = useMemo(
    () => getUsageProjectScope(searchParams.toString(), activeProjectId),
    [activeProjectId, searchParams],
  );
  // The window's start is fixed when the range is chosen, so polling keeps
  // one query key instead of refetching a moving one every second.
  const start = useMemo(
    () => Math.floor(Date.now() / 1000) - days * DAY,
    [days],
  );
  const params = { status, start, q, page, project };

  const query = useQuery({
    queryKey: queryKeys.console.table('tasks', params),
    queryFn: async () => {
      const search = createSearchParams({
        p: page,
        page_size: PAGE_SIZE,
        project_id: project.project_id,
        q: q || undefined,
        start_timestamp: start,
        status: status || undefined,
        token_id: project.token_id,
        user_id: project.user_id,
      });
      return unwrap<TaskPage>(await API.get(`/api/log/self/tasks?${search}`));
    },
    placeholderData: keepPreviousData,
    refetchInterval: (state) =>
      (state.state.data?.summary?.active ?? 0) > 0 ? ACTIVE_POLL_MS : false,
  });
  const data = query.data;
  const items = data?.items ?? [];
  const summary = data?.summary;
  const hasActive = (summary?.active ?? 0) > 0;

  useEffect(() => {
    if (!hasActive && !openId) return undefined;
    const timer = window.setInterval(
      () => setNowSec(Math.floor(Date.now() / 1000)),
      1000,
    );
    return () => window.clearInterval(timer);
  }, [hasActive, openId]);

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(key, value);
    else next.delete(key);
    setSearchParams(next, { replace: true });
    if (key !== 'task') setPage(1);
  };

  const statusLabel = (value: (typeof STATUSES)[number]) =>
    value === ''
      ? t('page.console.tasks.filter.all')
      : value === 'active'
        ? t('page.console.tasks.filter.active', { n: summary?.active ?? 0 })
        : t(`page.console.tasks.filter.${value}`);

  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE));

  return (
    <div className='tasks-page'>
      <div className='logv2-summary tasks-summary'>
        <div className='logv2-summary__cell'>
          <span>{t('page.console.tasks.summary.spend')}</span>
          <b>{formatTaskCost(summary?.spend_microusd ?? 0)}</b>
        </div>
        <div className='logv2-summary__cell'>
          <span>{t('page.console.tasks.summary.tasks')}</span>
          <b>{(summary?.tasks ?? 0).toLocaleString()}</b>
        </div>
        <div className='logv2-summary__cell'>
          <span>{t('page.console.tasks.summary.active')}</span>
          <b>
            {summary?.active ?? 0} ·{' '}
            {formatTaskCost(summary?.active_hold_microusd ?? 0)}
          </b>
        </div>
        <div className='logv2-summary__cell'>
          <span>{t('page.console.tasks.summary.failed')}</span>
          <b>
            {summary?.failed ?? 0} ·{' '}
            {formatTaskCost(summary?.refunded_microusd ?? 0)}
          </b>
        </div>
        <div className='logv2-summary__cell'>
          <span>{t('page.console.tasks.summary.expiring')}</span>
          <b className={summary?.expiring_soon ? 'tasks-tone--warn' : ''}>
            {summary?.expiring_soon ?? 0}
          </b>
        </div>
      </div>

      <div className='tasks-filters'>
        <div
          aria-label={t('page.console.tasks.filter.range')}
          className='tasks-seg'
          role='group'
        >
          {RANGES.map((value) => (
            <button
              aria-pressed={days === value}
              className='tasks-seg__button'
              key={value}
              type='button'
              onClick={() => setParam('days', value === 7 ? '' : String(value))}
            >
              {t(`page.console.tasks.range.d${value}`)}
            </button>
          ))}
        </div>
        <div
          aria-label={t('page.console.tasks.filter.status')}
          className='tasks-seg'
          role='group'
        >
          {STATUSES.map((value) => (
            <button
              aria-pressed={status === value}
              className='tasks-seg__button'
              key={value || 'all'}
              type='button'
              onClick={() => setParam('status', value)}
            >
              {statusLabel(value)}
            </button>
          ))}
        </div>
        <form
          className='tasks-search'
          role='search'
          onSubmit={(event) => {
            event.preventDefault();
            setParam('q', draft.trim());
          }}
        >
          <label className='sr-only' htmlFor='tasks-q'>
            {t('page.console.tasks.filter.search')}
          </label>
          <input
            id='tasks-q'
            placeholder={t('page.console.tasks.filter.search')}
            type='search'
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </form>
      </div>

      <div className='logv2-card'>
        <div className='logv2-scroll'>
          <table className='logv2-table tasks-table'>
            <thead>
              <tr>
                <th scope='col'>{t('page.console.tasks.col.time')}</th>
                <th scope='col'>{t('page.console.tasks.col.status')}</th>
                <th scope='col'>{t('page.console.tasks.col.model')}</th>
                <th scope='col'>{t('page.console.tasks.col.key')}</th>
                <th scope='col'>{t('page.console.tasks.col.took')}</th>
                <th className='logv2-num' scope='col'>
                  {t('page.console.tasks.col.cost')}
                </th>
                <th scope='col'>{t('page.console.tasks.col.keep')}</th>
                <th scope='col'>{t('page.console.tasks.col.spec')}</th>
                <th scope='col'>{t('page.console.tasks.col.content')}</th>
              </tr>
            </thead>
            <tbody>
              {items.length === 0 ? (
                <tr>
                  <td className='tasks-empty' colSpan={9}>
                    {query.isLoading
                      ? t('page.system.loading.title')
                      : t('page.console.tasks.empty')}
                  </td>
                </tr>
              ) : null}
              {items.map((task) => {
                const open = openId === task.id;
                const took = isActive(task)
                  ? t('page.console.tasks.took.waiting', {
                      time: formatTaskDuration(nowSec - task.created_at),
                    })
                  : task.completed_at
                    ? formatTaskDuration(task.completed_at - task.created_at)
                    : '—';
                const expiresAt = task.artifact?.expires_at;
                return (
                  <Fragment key={task.id}>
                    <tr
                      className='logv2-row'
                      data-open={open ? 'true' : undefined}
                      onClick={() => setParam('task', open ? '' : task.id)}
                    >
                      <td>
                        <button
                          aria-expanded={open}
                          className='logv2-link logv2-time'
                          type='button'
                          onClick={(event) => {
                            event.stopPropagation();
                            setParam('task', open ? '' : task.id);
                          }}
                        >
                          {formatTaskTime(task.created_at)}
                        </button>
                      </td>
                      <td>
                        <TaskStatusCell task={task} t={t} />
                      </td>
                      <td>
                        <span className='logv2-model'>{task.model}</span>
                      </td>
                      <td>
                        {task.token_name ? (
                          task.token_name
                        ) : (
                          <span className='logv2-soft'>
                            {task.key_deleted
                              ? t('page.console.tasks.keyDeleted')
                              : task.source && task.source !== 'media_job'
                                ? 'Studio'
                                : t('page.console.tasks.noKey')}
                          </span>
                        )}
                      </td>
                      <td className='logv2-soft tasks-nowrap'>{took}</td>
                      <td className='logv2-num'>
                        {isActive(task) ? (
                          <span className='logv2-soft'>
                            {t('page.console.tasks.cost.held', {
                              amount: formatTaskCost(task.hold_microusd),
                            })}
                          </span>
                        ) : (
                          formatTaskCost(task.charge_microusd)
                        )}
                        {task.status === 'failed' && task.refund_microusd ? (
                          <span className='logv2-chip logv2-chip--bad tasks-chip'>
                            {t('page.console.tasks.cost.refunded')}
                          </span>
                        ) : null}
                      </td>
                      <td className='tasks-nowrap'>
                        {expiresAt && !task.file_expired ? (
                          <span
                            className={
                              expiresAt - nowSec < DAY
                                ? 'tasks-tone--warn'
                                : 'logv2-soft'
                            }
                          >
                            {formatTaskRemaining(expiresAt, nowSec, t)}
                          </span>
                        ) : (
                          <span className='logv2-faint'>
                            {task.file_expired
                              ? t('page.console.tasks.keep.gone')
                              : '—'}
                          </span>
                        )}
                      </td>
                      <td className='tasks-nowrap'>{taskSpecText(task, t)}</td>
                      <td>
                        <span className='tasks-prompt' title={task.prompt}>
                          {task.prompt || '—'}
                        </span>
                      </td>
                    </tr>
                    {open ? (
                      <tr className='logv2-detail-row'>
                        <td colSpan={9}>
                          <TaskDetail nowSec={nowSec} t={t} task={task} />
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className='tasks-pager'>
        <span>{t('page.console.tasks.count', { n: data?.total ?? 0 })}</span>
        <span className='tasks-pager__buttons'>
          <button
            className='tasks-button'
            disabled={page <= 1}
            type='button'
            onClick={() => setPage((value) => Math.max(1, value - 1))}
          >
            {t('page.console.tasks.prev')}
          </button>
          <span>
            {page} / {totalPages}
          </span>
          <button
            className='tasks-button'
            disabled={page >= totalPages}
            type='button'
            onClick={() => setPage((value) => value + 1)}
          >
            {t('page.console.tasks.next')}
          </button>
        </span>
        <span className='logv2-soft'>{t('page.console.tasks.retention')}</span>
      </div>
    </div>
  );
}
