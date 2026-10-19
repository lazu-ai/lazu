import React, { useEffect, useMemo, useRef, useState } from 'react';
import { cn } from '../../lib/cn';

/**
 * Bars + line on one time axis — the console's locked chart (墨 + 柱与线,
 * brand/foundations/yunmu-console.md). Volume draws as thin bars on the left
 * axis; a rate or an amount rides above it as a smooth line on the right
 * axis. The bucket still in progress is hatched, failures are the only red.
 */

export type ComboTone = 'ink' | 'grey' | 'pale' | 'danger';

export type ComboBarSeries = {
  key: string;
  label: string;
  values: number[];
  tone: ComboTone;
};

export type ComboLineSeries = {
  key: string;
  label: string;
  values: Array<number | null>;
  tone: ComboTone;
  dashed?: boolean;
  /** Light area under the line. */
  area?: boolean;
};

export type ComboAxis = {
  format: (value: number) => string;
  /** Fixed domain; otherwise [0, nice(max)]. */
  min?: number;
  max?: number;
};

export type ComboTooltipRow = {
  label: string;
  value: string;
  tone: ComboTone;
  alert?: boolean;
};

export type ComboChartProps = {
  labels: string[];
  titles: string[];
  partial?: boolean[];
  bars?: ComboBarSeries[];
  lines?: ComboLineSeries[];
  left: ComboAxis;
  right?: ComboAxis;
  /** A dashed target on the right axis, e.g. 99% success. */
  threshold?: { value: number; label: string };
  /** Points on the first line drawn as red dots (e.g. below target). */
  alertPoints?: boolean[];
  height?: number;
  tooltip: (index: number) => ComboTooltipRow[];
  /** Legend entries beyond the series (e.g. the target line). */
  extraLegend?: Array<{
    label: string;
    tone: ComboTone;
    kind: 'bar' | 'line' | 'dash';
  }>;
  /** Bucket the legend reads out before the pointer moves (defaults to the
   *  last full one). The floating tip itself only appears on hover, so it
   *  never covers a third of the plot on a page nobody has touched. */
  defaultIndex?: number;
  className?: string;
  ariaLabel: string;
};

const PAD_LEFT = 48;
const PAD_TOP = 14;
const PAD_BOTTOM = 26;

function niceMax(value: number): number {
  if (!(value > 0)) return 1;
  const exponent = 10 ** Math.floor(Math.log10(value));
  const mantissa = value / exponent;
  const step =
    mantissa <= 1
      ? 1
      : mantissa <= 2
        ? 2
        : mantissa <= 2.5
          ? 2.5
          : mantissa <= 5
            ? 5
            : 10;
  return step * exponent;
}

/** Monotone cubic (Fritsch–Carlson): smooth, but never overshoots the data,
 *  so a rate line cannot bulge past 100% or dip below a real minimum. */
function smoothPath(points: Array<[number, number]>): string {
  const n = points.length;
  if (!n) return '';
  const fmt = (p: number) => p.toFixed(1);
  let d = `M${fmt(points[0][0])},${fmt(points[0][1])}`;
  if (n === 1) return d;
  const slopes: number[] = [];
  for (let i = 0; i < n - 1; i += 1) {
    const dx = points[i + 1][0] - points[i][0];
    slopes.push(dx === 0 ? 0 : (points[i + 1][1] - points[i][1]) / dx);
  }
  const tangents = points.map((_, i) => {
    if (i === 0) return slopes[0];
    if (i === n - 1) return slopes[n - 2];
    const a = slopes[i - 1];
    const b = slopes[i];
    return a * b <= 0 ? 0 : (a + b) / 2;
  });
  for (let i = 0; i < n - 1; i += 1) {
    const slope = slopes[i];
    if (slope === 0) {
      tangents[i] = 0;
      tangents[i + 1] = 0;
      continue;
    }
    const a = tangents[i] / slope;
    const b = tangents[i + 1] / slope;
    const h = a * a + b * b;
    if (h > 9) {
      const t = 3 / Math.sqrt(h);
      tangents[i] = t * a * slope;
      tangents[i + 1] = t * b * slope;
    }
  }
  for (let i = 0; i < n - 1; i += 1) {
    const [x0, y0] = points[i];
    const [x1, y1] = points[i + 1];
    const dx = (x1 - x0) / 3;
    d += `C${fmt(x0 + dx)},${fmt(y0 + tangents[i] * dx)} ${fmt(x1 - dx)},${fmt(y1 - tangents[i + 1] * dx)} ${fmt(x1)},${fmt(y1)}`;
  }
  return d;
}

