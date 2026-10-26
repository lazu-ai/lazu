import i18n from '../i18n/i18n';

const pad = (value: number) => String(value).padStart(2, '0');

// A local calendar date as YYYY-MM-DD.
const formatDate = (date: Date) =>
  `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;

// A Unix timestamp in seconds as local YYYY-MM-DD HH:mm:ss.
export function formatTimestamp(seconds: number): string {
  const date = new Date(seconds * 1000);
  return `${formatDate(date)} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

// How long ago a moment was, in the current language: "just now", minutes,
// hours, days, weeks, months, "a year ago", then the date itself. A future or
// unparseable moment reads as its date or its original text.
export function formatRelativeTime(
  moment: Date | number | string | undefined,
): string {
  if (!moment) return '';
  const then = new Date(moment);
  if (isNaN(then.getTime())) return String(moment);

  const diffMs = Date.now() - then.getTime();
  if (diffMs < 0) return formatDate(then);
  const seconds = Math.floor(diffMs / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  const weeks = Math.floor(days / 7);
  const months = Math.floor(days / 30);
  const years = Math.floor(days / 365);

  if (seconds < 60) return String(i18n.t('common.relativeTime.justNow'));
  if (minutes < 60)
    return String(i18n.t('common.relativeTime.minutesAgo', { count: minutes }));
  if (hours < 24)
    return String(i18n.t('common.relativeTime.hoursAgo', { count: hours }));
  if (days < 7)
    return String(i18n.t('common.relativeTime.daysAgo', { count: days }));
  if (weeks < 4)
    return String(i18n.t('common.relativeTime.weeksAgo', { count: weeks }));
  if (months < 12)
    return String(i18n.t('common.relativeTime.monthsAgo', { count: months }));
  if (years < 2) return String(i18n.t('common.relativeTime.oneYearAgo'));
  return formatDate(then);
}
