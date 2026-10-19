/* How a session's work unfolded, above its call table. A session that ran
 * subagents shows who dispatched whom and what ran in parallel (子代理协作);
 * a single-thread session shows its work segments — stretches of calls
 * split wherever it stopped for more than two minutes (工作段) — or, when it
 * ran in one go, a single sentence. Stops are folded, never drawn to scale. */
import { Fragment, useMemo } from 'react';
import { requestConcurrency, requestInterval } from './request-intervals';
import {
  SK,
  clockSecondsText,
  clockText,
  durationText,
  shortThreadId,
} from './session-format';
import { formatCount, formatMicroUsd } from '../../../components/ui';
import type {
  AgentSessionDetail,
  AgentSessionRequest,
} from '../../../hooks/usage-logs/useAgentSessions';
import type { TFunction } from 'i18next';

/** A stop longer than this splits the work into segments. */
const IDLE_GAP_SECONDS = 120;
/** A folded stop takes this share of the active time on the axis. */
const FOLDED_GAP_SHARE = 0.04;
const SPARK_BARS = 48;

export type ActivityCall = { key: string; request: AgentSessionRequest };

export type CallRange = { from: number; label: string; to: number };

type Props = {
  calls: ActivityCall[];
  detail: AgentSessionDetail;
  onPickCall: (key: string) => void;
  onPickRange: (range: CallRange) => void;
  t: TFunction;
};

type Segment = { calls: ActivityCall[]; end: number; start: number };

const seconds = (value: number, t: TFunction) =>
  value < 60
    ? t(`${SK}.duration.s`, { defaultValue: '{{n}} 秒', n: Math.round(value) })
    : durationText(value, t);

function segmentsOf(calls: ActivityCall[]): Segment[] {
  const out: Segment[] = [];
  for (const call of [...calls].sort(
    (a, b) => requestInterval(a.request).from - requestInterval(b.request).from,
  )) {
    const { from, to: at } = requestInterval(call.request);
    const last = out[out.length - 1];
    if (last && from - last.end <= IDLE_GAP_SECONDS) {
      last.calls.push(call);
      last.end = Math.max(last.end, at);
    } else {
      out.push({ calls: [call], end: at, start: from });
    }
  }
  return out;
}

/** Maps a time to a position along an axis whose stops are folded to a
 *  fixed sliver, so a 12-minute wait does not take half the chart. */
function foldedAxis(segments: Segment[]) {
  const spans = segments.map((segment) =>
    Math.max(1, segment.end - segment.start),
  );
  const active = spans.reduce((sum, span) => sum + span, 0);
  const gap = Math.max(1, active * FOLDED_GAP_SHARE);
  const total = active + gap * Math.max(0, segments.length - 1);
  const offsets: number[] = [];
  let cursor = 0;
  spans.forEach((span, index) => {
    offsets.push(cursor);
    cursor += span + (index < spans.length - 1 ? gap : 0);
  });
  const toX = (at: number) => {
    let index = 0;
    for (let i = 0; i < segments.length; i++) {
      if (at >= segments[i].start) index = i;
    }
    const segment = segments[index];
    const within = Math.min(Math.max(0, at - segment.start), spans[index]);
    return ((offsets[index] + within) / total) * 100;
  };
  const gaps = segments.slice(1).map((segment, index) => ({
    from: segments[index].end,
    left: ((offsets[index] + spans[index]) / total) * 100,
    to: segment.start,
    width: (gap / total) * 100,
  }));
  return { gaps, toX };
}

const callTitle = (request: AgentSessionRequest, t: TFunction) => {
  const parts = [
    clockSecondsText(request.created_at),
    request.thread_id ? shortThreadId(request.thread_id) : 'main',
    request.kind && request.kind !== 'main' ? request.kind : '',
    request.model_name,
    request.failed
      ? t(`${SK}.status.failed`, { defaultValue: '失败' })
      : `${formatCount(request.prompt_tokens)} → ${formatCount(request.completion_tokens)}`,
    formatMicroUsd(request.amount_microusd),
  ];
  if (!request.failed && request.official_microusd) {
    const pct = Math.round(
      ((request.official_microusd - request.amount_microusd) /
        request.official_microusd) *
        100,
    );
    if (pct >= 1)
      parts.push(
        t(`${SK}.list.savedPct`, { defaultValue: '省 {{pct}}%', pct }),
      );
  }
  return parts.filter(Boolean).join(' · ');
};

