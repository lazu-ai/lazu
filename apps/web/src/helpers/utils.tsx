import { toast } from 'sonner';
import { translateMessage } from './message-i18n';
import { isAdminRole } from './roles';
import { sanitizeMarkup } from './sanitize';
import { openReportProblem } from '../components/support/reportChannel';
import { recordStatusIssue } from '../components/ui/status-center';
import { toastConstants } from '../constants';
import { MOBILE_BREAKPOINT } from '../hooks/common/useIsMobile';
import i18n from '../i18n/i18n';
import {
  SESSION_ENDED_CODES,
  errorMessage,
  errorReference,
  readApiError,
} from '../lib/api-error';

type UnknownRecord = Record<string, unknown>;

const isRecord = (value: unknown): value is UnknownRecord =>
  typeof value === 'object' && value !== null;

const HTMLToastContent = ({ htmlContent }: { htmlContent: unknown }) => {
  return (
    <div
      dangerouslySetInnerHTML={{ __html: sanitizeMarkup(String(htmlContent)) }}
    />
  );
};
export default HTMLToastContent;

export const safeJsonParse = <T = unknown,>(
  value: unknown,
  fallback: T = null as T,
): T => {
  if (value === undefined || value === null || value === '') {
    return fallback;
  }
  if (typeof value !== 'string') {
    return value as T;
  }
  try {
    return JSON.parse(value) as T;
  } catch (e) {
    return fallback;
  }
};

const readLocalStorage = (key: string): string | null => {
  if (typeof localStorage === 'undefined') return null;
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
};

type StoredUser = {
  id?: number | string;
  role?: number | string;
  [key: string]: unknown;
};

export const getUserFromLocalStorage = (): StoredUser | null =>
  safeJsonParse<StoredUser | null>(readLocalStorage('user'), null);

/** The signed-in account is a 管理员 (the only platform role above a customer). */
export function isRoot() {
  return isAdminRole(getUserFromLocalStorage()?.role);
}

export function getSystemName() {
  const system_name = readLocalStorage('system_name');
  if (!system_name || system_name === 'undefined' || system_name === 'null') {
    return 'Lazu';
  }
  return system_name;
}

export function getLogo() {
  const logo = readLocalStorage('logo');
  if (!logo) return '/logo.png';
  return logo;
}

export function getUserIdFromLocalStorage() {
  const user = getUserFromLocalStorage();
  const userId = user?.id;
  return userId === undefined || userId === null || userId === ''
    ? -1
    : String(userId);
}

export function formatDecimalValue(value: unknown, digits = 6) {
  if (value === undefined || value === null || Number.isNaN(value)) {
    return '0';
  }
  const fixed = Number(value).toFixed(digits);
  if (!fixed.includes('.')) {
    return fixed;
  }
  let trimmed = fixed.replace(/0+$/, '');
  if (trimmed.endsWith('.')) {
    trimmed = trimmed.slice(0, -1);
  }
  if (trimmed === '' || trimmed === '-0') {
    return '0';
  }
  return trimmed;
}

export { copy } from './clipboard';

// isMobile 函数已移除，请改用 useIsMobile Hook

type ToastOptions = NonNullable<Parameters<typeof toast.error>[1]>;

let showErrorOptions: ToastOptions = {
  duration: toastConstants.ERROR_TIMEOUT,
};
let showWarningOptions: ToastOptions = {
  duration: toastConstants.WARNING_TIMEOUT,
};
let showSuccessOptions: ToastOptions = {
  duration: toastConstants.SUCCESS_TIMEOUT,
};
let showInfoOptions: ToastOptions = {
  duration: toastConstants.INFO_TIMEOUT,
};
let showNoticeOptions: ToastOptions = { duration: Infinity };

const ERROR_DEDUP_WINDOW_MS = 4000;
const ERROR_BURST_WINDOW_MS = 2200;
const ERROR_MAX_BURST_COUNT = 2;
const errorLastShownAt = new Map<string, number>();
let errorBurstTimeline: number[] = [];

