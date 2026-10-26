import { toast } from 'sonner';
import { translateMessage } from './message-i18n';
import { isAdminRole } from './roles';
import { sanitizeMarkup } from './sanitize';
import { openReportProblem } from '../components/support/reportChannel';
import Pagination from '../components/ui/pagination';
import { recordStatusIssue } from '../components/ui/status-center';
import { toastConstants } from '../constants';
import { THINK_TAG_REGEX, MESSAGE_ROLES } from '../constants/chat.constants';
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

export function normalizeHeaderNavModulesConfig(
  headerNavModulesConfig: unknown,
) {
  const modules = safeJsonParse<UnknownRecord | null>(
    headerNavModulesConfig,
    null,
  );
  if (!modules) {
    return null;
  }

  if (typeof modules.pricing === 'boolean') {
    return {
      ...modules,
      pricing: {
        enabled: modules.pricing,
        requireAuth: false,
      },
    };
  }

  return modules;
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

const MODEL_TAG_SPLIT_REGEX = /[,;|\s]+/;
const CONTEXT_NUMERIC_TAG_REGEX = /^\d+(?:\.\d+)?(?:k|m|b)?$/i;
export const MODEL_MARKET_TAG_MAX_DISPLAY = 2;

export function splitModelTags(tags: unknown) {
  if (!tags) return [];
  return String(tags)
    .split(MODEL_TAG_SPLIT_REGEX)
    .map((tag) => tag.trim())
    .filter(Boolean);
}

export function normalizeModelTag(tag: unknown) {
  return String(tag || '')
    .trim()
    .toLowerCase();
}

export function isContextNumericModelTag(tag: unknown) {
  return CONTEXT_NUMERIC_TAG_REGEX.test(String(tag || '').trim());
}

export function getVisibleModelTags(tags: unknown) {
  return splitModelTags(tags).filter((tag) => !isContextNumericModelTag(tag));
}

export function formatModelContextLength(contextLength: unknown) {
  const numericLength = Number(contextLength);
  if (!numericLength) return null;
  if (numericLength >= 1000000)
    return `${Math.round(numericLength / 1000000)}M`;
  if (numericLength >= 1000) return `${Math.round(numericLength / 1000)}K`;
  return String(contextLength);
}

export const MODEL_PRICING_ENDPOINT_COLOR_MAP: Record<string, string> = {
  openai: 'green',
  'openai-response': 'blue',
  anthropic: 'amber',
  gemini: 'cyan',
  'image-generation': 'pink',
  embeddings: 'indigo',
  'jina-rerank': 'teal',
};

export function getModelPricingEndpointColor(type: unknown) {
  return MODEL_PRICING_ENDPOINT_COLOR_MAP[String(type)] || 'grey';
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

export function openPage(url: string) {
  window.open(url);
}

export function removeTrailingSlash(url: string | null | undefined) {
  if (!url) return '';
  if (url.endsWith('/')) {
    return url.slice(0, -1);
  } else {
    return url;
  }
}

export function getTodayStartTimestamp() {
  var now = new Date();
  now.setHours(0, 0, 0, 0);
  return Math.floor(now.getTime() / 1000);
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

export const verifyJSON = (str: string) => {
  try {
    JSON.parse(str);
  } catch (e) {
    return false;
  }
  return true;
};

export function verifyJSONPromise(value: string) {
  try {
    JSON.parse(value);
    return Promise.resolve();
  } catch (e) {
    return Promise.reject(i18n.t('error.invalidJsonString'));
  }
}

export function shouldShowPrompt(id: number | string) {
  let prompt = localStorage.getItem(`prompt-${id}`);
  return !prompt;
}

export function setPromptShown(id: number | string) {
  localStorage.setItem(`prompt-${id}`, 'true');
}

/**
 * 比较两个对象的属性，找出有变化的属性，并返回包含变化属性信息的数组
 * @param {Object} oldObject - 旧对象
 * @param {Object} newObject - 新对象
 * @return {Array} 包含变化属性信息的数组，每个元素是一个对象，包含 key, oldValue 和 newValue
 */
type ChangedProperty = { key: string; newValue: unknown; oldValue: unknown };

export function compareObjects(
  oldObject: UnknownRecord,
  newObject: UnknownRecord,
) {
  const changedProperties: ChangedProperty[] = [];

  // 比较两个对象的属性
  for (const key in oldObject) {
    if (oldObject.hasOwnProperty(key) && newObject.hasOwnProperty(key)) {
      if (oldObject[key] !== newObject[key]) {
        changedProperties.push({
          key: key,
          oldValue: oldObject[key],
          newValue: newObject[key],
        });
      }
    }
  }

  return changedProperties;
}

// playground message

// 生成唯一ID - 使用时间戳 + 随机数确保唯一性
export const generateMessageId = () =>
  `msg_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;

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

export const getTextContent = (message: ChatMessage | null | undefined) => {
  if (!message || !message.content) return '';

  if (Array.isArray(message.content)) {
    const textContent = message.content.find((item) => item.type === 'text');
    return textContent?.text || '';
  }
  return typeof message.content === 'string' ? message.content : '';
};

// 处理 think 标签
export const processThinkTags = (content: string, reasoningContent = '') => {
  if (!content || !content.includes('<think>')) {
    return { content, reasoningContent };
  }

  const thoughts: string[] = [];
  const replyParts: string[] = [];
  let lastIndex = 0;
  let match;

  THINK_TAG_REGEX.lastIndex = 0;
  while ((match = THINK_TAG_REGEX.exec(content)) !== null) {
    replyParts.push(content.substring(lastIndex, match.index));
    thoughts.push(match[1]);
    lastIndex = match.index + match[0].length;
  }
  replyParts.push(content.substring(lastIndex));

  const processedContent = replyParts
    .join('')
    .replace(/<\/?think>/g, '')
    .trim();
  const thoughtsStr = thoughts.join('\n\n---\n\n');
  const processedReasoningContent =
    reasoningContent && thoughtsStr
      ? `${reasoningContent}\n\n---\n\n${thoughtsStr}`
      : reasoningContent || thoughtsStr;

  return {
    content: processedContent,
    reasoningContent: processedReasoningContent,
  };
};

// 处理未完成的 think 标签
export const processIncompleteThinkTags = (
  content: string,
  reasoningContent = '',
) => {
  if (!content) return { content: '', reasoningContent };

  const lastOpenThinkIndex = content.lastIndexOf('<think>');
  if (lastOpenThinkIndex === -1) {
    return processThinkTags(content, reasoningContent);
  }

  const fragmentAfterLastOpen = content.substring(lastOpenThinkIndex);
  if (!fragmentAfterLastOpen.includes('</think>')) {
    const unclosedThought = fragmentAfterLastOpen
      .substring('<think>'.length)
      .trim();
    const cleanContent = content.substring(0, lastOpenThinkIndex);
    const processedReasoningContent = unclosedThought
      ? reasoningContent
        ? `${reasoningContent}\n\n---\n\n${unclosedThought}`
        : unclosedThought
      : reasoningContent;

    return processThinkTags(cleanContent, processedReasoningContent);
  }

  return processThinkTags(content, reasoningContent);
};

// 构建消息内容（包含图片）
export const buildMessageContent = (
  textContent: string,
  imageUrls: string[] = [],
  imageEnabled = false,
) => {
  if (!textContent && (!imageUrls || imageUrls.length === 0)) {
    return '';
  }

  const validImageUrls = imageUrls.filter((url) => url && url.trim() !== '');

  if (imageEnabled && validImageUrls.length > 0) {
    return [
      { type: 'text', text: textContent || '' },
      ...validImageUrls.map((url) => ({
        type: 'image_url',
        image_url: { url: url.trim() },
      })),
    ];
  }

  return textContent || '';
};

// 创建新消息
export const createMessage = (
  role: string,
  content: ChatMessage['content'],
  options: UnknownRecord = {},
) => ({
  role,
  content,
  createAt: Date.now(),
  id: generateMessageId(),
  ...options,
});

// 创建加载中的助手消息
export const createLoadingAssistantMessage = () =>
  createMessage(MESSAGE_ROLES.ASSISTANT, '', {
    reasoningContent: '',
    isReasoningExpanded: true,
    isThinkingComplete: false,
    hasAutoCollapsed: false,
    status: 'loading',
  });

// 检查消息是否包含图片
export const hasImageContent = (message: ChatMessage | null | undefined) => {
  return (
    message &&
    Array.isArray(message.content) &&
    message.content.some((item) => item.type === 'image_url')
  );
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

// 获取最后一条用户消息
export const getLastUserMessage = (messages: ChatMessage[] | unknown) => {
  if (!Array.isArray(messages)) return null;

  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === MESSAGE_ROLES.USER) {
      return messages[i];
    }
  }
  return null;
};

// 获取最后一条助手消息
export const getLastAssistantMessage = (messages: ChatMessage[] | unknown) => {
  if (!Array.isArray(messages)) return null;

  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === MESSAGE_ROLES.ASSISTANT) {
      return messages[i];
    }
  }
  return null;
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
export const formatDateString = (date: Date) => {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
};

// 格式化日期时间字符串（包含时间）
export const formatDateTimeString = (date: Date) => {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  return `${year}-${month}-${day} ${hours}:${minutes}`;
};

// -------------------------------
// Select 组件统一过滤逻辑
// 使用方式： <Select filter={selectFilter} ... />
// 统一的 Select 搜索过滤逻辑 -- 支持同时匹配 option.value 与 option.label
export const selectFilter = (
  input: string,
  option: { label?: unknown; value?: unknown } | null | undefined,
) => {
  if (!input) return true;

  const keyword = input.trim().toLowerCase();
  const valueText = (option?.value ?? '').toString().toLowerCase();
  const labelText = (option?.label ?? '').toString().toLowerCase();

  return valueText.includes(keyword) || labelText.includes(keyword);
};

// -------------------------------
// 模型定价计算工具函数
type CalculatePricingDisplayParams = {
  displayPrice?: (price: number) => unknown;
  precision?: number;
  pricingAdjustments?: Record<string, unknown>;
  record: UnknownRecord;
  selectedGroup?: number | string;
  tokenUnit?: number | string;
  currency?: string;
};

export const calculatePricingDisplay = ({
  record,
  selectedGroup = 'all',
  pricingAdjustments = {},
  tokenUnit = 'M',
  displayPrice = String,
  precision = 4,
}: CalculatePricingDisplayParams) => {
  // 1. 选择实际使用的分组
  let usedGroup = String(selectedGroup);
  let effectiveAdjustmentFactor = Number(
    pricingAdjustments[String(selectedGroup)],
  );
  if (!Number.isFinite(effectiveAdjustmentFactor)) {
    effectiveAdjustmentFactor = 1;
  }

  if (
    selectedGroup === 'all' ||
    pricingAdjustments[String(selectedGroup)] === undefined
  ) {
    // 在模型可用分组中选择最低有效价格因子，若无则使用 1
    let bestAdjustmentFactor = Number.POSITIVE_INFINITY;
    if (
      Array.isArray(record.enable_groups) &&
      record.enable_groups.length > 0
    ) {
      record.enable_groups.forEach((g) => {
        const pricingAdjustment = Number(pricingAdjustments[g]);
        if (
          Number.isFinite(pricingAdjustment) &&
          pricingAdjustment < bestAdjustmentFactor
        ) {
          bestAdjustmentFactor = pricingAdjustment;
          usedGroup = g;
          effectiveAdjustmentFactor = pricingAdjustment;
        }
      });
    }

    // 如果找不到合适分组价格因子，回退为 1
    if (!Number.isFinite(effectiveAdjustmentFactor)) {
      effectiveAdjustmentFactor = 1;
    }
  }

  // 2. 根据计费类型计算价格
  if (record.quota_type === 0) {
    // 按量计费
    const hasNumericValue = (value: unknown) =>
      value !== undefined &&
      value !== null &&
      value !== '' &&
      Number.isFinite(Number(value));

    const inputUnitPriceUSD = hasNumericValue(record.input_per_mtok)
      ? Number(record.input_per_mtok) * effectiveAdjustmentFactor
      : null;
    const completionUnitPriceUSD = hasNumericValue(record.output_per_mtok)
      ? Number(record.output_per_mtok) * effectiveAdjustmentFactor
      : null;

    const unitDivisor = tokenUnit === 'K' ? 1000 : 1;
    const unitLabel = tokenUnit === 'K' ? 'K' : 'M';

    const rawDisplayInput =
      inputUnitPriceUSD === null ? null : displayPrice(inputUnitPriceUSD);
    const rawDisplayCompletion =
      completionUnitPriceUSD === null
        ? null
        : displayPrice(completionUnitPriceUSD);

    const numInput =
      rawDisplayInput === null
        ? null
        : parseFloat(String(rawDisplayInput).replace(/[^0-9.]/g, '')) /
          unitDivisor;
    const numCompletion =
      rawDisplayCompletion === null
        ? null
        : parseFloat(String(rawDisplayCompletion).replace(/[^0-9.]/g, '')) /
          unitDivisor;

    const symbol = '$';

    const formatPerUnit = (absolutePrice: unknown) => {
      if (hasNumericValue(absolutePrice)) {
        const usd = Number(absolutePrice) * effectiveAdjustmentFactor;
        const raw = displayPrice(usd);
        const num =
          parseFloat(String(raw).replace(/[^0-9.]/g, '')) / unitDivisor;
        return `${symbol}${formatDecimalValue(num, precision)}`;
      }
      return null;
    };

    return {
      inputPrice:
        numInput === null
          ? '-'
          : `${symbol}${formatDecimalValue(numInput, precision)}`,
      completionPrice:
        numCompletion === null
          ? '-'
          : `${symbol}${formatDecimalValue(numCompletion, precision)}`,
      cacheReadPrice: formatPerUnit(record.cache_read_per_mtok),
      cacheCreate5mPrice: formatPerUnit(record.cache_write_5m_per_mtok),
      cacheCreate1hPrice: formatPerUnit(record.cache_write_1h_per_mtok),
      audioInputPrice: formatPerUnit(record.audio_input_per_mtok),
      audioOutputPrice: formatPerUnit(record.audio_output_per_mtok),
      imageInputPrice: formatPerUnit(record.image_input_per_mtok),
      imageOutputPrice: formatPerUnit(record.image_output_per_mtok),
      unitLabel,
      isPerToken: true,
      usedGroup,
      effectiveAdjustmentFactor,
    };
  }

  if (record.quota_type === 1) {
    // 按次计费
    const hasRequestPrice =
      record.request_usd !== undefined &&
      record.request_usd !== null &&
      record.request_usd !== '';
    const basePrice = Number(record.request_usd);
    const displayVal =
      hasRequestPrice && Number.isFinite(basePrice)
        ? String(displayPrice(basePrice * effectiveAdjustmentFactor))
        : '-';
    const secondUSD = Number(record.second_usd);
    const secondPrice = Number.isFinite(secondUSD)
      ? String(displayPrice(secondUSD * effectiveAdjustmentFactor))
      : null;

    return {
      price: displayVal,
      secondPrice,
      isPerToken: false,
      usedGroup,
      effectiveAdjustmentFactor,
    };
  }

  // 未知计费类型，返回占位信息
  return {
    price: '-',
    isPerToken: false,
    usedGroup,
    effectiveAdjustmentFactor,
  };
};

// 格式化价格信息为结构化对象（用于新卡片视图自由渲染）
type PricingDisplayData = {
  additional?: Array<{ label: string; price: string; unit: string }>;
  audioInputPrice?: string | null;
  audioOutputPrice?: string | null;
  cacheCreate1hPrice?: string | null;
  cacheCreate5mPrice?: string | null;
  cacheReadPrice?: string | null;
  completionPrice?: string;
  imageInputPrice?: string | null;
  imageOutputPrice?: string | null;
  inputPrice?: string;
  isPerToken: boolean;
  price?: string;
  secondPrice?: string | null;
  unitLabel?: string;
};

type StructuredPriceItem = {
  hint?: string;
  label: string;
  price: string;
  unit: string;
};

export const formatPriceInfoStructured = (
  priceData: PricingDisplayData,
  t: (key: string, options?: Record<string, unknown>) => string,
) => {
  if (priceData.isPerToken) {
    const unit = `/ ${priceData.unitLabel} tokens`;
    const cache: StructuredPriceItem[] = [];
    const additional: StructuredPriceItem[] = [];
    if (priceData.cacheReadPrice) {
      cache.push({
        label: t('legacy.i18n.cache_read.d6692a420b'),
        price: priceData.cacheReadPrice,
        unit,
        hint: t('legacy.i18n.unit_price_for_cache_hits.2a4c35f44d'),
      });
    }
    if (priceData.cacheCreate5mPrice) {
      cache.push({
        label: t('legacy.i18n.cache_write_5_minutes.7a8cf7f7dc'),
        price: priceData.cacheCreate5mPrice,
        unit,
        hint: t(
          'legacy.i18n.unit_price_for_initial_write_to_prompt_cache_5_minute_ttl.6a8151c75e',
        ),
      });
    }
    if (priceData.cacheCreate1hPrice) {
      cache.push({
        label: t('legacy.i18n.cache_write_1_hour.fa33760199'),
        price: priceData.cacheCreate1hPrice,
        unit,
        hint: t(
          'legacy.i18n.unit_price_for_initial_write_to_prompt_cache_1_hour_ttl.5d0265bd4d',
        ),
      });
    }
    if (priceData.audioInputPrice) {
      additional.push({
        label: t('legacy.i18n.audio_input_price.68fe891ad7'),
        price: priceData.audioInputPrice,
        unit,
      });
    }
    if (priceData.audioOutputPrice) {
      additional.push({
        label: t('legacy.i18n.audio_completion_price.84bbbe43ba'),
        price: priceData.audioOutputPrice,
        unit,
      });
    }
    if (priceData.imageInputPrice) {
      additional.push({
        label: t('legacy.i18n.image_input_price.7d8b7f3a21'),
        price: priceData.imageInputPrice,
        unit,
      });
    }
    if (priceData.imageOutputPrice) {
      additional.push({
        label: t('page.console.pricing.imageOutputPrice'),
        price: priceData.imageOutputPrice,
        unit,
      });
    }
    return {
      type: 'per_token' as const,
      input: {
        label: t('legacy.i18n.input.e8850440f2'),
        price: priceData.inputPrice || '',
        unit,
      },
      output: {
        label: t('legacy.i18n.output.ded698ae1e'),
        price: priceData.completionPrice || '',
        unit,
      },
      cache,
      additional,
    };
  }
  return {
    type: 'per_call' as const,
    price: priceData.price || '',
    secondPrice: priceData.secondPrice,
    label: t('legacy.i18n.per_call_price.6a1d78b2c9'),
  };
};

// 格式化价格信息（用于卡片视图）
export const formatPriceInfo = (
  priceData: PricingDisplayData,
  t: (key: string) => string,
) => {
  if (priceData.isPerToken) {
    return (
      <>
        <span style={{ color: 'var(--ui-text-muted)' }}>
          {t('legacy.i18n.input.e8850440f2')} {priceData.inputPrice}/
          {priceData.unitLabel}
        </span>
        <span style={{ color: 'var(--ui-text-muted)' }}>
          {t('legacy.i18n.output.ded698ae1e')} {priceData.completionPrice}/
          {priceData.unitLabel}
        </span>
      </>
    );
  }

  return (
    <>
      <span style={{ color: 'var(--ui-text-muted)' }}>
        {t('legacy.i18n.per_call_price.6a1d78b2c9')} {priceData.price}
      </span>
    </>
  );
};

// -------------------------------
// CardPro 分页配置函数
// 用于创建 CardPro 的 paginationArea 配置
type CardPaginationParams = {
  currentPage: number;
  isMobile?: boolean;
  onPageChange?: (page: number) => void;
  onPageSizeChange?: (pageSize: number) => void;
  pageSize: number;
  pageSizeOpts?: number[];
  showSizeChanger?: boolean;
  t?: (key: string) => string;
  total: number;
};

export const createCardProPagination = ({
  currentPage,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
  isMobile = false,
  pageSizeOpts = [10, 20, 50, 100],
  showSizeChanger = true,
  t = (key) => key,
}: CardPaginationParams) => {
  if (!total || total <= 0) return null;

  const start = (currentPage - 1) * pageSize + 1;
  const end = Math.min(currentPage * pageSize, total);
  const totalText = `${t('legacy.i18n.showing.78715e49e9')} ${start} ${t('legacy.i18n.key.cdf5de37f8')} ${end} ${t('legacy.i18n.key.a0ba8f7a92')} ${total} ${t('legacy.i18n.items.bce2ef6151')}`;

  return (
    <>
      {/* 桌面端左侧总数信息 */}
      {!isMobile && (
        <span
          className='text-sm select-none'
          style={{ color: 'var(--ui-text-faint)' }}
        >
          {totalText}
        </span>
      )}

      {/* 右侧分页控件 */}
      <Pagination
        currentPage={currentPage}
        pageSize={pageSize}
        total={total}
        pageSizeOpts={pageSizeOpts}
        showSizeChanger={showSizeChanger}
        onPageSizeChange={onPageSizeChange}
        onPageChange={onPageChange}
        showTotal={isMobile}
      />
    </>
  );
};

// 模型定价筛选条件默认值
const DEFAULT_PRICING_FILTERS = {
  search: '',
  showAdjustments: false,
  viewMode: 'card',
  filterGroup: 'all',
  filterQuotaType: 'all',
  filterEndpointType: 'all',
  filterVendor: 'all',
  filterTag: 'all',
  filterOpenSource: 'all',
  currentPage: 1,
  selectedTier: null, // null 表示使用当前用户等级
};

// 重置模型定价筛选条件
type PricingFilterResetParams = {
  handleChange?: (value: string) => void;
  setCurrentPage?: (value: number) => void;
  setFilterEndpointType?: (value: string) => void;
  setFilterGroup?: (value: string) => void;
  setFilterOpenSource?: (value: string) => void;
  setFilterQuotaType?: (value: string) => void;
  setFilterTag?: (value: string) => void;
  setFilterVendor?: (value: string) => void;
  setSelectedTier?: (value: null) => void;
  setShowAdjustments?: (value: boolean) => void;
  setViewMode?: (value: string) => void;
};

export const resetPricingFilters = ({
  handleChange,
  setShowAdjustments,
  setViewMode,
  setFilterGroup,
  setFilterQuotaType,
  setFilterEndpointType,
  setFilterVendor,
  setFilterTag,
  setFilterOpenSource,
  setCurrentPage,
  setSelectedTier,
}: PricingFilterResetParams) => {
  handleChange?.(DEFAULT_PRICING_FILTERS.search);
  setShowAdjustments?.(DEFAULT_PRICING_FILTERS.showAdjustments);
  setViewMode?.(DEFAULT_PRICING_FILTERS.viewMode);
  setFilterGroup?.(DEFAULT_PRICING_FILTERS.filterGroup);
  setFilterQuotaType?.(DEFAULT_PRICING_FILTERS.filterQuotaType);
  setFilterEndpointType?.(DEFAULT_PRICING_FILTERS.filterEndpointType);
  setFilterVendor?.(DEFAULT_PRICING_FILTERS.filterVendor);
  setFilterTag?.(DEFAULT_PRICING_FILTERS.filterTag);
  setFilterOpenSource?.(DEFAULT_PRICING_FILTERS.filterOpenSource);
  setCurrentPage?.(DEFAULT_PRICING_FILTERS.currentPage);
  setSelectedTier?.(DEFAULT_PRICING_FILTERS.selectedTier);
};
