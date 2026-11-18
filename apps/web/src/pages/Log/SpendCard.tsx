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
export const formatSpendMoney = (micro: number) =>
  `$${(micro / 1_000_000).toFixed(Math.abs(micro) >= 10_000 || !micro ? 2 : 4)}`;

/** The hourly series folded into local days (or kept by the hour when
 *  `hourly`), with the empty ones filled in. */
export function spendBuckets(
  series: UsageLogStat['series'],
  start: number,
  end: number,
  hourly: boolean,
): { buckets: Bucket[]; hourly: boolean } {
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
  amountMicrousd,
  loading,
  preset,
  range,
  stat,
}: {
  amountMicrousd?: number;
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
  const statAmount = Number(stat.amount_microusd || 0);
  const amount = amountMicrousd ?? statAmount;
  const previous =
    stat.previous_amount_microusd == null
      ? null
      : Number(stat.previous_amount_microusd);
  const delta = previous == null ? 0 : statAmount - previous;
  const compare =
    previous == null
      ? ''
      : delta === 0
        ? t(`${SC}.same`, { range: span })
        : t(delta > 0 ? `${SC}.more` : `${SC}.less`, {
            amount: formatSpendMoney(Math.abs(delta)),
            range: span,
          });

  // 24 小时 runs from a day ago to the end of today, so its window is longer
  // than a day; the range's own length decides, not the window's.
  const { buckets, hourly } = spendBuckets(
    stat.series,
    range.start,
    range.end,
    preset === '24h' || range.days <= 1,
  );
  const max = Math.max(0, ...buckets.map((bucket) => bucket.amount));
  const peak = buckets.find((bucket) => max > 0 && bucket.amount === max);
  const label = hourly ? hourLabel : dayLabel;
  const gap = buckets.length <= 14 ? 4 : buckets.length <= 31 ? 3 : 2;
  const chartSummary = peak
    ? t(hourly ? `${SC}.chartHourly` : `${SC}.chartDaily`, {
        peak: label(peak.at),
      })
    : t(`${SC}.chartEmpty`);
  const chartNote = peak
    ? t(`${SC}.peakSlice`, {
        amount: formatSpendMoney(peak.amount),
        defaultValue: '{{peak}} 峰值 {{amount}}',
        peak: label(peak.at),
      })
    : t(`${SC}.chartEmpty`);

  const requests = Number(stat.request_count || 0);
  const errors = Number(stat.error_count || 0);
  const total = requests + errors;
  const successRatio = total > 0 ? requests / total : null;
  const ttft = stat.median_ttft_ms;
  const metrics: Array<{
    key: string;
    name: string;
    tone?: 'ok';
    value: string;
  }> = [
    {
      key: 'requests',
      name: t(`${SC}.requests`),
      value: total.toLocaleString(),
    },
    {
      key: 'success',
      name: t(`${SC}.success`),
      tone: successRatio !== null && successRatio >= 0.99 ? 'ok' : undefined,
      value:
        successRatio !== null
          ? `${(successRatio * 100).toFixed(1).replace(/\.0$/, '')}%`
          : '—',
    },
    {
      key: 'cache',
      name: t(`${SC}.cache`),
      value: requests
        ? `${(Number(stat.cache_hit_ratio || 0) * 100).toFixed(1).replace(/\.0$/, '')}%`
        : '—',
    },
    {
      key: 'ttft',
      name: t(`${SC}.ttft`),
      value:
        ttft == null
          ? '—'
          : t(`${SC}.seconds`, {
              value: (ttft / 1000).toFixed(ttft < 1000 ? 2 : 1),
            }),
    },
  ];

  return (
    <section
      className='spend-card'
      aria-label={t(`${SC}.title`, { range: span })}
    >
      <div className='spend-card__hero'>
        <div className='spend-card__label'>
          {t(`${SC}.title`, { range: span })}
        </div>
        <div className='spend-card__value'>
          <Pending chars={5} pending={loading}>
            {formatSpendMoney(amount)}
          </Pending>
        </div>
      </div>
      <div className='spend-card__chart'>
        <div className='spend-card__chart-head'>
          <span className='spend-card__chart-note'>
            <Pending chars={10} pending={loading}>
              {chartNote}
            </Pending>
          </span>
          {!loading && compare ? (
            <span
              className='spend-card__delta'
              data-less={delta < 0 || undefined}
            >
              {compare}
            </span>
          ) : null}
        </div>
        <div
          aria-label={chartSummary}
          className='spend-card__bars'
          role='img'
          style={{ gap }}
        >
          {buckets.map((bucket, i) => (
            <div
              className='spend-card__col'
              key={bucket.at.getTime()}
              title={`${label(bucket.at)} · ${formatSpendMoney(bucket.amount)} · ${t(`${SC}.barRequests`, { count: bucket.requests })}`}
            >
              <div
                className='spend-card__bar'
                data-current={i === buckets.length - 1 || undefined}
                data-empty={bucket.amount <= 0 || undefined}
                style={{
                  height: Math.max(
                    4,
                    Math.round((max ? bucket.amount / max : 0) * 38),
                  ),
                }}
              />
            </div>
          ))}
        </div>
      </div>
      {metrics.map((metric) => (
        <div className='spend-card__metric' key={metric.key}>
          <span>{metric.name}</span>
          <b data-tone={metric.tone}>
            <Pending chars={4} pending={loading}>
              {metric.value}
            </Pending>
          </b>
        </div>
      ))}
    </section>
  );
}
