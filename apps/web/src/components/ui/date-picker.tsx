import { CalendarDays, X } from 'lucide-react';
import React, { Suspense, lazy, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { IconButton } from './icon-button';
import { Popover, PopoverContent, PopoverTrigger } from './popover';
import { cn } from '../../lib/cn';
import type { CalendarDisabled, CalendarLocale } from './calendar';
import type { DateRange } from 'react-day-picker';

export type { CalendarLocale };

/* The grid, its locales and react-day-picker itself load when a popover opens
 * — or a moment earlier, when the trigger is hovered or focused. Until then a
 * date field costs nothing but its button. */
const loadCalendar = () => import('./calendar');

const Calendar = lazy(async () => ({
  default: (await loadCalendar()).Calendar,
}));

const RangeCalendar = lazy(async () => ({
  default: (await loadCalendar()).RangeCalendar,
}));

/* The grid's own footprint, held while it arrives so the popover opens at one
 * size instead of growing under the pointer. */
function CalendarFallback({ months = 1 }: { months?: number }) {
  return (
    <div
      aria-hidden='true'
      className='lazu-calendar-fallback'
      style={{ width: months * 284 }}
    />
  );
}
type DateValue = Date | number | string | null | undefined;
type DateRangeValue = DateRange | [DateValue, DateValue] | null | undefined;

type BasePickerProps = {
  className?: string;
  clearable?: boolean;
  disabled?: boolean;
  disabledDays?: CalendarDisabled;
  locale?: CalendarLocale | string;
  placeholder?: React.ReactNode;
};

type DatePickerProps = BasePickerProps & {
  defaultValue?: DateValue;
  format?: (date: Date) => React.ReactNode;
  onChange?: (value?: Date) => void;
  value?: DateValue;
};

type DateRangePickerProps = BasePickerProps & {
  defaultValue?: DateRangeValue;
  format?: (range: DateRange) => React.ReactNode;
  onChange?: (value?: DateRange) => void;
  value?: DateRangeValue;
};

const getDisplayLocale = (
  locale: CalendarLocale | string | undefined,
  language: string,
) => (typeof locale === 'string' ? locale : language);

const toDate = (value: DateValue): Date | undefined => {
  if (!value) return undefined;
  if (value instanceof Date) return value;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
};

const toRange = (value: DateRangeValue): DateRange | undefined => {
  if (!value) return undefined;
  if (Array.isArray(value)) {
    return { from: toDate(value[0]), to: toDate(value[1]) };
  }
  return { from: toDate(value.from), to: toDate(value.to) };
};

const formatDate = (date: Date | undefined, locale: string | undefined) =>
  date
    ? new Intl.DateTimeFormat(locale || undefined, {
        day: '2-digit',
        month: 'short',
        year: 'numeric',
      }).format(date)
    : '';

const formatRange = (range: DateRange | undefined, locale: string) => {
  if (!range?.from && !range?.to) return '';
  if (range.from && range.to) {
    return `${formatDate(range.from, locale)} - ${formatDate(range.to, locale)}`;
  }
  return formatDate(range.from || range.to, locale);
};

const CalendarTrigger = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement>
>(({ children, className, disabled, ...props }, ref) => (
  <button
    ref={ref}
    type='button'
    className={cn('lazu-date-trigger', className)}
    disabled={disabled}
    onFocus={() => void loadCalendar()}
    onPointerEnter={() => void loadCalendar()}
    {...props}
  >
    <CalendarDays aria-hidden='true' size={16} />
    <span>{children}</span>
  </button>
));

CalendarTrigger.displayName = 'CalendarTrigger';

export function DatePicker({
  className,
  clearable = true,
  defaultValue,
  disabled = false,
  disabledDays,
  format,
  locale,
  onChange,
  placeholder,
  value,
}: DatePickerProps) {
  const { i18n, t } = useTranslation();
  const isControlled = value !== undefined;
  const [internalValue, setInternalValue] = useState(() =>
    toDate(defaultValue),
  );
  const [open, setOpen] = useState(false);
  const selected = isControlled ? toDate(value) : internalValue;
  const activeLocale = getDisplayLocale(locale, i18n.language);
  const displayValue = useMemo(() => {
    if (!selected) return '';
    return format ? format(selected) : formatDate(selected, activeLocale);
  }, [activeLocale, format, selected]);

  const updateValue = (nextValue?: Date) => {
    if (!isControlled) {
      setInternalValue(nextValue);
    }
    onChange?.(nextValue);
  };

  return (
    <div className={cn('lazu-date-picker', className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger>
          <CalendarTrigger disabled={disabled}>
            {displayValue ||
              placeholder ||
              t('legacy.i18n.select_a_date.dc58f47982')}
          </CalendarTrigger>
        </PopoverTrigger>
        <PopoverContent align='start' className='lazu-calendar-popover'>
          <Suspense fallback={<CalendarFallback />}>
            <Calendar
              disabledDays={disabledDays}
              language={i18n.language}
              locale={locale}
              onSelect={(nextValue) => {
                updateValue(nextValue);
                if (nextValue) setOpen(false);
              }}
              selected={selected}
            />
          </Suspense>
          {clearable && selected ? (
            <div className='lazu-date-picker__footer'>
              <Button size='sm' variant='ghost' onClick={() => updateValue()}>
                {t('legacy.i18n.clear.7b15e5e8e7')}
              </Button>
            </div>
          ) : null}
        </PopoverContent>
      </Popover>
      {clearable && selected ? (
        <IconButton
          aria-label={t('legacy.i18n.clear.7b15e5e8e7')}
          className='lazu-date-picker__clear'
          disabled={disabled}
          onClick={() => updateValue()}
          size='sm'
          variant='ghost'
        >
          <X size={14} />
        </IconButton>
      ) : null}
    </div>
  );
}

export function DateRangePicker({
  className,
  clearable = true,
  defaultValue,
  disabled = false,
  disabledDays,
  format,
  locale,
  onChange,
  placeholder,
  value,
}: DateRangePickerProps) {
  const { i18n, t } = useTranslation();
  const isControlled = value !== undefined;
  const [internalValue, setInternalValue] = useState(() =>
    toRange(defaultValue),
  );
  const [open, setOpen] = useState(false);
  const selected = isControlled ? toRange(value) : internalValue;
  const activeLocale = getDisplayLocale(locale, i18n.language);
  const displayValue = useMemo(() => {
    if (!selected?.from && !selected?.to) return '';
    return format ? format(selected) : formatRange(selected, activeLocale);
  }, [activeLocale, format, selected]);

  const updateValue = (nextValue?: DateRange) => {
    if (!isControlled) {
      setInternalValue(nextValue);
    }
    onChange?.(nextValue);
  };

  return (
    <div className={cn('lazu-date-picker', className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger>
          <CalendarTrigger disabled={disabled}>
            {displayValue ||
              placeholder ||
              t('legacy.i18n.please_select_a_date_range.353ebab965')}
          </CalendarTrigger>
        </PopoverTrigger>
        <PopoverContent align='start' className='lazu-calendar-popover'>
          <Suspense fallback={<CalendarFallback months={2} />}>
            <RangeCalendar
              disabledDays={disabledDays}
              language={i18n.language}
              locale={locale}
              onSelect={updateValue}
              selected={selected}
            />
          </Suspense>
          {clearable && (selected?.from || selected?.to) ? (
            <div className='lazu-date-picker__footer'>
              <Button size='sm' variant='ghost' onClick={() => updateValue()}>
                {t('legacy.i18n.clear.7b15e5e8e7')}
              </Button>
            </div>
          ) : null}
        </PopoverContent>
      </Popover>
      {clearable && (selected?.from || selected?.to) ? (
        <IconButton
          aria-label={t('legacy.i18n.clear.7b15e5e8e7')}
          className='lazu-date-picker__clear'
          disabled={disabled}
          onClick={() => updateValue()}
          size='sm'
          variant='ghost'
        >
          <X size={14} />
        </IconButton>
      ) : null}
    </div>
  );
}
