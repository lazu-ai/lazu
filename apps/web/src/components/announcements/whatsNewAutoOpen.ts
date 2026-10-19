// Throttle logic for the What's New dialog's automatic popup.
//
// The dialog should interrupt a user with an automatic popup at most once per
// calendar day. Whether we have already auto-opened today is tracked in
// localStorage, scoped per user so different accounts sharing one browser each
// get their own once-per-day popup. This is intentionally a per-browser,
// frontend-only concern; the unread indicator is driven separately and is
// never suppressed by this throttle.

export const WHATS_NEW_AUTO_OPEN_DAY_PREFIX = 'whats_new_last_auto_day';

const pad = (value: number): string => String(value).padStart(2, '0');

// Local-date key in YYYY-MM-DD form. Uses the browser's local calendar day so
// "once per day" matches what the user perceives as a day.
export const dayKeyFromDate = (date: Date): string =>
  `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;

export const todayDayKey = (): string => dayKeyFromDate(new Date());

// localStorage key scoped to a specific user, so one account's daily popup
// does not suppress another account's popup on a shared browser.
export const autoOpenStorageKey = (userKey: string | number): string =>
  `${WHATS_NEW_AUTO_OPEN_DAY_PREFIX}:${userKey}`;

// Auto-open only when there is something unseen and we have not already
// auto-opened during the current calendar day.
export const shouldAutoOpenToday = (
  lastShownDay: string | null,
  todayKey: string,
  hasUnseen: boolean,
): boolean => hasUnseen && lastShownDay !== todayKey;

export const readLastAutoOpenDay = (
  userKey: string | number,
): string | null => {
  try {
    if (typeof localStorage === 'undefined') return null;
    return localStorage.getItem(autoOpenStorageKey(userKey));
  } catch (_) {
    return null;
  }
};

export const markAutoOpenedToday = (userKey: string | number): void => {
  try {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(autoOpenStorageKey(userKey), todayDayKey());
  } catch (_) {
    // Ignore storage write failures; worst case the dialog auto-opens again.
  }
};