export default function SessionActivity(props: Props) {
  const hasSubagents = props.detail.threads.some((thread) => thread.thread_id);
  if (props.calls.length === 0 || !props.detail.requests_complete) return null;
  return hasSubagents ? (
    <Collaboration {...props} />
  ) : (
    <WorkSegments {...props} />
  );
}

/* ---- 子代理协作 ------------------------------------------------------- */

const MAIN_LANE = 68;
const SUB_LANE = 42;
const LANE_GAP = 8;

function Collaboration({ calls, detail, onPickCall, t }: Props) {
  const view = useMemo(() => {
    const segments = segmentsOf(calls);
    const axis = foldedAxis(segments);
    const span = detail.session.last_at - detail.session.started_at;
    const fmt = span < 600 ? clockSecondsText : clockText;
    const maxAmount = Math.max(
      1,
      ...calls.map((call) => call.request.amount_microusd),
    );
    const byThread = new Map<string, ActivityCall[]>();
    for (const call of calls) {
      const id = call.request.thread_id || '';
      byThread.set(id, [...(byThread.get(id) ?? []), call]);
    }
    const subIds = [
      ...detail.threads
        .filter((thread) => thread.thread_id)
        .sort((a, b) => a.started_at - b.started_at)
        .map((thread) => thread.thread_id),
    ];
    for (const id of byThread.keys()) {
      if (id && !subIds.includes(id)) subIds.push(id);
    }
    const total = Math.max(1, detail.session.amount_microusd);
    const lane = (id: string) => {
      const own = byThread.get(id) ?? [];
      const cost = own.reduce(
        (sum, call) => sum + call.request.amount_microusd,
        0,
      );
      const first = own[0]?.request.created_at ?? 0;
      const last = own[own.length - 1]?.request.created_at ?? 0;
      return {
        calls: own,
        cost,
        first,
        id,
        last,
        share: cost / total,
      };
    };
    const main = lane('');
    const subs = subIds.map(lane).filter((item) => item.calls.length > 0);

    // Gateway-observed request overlap, not the lifetime of a thread.
    const steps = requestConcurrency(calls.map((call) => call.request));
    const peak = steps.reduce(
      (best, step) => (step.count > best.count ? step : best),
      { count: 0, from: 0, to: 0 },
    );

    const longestGap = axis.gaps.reduce(
      (best, gap) => (gap.to - gap.from > best.to - best.from ? gap : best),
      { from: 0, left: 0, to: 0, width: 0 },
    );
    const priciest = subs.reduce(
      (best, sub) => (sub.cost > best.cost ? sub : best),
      subs[0],
    );
    const subShare = subs.reduce((sum, sub) => sum + sub.share, 0);

    // Compaction: the thread's average call before and after the first one.
    const compacts = calls.filter((call) => call.request.kind === 'compact');
    let compactSaving: number | null = null;
    const firstCompact = compacts[0];
    if (firstCompact) {
      const own = byThread.get(firstCompact.request.thread_id || '') ?? [];
      const at = own.indexOf(firstCompact);
      const plain = (list: ActivityCall[]) =>
        list.filter(
          (call) => !call.request.failed && call.request.kind !== 'compact',
        );
      const before = plain(own.slice(Math.max(0, at - 10), at));
      const after = plain(own.slice(at + 1, at + 11));
      const avg = (list: ActivityCall[]) =>
        list.reduce((sum, call) => sum + call.request.amount_microusd, 0) /
        Math.max(1, list.length);
      if (before.length > 0 && after.length > 0 && avg(before) > 0) {
        const pct = Math.round((1 - avg(after) / avg(before)) * 100);
        if (pct >= 1) compactSaving = pct;
      }
    }

    // Axis labels: every segment edge first, then points inside long
    // segments, each dropped when it would collide with one already placed.
    const segmentEdges = segments.flatMap((segment) => [
      segment.start,
      segment.end,
    ]);
    const inside = segments.flatMap((segment) =>
      [0.25, 0.5, 0.75].map((f) =>
        Math.round(segment.start + f * (segment.end - segment.start)),
      ),
    );
    const labels: { at: number; x: number }[] = [];
    for (const at of [...segmentEdges, ...inside]) {
      const x = axis.toX(at);
      if (labels.every((label) => Math.abs(label.x - x) >= 7))
        labels.push({ at, x });
    }
    return {
      axis,
      compactSaving,
      compacts: compacts.length,
      fmt,
      labels,
      longestGap,
      main,
      maxAmount,
      peak,
      priciest,
      steps,
      subShare,
      subs,
    };
  }, [calls, detail]);

  const { axis, fmt, main, subs } = view;
  const laneTop = (index: number) =>
    MAIN_LANE + LANE_GAP + index * (SUB_LANE + LANE_GAP);

  const bars = (own: ActivityCall[], height: number) =>
    own.map((call) => {
      const { request } = call;
      const share = request.failed
        ? 0.3
        : Math.max(0.1, request.amount_microusd / view.maxAmount);
      return (
        <button
          data-owns-chrome
          aria-label={callTitle(request, t)}
          className='sa-bar'
          data-failed={request.failed || undefined}
          key={call.key}
          onClick={() => onPickCall(call.key)}
          style={{
            height: (height - 10) * share,
            left: `${axis.toX(request.created_at)}%`,
          }}
          title={callTitle(request, t)}
          type='button'
        />
      );
    });
  const compactMarks = (own: ActivityCall[]) =>
    own
      .filter((call) => call.request.kind === 'compact')
      .map((call) => (
        <i
          aria-hidden='true'
          className='sa-compact'
          key={`c-${call.key}`}
          style={{ left: `${axis.toX(call.request.created_at)}%` }}
        />
      ));
  const folds = axis.gaps.map((gap) => (
    <i
      aria-hidden='true'
      className='sa-fold'
      key={gap.from}
      style={{ left: `${gap.left}%`, width: `${gap.width}%` }}
    />
  ));
  const costCell = (cost: number, share: number) => (
    <span className='sa-cost'>
      <span className='sv-money'>{formatMicroUsd(cost)}</span>
      <span aria-hidden='true' className='sa-share'>
        <i style={{ width: `${Math.min(100, share * 100)}%` }} />
      </span>
    </span>
  );

  return (
    <section className='sd-card'>
      <div className='sd-card__head'>
        <h2 className='sd-card__title'>
          {t(`${SK}.activity.collabTitle`, { defaultValue: '子代理协作' })}
        </h2>
        <span className='sa-legend'>
          <span>
            <i className='sa-key sa-key--bar' />
            {t(`${SK}.activity.legendCall`, {
              defaultValue: '一次调用（高 = 贵）',
            })}
          </span>
          <span>
            <i className='sa-key sa-key--fail' />
            {t(`${SK}.status.failed`, { defaultValue: '失败' })}
          </span>
          <span>
            <i className='sa-key sa-key--compact' />
            {t(`${SK}.activity.legendCompact`, { defaultValue: '压缩' })}
          </span>
          <span>
            <i className='sa-key sa-key--fold' />
            {t(`${SK}.activity.legendFold`, { defaultValue: '停顿（已折叠）' })}
          </span>
        </span>
      </div>

      <div className='sa-grid'>
        <span className='sa-rowlabel'>
          {t(`${SK}.activity.concurrent`, { defaultValue: '请求并发（约）' })}
        </span>
        <div className='sa-conc'>
          {view.steps
            .filter((step) => step.count > 0)
            .map((step) => {
              const left = axis.toX(step.from);
              return (
                <i
                  key={step.from}
                  style={{
                    height: Math.min(4, step.count) * 6,
                    left: `${left}%`,
                    opacity: 0.25 + Math.min(4, step.count) * 0.18,
                    width: `${Math.max(0.3, axis.toX(step.to) - left)}%`,
                  }}
                  title={t(`${SK}.activity.concurrentTip`, {
                    defaultValue: '{{from}}–{{to}} · 约 {{n}} 个请求并发',
                    from: fmt(step.from),
                    n: step.count,
                    to: fmt(step.to),
                  })}
                />
              );
            })}
          {view.peak.count > 1 ? (
            <b
              className='sa-peak'
              style={{ left: `${axis.toX(view.peak.from)}%` }}
            >
              {view.peak.count}
            </b>
          ) : null}
        </div>
        <span className='sa-rowlabel sv-right'>
          {t(`${SK}.activity.costShare`, { defaultValue: '花费 · 占比' })}
        </span>

        <span className='sa-lane-name'>
          <b>main</b>
          <span className='sv-sub'>
            {t(`${SK}.detail.modelCalls`, {
              defaultValue: '{{n}} 次调用',
              n: formatCount(main.calls.length),
            })}
          </span>
        </span>
        <div className='sa-lane' style={{ height: MAIN_LANE }}>
          {folds}
          {/* Where each subagent was dispatched: a dashed drop from main to
              its lane, at its first call. */}
          {subs.map((sub, index) => (
            <i
              aria-hidden='true'
              className='sa-link'
              key={`link-${sub.id}`}
              style={{
                height: laneTop(index) - MAIN_LANE + 8,
                left: `${axis.toX(sub.first)}%`,
                top: MAIN_LANE - 4,
              }}
            />
          ))}
          {bars(main.calls, MAIN_LANE)}
          {compactMarks(main.calls)}
        </div>
        {costCell(main.cost, main.share)}

        {subs.map((sub) => {
          const left = axis.toX(sub.first);
          return (
            <div className='sa-row' key={sub.id}>
              <span className='sa-lane-name' data-sub>
                <span className='sa-lane-id'>
                  <b title={sub.id}>{shortThreadId(sub.id)}</b>
                  <span className='lz-kind-tag'>subagent</span>
                </span>
                <span className='sv-sub'>
                  {t(`${SK}.activity.subMeta`, {
                    defaultValue: '{{n}} 次 · {{from}}–{{to}}',
                    from: fmt(sub.first),
                    n: formatCount(sub.calls.length),
                    to: fmt(sub.last),
                  })}
                </span>
              </span>
              <div className='sa-lane' style={{ height: SUB_LANE }}>
                {folds}
                <i
                  aria-hidden='true'
                  className='sa-span'
                  style={{
                    left: `${left}%`,
                    width: `${Math.max(0.6, axis.toX(sub.last) - left)}%`,
                  }}
                />
                {bars(sub.calls, SUB_LANE)}
                {compactMarks(sub.calls)}
              </div>
              {costCell(sub.cost, sub.share)}
            </div>
          );
        })}

        <span />
        <div className='sa-axis'>
          {view.labels.map((label) => (
            <span
              key={label.at}
              style={{
                left: `${label.x}%`,
                transform:
                  label.x < 4
                    ? 'none'
                    : label.x > 96
                      ? 'translateX(-100%)'
                      : 'translateX(-50%)',
              }}
            >
              {fmt(label.at)}
            </span>
          ))}
        </div>
        <span />
      </div>

      <div className='sa-facts'>
        <span>
          {view.peak.count > 1
            ? t(`${SK}.activity.factPeak`, {
                defaultValue:
                  '按秒级耗时估算，最多 {{n}} 个请求并发，{{from}}–{{to}}',
                from: fmt(view.peak.from),
                n: view.peak.count,
                to: fmt(view.peak.to),
              })
            : t(`${SK}.activity.factSerial`, {
                defaultValue:
                  '秒级请求记录未观测到重叠；不代表代理没有并行工作',
              })}
        </span>
        <span>
          {t(`${SK}.activity.factSubShare`, {
            defaultValue: '子代理花掉 {{pct}}，{{name}} 最贵',
            name: view.priciest ? shortThreadId(view.priciest.id) : '—',
            pct: `${Math.round(view.subShare * 100)}%`,
          })}
        </span>
        <span>
          {view.compacts === 0
            ? t(`${SK}.activity.factNoCompact`, {
                defaultValue: '没有压缩上下文',
              })
            : view.compactSaving !== null
              ? t(`${SK}.activity.factCompactSaving`, {
                  defaultValue:
                    '压缩 {{n}} 次；首次压缩后相邻样本平均费用低 {{pct}}%（非因果结论）',
                  n: view.compacts,
                  pct: view.compactSaving,
                })
              : t(`${SK}.activity.factCompact`, {
                  defaultValue: '压缩 {{n}} 次',
                  n: view.compacts,
                })}
        </span>
        <span>
          {view.longestGap.to > view.longestGap.from
            ? t(`${SK}.activity.factGap`, {
                defaultValue: '停了 {{d}}（{{from}}–{{to}}，没有调用）',
                d: seconds(view.longestGap.to - view.longestGap.from, t),
                from: fmt(view.longestGap.from),
                to: fmt(view.longestGap.to),
              })
            : t(`${SK}.activity.factNoGap`, {
                defaultValue: '未观测到超过两分钟的请求间隔',
              })}
        </span>
      </div>
    </section>
  );
}

