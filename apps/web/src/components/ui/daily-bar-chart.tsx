import type { ReactNode } from 'react';

type DailyBarChartProps = {
  data: Array<{ label: string; value: number }>;
  label: string;
  formatValue: (value: number) => string;
  empty?: ReactNode;
};

/** Daily totals use discrete bars, as selected in the overview reference. */
export function DailyBarChart({
  data,
  label,
  formatValue,
  empty,
}: DailyBarChartProps) {
  const maximum = Math.max(0, ...data.map((point) => point.value));
  if (!data.length)
    return <div className='lazu-daily-bars__empty'>{empty}</div>;
  return (
    <figure className='lazu-daily-bars' aria-label={label}>
      <div className='lazu-daily-bars__plot' aria-hidden='true'>
        {data.map((point, index) => (
          <div
            key={`${point.label}-${index}`}
            className='lazu-daily-bars__slot'
          >
            <div
              className='lazu-daily-bars__bar'
              title={`${point.label}: ${formatValue(point.value)}`}
              data-latest={index === data.length - 1 || undefined}
              style={{
                height: `${maximum ? (Math.max(0, point.value) / maximum) * 88 : 0}%`,
              }}
            />
          </div>
        ))}
      </div>
      <div className='lazu-daily-bars__labels' aria-hidden='true'>
        {data.map((point, index) => (
          <span key={`${point.label}-${index}`}>
            {index === 0 ||
            index === data.length - 1 ||
            index === Math.floor(data.length / 2)
              ? point.label
              : ''}
          </span>
        ))}
      </div>
      <ul className='sr-only' aria-label={label}>
        {data.map((point, index) => (
          <li key={`${point.label}-${index}`}>
            {point.label}: {formatValue(point.value)}
          </li>
        ))}
      </ul>
    </figure>
  );
}
