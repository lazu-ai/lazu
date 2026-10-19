import { useId } from 'react';

/**
 * Lightweight SVG mini-charts ported from the Claude design prototype
 * (design/Lazu-Console-源码/lazu/lib.jsx) into the app's token system.
 * Use these for sparklines / donuts / health strips in the redesigned
 * console pages. All colors come from CSS vars so light + dark both work.
 */

const pathFrom = (values: number[], w: number, h: number, pad = 2) => {
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const rng = max - min || 1;
  const step = (w - pad * 2) / (values.length - 1 || 1);
  return values
    .map((v, i) => {
      const x = pad + i * step;
      const y = h - pad - ((v - min) / rng) * (h - pad * 2);
      return `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`;
    })
    .join(' ');
};

export function Sparkline({
  values,
  color = 'var(--ui-accent)',
  width = 96,
  height = 30,
  fill = true,
  strokeWidth = 1.8,
  className,
}: {
  values: number[];
  color?: string;
  width?: number;
  height?: number;
  fill?: boolean;
  strokeWidth?: number;
  className?: string;
}) {
  const id = useId();
  if (!values.length) return null;
  const line = pathFrom(values, width, height, 2);
  const area = `${line} L${width - 2} ${height - 2} L2 ${height - 2} Z`;
  return (
    <svg
      className={className}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      style={{ display: 'block', overflow: 'visible' }}
    >
      {fill ? (
        <>
          <defs>
            <linearGradient id={id} x1='0' y1='0' x2='0' y2='1'>
              <stop offset='0' stopColor={color} stopOpacity='.22' />
              <stop offset='1' stopColor={color} stopOpacity='0' />
            </linearGradient>
          </defs>
          <path d={area} fill={`url(#${id})`} />
        </>
      ) : null}
      <path
        d={line}
        fill='none'
        stroke={color}
        strokeWidth={strokeWidth}
        strokeLinecap='round'
        strokeLinejoin='round'
      />
    </svg>
  );
}

export function Donut({
  value,
  max = 100,
  size = 132,
  stroke = 14,
  color = 'var(--ui-accent)',
  track = 'var(--ui-bg-muted)',
  label,
  sub,
}: {
  value: number;
  max?: number;
  size?: number;
  stroke?: number;
  color?: string;
  track?: string;
  label?: React.ReactNode;
  sub?: React.ReactNode;
}) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const pct = Math.max(0, Math.min(1, value / (max || 1)));
  return (
    <div style={{ position: 'relative', width: size, height: size }}>
      <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }}>
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill='none'
          stroke={track}
          strokeWidth={stroke}
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill='none'
          stroke={color}
          strokeWidth={stroke}
          strokeDasharray={`${c * pct} ${c}`}
          strokeLinecap='round'
          style={{ transition: 'stroke-dasharray .6s ease' }}
        />
      </svg>
      <div
        style={{
          position: 'absolute',
          inset: 0,
          display: 'grid',
          placeContent: 'center',
          textAlign: 'center',
        }}
      >
        <div
          className='tabular-nums'
          style={{ fontSize: 22, fontWeight: 700, color: 'var(--ui-text)' }}
        >
          {label}
        </div>
        {sub ? (
          <div
            style={{
              fontSize: 11,
              color: 'var(--ui-text-tertiary)',
              marginTop: 2,
            }}
          >
            {sub}
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** 24-slot health heat strip; value 0=ok 1=warn 2=down */
export function HeatStrip({
  data,
  height = 26,
}: {
  data: number[];
  height?: number;
}) {
  const col = (v: number) =>
    v === 0
      ? 'var(--ui-success)'
      : v === 1
        ? 'var(--ui-warning)'
        : 'var(--ui-danger)';
  return (
    <div style={{ display: 'flex', gap: 2 }}>
      {data.map((v, i) => (
        <div
          key={i}
          title={`${i}:00`}
          style={{
            flex: 1,
            height,
            borderRadius: 2,
            background: col(v),
            opacity: v === 0 ? 0.5 : 0.9,
          }}
        />
      ))}
    </div>
  );
}

/** simple horizontal distribution bar (e.g. rating 5★…1★ breakdown) */
export function DistBar({
  value,
  total,
  color = 'var(--ui-accent)',
}: {
  value: number;
  total: number;
  color?: string;
}) {
  const pct = total > 0 ? Math.round((value / total) * 100) : 0;
  return (
    <div
      style={{
        height: 8,
        borderRadius: 999,
        background: 'var(--ui-bg-muted)',
        overflow: 'hidden',
      }}
    >
      <div
        style={{
          width: `${pct}%`,
          height: '100%',
          borderRadius: 999,
          background: color,
          transition: 'width .5s ease',
        }}
      />
    </div>
  );
}
