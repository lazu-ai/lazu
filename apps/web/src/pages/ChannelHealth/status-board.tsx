import { memo, useMemo, useState, type MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { StatusDot } from '../../components/ui/status-dot';
import { groupByMaker, makerLabel, makerOf } from '../../lib/model-makers';
import type {
  ChannelHealthBucket,
  ChannelHealthKind,
  ChannelHealthKindSection,
  ChannelHealthRow,
  ChannelHealthStatus,
  ChannelHealthTone,
} from '../../hooks/health/useChannelHealthQuery';

const ns = 'page.console.channelHealth';

/* The page reads like a public status page, packed into cards: one card per
   model, and inside it one block per lane — status and the week's
   availability, a line of thin bars across the week, and the lane's own
   figures under them, so a lane's verdict and its speed read together.
   Order is fixed — a reader finds the model they call in the same place every
   visit — and an operator's banner above says what is wrong. */

/** An interruption rate this high means a reader regularly watches replies
 *  die mid-sentence, whatever the availability says. */
const INTERRUPTION_WARN = 0.02;

const rowKind = (row: ChannelHealthRow): ChannelHealthKind => row.kind ?? 'llm';

// Operators can have several real routes projected to the same public lane.
const displayLane = (
  row: ChannelHealthRow,
  laneLabel: (lane: string) => string,
) =>
  row.lane_slug && !['direct', 'cheap'].includes(row.lane_slug)
    ? row.lane_slug
    : laneLabel(row.lane);

const formatMs = (value?: number) => {
  if (value == null || value <= 0) return undefined;
  return value >= 1000
    ? `${(value / 1000).toFixed(2)}s`
    : `${Math.round(value)}ms`;
};

/** Status pages print availability to the precision that separates good
 *  weeks: two decimals above 99%, one below it. */
export const formatUptime = (value?: number) => {
  if (value == null) return '—';
  const percent = value * 100;
  if (value >= 1) return '100.0%';
  return `${Math.min(percent, 99.99).toFixed(value >= 0.99 ? 2 : 1)}%`;
};

/** One slice's availability: whole numbers where they are whole (0%, 100%),
 *  one decimal otherwise. */
const formatRate = (value?: number) => {
  if (value == null) return '—';
  const percent = Number(
    (value < 1 ? Math.min(value * 100, 99.9) : 100).toFixed(1),
  );
  return `${Number.isInteger(percent) ? percent.toFixed(0) : percent.toFixed(1)}%`;
};

const formatRatio = (value: number) => {
  const percent = value * 100;
  return `${percent > 0 && percent < 1 ? percent.toFixed(1) : percent.toFixed(0)}%`;
};

type FigurePart = { text: string; warn?: boolean };

export type StatusBoardProps = {
  rows: ChannelHealthRow[];
  allRows: ChannelHealthRow[];
  sections: ChannelHealthKindSection[];
  bucketSeconds: number;
  windowSeconds: number;
  isAdmin: boolean;
  laneLabel: (lane: string) => string;
  kindLabel: (kind: ChannelHealthKind) => string;
  sourceLabel: (source: ChannelHealthRow['source']) => string;
};

/** The operator's one-line tally. Each family already states its own
 *  count, so this says only how the whole board adds up; a single troubled
 *  lane is named, since a count of one says less than its name. */
export function StatusBanner({
  rows,
  laneLabel,
}: {
  rows: ChannelHealthRow[];
  laneLabel: (lane: string) => string;
}) {
  const { t } = useTranslation();
  const outages = rows.filter((row) => row.status === 'outage');
  const degraded = rows.filter((row) => row.status === 'degraded');
  const operational = rows.filter((row) => row.status === 'operational');
  const unknown = rows.filter((row) => row.status === 'unknown');
  const tone = outages.length
    ? 'bad'
    : degraded.length
      ? 'warn'
      : unknown.length || !rows.length
        ? 'unknown'
        : 'ok';

  const laneName = (row: ChannelHealthRow) => {
    if (rowKind(row) !== 'llm') return row.model_name;
    // The agent lane's name already ends in its own noun (Agent 专线), so it
    // does not take the 「…线路」 suffix the other lanes use.
    if (row.lane === 'eco' && (!row.lane_slug || row.lane_slug === 'cheap')) {
      return t(`${ns}.banner.laneNameAgent`, {
        defaultValue: '{{model}} Agent 专线',
        model: row.model_name,
      });
    }
    return t(`${ns}.banner.laneName`, {
      defaultValue: '{{model}} {{lane}}线路',
      model: row.model_name,
      lane: displayLane(row, laneLabel),
    });
  };

  const parts: string[] = [];
  if (outages.length === 1) {
    parts.push(
      t(`${ns}.banner.outageOne`, {
        defaultValue: '{{name}}中断',
        name: laneName(outages[0]),
      }),
    );
  } else if (outages.length) {
    parts.push(
      t(`${ns}.banner.outageCount`, {
        defaultValue: '{{count}} 中断',
        count: outages.length,
      }),
    );
  }
  if (degraded.length === 1) {
    parts.push(
      t(`${ns}.banner.degradedOne`, {
        defaultValue: '{{name}}降级',
        name: laneName(degraded[0]),
      }),
    );
  } else if (degraded.length) {
    parts.push(
      t(`${ns}.banner.degradedCount`, {
        defaultValue: '{{count}} 降级',
        count: degraded.length,
      }),
    );
  }
  if (unknown.length) {
    parts.push(
      t(`${ns}.banner.unknownCount`, {
        defaultValue: '{{count}} 暂无数据',
        count: unknown.length,
      }),
    );
  }
  if (operational.length) {
    parts.push(
      parts.length
        ? t(`${ns}.banner.okCount`, {
            defaultValue: '{{count}} 正常',
            count: operational.length,
          })
        : t(`${ns}.banner.allOk`, {
            defaultValue: '全部 {{count}} 条线路正常',
            count: operational.length,
          }),
    );
  }

  return (
    <div className={`chealth-banner chealth-banner--${tone}`} role='status'>
      <i aria-hidden='true' className='chealth-banner__dot' />
      <span className='chealth-banner__text'>
        {parts.map((part, index) => (
          <span
            className={index ? undefined : 'chealth-banner__lead'}
            key={part}
          >
            {index ? ' · ' : ''}
            {part}
          </span>
        ))}
      </span>
    </div>
  );
}

type StatusLaneProps = Pick<StatusBoardProps, 'isAdmin' | 'sourceLabel'> & {
  row: ChannelHealthRow;
  /** The lane's name inside its model's card; empty when the model has only
   *  the one lane and there is nothing to tell apart. */
  label: string;
  section: ChannelHealthKindSection;
  windowDays: number;
  tipWhen: (bucket: ChannelHealthBucket, seconds: number) => string;
};

// Pointer and keyboard inspection only update this lane, not the whole board.
const StatusLane = memo(function StatusLane({
  row,
  label,
  section,
  windowDays,
  isAdmin,
  sourceLabel,
  tipWhen,
}: StatusLaneProps) {
  const { t } = useTranslation();
  const [hoverIndex, setHoverIndex] = useState(-1);
  const [focusIndex, setFocusIndex] = useState(0);
  const onBarsMove = (event: MouseEvent<HTMLElement>) => {
    const target = event.target as HTMLElement;
    if (target.dataset.index == null) return;
    const index = Number(target.dataset.index);
    if (Number.isInteger(index)) setHoverIndex(index);
  };
  const statusWord = (status: ChannelHealthStatus) =>
    t(`${ns}.statusWord.${status}`, {
      defaultValue: {
        operational: '正常',
        degraded: '降级',
        outage: '中断',
        unknown: '暂无数据',
      }[status],
    });

  const toneWord = (tone: ChannelHealthTone) =>
    t(`${ns}.tip.${tone}`, {
      defaultValue: {
        ok: '正常',
        warn: '部分失败',
        bad: '中断',
        none: '暂无数据',
        blocked: '无法探测',
      }[tone],
    });

  const figures = (row: ChannelHealthRow): FigurePart[] => {
    const parts: FigurePart[] = [];
    const push = (key: string, fallback: string, value?: string) => {
      if (value)
        parts.push({ text: t(key, { defaultValue: fallback, value }) });
    };
    const p50 = (key: string, fallback: string, value?: number) =>
      push(`${ns}.fig.${key}`, fallback, formatMs(value));
    switch (rowKind(row)) {
      case 'llm':
        p50('ttft', '首字 {{value}}', row.ttft_p50_ms);
        p50('p95', 'p95 {{value}}', row.ttft_p95_ms);
        if (row.output_tps != null && row.output_tps > 0) {
          push(`${ns}.fig.tps`, '{{value}} tok/s', row.output_tps.toFixed(0));
        }
        // Broken streams are named only once they are common enough to
        // notice; below that the figure is noise beside the lane's status.
        if (
          row.interruption_rate != null &&
          row.interruption_rate >= INTERRUPTION_WARN
        ) {
          parts.push({
            text: t(`${ns}.fig.interruption`, {
              defaultValue: '截断 {{value}}',
              value: formatRatio(row.interruption_rate),
            }),
            warn: true,
          });
        }
        return parts;
      case 'image':
      case 'video':
        p50('generation', '生成 p50 {{value}}', row.latency_p50_ms);
        break;
      case 'search':
        p50('response', '响应 p50 {{value}}', row.latency_p50_ms);
        break;
      default:
        p50('latency', '延迟 p50 {{value}}', row.latency_p50_ms);
    }
    p50('p95', 'p95 {{value}}', row.latency_p95_ms);
    return parts;
  };

  const figureHint = (kind: ChannelHealthKind) =>
    kind === 'llm'
      ? t(`${ns}.fig.llmHint`, {
          defaultValue:
            '首字：从发出请求到第一个输出 token 的时间，推理模型含思考过程。截断：已经开始输出、却没能正常结束的流式回复占比，超过 2% 才显示。',
        })
      : undefined;

  const kind = rowKind(row);
  const count = row.buckets?.length || section.bucket_count;
  const seconds = section.bucket_seconds;
  const buckets = useMemo<ChannelHealthBucket[]>(
    () =>
      row.buckets?.length
        ? row.buckets
        : Array.from({ length: count }, (_unused, index) => ({
            index,
            start_at: '',
            source: 'none' as const,
            request_count: 0,
            error_count: 0,
            tone: 'none' as const,
          })),
    [row.buckets, count],
  );
  // Labels depend on evidence and language, never on the pointer position.
  const labels = useMemo(
    () =>
      buckets.map((bucket) => {
        const parts = [
          tipWhen(bucket, seconds),
          t(`${ns}.tip.${bucket.tone}`, {
            defaultValue: {
              ok: '正常',
              warn: '部分失败',
              bad: '中断',
              none: '暂无数据',
              blocked: '无法探测',
            }[bucket.tone],
          }),
        ];
        if (bucket.success_rate != null)
          parts.push(formatRate(bucket.success_rate));
        if (isAdmin && bucket.request_count > 0)
          parts.push(
            t(`${ns}.tip.samples`, {
              source: sourceLabel(bucket.source),
              count: bucket.request_count,
            }),
          );
        return parts.filter(Boolean).join(' · ');
      }),
    [buckets, seconds, tipWhen, t, isAdmin, sourceLabel],
  );
  const parts = figures(row);
  const hasRecord = buckets.some((bucket) => bucket.tone !== 'none');
  const hovered = hoverIndex >= 0 ? (buckets[hoverIndex] ?? null) : null;
  const status = row.status;

  return (
    <div className='chealth-lane' data-lane={row.lane} data-status={status}>
      <div className='chealth-lane__head'>
        {label ? <span className='chealth-lane__label'>{label}</span> : null}
        {isAdmin && row.fallback_active ? (
          <span className='chealth-lane__flag'>
            {t(`${ns}.row.fallback`, { defaultValue: '备用承接中' })}
          </span>
        ) : null}
        <span className='chealth-lane__spacer' />
        <StatusDot
          className={`chealth-lane__status chealth-lane__status--${status}`}
          tone={
            status === 'operational'
              ? 'success'
              : status === 'degraded'
                ? 'warning'
                : status === 'outage'
                  ? 'danger'
                  : 'neutral'
          }
        >
          {/* Most lanes are fine; a dot says so without a word on every
              card, and only a lane that needs attention spells it out. */}
          {status === 'operational' ? (
            <span className='sr-only'>{statusWord(status)}</span>
          ) : (
            statusWord(status)
          )}
        </StatusDot>
        {hasRecord ? (
          <span
            className='chealth-lane__uptime'
            title={t(`${ns}.board.uptimeHint`, {
              defaultValue: '过去 {{days}} 天可用',
              days: windowDays,
            })}
          >
            {formatUptime(row.success_rate)}
          </span>
        ) : null}
      </div>

      {hasRecord ? (
        <>
          <div
            className='chealth-lane__track'
            onMouseLeave={() => setHoverIndex(-1)}
            onMouseMove={onBarsMove}
          >
            <div
              aria-label={t(`${ns}.board.barsLabel`, {
                defaultValue: '{{name}} 过去 {{days}} 天的记录',
                name: [row.model_name, label].filter(Boolean).join(' · '),
                days: windowDays,
              })}
              className='chealth-bars'
              role='group'
            >
              {buckets.map((bucket, index) => (
                <button
                  type='button'
                  data-owns-chrome
                  aria-label={labels[index]}
                  tabIndex={index === Math.min(focusIndex, count - 1) ? 0 : -1}
                  onFocus={() => {
                    setFocusIndex(index);
                    setHoverIndex(index);
                  }}
                  onBlur={() => setHoverIndex(-1)}
                  onClick={() => setHoverIndex(index)}
                  onKeyDown={(event) => {
                    if (event.key === 'Escape') {
                      setHoverIndex(-1);
                      return;
                    }
                    const next = {
                      ArrowLeft: Math.max(0, index - 1),
                      ArrowRight: Math.min(count - 1, index + 1),
                      Home: 0,
                      End: count - 1,
                    }[event.key];
                    if (next == null) return;
                    event.preventDefault();
                    const target =
                      event.currentTarget.parentElement?.children[next];
                    if (target instanceof HTMLButtonElement) target.focus();
                  }}
                  className={`chealth-bar chealth-bar--${bucket.tone}${hovered === bucket ? ' is-hovered' : ''}`}
                  data-index={index}
                  key={bucket.index}
                  title={labels[index]}
                />
              ))}
            </div>
            {hovered ? (
              <div
                className='chealth-tip'
                /* Anchored to its bar, and slid along itself by the same
                 share, so a bar at either end keeps its tooltip inside the
                 card instead of hanging half of it off the edge. */
                style={{
                  left: `${((hoverIndex + 0.5) / count) * 100}%`,
                  transform: `translateX(-${((hoverIndex + 0.5) / count) * 100}%)`,
                }}
              >
                <span className='chealth-tip__when'>
                  {tipWhen(hovered, seconds)}
                </span>
                <span
                  className={`chealth-tip__verdict chealth-tip__verdict--${hovered.tone}`}
                >
                  {toneWord(hovered.tone)}
                  {hovered.success_rate != null
                    ? ` · ${t(`${ns}.tip.rate`, {
                        defaultValue: '可用 {{value}}',
                        value: formatRate(hovered.success_rate),
                      })}`
                    : ''}
                </span>
                {isAdmin && hovered.request_count > 0 ? (
                  <span className='chealth-tip__note'>
                    {t(`${ns}.tip.samples`, {
                      defaultValue: '{{source}} · {{count}} 次',
                      source: sourceLabel(hovered.source),
                      count: hovered.request_count,
                    })}
                  </span>
                ) : null}
              </div>
            ) : null}
          </div>

          {/* A lane with no figures still keeps the line, so every lane block
          is the same height and a card's size says how many lanes it has. */}
          <span className='chealth-lane__figures' title={figureHint(kind)}>
            {parts.length
              ? parts.map((part, index) => (
                  <span
                    className={part.warn ? 'chealth-tone--warn' : undefined}
                    key={part.text}
                  >
                    {index ? ' · ' : ''}
                    {part.text}
                  </span>
                ))
              : '—'}
          </span>
        </>
      ) : (
        /* A week with no record is said in words — never a row of empty
           cells (the health page shows no blank grid to customers). */
        <span className='chealth-lane__norecord'>
          {t(`${ns}.board.noRecord`, {
            defaultValue: '最近 7 天没有请求，有请求后开始记录。',
          })}
        </span>
      )}
    </div>
  );
});

export const StatusBoard = memo(function StatusBoard({
  rows,
  allRows,
  sections,
  bucketSeconds,
  windowSeconds,
  isAdmin,
  laneLabel,
  kindLabel,
  sourceLabel,
}: StatusBoardProps) {
  const { t, i18n } = useTranslation();

  const windowDays = Math.max(1, Math.round(windowSeconds / 86400));
  // Filtering changes what is visible, never what lanes a model offers.
  const modelLanes = useMemo(() => {
    const result = new Map<string, Set<string>>();
    allRows.forEach((row) => {
      const key = `${rowKind(row)}::${row.model_name}`;
      const lanes = result.get(key) ?? new Set<string>();
      lanes.add(row.lane_slug || row.lane);
      result.set(key, lanes);
    });
    return result;
  }, [allRows]);

  const statusWord = (status: ChannelHealthStatus) =>
    t(`${ns}.statusWord.${status}`, {
      defaultValue: {
        operational: '正常',
        degraded: '降级',
        outage: '中断',
        unknown: '暂无数据',
      }[status],
    });

  // Construct formatters once per language, shared by every row.
  const formatters = useMemo(() => {
    const locale = i18n.language || undefined;
    return {
      day: new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }),
      time: new Intl.DateTimeFormat(locale, {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }),
    };
  }, [i18n.language]);
  const tipWhen = useMemo(() => {
    const labels = new Map<string, string>();
    return (bucket: ChannelHealthBucket, seconds: number) => {
      if (!bucket.start_at) return '';
      const key = `${bucket.start_at}:${seconds}`;
      const cached = labels.get(key);
      if (cached) return cached;
      const start = new Date(bucket.start_at);
      const end = new Date(start.getTime() + seconds * 1000);
      const label = `${formatters.day.format(start)} ${formatters.time.format(start)} – ${formatters.time.format(end)}`;
      labels.set(key, label);
      return label;
    };
  }, [formatters]);

  // Rows arrive in the catalog's display order (the operator's maker order,
  // newest release first, stable before eco), so grouping keeps it as sent.
  const sectionEntries = useMemo(() => {
    return sections
      .map((section) => {
        const kindRows = rows.filter((row) => rowKind(row) === section.kind);
        if (section.kind !== 'llm') {
          return { section, groups: [{ family: '', rows: kindRows }] };
        }
        return {
          section,
          groups: groupByMaker(
            kindRows,
            (row) => makerOf(row.maker, { id: row.model_name }),
            (row) => row.maker_rank,
          ).map(({ maker, rows: makerRows }) => ({
            family: makerLabel(maker, t),
            rows: makerRows,
          })),
        };
      })
      .filter((entry) => entry.groups.some((group) => group.rows.length));
  }, [rows, sections, t]);

  const sliceHours = (seconds: number) =>
    Math.round((seconds / 3600) * 10) / 10;

  const legend = (
    <span className='chealth-board-legend'>
      {(
        [
          'ok',
          'warn',
          'bad',
          'none',
          ...(isAdmin ? ['blocked' as const] : []),
        ] as const
      ).map((tone) => (
        <span key={tone}>
          <i className={`chealth-bar chealth-bar--${tone}`} />
          {t(`${ns}.boardLegend.${tone}`, {
            defaultValue: {
              ok: '正常',
              warn: '部分失败',
              bad: '中断',
              none: '暂无数据',
              blocked: '无法抽测',
            }[tone],
          })}
        </span>
      ))}
    </span>
  );

  /** A model's lanes, in reading order. Rows arrive one per lane; a card
   *  holds every lane the model has, and most models have only one. */
  const byModel = (entries: ChannelHealthRow[]) => {
    const models = new Map<string, ChannelHealthRow[]>();
    entries.forEach((row) => {
      models.set(row.model_name, [...(models.get(row.model_name) ?? []), row]);
    });
    // Within a card the stable lanes read first, the agent lane last.
    return [...models.entries()].map(([name, lanes]) => ({
      name,
      lanes: [...lanes].sort(
        (a, b) => Number(a.lane === 'eco') - Number(b.lane === 'eco'),
      ),
    }));
  };

  const renderCard = (
    model: { name: string; lanes: ChannelHealthRow[] },
    section: ChannelHealthKindSection,
    family?: string,
  ) => {
    const two =
      (modelLanes.get(`${section.kind}::${model.name}`)?.size ?? 0) > 1;
    const onlyEco =
      section.kind === 'llm' && !two && model.lanes[0]?.lane === 'eco';
    // Two lanes show as two labelled blocks; only a discount-only model has
    // something the blocks cannot say.
    const tag = onlyEco
      ? t(`${ns}.board.onlyEco`, { defaultValue: '仅 Agent 专线' })
      : '';
    return (
      <article
        className='chealth-mcard'
        data-kind={section.kind}
        key={`${section.kind}::${model.name}`}
      >
        <div className='chealth-mcard__head'>
          <span
            className={`chealth-mcard__name${section.kind === 'search' ? ' chealth-mcard__name--plain' : ''}`}
            title={model.name}
          >
            {model.name}
          </span>
          {family ? (
            <span className='chealth-mcard__maker'>{family}</span>
          ) : null}
          {tag ? <span className='chealth-mcard__tag'>{tag}</span> : null}
        </div>
        {model.lanes.map((row, index) => (
          <StatusLane
            key={`${section.kind}::${row.model_name}::${row.lane_slug || `${row.lane}::${index}`}`}
            row={row}
            label={
              two ||
              (isAdmin &&
                row.lane_slug &&
                !['direct', 'cheap'].includes(row.lane_slug))
                ? displayLane(row, laneLabel)
                : ''
            }
            section={section}
            windowDays={windowDays}
            isAdmin={isAdmin}
            sourceLabel={sourceLabel}
            tipWhen={tipWhen}
          />
        ))}
      </article>
    );
  };

  const modelCount = (entries: ChannelHealthRow[]) =>
    new Set(entries.map((row) => row.model_name)).size;

  const sectionCount = (
    section: ChannelHealthKindSection,
    entries: ChannelHealthRow[],
  ) => {
    const parts = [
      section.kind === 'search'
        ? t(`${ns}.board.sourceCount`, {
            defaultValue: '{{count}} 个来源',
            count: entries.length,
          })
        : t(`${ns}.board.modelCount`, {
            defaultValue: '{{count}} 个模型',
            count: modelCount(entries),
          }),
    ];
    if (section.kind === 'llm') {
      parts.push(
        t(`${ns}.board.laneCount`, {
          defaultValue: '{{count}} 条线路',
          count: entries.length,
        }),
      );
    }
    if (section.bucket_seconds !== bucketSeconds) {
      parts.push(
        t(`${ns}.board.slice`, {
          defaultValue: '每格 {{hours}} 小时',
          hours: sliceHours(section.bucket_seconds),
        }),
      );
    }
    return parts.join(' · ');
  };

  return (
    <div className='chealth-board'>
      {sectionEntries.map(({ section, groups }, sectionIndex) => {
        const entries = groups.flatMap((group) => group.rows);
        return (
          <section
            aria-label={kindLabel(section.kind)}
            className='chealth-bsection'
            data-kind={section.kind}
            key={section.kind}
          >
            <div className='chealth-bsection__head'>
              <h2 className='chealth-bsection__title'>
                {kindLabel(section.kind)}
              </h2>
              <span className='chealth-bsection__count'>
                {sectionCount(section, entries)}
              </span>
              {sectionIndex === 0 ? legend : null}
            </div>

            {/* One grid per kind. A sub-heading per maker left most rows
                holding a single card beside two empty slots; the maker now
                rides on each card, top right, and the banner above carries
                what used to be each maker's verdict. */}
            <div className='chealth-mgrid'>
              {groups.flatMap(({ family, rows: groupRows }) =>
                byModel(groupRows).map((model) =>
                  renderCard(model, section, family || undefined),
                ),
              )}
            </div>
          </section>
        );
      })}
    </div>
  );
});
