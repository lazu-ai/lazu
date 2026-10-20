import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { createSearchParams } from '../../components/ui/table-tools';
import { API } from '../../helpers/api';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { UsageProjectScope } from '../../hooks/usage-logs/usageProjectScope';
import type { TFunction } from 'i18next';

type InFlightJob = {
  id: string;
  kind: string;
  model: string;
  status: 'pending' | 'running';
  created_at: number;
  request_id?: string;
  cost_snapshot?: { customer_charge_usd?: number };
};

const ACTIVE_POLL_MS = 10_000;
const IDLE_POLL_MS = 60_000;

const elapsed = (createdAt: number, now: number) => {
  const seconds = Math.max(0, Math.floor(now / 1000 - createdAt));
  const minutes = Math.floor(seconds / 60);
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`;
};

/* Media jobs log their usage row only when they settle, minutes after the
 * request. Until then they show here, above the log, with the amount held;
 * when one leaves this list the log is refreshed so its settled row appears. */
export function InFlightJobs({
  enabled,
  onSettled,
  project,
  t,
}: {
  enabled: boolean;
  onSettled: () => void;
  project: UsageProjectScope;
  t: TFunction;
}) {
  const query = useQuery({
    queryKey: queryKeys.console.detail('usageLogs', ['inFlight', project]),
    queryFn: async () => {
      const search = createSearchParams({
        project_id: project.project_id,
        token_id: project.token_id,
        user_id: project.user_id,
      });
      const data = unwrap<InFlightJob[]>(
        await API.get(`/api/log/self/in_flight?${search}`, {
          skipErrorHandler: true,
        } as Parameters<typeof API.get>[1]),
      );
      return Array.isArray(data) ? data : [];
    },
    enabled,
    refetchInterval: (state) =>
      state.state.data?.length ? ACTIVE_POLL_MS : IDLE_POLL_MS,
  });
  const jobs = query.data ?? [];

  const seen = useRef<Set<string>>(new Set());
  useEffect(() => {
    const current = new Set(jobs.map((job) => job.id));
    const settled = [...seen.current].some((id) => !current.has(id));
    seen.current = current;
    if (settled) onSettled();
  }, [jobs, onSettled]);

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!jobs.length) return undefined;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [jobs.length]);

  if (!enabled || !jobs.length) return null;
  return (
    <section
      className='logv2-card logv2-inflight'
      aria-label={t('page.console.log.inFlight.title')}
    >
      <div className='logv2-inflight__head'>
        <span className='logv2-inflight__dot' aria-hidden='true' />
        <span>
          {t('page.console.log.inFlight.title')} · {jobs.length}
        </span>
        <span className='logv2-inflight__hint'>
          {t('page.console.log.inFlight.hint')}
        </span>
      </div>
      <ul className='logv2-inflight__list'>
        {jobs.map((job) => {
          const held = job.cost_snapshot?.customer_charge_usd;
          return (
            <li key={job.id} className='logv2-inflight__row'>
              <span className='logv2-inflight__status'>
                {job.status === 'pending'
                  ? t('page.console.log.inFlight.pending')
                  : t('page.console.log.inFlight.running')}
              </span>
              <span className='logv2-inflight__model'>{job.model}</span>
              <span className='logv2-inflight__meta'>
                {job.kind === 'video'
                  ? t('page.console.log.inFlight.video')
                  : t('page.console.log.inFlight.image')}
              </span>
              <span className='logv2-inflight__meta logv2-num'>
                {t('page.console.log.inFlight.elapsed', {
                  time: elapsed(job.created_at, now),
                })}
              </span>
              {typeof held === 'number' ? (
                <span className='logv2-inflight__meta logv2-num'>
                  {t('page.console.log.inFlight.held', {
                    amount: `$${held}`,
                  })}
                </span>
              ) : null}
              <code className='logv2-inflight__id'>{job.id}</code>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
