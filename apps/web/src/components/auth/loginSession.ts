import axios from 'axios';
import { API } from '../../helpers/api';

const SESSION_CHECK_TIMEOUT_MS = 8_000;
const PREFETCH_REUSE_MS = 10_000;

export type LoginSessionResult =
  | { kind: 'authenticated'; user: Record<string, unknown> }
  | { kind: 'unauthorized' }
  | { kind: 'unavailable' };

export type LoginSessionProbe = {
  hint: string;
  result: Promise<LoginSessionResult>;
};

let cachedProbe: (LoginSessionProbe & { startedAt: number }) | null = null;

export const readStoredSessionHint = (): string | null => {
  try {
    return globalThis.localStorage?.getItem('user') ?? null;
  } catch {
    return null;
  }
};

const identityFromHint = (hint: string): string | null => {
  try {
    const id: unknown = JSON.parse(hint)?.id;
    return (typeof id === 'string' && id.length > 0) ||
      (typeof id === 'number' && Number.isFinite(id))
      ? String(id)
      : null;
  } catch {
    return null;
  }
};

/** A read-only, bounded check shared by link prefetch and the mounted door. */
export const startLoginSessionProbe = (hint: string): LoginSessionProbe => {
  if (
    cachedProbe?.hint === hint &&
    Date.now() - cachedProbe.startedAt < PREFETCH_REUSE_MS
  ) {
    return cachedProbe;
  }

  const identity = identityFromHint(hint);
  if (!identity) {
    return { hint, result: Promise.resolve({ kind: 'unavailable' }) };
  }

  const controller = new AbortController();
  let timeout: ReturnType<typeof setTimeout> | undefined;
  const request = API.get('/api/user/self', {
    skipErrorHandler: true,
    disableDuplicate: true,
    // API's default header was captured when its module loaded. Another tab
    // may have signed in since then, so use the identity from this very hint.
    headers: { 'X-Lazu-User': identity },
    signal: controller.signal,
  } as Parameters<typeof API.get>[1])
    .then((response): LoginSessionResult => {
      const user: unknown = response.data?.data;
      return response.data?.success && user && typeof user === 'object'
        ? { kind: 'authenticated', user: user as Record<string, unknown> }
        : { kind: 'unavailable' };
    })
    .catch(
      (error): LoginSessionResult =>
        axios.isAxiosError(error) && error.response?.status === 401
          ? { kind: 'unauthorized' }
          : { kind: 'unavailable' },
    );
  const deadline = new Promise<LoginSessionResult>((resolve) => {
    timeout = setTimeout(() => {
      controller.abort();
      resolve({ kind: 'unavailable' });
    }, SESSION_CHECK_TIMEOUT_MS);
  });
  const result = Promise.race([request, deadline]).finally(() => {
    if (timeout) clearTimeout(timeout);
  });
  cachedProbe = { hint, result, startedAt: Date.now() };
  return cachedProbe;
};

export const prefetchLoginSession = () => {
  const hint = readStoredSessionHint();
  if (hint) startLoginSessionProbe(hint);
};
