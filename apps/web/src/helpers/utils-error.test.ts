import { createInstance } from 'i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import zhErrors from '../i18n/locales/errors/zh/shared.json';

vi.mock('../i18n/i18n', () => {
  const instance = createInstance();
  void instance.init({
    initAsync: false,
    lng: 'zh',
    fallbackLng: 'zh',
    ns: ['errors', 'translation'],
    resources: { zh: { errors: zhErrors, translation: {} } },
    interpolation: { escapeValue: false },
  });
  return { default: instance };
});
vi.mock('sonner', () => ({ toast: { error: vi.fn(() => 'error-toast') } }));

const { toast } = await import('sonner');
const { showError } = await import('./utils');
const { API } = await import('./api');
const { apiErrorHandlerWith } = await import('../lib/api-error');
const { clearStatusIssues, setStatusIssueScope } =
  await import('../components/ui/status-center');

const failure = (code = 'internal_error') => ({
  response: {
    status: 500,
    data: {
      error: {
        code,
        type: 'api_error',
        lazu: 'LZ-9001',
        request_id: 'req_sync',
      },
    },
  },
});

describe('error reporting across interceptor and caller', () => {
  let stored: string;
  let now = Date.now();
  beforeEach(() => {
    vi.useFakeTimers();
    now += 10000;
    vi.setSystemTime(now);
    vi.clearAllMocks();
    stored = '[]';
    vi.stubGlobal('window', {
      localStorage: {
        setItem: (_key: string, value: string) => {
          stored = value;
        },
        getItem: () => null,
        removeItem: () => {},
      },
    });
    clearStatusIssues();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('enriches one toast and one status occurrence with the caller fallback', async () => {
    const error = failure();
    await API.post(
      '/api/models/sync_upstream',
      {},
      {
        adapter: async () => {
          throw error;
        },
      },
    ).catch(apiErrorHandlerWith('同步失败，请重试。'));
    showError(error);
    apiErrorHandlerWith('同步失败，请重试。')(error);
    expect(toast.error).toHaveBeenCalledTimes(2);
    expect(toast.error).toHaveBeenLastCalledWith(
      '同步失败，请重试。',
      expect.objectContaining({
        id: 'error-toast',
        description: expect.stringContaining('req_sync'),
      }),
    );
    expect(JSON.parse(stored)).toMatchObject([
      {
        count: 1,
        message: '同步失败，请重试。 错误编号 LZ-9001 · 请求 req_sync',
      },
    ]);
  });

  it('keeps catalog copy when a fallback cannot improve it', () => {
    const error = failure('insufficient_quota');
    showError(error);
    apiErrorHandlerWith('同步失败')(error);
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(JSON.parse(stored)[0].message).toContain(
      zhErrors.insufficient_quota,
    );
  });

  it('does not resurrect a dismissed toast when enriching its status entry', () => {
    const error = failure();
    showError(error);
    const options = vi.mocked(toast.error).mock.calls[0][1];
    options?.onDismiss?.({ id: 'error-toast' });
    apiErrorHandlerWith('同步失败')(error);
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(JSON.parse(stored)[0]).toMatchObject({
      count: 1,
      message: expect.stringContaining('同步失败'),
    });
  });

  it('does not overwrite a newer occurrence of the same code', () => {
    const old = failure();
    showError(old);
    showError(failure(), '新请求失败');
    apiErrorHandlerWith('旧请求失败')(old);
    expect(JSON.parse(stored)[0]).toMatchObject({
      count: 2,
      message: expect.stringContaining('新请求失败'),
    });
  });

  it('does not restore a cleared issue or write across scopes', () => {
    const error = failure();
    showError(error);
    clearStatusIssues();
    apiErrorHandlerWith('同步失败')(error);
    expect(JSON.parse(stored)).toEqual([]);
    const next = failure();
    showError(next);
    setStatusIssueScope('another-account:workspace:default');
    clearStatusIssues();
    apiErrorHandlerWith('同步失败')(next);
    expect(JSON.parse(stored)).toEqual([]);
  });

  it('does not report canceled requests', () => {
    showError({ code: 'ERR_CANCELED' });
    expect(toast.error).not.toHaveBeenCalled();
    expect(JSON.parse(stored)).toEqual([]);
  });

  it('ends an expired session once without a second caller report', () => {
    let user: string | null = '{"id":1}';
    const removeItem = vi.fn(() => {
      user = null;
    });
    vi.stubGlobal('localStorage', { getItem: () => user, removeItem });
    Object.assign(window, { location: { href: '' } });
    const error = {
      config: { url: '/api/user/self' },
      response: { status: 401, data: { error: { code: 'session_expired' } } },
    };
    showError(error);
    apiErrorHandlerWith('读取失败')(error);
    expect(removeItem).toHaveBeenCalledTimes(1);
    expect(window.location.href).toBe('/login?expired=true');
    expect(toast.error).not.toHaveBeenCalled();
    expect(JSON.parse(stored)).toEqual([]);
  });
});
