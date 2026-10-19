import axios from 'axios';
import { buildAPIURL, getAPIBaseURL } from './apiUrl';
import {
  getUserIdFromLocalStorage,
  showError,
  formatMessageForAPI,
  isValidMessage,
  safeJsonParse,
} from './utils';
import { MESSAGE_ROLES } from '../constants/chat.constants';
import i18n from '../i18n/i18n';
import { LazuApiError, readApiError } from '../lib/api-error';
import type { AxiosInstance, AxiosRequestConfig, AxiosResponse } from 'axios';

type RequestConfig = AxiosRequestConfig & {
  disableDuplicate?: boolean;
  skipErrorHandler?: boolean;
};

type ApiInputs = Record<string, unknown> & {
  group?: unknown;
  model?: unknown;
  stream?: unknown;
};

type ApiPayload = Record<string, unknown> & {
  group: unknown;
  messages: unknown[];
  model: unknown;
  stream: unknown;
};

type ApiErrorInfo = {
  details?: string;
  error: string;
  stack?: string;
  status?: number;
  statusText?: string;
  timestamp: string;
};

type ApiErrorResponse = {
  status?: number;
  statusText?: string;
};

type ApiResponseShape = {
  data?: unknown;
  message?: string;
  success?: boolean;
};

// 模块级别的请求缓存Map，确保全局唯一
const inFlightGetRequests = new Map<string, Promise<AxiosResponse<unknown>>>();

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const toApiResponseShape = (value: unknown): ApiResponseShape =>
  isRecord(value) ? value : {};

// 创建带有用户ID头的axios实例
function createAPIInstance(): AxiosInstance {
  const userId = getUserIdFromLocalStorage();
  const headers: Record<string, string> = {
    'Cache-Control': 'no-store',
  };

  // 只有当用户已登录时才设置用户ID头
  if (userId !== -1) {
    headers['X-Lazu-User'] = String(userId);
  }

  return axios.create({
    baseURL: getAPIBaseURL(),
    headers,
    withCredentials: true,
  });
}

export let API = createAPIInstance();

function patchAPIInstance(instance: AxiosInstance) {
  // The backend writes mail in the language the reader uses here, so every
  // request names the console language rather than the browser default.
  instance.interceptors.request.use((config) => {
    const language = i18n.resolvedLanguage || i18n.language;
    if (language) {
      config.headers.set('Accept-Language', language);
    }
    return config;
  });
  // Installed per instance: updateAPI() builds a new one after sign-in, and
  // an interceptor attached only to the first would silently stop reporting.
  instance.interceptors.response.use(
    (response) => response,
    (error: Error & { config?: RequestConfig }) => {
      if (!axios.isCancel(error) && !error.config?.skipErrorHandler) {
        showError(error);
      }
      return Promise.reject(error);
    },
  );

  const originalGet = instance.get.bind(instance) as AxiosInstance['get'];

  const genKey = (url: string, config: RequestConfig = {}) => {
    const params = config.params ? JSON.stringify(config.params) : '{}';
    return `${url}?${params}`;
  };

  instance.get = ((url: string, config: RequestConfig = {}) => {
    if (config?.disableDuplicate) {
      return originalGet(url, config);
    }

    const key = genKey(url, config);
    if (inFlightGetRequests.has(key)) {
      return inFlightGetRequests.get(key);
    }

    const reqPromise = originalGet(url, config).finally(() => {
      inFlightGetRequests.delete(key);
    });

    inFlightGetRequests.set(key, reqPromise);
    return reqPromise;
  }) as AxiosInstance['get'];
}

patchAPIInstance(API);

export function updateAPI() {
  // 清理旧的缓存，防止内存泄漏
  inFlightGetRequests.clear();

  API = createAPIInstance();

  patchAPIInstance(API);
  // A fresh sign-in: tell the account which language this console speaks.
  writeSyncedLanguage('');
  syncAccountLanguage();
  writeSyncedTimezone('');
  syncAccountTimezone();
}

// playground

