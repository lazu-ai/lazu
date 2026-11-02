import type { TFunction } from 'i18next';

const DAY = 86_400;

/** "2 天 23 小时" / "18 小时" / "40 分钟" until a file is deleted. */
export const formatTaskRemaining = (
  expiresAt: number,
  nowSec: number,
  t: TFunction,
) => {
  const left = expiresAt - nowSec;
  if (left <= 0) return t('page.console.tasks.keep.gone');
  const days = Math.floor(left / DAY);
  const hours = Math.floor((left % DAY) / 3600);
  if (days > 0) return t('page.console.tasks.keep.days', { days, hours });
  if (hours > 0) return t('page.console.tasks.keep.hours', { hours });
  return t('page.console.tasks.keep.minutes', {
    minutes: Math.max(1, Math.floor(left / 60)),
  });
};
