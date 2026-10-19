import { createInstance } from 'i18next';
import { describe, expect, it, vi } from 'vitest';
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

vi.mock('../helpers/utils', () => ({ showError: vi.fn() }));

const { errorMessage, errorReference, readApiError, unwrap, LazuApiError } =
  await import('./api-error');

const axiosFailure = (status: number, body: unknown, headers = {}) => ({
  name: 'AxiosError',
  message: `Request failed with status code ${status}`,
  response: { status, data: body, headers },
  config: { url: '/api/token/' },
});

describe('readApiError', () => {
  it('reads the console envelope', () => {
    const info = readApiError(
      axiosFailure(429, {
        success: false,
        error: {
          code: 'insufficient_quota',
          type: 'insufficient_quota',
          lazu: 'LZ-3101',
          message: 'Wallet balance is too low',
          request_id: 'req_1',
          details: { balance_usd: 0.1 },
        },
      }),
    );
    expect(info).toMatchObject({
      code: 'insufficient_quota',
      lazu: 'LZ-3101',
      requestId: 'req_1',
      status: 429,
      details: { balance_usd: 0.1 },
    });
  });

  it('reads the gateway envelope and the request-id header', () => {
    const info = readApiError(
      axiosFailure(
        401,
        { error: { code: 'token_expired', type: 'authentication_error' } },
        { 'x-lazu-request-id': 'req_2', 'x-lazu-error': 'LZ-2002' },
      ),
    );
    expect(info).toMatchObject({
      code: 'token_expired',
      lazu: 'LZ-2002',
      requestId: 'req_2',
    });
  });

  it('reads the Anthropic request ID from its envelope', () => {
    const body = {
      type: 'error',
      request_id: 'req_claude',
      error: {
        code: 'internal_error',
        type: 'api_error',
        lazu: 'LZ-9001',
      },
    };
    expect(readApiError(body).requestId).toBe('req_claude');
    expect(readApiError(axiosFailure(500, body)).requestId).toBe('req_claude');
    expect(errorReference(body)).toContain('req_claude');
  });

  it('marks failures that never got an answer', () => {
    expect(
      readApiError({ code: 'ERR_NETWORK', message: 'Network Error' }).local,
    ).toBe('network');
    expect(readApiError({ code: 'ECONNABORTED' }).local).toBe('timeout');
    expect(
      readApiError({ code: 'ERR_CANCELED', name: 'CanceledError' }).local,
    ).toBe('canceled');
  });

  it.each(['x-lazu-request-id', 'x-request-id'])(
    'keeps support headers for a non-JSON response using %s',
    (header) => {
      const failure = axiosFailure(502, '<html>bad gateway</html>', {
        [header]: 'req_proxy',
        'x-lazu-error': 'LZ-9001',
      });
      expect(readApiError(failure)).toMatchObject({
        status: 502,
        requestId: 'req_proxy',
        lazu: 'LZ-9001',
      });
      expect(errorReference(failure)).toContain('req_proxy');
      expect(errorMessage(failure)).toBe(zhErrors.local.unknown);
    },
  );
});

describe('errorMessage', () => {
  it('speaks for the code, not the wire message', () => {
    const message = errorMessage(
      axiosFailure(429, {
        success: false,
        error: {
          code: 'insufficient_quota',
          type: 'insufficient_quota',
          message: 'English',
        },
      }),
    );
    expect(message).toBe(zhErrors.insufficient_quota);
  });

  it('does not call a budget failure a rate limit', () => {
    const budget = errorMessage(
      axiosFailure(429, {
        error: { code: 'key_budget_exceeded', type: 'insufficient_quota' },
      }),
    );
    expect(budget).toBe(zhErrors.key_budget_exceeded);
    expect(budget).not.toBe(zhErrors.request_rate_limit_exceeded);
  });

  it('falls back to the type, then the caller, then a generic sentence', () => {
    expect(
      errorMessage(
        axiosFailure(500, {
          error: { code: 'brand_new_code', type: 'api_error' },
        }),
      ),
    ).toBe(zhErrors.type.api_error);
    expect(
      errorMessage(axiosFailure(502, '<html>bad gateway</html>'), '保存失败'),
    ).toBe('保存失败');
    expect(errorMessage(axiosFailure(502, '<html>bad gateway</html>'))).toBe(
      zhErrors.local.unknown,
    );
  });

  it('prefers the caller context over a bare internal_error', () => {
    const failure = axiosFailure(500, {
      error: { code: 'internal_error', type: 'api_error' },
    });
    expect(errorMessage(failure, '无法保存此选择。')).toBe('无法保存此选择。');
    expect(errorMessage(failure)).toBe(zhErrors.internal_error);
  });

  it('never claims a timed-out write failed', () => {
    expect(errorMessage({ code: 'ECONNABORTED' })).toBe(zhErrors.local.timeout);
  });

  it('keeps a message the console wrote itself', () => {
    expect(errorMessage(new Error('请填写名称'))).toBe('请填写名称');
  });

  it('gives support a reference', () => {
    const failure = axiosFailure(500, {
      error: {
        code: 'internal_error',
        type: 'api_error',
        lazu: 'LZ-9001',
        request_id: 'req_9',
      },
    });
    expect(errorReference(failure)).toBe('错误编号 LZ-9001 · 请求 req_9');
  });
});

describe('unwrap', () => {
  it('keeps support headers for a legacy failure without an error envelope', () => {
    try {
      unwrap({
        data: { success: false, message: 'legacy failure' },
        status: 200,
        headers: { 'x-request-id': 'req_legacy', 'x-lazu-error': 'LZ-9001' },
      } as never);
      expect.fail('expected a failure');
    } catch (error) {
      expect(error).toBeInstanceOf(LazuApiError);
      expect(readApiError(error)).toMatchObject({
        requestId: 'req_legacy',
        lazu: 'LZ-9001',
      });
    }
  });
  it('returns data and throws the failure a 2xx body carries', () => {
    expect(
      unwrap({
        data: { success: true, data: [1] },
        status: 200,
        headers: {},
      } as never),
    ).toEqual([1]);
    expect(() =>
      unwrap({
        data: {
          success: false,
          error: { code: 'resource_not_found', type: 'not_found_error' },
        },
        status: 200,
        headers: {},
      } as never),
    ).toThrow(LazuApiError);
  });
});