/* ---- 工作段 ----------------------------------------------------------- */

function spark(calls: ActivityCall[]) {
  const buckets = Math.min(SPARK_BARS, calls.length);
  const sums = Array.from({ length: buckets }, () => 0);
  calls.forEach((call, index) => {
    sums[Math.floor((index * buckets) / calls.length)] +=
      call.request.amount_microusd;
  });
  const max = Math.max(1, ...sums);
  return sums.map((sum) => Math.max(0.12, sum / max));
}

function WorkSegments({ calls, detail, onPickRange, t }: Props) {
  const view = useMemo(() => {
    const segments = segmentsOf(calls).map((segment, index) => {
      const cost = segment.calls.reduce(
        (sum, call) => sum + call.request.amount_microusd,
        0,
      );
      return {
        ...segment,
        compacts: segment.calls.filter(
          (call) => call.request.kind === 'compact',
        ).length,
        cost,
        failed: segment.calls.filter((call) => call.request.failed).length,
        n: index + 1,
        spark: spark(segment.calls),
      };
    });
    const span = detail.session.last_at - detail.session.started_at;
    const fmt = span < 600 ? clockSecondsText : clockText;
    const work = segments.reduce(
      (sum, segment) => sum + (segment.end - segment.start),
      0,
    );
    const total = Math.max(1, detail.session.amount_microusd);
    const longest = segments.reduce((best, segment) =>
      segment.end - segment.start > best.end - best.start ? segment : best,
    );
    const priciest = segments.reduce((best, segment) =>
      segment.cost > best.cost ? segment : best,
    );
    return {
      fmt,
      idle: Math.max(0, span - work),
      longest,
      priciest,
      segments,
      total,
      work,
    };
  }, [calls, detail]);
  const { fmt, segments } = view;

  if (segments.length === 1) {
    const only = segments[0];
    const every =
      only.calls.length > 1
        ? (only.end - only.start) / (only.calls.length - 1)
        : null;
    return (
      <section className='sd-card sa-oneline'>
        <span className='sa-oneline__text'>
          <h2 className='sd-card__title'>
            {t(`${SK}.activity.segmentsTitle`, { defaultValue: '工作段' })}
          </h2>
          <span>
            {t(`${SK}.activity.oneGo`, {
              defaultValue:
                '一口气跑完：{{span}}，{{n}} 次调用{{pace}}，没有停顿',
              n: formatCount(only.calls.length),
              pace:
                every === null
                  ? ''
                  : t(`${SK}.activity.pace`, {
                      defaultValue: '，平均 {{d}}一次',
                      d: seconds(every, t),
                    }),
              span: `${fmt(only.start)} → ${fmt(only.end)}`,
            })}
          </span>
        </span>
        <span className='ses-note'>
          {t(`${SK}.activity.seeTable`, {
            defaultValue: '每次调用的明细在下面的列表里',
          })}
        </span>
      </section>
    );
  }

  const failedIn = segments.filter((segment) => segment.failed > 0);
  const failures = failedIn.reduce((sum, segment) => sum + segment.failed, 0);
  const lastFailed =
    detail.session.error_count > 0 &&
    (detail.session.last_error_at ?? 0) >= detail.session.last_at;

  return (
    <section className='sd-card'>
      <div className='sd-card__head'>
        <h2 className='sd-card__title'>
          {t(`${SK}.activity.segmentsTitle`, { defaultValue: '工作段' })}
        </h2>
        <span className='ses-note'>
          {t(`${SK}.activity.segmentsHint`, {
            defaultValue:
              '连续调用算一段，停下超过 2 分钟就断开 · 块宽 = 时长 · 点一段只看它的调用',
          })}
        </span>
      </div>
      <div className='sa-segments'>
        {segments.map((segment, index) => {
          const gap =
            index > 0 ? segment.start - segments[index - 1].end : null;
          const weight = Math.max(
            segment.end - segment.start,
            view.work * 0.06,
            1,
          );
          const label = t(`${SK}.activity.segmentN`, {
            defaultValue: '第 {{n}} 段',
            n: segment.n,
          });
          const meta = [
            t(`${SK}.detail.modelCalls`, {
              defaultValue: '{{n}} 次调用',
              n: formatCount(segment.calls.length),
            }),
            segment.compacts
              ? t(`${SK}.activity.compacts`, {
                  defaultValue: '压缩 {{n}} 次',
                  n: segment.compacts,
                })
              : '',
            segment.failed
              ? t(`${SK}.detail.failedCount`, {
                  defaultValue: '失败 {{n}} 次',
                  n: segment.failed,
                })
              : '',
          ]
            .filter(Boolean)
            .join(' · ');
          return (
            <Fragment key={segment.start}>
              {gap !== null ? (
                <span className='sa-gap' data-long={gap >= 600 || undefined}>
                  {t(`${SK}.activity.stoppedLabel`, { defaultValue: '停了' })}
                  <b>{seconds(gap, t)}</b>
                </span>
              ) : null}
              <button
                data-owns-chrome
                className='sa-seg'
                style={{ flex: `${weight} 1 0` }}
                data-hot={
                  segment === view.priciest && segments.length > 1
                    ? ''
                    : undefined
                }
                onClick={() =>
                  onPickRange({ from: segment.start, label, to: segment.end })
                }
                type='button'
              >
                <span className='sa-seg__top'>
                  <b>{label}</b>
                  <span className='sv-money'>
                    {formatMicroUsd(segment.cost)}
                  </span>
                </span>
                <span className='sa-seg__span'>
                  {fmt(segment.start)}–{fmt(segment.end)}
                </span>
                <span aria-hidden='true' className='sa-spark'>
                  {segment.spark.map((height, bar) => (
                    <i key={bar} style={{ height: `${height * 100}%` }} />
                  ))}
                </span>
                <span className='sv-sub sv-ellipsis'>{meta}</span>
              </button>
            </Fragment>
          );
        })}
      </div>
      <div className='sa-facts'>
        <span>
          {t(`${SK}.activity.factWork`, {
            defaultValue: '干活 {{work}}，停了 {{idle}}',
            idle: seconds(view.idle, t),
            work: seconds(view.work, t),
          })}
        </span>
        <span>
          {t(`${SK}.activity.factSegments`, {
            defaultValue: '{{n}} 段，最长一段 {{d}}（第 {{i}} 段）',
            d: seconds(view.longest.end - view.longest.start, t),
            i: view.longest.n,
            n: segments.length,
          })}
        </span>
        <span>
          {t(`${SK}.activity.factPriciest`, {
            cost: formatMicroUsd(view.priciest.cost),
            defaultValue: '第 {{i}} 段最贵：{{cost}}，占 {{pct}}',
            i: view.priciest.n,
            pct: `${Math.round((view.priciest.cost / view.total) * 100)}%`,
          })}
        </span>
        <span>
          {failures === 0
            ? t(`${SK}.activity.factNoFailures`, { defaultValue: '没有失败' })
            : t(
                lastFailed
                  ? `${SK}.activity.factFailuresEnded`
                  : `${SK}.activity.factFailures`,
                {
                  defaultValue: lastFailed
                    ? '失败 {{n}} 次，在第 {{list}} 段，最后一次调用失败'
                    : '失败 {{n}} 次，在第 {{list}} 段，之后接着跑完',
                  list: failedIn.map((segment) => segment.n).join('、'),
                  n: failures,
                },
              )}
        </span>
      </div>
    </section>
  );
}
