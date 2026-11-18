import { ChevronDown, Download } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  OVERVIEW_PRESETS,
  type OverviewPreset,
  type OverviewRange,
} from './overview-api';
import {
  createDefaultValue,
  InteractiveTimePicker,
  rangeToQueryWindow,
  type TimePickerValue,
} from '../../../components/dashboard/time-picker-v3';
import { Button } from '../../../components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../../components/ui/dropdown-menu';
import {
  formatCount,
  relativeChange,
} from '../../../components/ui/metric-format';
import { PendingValue } from '../../../components/ui/pending';

const ns = 'page.console.overview';

type HeaderProps = {
  title: string;
  range: OverviewRange;
  onRange: (range: OverviewRange) => void;
  onExport: (format: 'csv' | 'xlsx') => void;
  syncedLabel?: string;
  leading?: React.ReactNode;
  children?: React.ReactNode;
};

function rangeMetaLabel(
  range: OverviewRange,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  switch (range.preset) {
    case '24h':
      return t(`${ns}.range.meta.24h`, {
        defaultValue: '最近 24 小时 · 按小时',
      });
    case '7d':
      return t(`${ns}.range.meta.7d`, { defaultValue: '最近 7 天 · 按天' });
    case '30d':
      return t(`${ns}.range.meta.30d`, { defaultValue: '最近 30 天 · 按天' });
    case '90d':
      return t(`${ns}.range.meta.90d`, { defaultValue: '最近 90 天 · 按周' });
    default: {
      const format = new Intl.DateTimeFormat(undefined, {
        month: 'numeric',
        day: 'numeric',
      });
      return `${format.format(new Date(range.start * 1000))} – ${format.format(new Date(range.end * 1000))}`;
    }
  }
}

export function comparisonLabel(
  range: OverviewRange,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  switch (range.preset) {
    case '24h':
      return t(`${ns}.compare.24h`, { defaultValue: '较昨天同时段' });
    case '7d':
      return t(`${ns}.compare.7d`, { defaultValue: '较上 7 天' });
    case '30d':
      return t(`${ns}.compare.30d`, { defaultValue: '较上 30 天' });
    case '90d':
      return t(`${ns}.compare.90d`, { defaultValue: '较上 90 天' });
    default:
      return t(`${ns}.compare.custom`, { defaultValue: '较上一段同长时间' });
  }
}

export function OverviewHeader({
  title,
  range,
  onRange,
  onExport,
  syncedLabel,
  leading,
  children,
}: HeaderProps) {
  const { t } = useTranslation();
  const [pickerValue, setPickerValue] = React.useState<TimePickerValue | null>(
    null,
  );
  const presetLabel = (preset: OverviewPreset) =>
    ({
      '24h': t(`${ns}.range.24h`, { defaultValue: '24 小时' }),
      '7d': t(`${ns}.range.7d`, { defaultValue: '7 天' }),
      '30d': t(`${ns}.range.30d`, { defaultValue: '30 天' }),
      '90d': t(`${ns}.range.90d`, { defaultValue: '90 天' }),
    })[preset];
  const meta = [rangeMetaLabel(range, t), syncedLabel]
    .filter(Boolean)
    .join(' · ');
  return (
    <div className='ov-head'>
      <div className='ov-head__copy'>
        <h1 className='ov-title'>{title}</h1>
        <span className='ov-head__meta'>{meta}</span>
      </div>
      <div className='ov-head__actions'>
        {leading}
        <div
          className='ov-seg'
          role='group'
          aria-label={t(`${ns}.range.group`, { defaultValue: '时间范围' })}
        >
          {OVERVIEW_PRESETS.map((preset) => (
            <button
              key={preset}
              type='button'
              className='ov-seg__btn'
              aria-pressed={range.preset === preset}
              onClick={() => onRange({ preset })}
            >
              {presetLabel(preset)}
            </button>
          ))}
          <InteractiveTimePicker
            compact
            value={pickerValue ?? createDefaultValue()}
            onChange={(value) => {
              setPickerValue(value);
              const window = rangeToQueryWindow(value);
              onRange({
                preset: 'custom',
                start: window.startTimestamp,
                end: window.endTimestamp,
              });
            }}
          />
        </div>
        {children}
        <DropdownMenu>
          <DropdownMenuTrigger>
            <Button size='sm' variant='secondary'>
              <Download aria-hidden='true' size={14} />
              {t(`${ns}.export`, { defaultValue: '导出' })}
              <ChevronDown aria-hidden='true' size={13} />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end'>
            <DropdownMenuItem onSelect={() => onExport('csv')}>
              CSV
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onExport('xlsx')}>
              Excel
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}

export type NeedItem = { key: string; to: string; content: React.ReactNode };

/** One quiet line of things to act on; absent when there is nothing. */
export function NeedsLine({ items }: { items: NeedItem[] }) {
  const { t } = useTranslation();
  if (!items.length) return null;
  return (
    <div className='ov-panel ov-needs'>
      <span className='ov-needs__label'>
        {t(`${ns}.needs`, { defaultValue: '需要处理' })}
      </span>
      {items.map((item, index) => (
        <React.Fragment key={item.key}>
          {index > 0 ? (
            <span className='ov-needs__sep' aria-hidden='true' />
          ) : null}
          <Link to={item.to} className='ov-needs__item'>
            {item.content} →
          </Link>
        </React.Fragment>
      ))}
    </div>
  );
}

/** "▲ +12.4% 较昨天同时段" — or nothing when there is no baseline. */
export function Delta({
  current,
  previous,
  label,
  invert,
}: {
  current: number;
  previous: number;
  label: string;
  invert?: boolean;
}) {
  const change = relativeChange(current, previous);
  if (change === null) return <span className='ov-note'>{label}</span>;
  const up = change >= 0;
  const good = invert ? !up : up;
  return (
    <span className='ov-note'>
      <span className='ov-delta' data-dir={good ? 'up' : 'down'}>
        {change >= 9
          ? `▲ ${formatCount(current / previous)}×`
          : `${up ? '▲ +' : '▼ '}${(change * 100).toFixed(1)}%`}
      </span>{' '}
      {label}
    </span>
  );
}

export function Metric({
  pending,
  chars = 5,
  children,
}: {
  pending: boolean;
  chars?: number;
  children: React.ReactNode;
}) {
  return pending ? <PendingValue chars={chars} /> : <>{children}</>;
}