const isMobileScreen =
  typeof window !== 'undefined' &&
  window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`).matches;
if (isMobileScreen) {
  // Narrow screens get a centered toast; every tone must be listed or that
  // tone silently keeps the desktop corner position.
  for (const options of [
    showErrorOptions,
    showWarningOptions,
    showSuccessOptions,
    showInfoOptions,
    showNoticeOptions,
  ]) {
    options.position = 'bottom-center';
  }
}

const compactErrorHistory = (now = Date.now()) => {
  errorBurstTimeline = errorBurstTimeline.filter(
    (ts) => now - ts < ERROR_BURST_WINDOW_MS,
  );
  for (const [key, ts] of errorLastShownAt.entries()) {
    if (now - ts > ERROR_DEDUP_WINDOW_MS * 3) {
      errorLastShownAt.delete(key);
    }
  }
};

const shouldShowErrorToast = (dedupeKey: string) => {
  const now = Date.now();
  compactErrorHistory(now);
  if (!dedupeKey) return false;
  const lastShown = errorLastShownAt.get(dedupeKey);
  if (lastShown && now - lastShown < ERROR_DEDUP_WINDOW_MS) {
    return false;
  }
  if (errorBurstTimeline.length >= ERROR_MAX_BURST_COUNT) {
    return false;
  }
  errorLastShownAt.set(dedupeKey, now);
  errorBurstTimeline.push(now);
  return true;
};

// A request can reach both the axios interceptor and a caller's onError.
// Remember the error object so it produces one toast and one status entry.
type ErrorReport = {
  message: string;
  fallback?: string;
  toastId?: string | number;
  updateStatusMessage?: (message: string) => void;
};
const reportedErrors = new WeakMap<object, ErrorReport>();

const endConsoleSession = () => {
  if (typeof window === 'undefined') return;
  localStorage.removeItem('user');
  window.location.href = '/login?expired=true';
};

/** Tell the reader a request failed.
 *
 *  Pass the error object itself, never `error.message`: the code, type and
 *  request ID it carries decide the sentence, whether the session ends and
 *  whether a support reference is shown. `fallback` is used only when
 *  neither the code nor the type has copy, or for a generic internal_error. */
export function showError(error: unknown, fallback?: string) {
  const info = readApiError(error);
  if (info.local === 'canceled') return;
  const message = errorMessage(error, fallback);
  const reference =
    (info.status ?? 0) >= 500 || info.type === 'api_error'
      ? errorReference(error)
      : '';
  const statusMessage = reference ? `${message} ${reference}` : message;
  const previous = isRecord(error) ? reportedErrors.get(error) : undefined;
  if (previous) {
    if (fallback && !previous.fallback && message !== previous.message) {
      previous.fallback = fallback;
      previous.message = message;
      previous.updateStatusMessage?.(statusMessage);
      if (previous.toastId !== undefined) {
        toast.error(message, {
          ...showErrorOptions,
          id: previous.toastId,
          description: reference || undefined,
          onDismiss: () => {
            previous.toastId = undefined;
          },
          onAutoClose: () => {
            previous.toastId = undefined;
          },
        });
      }
    }
    return;
  }
  const report: ErrorReport = { message, fallback };
  if (isRecord(error)) reportedErrors.set(error, report);
  // A server answer is expected behaviour, not a bug; only failures that
  // never came from the server (a thrown exception) are logged.
  if (!info.code && info.status === undefined && typeof error !== 'string') {
    console.error(error);
  }

  // Only a console session that has ended signs the reader out. A rejected
  // API key (invalid_api_key, token_expired) is a 401 too, and must not.
  const config = isRecord(error) && isRecord(error.config) ? error.config : {};
  const url = typeof config.url === 'string' ? config.url : '';
  if (
    info.status === 401 &&
    (SESSION_ENDED_CODES.has(info.code) ||
      (!info.code && url.startsWith('/api/'))) &&
    getUserIdFromLocalStorage() !== -1
  ) {
    endConsoleSession();
    return;
  }

  report.updateStatusMessage = recordStatusIssue({
    kind: 'error',
    message: statusMessage,
    source: 'Request',
    dedupeKey: info.code || undefined,
  });
  if (!shouldShowErrorToast(info.code || message)) return;
  // A failure worth a support reference is worth a report: the toast offers
  // one, already carrying the error number and the request id.
  const reportAction =
    reference && getUserIdFromLocalStorage() !== -1
      ? {
          label: String(
            i18n.t('console.report.action', { defaultValue: '报告问题' }),
          ),
          onClick: () =>
            openReportProblem({
              error: info.lazu || undefined,
              message,
              requestId: info.requestId || undefined,
            }),
        }
      : undefined;
  report.toastId = toast.error(message, {
    ...showErrorOptions,
    action: reportAction,
    description: reference || undefined,
    onDismiss: () => {
      report.toastId = undefined;
    },
    onAutoClose: () => {
      report.toastId = undefined;
    },
  });
}

export function showWarning(message: unknown) {
  toast.warning(translateMessage(message), showWarningOptions);
}

export function showSuccess(message: unknown) {
  toast.success(translateMessage(message), showSuccessOptions);
}

export function showInfo(message: unknown) {
  toast.info(translateMessage(message), showInfoOptions);
}

export function showNotice(message: unknown, isHTML = false) {
  if (isHTML) {
    toast.custom(
      () => <HTMLToastContent htmlContent={message} />,
      showNoticeOptions,
    );
  } else {
    toast.info(String(message), showNoticeOptions);
  }
}

export function timestamp2string(timestamp: number) {
  let date = new Date(timestamp * 1000);
  let year = date.getFullYear().toString();
  let month = (date.getMonth() + 1).toString();
  let day = date.getDate().toString();
  let hour = date.getHours().toString();
  let minute = date.getMinutes().toString();
  let second = date.getSeconds().toString();
  if (month.length === 1) {
    month = '0' + month;
  }
  if (day.length === 1) {
    day = '0' + day;
  }
  if (hour.length === 1) {
    hour = '0' + hour;
  }
  if (minute.length === 1) {
    minute = '0' + minute;
  }
  if (second.length === 1) {
    second = '0' + second;
  }
  return (
    year + '-' + month + '-' + day + ' ' + hour + ':' + minute + ':' + second
  );
}

export function timestamp2string1(
  timestamp: number,
  dataExportDefaultTime: 'hour' | 'week' | string = 'hour',
) {
  let date = new Date(timestamp * 1000);
  // let year = date.getFullYear().toString();
  let month = (date.getMonth() + 1).toString();
  let day = date.getDate().toString();
  let hour = date.getHours().toString();
  if (month.length === 1) {
    month = '0' + month;
  }
  if (day.length === 1) {
    day = '0' + day;
  }
  if (hour.length === 1) {
    hour = '0' + hour;
  }
  let str = month + '-' + day;
  if (dataExportDefaultTime === 'hour') {
    str += ' ' + hour + ':00';
  } else if (dataExportDefaultTime === 'week') {
    let nextWeek = new Date(timestamp * 1000 + 6 * 24 * 60 * 60 * 1000);
    let nextMonth = (nextWeek.getMonth() + 1).toString();
    let nextDay = nextWeek.getDate().toString();
    if (nextMonth.length === 1) {
      nextMonth = '0' + nextMonth;
    }
    if (nextDay.length === 1) {
      nextDay = '0' + nextDay;
    }
    str += ' - ' + nextMonth + '-' + nextDay;
  }
  return str;
}

export function downloadTextAsFile(text: BlobPart, filename: string) {
  let blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  let url = URL.createObjectURL(blob);
  let a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
}

// 提取消息中的文本内容
type MessageContentPart = {
  image_url?: { url: string };
  text?: string;
  type: string;
};

type ChatMessage = {
  content?: MessageContentPart[] | string;
  role?: string;
  [key: string]: unknown;
};

// 格式化消息用于API请求
export const formatMessageForAPI = (
  message: ChatMessage | null | undefined,
) => {
  if (!message) return null;

  return {
    role: message.role,
    content: message.content,
  };
};

// 验证消息是否有效
export const isValidMessage = (message: unknown): message is ChatMessage => {
  if (!isRecord(message) || typeof message.role !== 'string') return false;
  return message.content !== undefined && message.content !== null;
};

// 计算相对时间（几天前、几小时前等）
export const getRelativeTime = (
  publishDate: Date | number | string | undefined,
) => {
  if (!publishDate) return '';

  const now = new Date();
  const pubDate = new Date(publishDate);

  // 如果日期无效，返回原始字符串
  if (isNaN(pubDate.getTime())) return String(publishDate);

  const diffMs = now.getTime() - pubDate.getTime();
  const diffSeconds = Math.floor(diffMs / 1000);
  const diffMinutes = Math.floor(diffSeconds / 60);
  const diffHours = Math.floor(diffMinutes / 60);
  const diffDays = Math.floor(diffHours / 24);
  const diffWeeks = Math.floor(diffDays / 7);
  const diffMonths = Math.floor(diffDays / 30);
  const diffYears = Math.floor(diffDays / 365);

  // 如果是未来时间，显示具体日期
  if (diffMs < 0) {
    return formatDateString(pubDate);
  }

  // 根据时间差返回相应的描述
  if (diffSeconds < 60) {
    return String(i18n.t('common.relativeTime.justNow'));
  } else if (diffMinutes < 60) {
    return String(
      i18n.t('common.relativeTime.minutesAgo', { count: diffMinutes }),
    );
  } else if (diffHours < 24) {
    return String(i18n.t('common.relativeTime.hoursAgo', { count: diffHours }));
  } else if (diffDays < 7) {
    return String(i18n.t('common.relativeTime.daysAgo', { count: diffDays }));
  } else if (diffWeeks < 4) {
    return String(i18n.t('common.relativeTime.weeksAgo', { count: diffWeeks }));
  } else if (diffMonths < 12) {
    return String(
      i18n.t('common.relativeTime.monthsAgo', { count: diffMonths }),
    );
  } else if (diffYears < 2) {
    return String(i18n.t('common.relativeTime.oneYearAgo'));
  } else {
    // 超过2年显示具体日期
    return formatDateString(pubDate);
  }
};

// 格式化日期字符串
const formatDateString = (date: Date) => {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
};
