export type DateRangeValue = Date | number | string | undefined;

export type UsageLogDateRange =
  | [DateRangeValue, DateRangeValue]
  | {
      exact?: boolean;
      from?: DateRangeValue;
      to?: DateRangeValue;
    };

// A copy, so rounding a range to whole days never rewrites the filter's own
// dates.
const toDate = (value: DateRangeValue) => {
  if (value instanceof Date) {
    return new Date(value.getTime());
  }
  if (value === undefined) {
    return new Date();
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? new Date() : date;
};

/**
 * The range in Unix seconds. A range that is not exact covers whole local
 * days, from the first day's midnight to the midnight after the last day.
 */
export const toUnixRange = (dateRange: UsageLogDateRange) => {
  const from = Array.isArray(dateRange) ? dateRange[0] : dateRange?.from;
  const to = Array.isArray(dateRange) ? dateRange[1] : dateRange?.to;
  const start = toDate(from);
  const end = toDate(to);
  if (Array.isArray(dateRange) || !dateRange?.exact) {
    start.setHours(0, 0, 0, 0);
    end.setHours(23, 59, 59, 999);
  }
  return {
    end: Math.ceil(end.getTime() / 1000),
    start: Math.floor(start.getTime() / 1000),
  };
};

/**
 * Today, as a whole local day. The range ends on today itself: a later
 * moment such as an hour from now falls on tomorrow after 23:00, and the
 * page would then read two days of logs.
 */
export const getDefaultUsageLogDateRange = (
  now: Date = new Date(),
): UsageLogDateRange => {
  const today = new Date(now.getTime());
  today.setHours(0, 0, 0, 0);
  return { from: today, to: new Date(today.getTime()) };
};
