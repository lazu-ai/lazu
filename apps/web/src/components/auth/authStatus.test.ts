import { beforeEach, describe, expect, it, vi } from 'vitest';
const http = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('../../helpers/api', () => ({ API: http }));
import {
  fetchAuthStatus,
  getAuthStatusSnapshot,
  resetAuthStatusForTests,
  subscribeAuthStatus,
} from './authStatus';

const SEED_KEY = 'lazu.authStatus.v1';

const answer = (data: Record<string, unknown>) => ({ data: { data } });

const codeAndPassword = {
  break_glass: false,
  email_code_auth: true,
  password_login: true,
  providers: [{ id: 'google', provider: 'google' }],
};

const store = new Map<string, string>();
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  value: {
    clear: () => store.clear(),
    getItem: (key: string) => store.get(key) ?? null,
    key: (index: number) => [...store.keys()][index] ?? null,
    get length() {
      return store.size;
    },
    removeItem: (key: string) => store.delete(key),
    setItem: (key: string, value: string) => store.set(key, value),
  },
});

beforeEach(() => {
  vi.clearAllMocks();
  store.clear();
  resetAuthStatusForTests();
});

describe('the one shared answer about which ways in are open', () => {
  it('asks once however many callers there are', async () => {
    http.get.mockResolvedValue(answer(codeAndPassword));

    const [first, second, third] = await Promise.all([
      fetchAuthStatus(),
      fetchAuthStatus(),
      fetchAuthStatus(),
    ]);

    expect(http.get).toHaveBeenCalledTimes(1);
    expect(first).toBe(second);
    expect(second).toBe(third);
    expect(getAuthStatusSnapshot()).toEqual({
      ready: true,
      status: {
        break_glass: false,
        email_code_auth: true,
        enabled: true,
        password_login: true,
        provider: undefined,
        providers: [{ id: 'google', provider: 'google' }],
      },
    });
  });

  // The whole point of the change: until an answer exists, callers must be
  // able to tell that they have none, rather than reading a null as "password".
  it('is not ready before the first answer', () => {
    http.get.mockReturnValue(new Promise(() => undefined));
    fetchAuthStatus();
    expect(getAuthStatusSnapshot()).toEqual({ ready: false, status: null });
  });

  it('opens the door on the last visit’s answer before the new one lands', () => {
    store.set(SEED_KEY, JSON.stringify(codeAndPassword));
    http.get.mockReturnValue(new Promise(() => undefined));

    fetchAuthStatus();

    const snapshot = getAuthStatusSnapshot();
    expect(snapshot.ready).toBe(true);
    expect(snapshot.status?.email_code_auth).toBe(true);
    expect(snapshot.status?.providers).toEqual([
      { id: 'google', provider: 'google' },
    ]);
  });

  it('does not disturb the screen when the answer has not changed', async () => {
    store.set(SEED_KEY, JSON.stringify(codeAndPassword));
    http.get.mockResolvedValue(answer(codeAndPassword));
    const listener = vi.fn();
    subscribeAuthStatus(listener);

    getAuthStatusSnapshot();
    await fetchAuthStatus();

    expect(listener).not.toHaveBeenCalled();
  });

  it('reports a changed configuration once the real answer lands', async () => {
    store.set(SEED_KEY, JSON.stringify(codeAndPassword));
    http.get.mockResolvedValue(
      answer({ ...codeAndPassword, email_code_auth: false, providers: [] }),
    );
    const listener = vi.fn();
    subscribeAuthStatus(listener);

    getAuthStatusSnapshot();
    await fetchAuthStatus();

    expect(listener).toHaveBeenCalledTimes(1);
    expect(getAuthStatusSnapshot().status?.email_code_auth).toBe(false);
    expect(JSON.parse(store.get(SEED_KEY) ?? '{}').email_code_auth).toBe(false);
  });

  // A door that never paints is worse than one offering the credential every
  // deployment has, so an unreachable status endpoint still opens it.
  it('still opens the door when the status endpoint is unreachable', async () => {
    http.get.mockRejectedValue(new Error('offline'));

    await fetchAuthStatus();

    expect(getAuthStatusSnapshot()).toEqual({
      ready: true,
      status: {
        break_glass: false,
        email_code_auth: false,
        enabled: false,
        password_login: true,
        providers: [],
      },
    });
    // A guess is not worth remembering.
    expect(store.has(SEED_KEY)).toBe(false);
  });

  it('keeps the remembered answer when the request fails', async () => {
    store.set(SEED_KEY, JSON.stringify(codeAndPassword));
    http.get.mockRejectedValue(new Error('offline'));

    getAuthStatusSnapshot();
    await fetchAuthStatus();

    expect(getAuthStatusSnapshot().status?.email_code_auth).toBe(true);
  });

  it('ignores junk left in storage', () => {
    store.set(SEED_KEY, 'not-json');
    http.get.mockReturnValue(new Promise(() => undefined));

    fetchAuthStatus();

    expect(getAuthStatusSnapshot()).toEqual({ ready: false, status: null });
  });
});
