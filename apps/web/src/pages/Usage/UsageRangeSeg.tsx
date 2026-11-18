/* 用量与日志's time range, in the page head and shared by its three views:
 * 24 小时 / 7 天 / 30 天 / 90 天 / 自定义. A custom range shows its dates in
 * place of 自定义 (only the day count on phones) and leaves the presets
 * unselected; picking one returns to it. ← → move between the five. */
import { type KeyboardEvent, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import {
  CALENDAR_PRESET_IDS,
  InteractiveTimePicker,
} from '../../components/dashboard/time-picker-v3';
import {
  USAGE_RANGE_PRESETS,
  type UsageRangePreset,
  useUsageRange,
} from '../../hooks/usage-logs/usageRange';
import './usage-range.css';

const SK = 'page.console.log.session';
const MOBILE_768_QUERY = '(max-width: 768px)';

export function useMobile768() {
  return useSyncExternalStore(
    (callback) => {
      if (
        typeof window === 'undefined' ||
        typeof window.matchMedia !== 'function'
      ) {
        return () => {};
      }
      const mql = window.matchMedia(MOBILE_768_QUERY);
      mql.addEventListener('change', callback);
      return () => mql.removeEventListener('change', callback);
    },
    () =>
      typeof window !== 'undefined' &&
      typeof window.matchMedia === 'function' &&
      window.matchMedia(MOBILE_768_QUERY).matches,
    () => false,
  );
}

function usageRangeLabel(
  preset: UsageRangePreset,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  return t(`${SK}.range.${preset}`);
}

function moveFocus(event: KeyboardEvent<HTMLElement>) {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
  const buttons = [
    ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
      ':scope > button, :scope > .dashv3-tp-root > button',
    ),
  ];
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  if (index < 0) return;
  event.preventDefault();
  const step = event.key === 'ArrowRight' ? 1 : -1;
  buttons[(index + step + buttons.length) % buttons.length]?.focus();
}

export function UsageRangeSeg({
  calendar = true,
  className,
  compact = false,
}: {
  /* The phone filter sheet puts the calendar on its own row. */
  calendar?: boolean;
  className?: string;
  /* Phone inline switcher keeps concise 24h / 7d / 30d / 自定义 pills. */
  compact?: boolean;
}) {
  const { t } = useTranslation();
  const narrow = useMobile768();
  const { range, setPicker, setPreset } = useUsageRange();
  const custom = range.preset === 'custom';
  const presets: readonly UsageRangePreset[] = compact
    ? range.preset === '90d'
      ? ['24h', '7d', '30d', '90d']
      : ['24h', '7d', '30d']
    : USAGE_RANGE_PRESETS;
  return (
    <span
      aria-label={t('page.console.usage.range.label')}
      className={['logv2-seg', 'usage-range', className]
        .filter(Boolean)
        .join(' ')}
      onKeyDown={moveFocus}
      role='toolbar'
    >
      {presets.map((preset) => (
        <button
          aria-pressed={range.preset === preset}
          className='logv2-seg__button'
          data-active={range.preset === preset ? 'true' : undefined}
          key={preset}
          onClick={() => setPreset(preset)}
          title={compact ? usageRangeLabel(preset, t) : undefined}
          type='button'
        >
          {compact ? preset : usageRangeLabel(preset, t)}
        </button>
      ))}
      {calendar ? (
        <InteractiveTimePicker
          align='start'
          compact
          compactLabel={
            <span className='usage-range__custom'>
              {!custom
                ? t('page.console.usage.range.customShort')
                : narrow || compact
                  ? t('page.console.usage.range.days', { count: range.days })
                  : range.label}
            </span>
          }
          onChange={setPicker}
          presets={CALENDAR_PRESET_IDS}
          value={range.picker}
        />
      ) : null}
    </span>
  );
}

/** 「自定义日期 · 未设置」: the calendar as a row of its own, for the sheet. */
export function UsageRangeCalendarRow() {
  const { t } = useTranslation();
  const { range, setPicker } = useUsageRange();
  const custom = range.preset === 'custom';
  return (
    <div className='usage-range-row'>
      <span>{t('page.console.usage.range.custom')}</span>
      <span className='usage-range usage-range--row'>
        <InteractiveTimePicker
          align='start'
          compact
          compactLabel={
            <span className='usage-range__custom'>
              {custom ? range.label : t('page.console.usage.range.unset')}
            </span>
          }
          onChange={setPicker}
          presets={CALENDAR_PRESET_IDS}
          value={range.picker}
        />
      </span>
    </div>
  );
}
