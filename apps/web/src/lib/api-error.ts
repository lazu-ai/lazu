/* The console's single reading of a failed request.
 *
 * The server answers every failure with a real HTTP status and one error
 * object:
 *
 *   console /api:  { success: false, error: { code, type, message, lazu, docs, request_id, details } }
 *   gateway /v1:   { error: { code, type, message, lazu, docs, request_id, details } }
 *
 * `code` is the contract. The sentence a person reads comes from the
 * `errors` i18n namespace keyed by that code; the English `message` on the
 * wire is for developers and logs and is never shown here. When the copy for
 * a code is missing the error type's copy stands in, so a reader always gets
 * a sentence in their language. */
import { showError } from '../helpers/utils';
import i18n from '../i18n/i18n';
import type { AxiosResponse } from 'axios';

export type LocalFailure =
  | 'network'
  | 'timeout'
  | 'canceled'
  | 'incomplete_page';

export type ApiErrorInfo = {
  /** Catalog code, e.g. `insufficient_quota`. Empty for local failures. */
  code: string;
  /** Error type, e.g. `rate_limit_error`. */
  type: string;
  /** Lazu error number, e.g. `LZ-3101`. */
  lazu: string;
  /** English developer message from the wire. Never shown to readers. */
  message: string;
  requestId: string;
  status?: number;
  docs: string;
  details: Record<string, unknown>;
  /** A failure detected locally rather than reported in an HTTP error response. */
  local?: LocalFailure;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const text = (value: unknown): string =>
  typeof value === 'string' ? value.trim() : '';

/** An error the server described with a catalog code. */
export class LazuApiError extends Error implements ApiErrorInfo {
  readonly code: string;
  readonly type: string;
  readonly lazu: string;
  readonly requestId: string;
  readonly status?: number;
  readonly docs: string;
  readonly details: Record<string, unknown>;
  readonly local?: LocalFailure;

  constructor(info: ApiErrorInfo) {
    super(info.message || info.code || 'Request failed');
    this.name = 'LazuApiError';
    this.code = info.code;
    this.type = info.type;
    this.lazu = info.lazu;
    this.requestId = info.requestId;
    this.status = info.status;
    this.docs = info.docs;
    this.details = info.details;
    this.local = info.local;
  }
}

export const isLazuApiError = (value: unknown): value is LazuApiError =>
  value instanceof LazuApiError;

/** Read the error object of a response body (either envelope). */
const fromBody = (
  body: unknown,
  status: number | undefined,
  headers: Record<string, unknown>,
): ApiErrorInfo | null => {
  if (!isRecord(body)) return null;
  const error = isRecord(body.error) ? body.error : null;
  if (!error) return null;
  return {
    code: text(error.code),
    type: text(error.type),
    lazu: text(error.lazu) || text(headers['x-lazu-error']),
    message: text(error.message),
    requestId:
      text(error.request_id) ||
      text(body.request_id) ||
      text(headers['x-lazu-request-id']) ||
      text(headers['x-request-id']),
    status,
    docs: text(error.docs),
    details: isRecord(error.details) ? error.details : {},
  };
};

const empty = (message = ''): ApiErrorInfo => ({
  code: '',
  type: '',
  lazu: '',
  message,
  requestId: '',
  docs: '',
  details: {},
});

/** A failure detected by the console, without an HTTP error response. */
export const localApiError = (local: LocalFailure): LazuApiError =>
  new LazuApiError({ ...empty(), local });

/** Everything the console can know about a failure, from any source. */
export function readApiError(error: unknown): ApiErrorInfo {
  if (error instanceof LazuApiError) return error;
  if (!isRecord(error)) return empty(text(error));

  const response = isRecord(error.response) ? error.response : null;
  if (response) {
    const status =
      typeof response.status === 'number' ? response.status : undefined;
    const headers = isRecord(response.headers) ? response.headers : {};
    const parsed = fromBody(response.data, status, headers);
    if (parsed) return parsed;
    return {
      ...empty(text(error.message)),
      status,
      requestId:
        text(headers['x-lazu-request-id']) || text(headers['x-request-id']),
      lazu: text(headers['x-lazu-error']),
    };
  }

  const axiosCode = text(error.code);
  if (
    axiosCode === 'ERR_CANCELED' ||
    error.name === 'CanceledError' ||
    error.name === 'AbortError'
  ) {
    return { ...empty(), local: 'canceled' };
  }
  if (axiosCode === 'ECONNABORTED' || axiosCode === 'ETIMEDOUT') {
    return { ...empty(), local: 'timeout' };
  }
  if (
    axiosCode === 'ERR_NETWORK' ||
    error.message === 'Network Error' ||
    error.message === 'Failed to fetch'
  ) {
    return { ...empty(), local: 'network' };
  }

  // A raw body handed over directly, e.g. an SSE `error` frame.
  const parsed = fromBody(error, undefined, {});
  if (parsed) return parsed;

  // A thrown object that carries its own code (e.g. a feature's error class).
  if (text(error.code) && typeof error.status === 'number') {
    return {
      ...empty(text(error.message)),
      code: text(error.code),
      status: error.status,
      details: isRecord(error.details) ? error.details : {},
    };
  }
  return empty(text(error.message));
}

const errorsNs = { ns: 'errors' } as const;

const lookup = (key: string, values: Record<string, unknown> = {}) =>
  i18n.exists(key, errorsNs)
    ? String(i18n.t(key, { ...errorsNs, ...values }))
    : '';

/** The sentence a reader sees for a failure, in their language.
 *
 *  Order: local failure → copy for the code → copy for the type → the
 *  caller's fallback → a generic sentence. For internal_error the caller's
 *  fallback, when given, comes before the generic copy. A plain `Error` thrown by console
 *  code (not a server answer) keeps its own message, which the console wrote
 *  with `t()` already. */
export function errorMessage(error: unknown, fallback?: string): string {
  const info = readApiError(error);
  if (info.local) {
    return lookup(
      `local.${info.local === 'canceled' ? 'unknown' : info.local}`,
    );
  }
  // internal_error says nothing a reader can act on; the caller's sentence
  // about what they were doing is more useful. The LZ number and request ID
  // still travel in the reference line.
  if (info.code === 'internal_error' && fallback) return fallback;
  if (info.code) {
    const byCode = lookup(info.code, info.details);
    if (byCode) return byCode;
  }
  if (info.type) {
    const byType = lookup(`type.${info.type}`);
    if (byType) return byType;
  }
  if (fallback) return fallback;
  if (!info.code && !info.type && info.status === undefined && info.message) {
    return info.message;
  }
  return lookup('local.unknown') || 'Request failed';
}

/** "Error LZ-9001 · Request req_…", for readers who contact support. */
export function errorReference(error: unknown): string {
  const info = readApiError(error);
  if (!info.lazu && !info.requestId) return '';
  if (!info.requestId) return info.lazu;
  return lookup('local.reference', {
    lazu: info.lazu || info.code,
    requestId: info.requestId,
  });
}

/** Session codes after which the browser session is gone. */
export const SESSION_ENDED_CODES: ReadonlySet<string> = new Set([
  'session_required',
  'session_expired',
  'session_invalid',
  'session_user_mismatch',
]);

/** Return a console response's `data`, or throw the failure it carries.
 *  Non-2xx answers already reject in axios; this covers a 2xx whose body
 *  says `success: false`, which a proxy or an old handler may still send. */
export function unwrap<T>(response: AxiosResponse<unknown>): T {
  const body = response?.data;
  if (isRecord(body) && body.success === false) {
    const headers = isRecord(response.headers) ? response.headers : {};
    throw new LazuApiError(
      fromBody(body, response.status, headers) ?? {
        ...empty(text(body.message)),
        status: response.status,
        requestId:
          text(headers['x-lazu-request-id']) || text(headers['x-request-id']),
        lazu: text(headers['x-lazu-error']),
      },
    );
  }
  return (isRecord(body) ? body.data : undefined) as T;
}

// ── TanStack Query / promise helpers ──────────────────────────────────
//
// `apiErrorHandler` is strictly single-arg on purpose: TanStack Query's
// `onError` is typed `(error, variables, context) =>`, so a second optional
// parameter would be inferred as TVariables and break `.mutate()` callers.
// Use the factory for a domain-specific fallback sentence.
export const apiErrorHandler = (error: unknown): void => showError(error);

export const apiErrorHandlerWith =
  (fallback: string) =>
  (error: unknown): void =>
    showError(error, fallback);
