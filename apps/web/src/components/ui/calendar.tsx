/* The calendar itself — and with it react-day-picker and the date-fns locales
 * it drags along, about 310KB of source.
 *
 * It used to sit in the entry bundle: the picker imported it at module scope,
 * a legacy console primitive imported the picker, and an eagerly loaded helper
 * imported that — so the landing page downloaded a date picker it has no way to
 * show. Nothing here is needed until a popover actually opens, so this module
 * is loaded then (or a moment earlier, when the trigger is hovered).
 */
import { DayPicker } from 'react-day-picker';
import { enGB, fr, ru, zhCN } from 'react-day-picker/locale';
import type { DateRange } from 'react-day-picker';

export type CalendarLocale = typeof enGB;
export type CalendarDisabled = React.ComponentProps<
  typeof DayPicker
>['disabled'];

const dayPickerClassNames = {
  root: 'lazu-calendar',
  months: 'lazu-calendar__months',
  month: 'lazu-calendar__month',
  caption_label: 'lazu-calendar__caption',
  nav: 'lazu-calendar__nav',
  button_previous: 'lazu-calendar__nav-button',
  button_next: 'lazu-calendar__nav-button',
  chevron: 'lazu-calendar__chevron',
  month_caption: 'lazu-calendar__month-caption',
  month_grid: 'lazu-calendar__grid',
  weekdays: 'lazu-calendar__weekdays',
  weekday: 'lazu-calendar__weekday',
  weeks: 'lazu-calendar__weeks',
  week: 'lazu-calendar__week',
  day: 'lazu-calendar__day',
  day_button: 'lazu-calendar__day-button',
  today: 'lazu-calendar__day--today',
  selected: 'lazu-calendar__day--selected',
  range_start: 'lazu-calendar__day--range-start',
  range_middle: 'lazu-calendar__day--range-middle',
  range_end: 'lazu-calendar__day--range-end',
  outside: 'lazu-calendar__day--outside',
  disabled: 'lazu-calendar__day--disabled',
  hidden: 'lazu-calendar__day--hidden',
};

const localeMap: Record<string, CalendarLocale> = {
  en: enGB,
  fr,
  ru,
  zh: zhCN,
};

/* The picker's public `locale` prop takes either a locale object or a language
 * tag; resolving it needs the locale objects, so it resolves in here. */
const resolveLocale = (
  locale: CalendarLocale | string | undefined,
  language: string,
): CalendarLocale => {
  if (locale && typeof locale === 'object') return locale;
  const key = String(locale || language || 'en')
    .toLowerCase()
    .split('-')[0];
  return localeMap[key] || enGB;
};

type CommonProps = {
  disabledDays?: CalendarDisabled;
  language: string;
  locale?: CalendarLocale | string;
};

export function Calendar({
  disabledDays,
  language,
  locale,
  onSelect,
  selected,
}: CommonProps & {
  onSelect: (value?: Date) => void;
  selected?: Date;
}) {
  return (
    <DayPicker
      classNames={dayPickerClassNames}
      disabled={disabledDays}
      locale={resolveLocale(locale, language)}
      mode='single'
      onSelect={onSelect}
      selected={selected}
    />
  );
}

export function RangeCalendar({
  disabledDays,
  language,
  locale,
  numberOfMonths = 2,
  onSelect,
  selected,
}: CommonProps & {
  numberOfMonths?: number;
  onSelect: (value?: DateRange) => void;
  selected?: DateRange;
}) {
  return (
    <DayPicker
      classNames={dayPickerClassNames}
      disabled={disabledDays}
      locale={resolveLocale(locale, language)}
      mode='range'
      numberOfMonths={numberOfMonths}
      onSelect={onSelect}
      selected={selected}
    />
  );
}