// 构建API请求负载
export const buildApiPayload = (
  messages: unknown[],
  systemPrompt: string,
  inputs: ApiInputs,
  parameterEnabled: Record<string, boolean>,
): ApiPayload => {
  const processedMessages = messages
    .filter(isValidMessage)
    .map(formatMessageForAPI)
    .filter(Boolean);

  // 如果有系统提示，插入到消息开头
  if (systemPrompt && systemPrompt.trim()) {
    processedMessages.unshift({
      role: MESSAGE_ROLES.SYSTEM,
      content: systemPrompt.trim(),
    });
  }

  const payload: ApiPayload = {
    model: inputs.model,
    group: inputs.group,
    messages: processedMessages,
    stream: inputs.stream,
  };

  // 添加启用的参数
  const parameterMappings = {
    temperature: 'temperature',
    top_p: 'top_p',
    max_tokens: 'max_tokens',
    frequency_penalty: 'frequency_penalty',
    presence_penalty: 'presence_penalty',
    seed: 'seed',
  };

  Object.entries(parameterMappings).forEach(([key, param]) => {
    const enabled = parameterEnabled[key];
    const value = inputs[param];
    const hasValue = value !== undefined && value !== null;

    if (enabled && hasValue) {
      payload[param] = value;
    }
  });

  return payload;
};

// 处理API错误响应
export const handleApiError = (
  error: unknown,
  response: ApiErrorResponse | null = null,
): ApiErrorInfo => {
  const errorMessage =
    error instanceof Error && error.message
      ? error.message
      : i18n.t('error.unknown');
  const errorInfo: ApiErrorInfo = {
    error: errorMessage,
    timestamp: new Date().toISOString(),
    stack: error instanceof Error ? error.stack : undefined,
  };

  if (response) {
    errorInfo.status = response.status;
    errorInfo.statusText = response.statusText;
  }

  if (errorMessage.includes('HTTP error')) {
    errorInfo.details = i18n.t('error.httpStatus');
  } else if (errorMessage.includes('Failed to fetch')) {
    errorInfo.details = i18n.t('error.networkOrServer');
  }

  return errorInfo;
};

// 处理模型数据
export const processModelsData = (data: string[], currentModel?: string) => {
  const modelOptions = data.map((model) => ({
    label: model,
    value: model,
  }));

  const hasCurrentModel = modelOptions.some(
    (option) => option.value === currentModel,
  );
  const selectedModel =
    hasCurrentModel && modelOptions.length > 0
      ? currentModel
      : modelOptions[0]?.value;

  return { modelOptions, selectedModel };
};

// 原来 components 中的 utility helper

/**
 * Consumes a Server-Sent Events (SSE) stream from a POST endpoint.
 *
 * Axios cannot stream a response body, so this uses `fetch()` with a
 * `ReadableStream` reader. It replicates the auth/baseURL setup of the shared
 * axios `API` instance (VITE_REACT_APP_SERVER_URL base + X-Lazu-User header).
 *
 * The stream is read to completion. The promise resolves with the parsed `data`
 * of the final `event: done` frame, and rejects on an `event: error` frame
 * (or any transport/parse failure).
 */