/** Segments of consecutive non-null points, each drawn as its own path. */
function lineSegments(
  values: Array<number | null>,
  x: (i: number) => number,
  y: (v: number) => number,
) {
  const segments: Array<Array<[number, number]>> = [];
  let current: Array<[number, number]> = [];
  values.forEach((value, index) => {
    if (value === null || !Number.isFinite(value)) {
      if (current.length) segments.push(current);
      current = [];
      return;
    }
    current.push([x(index), y(value)]);
  });
  if (current.length) segments.push(current);
  return segments;
}

function roundedBar(
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
): string {
  if (h <= 0.4) return '';
  const radius = Math.min(r, w / 2, h);
  return `M${x},${y + h}V${y + radius}Q${x},${y} ${x + radius},${y}H${x + w - radius}Q${x + w},${y} ${x + w},${y + radius}V${y + h}Z`;
}

export function ComboChart({
  labels,
  titles,
  partial = [],
  bars = [],
  lines = [],
  left,
  right,
  threshold,
  alertPoints = [],
  height = 280,
  tooltip,
  extraLegend = [],
  defaultIndex,
  className,
  ariaLabel,
}: ComboChartProps) {
  const frameRef = useRef<HTMLDivElement>(null);
  const legendRef = useRef<HTMLElement>(null);
  const [width, setWidth] = useState(0);
  const [legendHeight, setLegendHeight] = useState(24);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    const node = frameRef.current;
    if (!node) return undefined;
    const update = () => {
      setWidth(node.clientWidth);
      setLegendHeight(Math.max(24, legendRef.current?.clientHeight ?? 24));
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(node);
    if (legendRef.current) observer.observe(legendRef.current);
    return () => observer.disconnect();
  }, []);

  const n = labels.length;
  const padRight = right ? 50 : 12;
  const plotHeight = Math.max(80, height - legendHeight);
  const plotWidth = Math.max(0, width - PAD_LEFT - padRight);
  const slot = n > 0 ? plotWidth / n : 0;
  const base = plotHeight - PAD_BOTTOM;
  const top = PAD_TOP;
  const span = base - top;

  const geometry = useMemo(() => {
    const stacked = labels.map((_, i) =>
      bars.reduce((sum, series) => sum + (series.values[i] || 0), 0),
    );
    const lineValues = lines.flatMap((line) =>
      line.values.filter((v): v is number => v !== null && Number.isFinite(v)),
    );
    const leftMax =
      left.max ??
      niceMax(
        Math.max(
          0,
          ...stacked,
          ...(bars.length === 0 && !right ? lineValues : []),
        ),
      );
    const leftMin = left.min ?? 0;
    const rightMax =
      right?.max ?? niceMax(Math.max(0, ...lineValues, threshold?.value ?? 0));
    const rightMin = right?.min ?? 0;
    const yLeft = (v: number) =>
      base - ((v - leftMin) / (leftMax - leftMin || 1)) * span;
    const yRight = (v: number) =>
      base - ((v - rightMin) / (rightMax - rightMin || 1)) * span;
    const x = (i: number) => PAD_LEFT + slot * (i + 0.5);
    const barWidth = Math.max(3, Math.min(slot * 0.46, 20));

    const barPaths = bars.map((series) => ({ tone: series.tone, d: '' }));
    let partialPath = '';
    labels.forEach((_, i) => {
      let acc = 0;
      const total = stacked[i];
      if (partial[i]) {
        const h = ((total - leftMin) / (leftMax - leftMin || 1)) * span;
        partialPath += roundedBar(
          x(i) - barWidth / 2,
          base - h,
          barWidth,
          h,
          3,
        );
        return;
      }
      bars.forEach((series, s) => {
        const value = series.values[i] || 0;
        const h = (value / (leftMax - leftMin || 1)) * span;
        const isTop = s === bars.length - 1;
        const y0 = base - ((acc + value) / (leftMax - leftMin || 1)) * span;
        barPaths[s].d += isTop
          ? roundedBar(x(i) - barWidth / 2, y0, barWidth, h, 3)
          : h > 0.4
            ? `M${x(i) - barWidth / 2},${y0 + h}V${y0}H${x(i) + barWidth / 2}V${y0 + h}Z`
            : '';
        acc += value;
      });
    });

    const y = right ? yRight : yLeft;
    const linePaths = lines.map((line) => {
      const segments = lineSegments(line.values, x, y);
      return {
        tone: line.tone,
        dashed: line.dashed,
        d: segments.map(smoothPath).join(''),
        area: line.area
          ? segments
              .map(
                (segment) =>
                  `${smoothPath(segment)}L${segment[segment.length - 1][0].toFixed(1)},${base}L${segment[0][0].toFixed(1)},${base}Z`,
              )
              .join('')
          : '',
      };
    });
    const alerts = lines[0]
      ? lines[0].values
          .map((value, i) =>
            alertPoints[i] && value !== null ? [x(i), y(value)] : null,
          )
          .filter((p): p is [number, number] => Boolean(p))
      : [];
    const lastFull = partial.length && partial[n - 1] ? n - 2 : n - 1;
    return {
      barPaths,
      partialPath,
      linePaths,
      alerts,
      x,
      leftTicks: [leftMin, (leftMin + leftMax) / 2, leftMax],
      rightTicks: right ? [rightMin, (rightMin + rightMax) / 2, rightMax] : [],
      thresholdY: threshold ? y(threshold.value) : null,
      lastFull: Math.max(0, lastFull),
    };
  }, [
    labels,
    bars,
    lines,
    left,
    right,
    threshold,
    alertPoints,
    partial,
    base,
    span,
    slot,
    n,
  ]);

  const hovering = hover !== null;
  const active = hover ?? defaultIndex ?? geometry.lastFull;
  const every = n <= 13 ? 1 : n <= 24 ? 3 : 5;
  const tooltipRows = n > 0 ? tooltip(active) : [];
  const readoutIndex = defaultIndex ?? geometry.lastFull;
  const readout =
    n > 0
      ? [
          titles[readoutIndex],
          ...tooltip(readoutIndex)
            .slice(0, 3)
            .map((row) => `${row.label} ${row.value}`),
        ]
          .filter(Boolean)
          .join(' · ')
      : '';
  const tipX =
    geometry.x(active) + 16 + 176 > width
      ? geometry.x(active) - 16 - 176
      : geometry.x(active) + 16;

  const legend = [
    ...bars.map((s) => ({
      label: s.label,
      tone: s.tone,
      kind: 'bar' as const,
    })),
    ...lines.map((l) => ({
      label: l.label,
      tone: l.tone,
      kind: l.dashed ? ('dash' as const) : ('line' as const),
    })),
    ...extraLegend,
  ];

  return (
    <figure
      className={cn('lz-combo', className)}
      aria-label={ariaLabel}
      style={{ height }}
    >
      <figcaption ref={legendRef} className='lz-combo__legend'>
        {readout ? (
          <span className='lz-combo__readout' aria-hidden='true'>
            {readout}
          </span>
        ) : null}
        {legend.map((item) => (
          <span key={`${item.kind}-${item.label}`} className='lz-combo__key'>
            <span
              className='lz-combo__swatch'
              data-kind={item.kind}
              data-tone={item.tone}
            />
            {item.label}
          </span>
        ))}
      </figcaption>
      {/* The plot is drawn for the eye; this is the same data for everyone
          else: one item per bucket with the values its tooltip would show. */}
      <ul className='sr-only'>
        {titles.map((title, index) => (
          <li key={`${title}-${index}`}>
            {[
              title,
              ...tooltip(index).map((row) => `${row.label} ${row.value}`),
            ].join(' · ')}
          </li>
        ))}
      </ul>
      <div
        ref={frameRef}
        className='lz-combo__frame'
        style={{ height: plotHeight }}
        onMouseLeave={() => setHover(null)}
      >
        {width > 0 && n > 0 ? (
          <>
            <svg
              width={width}
              height={plotHeight}
              className='lz-combo__svg'
              aria-hidden='true'
            >
              {hovering ? (
                <path
                  className='lz-combo__hover'
                  d={`M${(geometry.x(active) - slot / 2 + 1).toFixed(1)},${top}h${Math.max(0, slot - 2).toFixed(1)}V${base}h${(-Math.max(0, slot - 2)).toFixed(1)}Z`}
                />
              ) : null}
              <path
                className='lz-combo__grid'
                d={[top, (top + base) / 2]
                  .map(
                    (gy) => `M${PAD_LEFT},${gy.toFixed(1)}H${width - padRight}`,
                  )
                  .join('')}
              />
              <path
                className='lz-combo__axis'
                d={`M${PAD_LEFT},${base}H${width - padRight}`}
              />
              {geometry.linePaths.map((line, i) =>
                line.area ? (
                  <path
                    key={`area-${i}`}
                    className='lz-combo__area'
                    data-tone={line.tone}
                    d={line.area}
                  />
                ) : null,
              )}
              {geometry.barPaths.map((bar, i) => (
                <path
                  key={`bar-${i}`}
                  className='lz-combo__bar'
                  data-tone={bar.tone}
                  d={bar.d}
                />
              ))}
              <path
                className='lz-combo__bar lz-combo__bar--partial'
                d={geometry.partialPath}
              />
              {geometry.thresholdY !== null ? (
                <path
                  className='lz-combo__threshold'
                  d={`M${PAD_LEFT},${geometry.thresholdY.toFixed(1)}H${width - padRight}`}
                />
              ) : null}
              {geometry.linePaths.map((line, i) => (
                <path
                  key={`line-${i}`}
                  className='lz-combo__line'
                  data-tone={line.tone}
                  data-dashed={line.dashed || undefined}
                  d={line.d}
                />
              ))}
              {geometry.alerts.map(([ax, ay]) => (
                <circle
                  key={`${ax}-${ay}`}
                  className='lz-combo__alert'
                  cx={ax}
                  cy={ay}
                  r={4}
                />
              ))}
            </svg>
            {geometry.leftTicks.map((tick, i) => (
              <span
                key={`l-${i}`}
                className='lz-combo__tick'
                style={{ left: 0, top: base - (i / 2) * span }}
              >
                {left.format(tick)}
              </span>
            ))}
            {right
              ? geometry.rightTicks.map((tick, i) => (
                  <span
                    key={`r-${i}`}
                    className='lz-combo__tick lz-combo__tick--right'
                    style={{ left: width - 44, top: base - (i / 2) * span }}
                  >
                    {right.format(tick)}
                  </span>
                ))
              : null}
            {threshold && geometry.thresholdY !== null ? (
              <span
                className='lz-combo__threshold-label'
                style={{ left: PAD_LEFT + 6, top: geometry.thresholdY - 16 }}
              >
                {threshold.label}
              </span>
            ) : null}
            {labels.map((label, i) =>
              (n - 1 - i) % every === 0 ? (
                <span
                  key={`x-${i}`}
                  className='lz-combo__xlabel'
                  data-active={(hovering && i === active) || undefined}
                  style={{ left: geometry.x(i), top: plotHeight - 18 }}
                >
                  {label}
                </span>
              ) : null,
            )}
            {labels.map((_, i) => (
              <div
                key={`slot-${i}`}
                className='lz-combo__slot'
                style={{ left: PAD_LEFT + slot * i, width: slot }}
                onMouseEnter={() => setHover(i)}
              />
            ))}
            {hovering ? (
              <div
                className='lz-combo__tip'
                style={{ left: tipX }}
                role='status'
              >
                <strong>{titles[active]}</strong>
                {tooltipRows.map((row) => (
                  <span key={row.label} className='lz-combo__tip-row'>
                    <span className='lz-combo__tip-key'>
                      <i data-tone={row.tone} />
                      {row.label}
                    </span>
                    <span data-alert={row.alert || undefined}>{row.value}</span>
                  </span>
                ))}
              </div>
            ) : null}
          </>
        ) : null}
      </div>
    </figure>
  );
}

/** Wraps a render so React key churn on data changes never resets hover. */
export const MemoComboChart = React.memo(ComboChart);
