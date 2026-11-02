/* API 调用's head card (canvas 1B): the range's spend large on the left with
 * the change against the window before it, the spend per day (per hour for
 * 24 小时) as bars, and four figures that hold for any range on the right.
 * Everything reads off /api/log/(self/)stat under the list's filters. */
import { useTranslation } from 'react-i18next';
import { Pending } from '../../components/ui/pending';
import type { UsageRangePreset } from '../../hooks/usage-logs/usageRange';
import type { UsageLogStat } from '../../hooks/usage-logs/useUsageLogsQuery';

const SC = 'page.console.log.spendCard';
const HOUR = 3_600;

type Bucket = { amount: number; at: Date; requests: number };

const pad = (n: number) => String(n).padStart(2, '0');
const dayLabel = (at: Date) => `${pad(at.getMonth() + 1)}-${pad(at.getDate())}`;
const hourLabel = (at: Date) => `${pad(at.getHours())}:00`;
const dayStart = (at: Date) =>
  new Date(at.getFullYear(), at.getMonth(), at.getDate());

/* Small ranges cost fractions of a cent; two decimals would read $0.00. */
const money = (micro: number) =>
  `$${(micro / 1_000_000).toFixed(Math.abs(micro) >= 10_000 || !micro ? 2 : 4)}`;

/** The hourly series folded into local days (or kept by the hour for a
 *  window of a day or less), with the empty ones filled in. */
export function spendBuckets(
  series: UsageLogStat['series'],
  start: number,
  end: number,
): { buckets: Bucket[]; hourly: boolean } {
  const hourly = end - start <= 36 * HOUR;
  const buckets: Bucket[] = [];
  const index = new Map<number, Bucket>();
  const keyOf = (at: Date) =>
    hourly
      ? Math.floor(at.getTime() / 1000 / HOUR)
      : dayStart(at).getTime() / 1000;
  const last = new Date(Math.min(end, Date.now() / 1000) * 1000);
  let at = hourly
    ? new Date(Math.floor(start / HOUR) * HOUR * 1000)
    : dayStart(new Date(start * 1000));
  while (at <= last && buckets.length < 400) {
    const bucket = { amount: 0, at, requests: 0 };
    buckets.push(bucket);
    index.set(keyOf(at), bucket);
    at = hourly
      ? new Date(at.getTime() + HOUR * 1000)
      : new Date(at.getFullYear(), at.getMonth(), at.getDate() + 1);
  }
  for (const point of series ?? []) {
    const bucket = index.get(keyOf(new Date(point.bucket * 1000)));
    if (!bucket) continue;
    bucket.amount += Number(point.amount_microusd || 0);
    bucket.requests += Number(point.request_count || 0);
  }
  return { buckets, hourly };
}

export function SpendCard({
  loading,
  preset,
  range,
  stat,
}: {
  loading: boolean;
  preset: UsageRangePreset | 'custom';
  range: { days: number; end: number; label: string; start: number };
  stat: UsageLogStat;
}) {
  const { t } = useTranslation();
  const span =
    preset === 'custom'
      ? t('page.console.usage.range.days', { count: range.days })
      : t(`page.console.log.session.range.${preset}`);
  const amount = Number(stat.amount_microusd || 0);
  const previous =
    stat.previous_amount_microusd == null
      ? null
      : Number(stat.previous_amount_microusd);
  const delta = previous == null ? 0 : amount - previous;
  const compare =
    previous == null
      ? ''
      : delta === 0
        ? t(`${SC}.same`, { range: span })
        : t(delta > 0 ? `${SC}.more` : `${SC}.less`, {
            amount: money(Math.abs(delta)),
            range: span,
          });

  const { buckets, hourly } = spendBuckets(stat.series, range.start, range.end);
  const max = Math.max(0, ...buckets.map((bucket) => bucket.amount));
  const peak = buckets.find((bucket) => max > 0 && bucket.amount === max);
  const label = hourly ? hourLabel : dayLabel;
  // Seven to ten labels at most; a long range labels every n-th bar.
  const every = Math.max(1, Math.ceil(buckets.length / (hourly ? 8 : 10)));
  const gap = buckets.length <= 14 ? 10 : buckets.length <= 31 ? 4 : 2;

  const requests = Number(stat.request_count || 0);
  const errors = Number(stat.error_count || 0);
  const total = requests + errors;
  const ttft = stat.median_ttft_ms;
  const metrics = [
    [t(`${SC}.requests`), total.toLocaleString()],
    [
      t(`${SC}.success`),
      total
        ? `${((requests / total) * 100).toFixed(1).replace(/\.0$/, '')}%`
        : '—',
    ],
    [
      t(`${SC}.cache`),
      requests
        ? `${Math.round(Number(stat.cache_hit_ratio || 0) * 100)}%`
        : '—',
    ],
    [
      t(`${SC}.ttft`),
      ttft == null
        ? '—'
        : t(`${SC}.seconds`, { value: (ttft / 1000).toFixed(1) }),
    ],
  ];

  return (
    <section
      className='spend-card'
      aria-label={t(`${SC}.title`, { range: span })}
    >
      <div className='spend-card__hero'>
        <div>
          <div className='spend-card__label'>
            {t(`${SC}.title`, { range: span })}
          </div>
          <div className='spend-card__value'>
            <Pending chars={5} pending={loading}>
              {money(amount)}
            </Pending>
          </div>
        </div>
        <div className='spend-card__label'>{loading ? '' : compare}</div>
      </div>
      <div
        aria-label={
          peak
            ? t(hourly ? `${SC}.chartHourly` : `${SC}.chartDaily`, {
                peak: label(peak.at),
              })
            : t(`${SC}.chartEmpty`)
        }
        className='spend-card__bars'
        role='img'
        style={{ gap }}
      >
        {buckets.map((bucket, i) => (
          <div
            className='spend-card__col'
            key={bucket.at.getTime()}
            title={`${label(bucket.at)} · ${money(bucket.amount)} · ${t(`${SC}.barRequests`, { count: bucket.requests })}`}
          >
            <div
              className='spend-card__bar'
              data-current={i === buckets.length - 1 || undefined}
              style={{
                height: Math.round(8 + (max ? bucket.amount / max : 0) * 92),
              }}
            />
            <div className='spend-card__tick'>
              {(buckets.length - 1 - i) % every === 0 ? label(bucket.at) : ''}
            </div>
          </div>
        ))}
      </div>
      <div className='spend-card__side'>
        {metrics.map(([name, value]) => (
          <div className='spend-card__metric' key={name}>
            <span>{name}</span>
            <b>
              <Pending chars={4} pending={loading}>
                {value}
              </Pending>
            </b>
          </div>
        ))}
      </div>
    </section>
  );
}