export async function postSSE<T = Record<string, unknown>>(
  path: string,
  body: unknown,
): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'Cache-Control': 'no-store',
  };
  const userId = getUserIdFromLocalStorage();
  if (userId !== -1) {
    headers['X-Lazu-User'] = String(userId);
  }

  const response = await fetch(buildAPIURL(path), {
    method: 'POST',
    headers,
    body: JSON.stringify(body ?? {}),
  });

  // A refusal before the stream starts is an ordinary error envelope; read it
  // so the failure keeps its code, LZ number and request ID.
  const failure = (body: unknown, status?: number) => {
    const info = readApiError(body);
    return new LazuApiError({
      ...info,
      status: status ?? info.status,
      requestId:
        info.requestId ||
        response.headers.get('x-lazu-request-id') ||
        response.headers.get('x-request-id') ||
        '',
    });
  };

  if (!response.ok || !response.body) {
    let errorBody: unknown = null;
    try {
      errorBody = await response.json();
    } catch {
      // Not JSON: the status alone describes the failure.
    }
    throw failure(errorBody, response.status);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let doneData: T | null = null;
  let streamError: LazuApiError | null = null;

  // Parses one SSE frame ("event: x\ndata: {...}") and records done/error state.
  const handleFrame = (frame: string) => {
    let eventName = 'message';
    const dataLines: string[] = [];
    for (const line of frame.split('\n')) {
      if (line.startsWith('event:')) {
        eventName = line.slice('event:'.length).trim();
      } else if (line.startsWith('data:')) {
        dataLines.push(line.slice('data:'.length).trim());
      }
    }
    if (dataLines.length === 0) {
      return;
    }
    const raw = dataLines.join('\n');
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      return;
    }
    if (eventName === 'done') {
      doneData = parsed as T;
    } else if (eventName === 'error') {
      // The frame carries the same error object as the console envelope.
      streamError = failure(parsed, response.status);
    }
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (value) {
      buffer += decoder.decode(value, { stream: true });
      let separatorIndex = buffer.indexOf('\n\n');
      while (separatorIndex !== -1) {
        handleFrame(buffer.slice(0, separatorIndex));
        buffer = buffer.slice(separatorIndex + 2);
        separatorIndex = buffer.indexOf('\n\n');
      }
    }
    if (done) {
      break;
    }
  }
  if (buffer.trim()) {
    handleFrame(buffer);
  }

  if (streamError) {
    throw streamError;
  }
  if (doneData === null) {
    throw new Error('Sync stream ended without a result');
  }
  return doneData;
}

// Remember the console language on the account so mail follows it. Signed-out
// visitors have no account to update; a failed save only affects mail
// language and is not worth an error toast. One save per language per tab
// session keeps page loads from repeating it.
const LANGUAGE_SYNC_KEY = 'lazu.accountLanguage';

function readSyncedLanguage(): string {
  try {
    return sessionStorage.getItem(LANGUAGE_SYNC_KEY) || '';
  } catch {
    return '';
  }
}

function writeSyncedLanguage(language: string) {
  try {
    if (language) sessionStorage.setItem(LANGUAGE_SYNC_KEY, language);
    else sessionStorage.removeItem(LANGUAGE_SYNC_KEY);
  } catch {
    /* storage blocked: the next load saves again */
  }
}

export function syncAccountLanguage(
  language = i18n.resolvedLanguage || i18n.language,
) {
  if (!language || language === readSyncedLanguage()) return;
  if (getUserIdFromLocalStorage() === -1) return;
  writeSyncedLanguage(language);
  API.put('/api/user/self/language', { language }, {
    skipErrorHandler: true,
  } as RequestConfig).catch(() => writeSyncedLanguage(''));
}

i18n.on('languageChanged', (language: string) => syncAccountLanguage(language));

// Give the account the browser's time zone once, so weekly and monthly usage
// mail is cut and sent on the reader's own clock. The server only fills an
// empty value; a zone chosen in settings is never replaced.
const TIMEZONE_SYNC_KEY = 'lazu.accountTimezone';

function writeSyncedTimezone(zone: string) {
  try {
    if (zone) sessionStorage.setItem(TIMEZONE_SYNC_KEY, zone);
    else sessionStorage.removeItem(TIMEZONE_SYNC_KEY);
  } catch {
    /* storage blocked: the next load tries again */
  }
}

export function syncAccountTimezone() {
  let zone = '';
  try {
    zone = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return;
  }
  if (!zone || getUserIdFromLocalStorage() === -1) return;
  try {
    if (sessionStorage.getItem(TIMEZONE_SYNC_KEY) === zone) return;
  } catch {
    /* storage blocked: sync anyway */
  }
  writeSyncedTimezone(zone);
  API.put('/api/user/self/timezone', { timezone: zone, auto: true }, {
    skipErrorHandler: true,
  } as RequestConfig).catch(() => writeSyncedTimezone(''));
}

syncAccountTimezone();
