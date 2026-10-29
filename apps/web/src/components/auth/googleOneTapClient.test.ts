import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../helpers/apiUrl', () => ({
  buildAPIURL: (path: string) => `https://api.test${path}`,
}));

type InitConfig = {
  auto_select: boolean;
  callback: (response: { credential?: string }) => void;
  client_id: string;
  nonce: string;
};

const google = {
  cancel: vi.fn(),
  initialize: vi.fn<(config: InitConfig) => void>(),
  prompt: vi.fn(),
};

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json' },
    status,
  });

const fetchMock = vi.fn<typeof fetch>();

// Each test gets the module fresh: it keeps one initialization per page.
const loadClient = async () => {
  vi.resetModules();
  return import('./googleOneTapClient');
};

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('fetch', fetchMock);
  // Google's script is already on the page, so nothing is injected.
  vi.stubGlobal('window', { google: { accounts: { id: google } } });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const NINE_MINUTES = 9 * 60 * 1000;

describe('Google One Tap', () => {
  it('prompts with the nonce the server issued', async () => {
    fetchMock.mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }));
    const { startGoogleOneTap } = await loadClient();

    startGoogleOneTap({ clientId: 'client-1', onSignedIn: vi.fn() });
    await settle();

    expect(fetchMock).toHaveBeenCalledWith(
      'https://api.test/api/auth/google/one-tap',
      expect.objectContaining({ credentials: 'include' }),
    );
    expect(google.initialize).toHaveBeenCalledTimes(1);
    expect(google.initialize.mock.calls[0][0]).toMatchObject({
      auto_select: false,
      client_id: 'client-1',
      nonce: 'n-1',
    });
    expect(google.prompt).toHaveBeenCalledTimes(1);
  });

  it('posts the credential and hands back the signed-in user', async () => {
    fetchMock
      .mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }))
      .mockResolvedValueOnce(
        json(200, { data: { id: 'usr_1', role: 1 }, success: true }),
      );
    const order: string[] = [];
    const onSignedIn = vi.fn(() => order.push('signed-in'));
    const { startGoogleOneTap } = await loadClient();

    startGoogleOneTap({
      beforeSubmit: () => order.push('before'),
      clientId: 'client-1',
      headers: { 'Accept-Language': 'zh' },
      onSignedIn,
    });
    await settle();
    google.initialize.mock.calls[0][0].callback({ credential: 'id-token' });
    await settle();

    const [url, init] = fetchMock.mock.calls[1];
    expect(url).toBe('https://api.test/api/auth/google/one-tap');
    expect(init).toMatchObject({
      body: JSON.stringify({ credential: 'id-token' }),
      credentials: 'include',
      method: 'POST',
    });
    expect((init?.headers as Record<string, string>)['Accept-Language']).toBe(
      'zh',
    );
    expect(onSignedIn).toHaveBeenCalledWith({ id: 'usr_1', role: 1 });
    expect(order).toEqual(['before', 'signed-in']);
  });

  it('reports the server’s error code', async () => {
    fetchMock
      .mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }))
      .mockResolvedValueOnce(
        json(403, {
          error: { code: 'registration_closed' },
          success: false,
        }),
      );
    const onError = vi.fn();
    const onSignedIn = vi.fn();
    const { startGoogleOneTap } = await loadClient();

    startGoogleOneTap({ clientId: 'client-1', onError, onSignedIn });
    await settle();
    google.initialize.mock.calls[0][0].callback({ credential: 'id-token' });
    await settle();

    expect(onError).toHaveBeenCalledWith('registration_closed');
    expect(onSignedIn).not.toHaveBeenCalled();
  });

  it('shows nothing when the server offers no nonce', async () => {
    fetchMock.mockResolvedValueOnce(
      json(404, { error: { code: 'oauth_provider_not_configured' } }),
    );
    const onError = vi.fn();
    const { startGoogleOneTap } = await loadClient();

    startGoogleOneTap({ clientId: 'client-1', onError, onSignedIn: vi.fn() });
    await settle();

    expect(google.initialize).not.toHaveBeenCalled();
    expect(google.prompt).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });

  it('initializes once and prompts again for a later page', async () => {
    fetchMock.mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }));
    const { startGoogleOneTap, stopGoogleOneTap } = await loadClient();

    const first = { clientId: 'client-1', onSignedIn: vi.fn() };
    startGoogleOneTap(first);
    await settle();
    stopGoogleOneTap(first);
    startGoogleOneTap({ clientId: 'client-1', onSignedIn: vi.fn() });
    await settle();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(google.initialize).toHaveBeenCalledTimes(1);
    expect(google.cancel).toHaveBeenCalledTimes(1);
    expect(google.prompt).toHaveBeenCalledTimes(2);
  });

  // A visitor reads the home page for more than the nonce's lifetime, then
  // moves to the sign-in page of the same app.
  it('asks for a fresh nonce when a later page starts after it aged', async () => {
    fetchMock
      .mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }))
      .mockResolvedValueOnce(json(200, { data: { nonce: 'n-2' } }));
    const { startGoogleOneTap, stopGoogleOneTap } = await loadClient();
    const now = vi.spyOn(Date, 'now').mockReturnValue(1_000_000);

    const home = { clientId: 'client-1', onSignedIn: vi.fn() };
    startGoogleOneTap(home);
    await settle();
    stopGoogleOneTap(home);
    now.mockReturnValue(1_000_000 + NINE_MINUTES);
    startGoogleOneTap({ clientId: 'client-1', onSignedIn: vi.fn() });
    await settle();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(google.initialize).toHaveBeenCalledTimes(2);
    expect(google.initialize.mock.calls[1][0].nonce).toBe('n-2');
    expect(google.prompt).toHaveBeenCalledTimes(2);
  });

  it('renews a prompt still on screen when its nonce expires', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('document', { visibilityState: 'visible' });
    fetchMock
      .mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }))
      .mockResolvedValueOnce(json(200, { data: { nonce: 'n-2' } }));
    const { startGoogleOneTap } = await loadClient();

    startGoogleOneTap({ clientId: 'client-1', onSignedIn: vi.fn() });
    await vi.advanceTimersByTimeAsync(0);
    expect(google.prompt).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(NINE_MINUTES);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(google.initialize.mock.calls[1][0].nonce).toBe('n-2');
    expect(google.prompt).toHaveBeenCalledTimes(2);
  });

  it('leaves a hidden page alone until it starts again', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('document', { visibilityState: 'hidden' });
    fetchMock.mockResolvedValueOnce(json(200, { data: { nonce: 'n-1' } }));
    const { startGoogleOneTap } = await loadClient();

    startGoogleOneTap({ clientId: 'client-1', onSignedIn: vi.fn() });
    await vi.advanceTimersByTimeAsync(NINE_MINUTES + 1000);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(google.prompt).toHaveBeenCalledTimes(1);
  });
});
