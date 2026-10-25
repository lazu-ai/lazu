import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
const http = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('../../helpers/api', () => ({ API: http }));
import { collectPages, realProjectsClient } from './api';
import { validateTopUpAmount } from './budget';
import i18n, { loadErrorsNamespace } from '../../i18n/i18n';
import { LazuApiError, errorMessage } from '../../lib/api-error';

beforeEach(() => vi.clearAllMocks());
afterEach(async () => {
  await i18n.changeLanguage('en');
});
describe('project client review regressions', () => {
  it('loads later pages without dropping project parameters', async () => {
    http.get.mockImplementation(async (_url, options) => ({
      data: {
        success: true,
        data: {
          items:
            options.params.page === 1 ? [{ id: 'first' }] : [{ id: 'later' }],
          total: 2,
          page: options.params.page,
          page_size: 1,
        },
      },
    }));
    expect(await realProjectsClient.listMyTokens('project-a')).toEqual([
      { id: 'first' },
      { id: 'later' },
    ]);
    expect(http.get).toHaveBeenNthCalledWith(
      2,
      '/api/token/',
      expect.objectContaining({
        params: expect.objectContaining({ project_id: 'project-a', page: 2 }),
      }),
    );
  });
  it.each([
    [
      'en',
      'The list changed while loading and could not be fully loaded. Refresh and try again.',
    ],
    ['zh', '列表在加载期间发生变化，未能完整加载。请刷新后重试。'],
    ['zh-TW', '清單在載入期間發生變化，未能完整載入。請重新整理後再試。'],
    [
      'ja',
      '読み込み中に一覧が変更されたため、すべての項目を読み込めませんでした。再読み込みしてお試しください。',
    ],
  ])(
    'explains incomplete pagination in %s without returning a partial list',
    async (language, message) => {
      await i18n.changeLanguage(language);
      // The failure copy loads after the first screen; readers that call
      // errorMessage() synchronously run once it is in.
      await loadErrorsNamespace();
      const fetchPage = vi.fn(async (page: number) => ({
        items: page === 1 ? [{ id: 'first' }] : [],
        total: 10,
        page,
        page_size: 1,
      }));
      const failure = await collectPages(fetchPage).catch(
        (error: unknown) => error,
      );
      expect(failure).toBeInstanceOf(LazuApiError);
      expect(failure).toMatchObject({ local: 'incomplete_page', code: '' });
      expect((failure as LazuApiError).status).toBeUndefined();
      expect(fetchPage.mock.calls).toEqual([[1], [2]]);
      expect(errorMessage(failure)).toBe(message);
      expect(errorMessage(failure, 'Generic load failure')).toBe(message);
    },
  );
  it('rejects fractional money before sending a payment', async () => {
    expect(validateTopUpAmount(10.99, 5, 500)).toBe('invalid');
    await expect(
      realProjectsClient.startTopup('p', 10.99, 'stripe', 'op'),
    ).rejects.toMatchObject({ code: 'invalid_amount' });
    expect(http.post).not.toHaveBeenCalled();
  });
  it('retains the caller operation ID and accepts already-paid replay', async () => {
    http.post
      .mockRejectedValueOnce(
        Object.assign(new Error('Network Error'), { code: 'ERR_NETWORK' }),
      )
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: { payment_status: 'paid', top_up_id: 'top-1' },
        },
      });
    // No response: the outcome is unknown, so the retry keeps the same id.
    await expect(
      realProjectsClient.startTopup('p', 10, 'stripe', 'stable-op'),
    ).rejects.toMatchObject({ local: 'network' });
    expect(
      await realProjectsClient.startTopup('p', 10, 'stripe', 'stable-op'),
    ).toMatchObject({
      top_up_id: 'top-1',
      payment_status: 'paid',
      pay_link: '',
    });
    for (const call of http.post.mock.calls)
      expect(call[2].headers['Idempotency-Key']).toBe('stable-op');
  });
  it('reads the catalog error envelope into one LazuApiError', async () => {
    http.post.mockRejectedValueOnce({
      response: {
        status: 409,
        headers: {},
        data: {
          success: false,
          error: {
            code: 'already_member',
            type: 'conflict_error',
            lazu: 'LZ-7619',
            message: 'This person is already a member of the project.',
            request_id: 'req_1',
            details: {},
          },
        },
      },
    });
    const failure = await realProjectsClient
      .createInvite('p', { email: 'a@example.com', role: 'member' })
      .catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(LazuApiError);
    expect(failure).toMatchObject({
      code: 'already_member',
      status: 409,
      lazu: 'LZ-7619',
      requestId: 'req_1',
    });
  });
  it('treats a checkout answer without a link as the provider being unavailable', async () => {
    http.post.mockResolvedValueOnce({
      data: { success: true, data: { top_up_id: 'top-2' } },
    });
    await expect(
      realProjectsClient.startTopup('p', 10, 'stripe', 'op-2'),
    ).rejects.toMatchObject({ code: 'payment_provider_unavailable' });
  });
  it('edits Key policy without claiming to replace unrelated restrictions', async () => {
    http.put.mockResolvedValue({ data: { success: true, data: {} } });
    await realProjectsClient.updateToken('key-1', 'p', {
      name: 'updated',
      expected_version: 7,
      model_limits: ['model'],
      cap_microusd: null,
    });
    expect(http.put).toHaveBeenCalledWith(
      '/api/token/',
      {
        id: 'key-1',
        project_id: 'p',
        name: 'updated',
        expected_version: 7,
        model_limits: ['model'],
        cap_microusd: null,
      },
      expect.objectContaining({ params: { policy_only: true } }),
    );
  });
  it('scopes rotation in the query and retains its operation id', async () => {
    http.post.mockResolvedValue({ data: { success: true, data: {} } });
    await realProjectsClient.rotateToken(
      'key-1',
      'joined-project',
      '24h',
      'rotation-op',
    );
    expect(http.post).toHaveBeenCalledWith(
      '/api/token/key-1/rotate',
      { old_expires: '24h' },
      expect.objectContaining({
        params: { project_id: 'joined-project' },
        headers: { 'Idempotency-Key': 'rotation-op' },
      }),
    );
  });
});

it('connection updates retain project and version without replacing budget policy', async () => {
  http.put.mockResolvedValue({
    data: { success: true, data: { id: 'key-1' } },
  });
  await realProjectsClient.updateTokenConnect('key-1', 'project-a', {
    expected_version: 3,
    product: 'codex',
    vendor_groups: { openai: 'cheap' },
  });
  expect(http.put).toHaveBeenCalledWith(
    '/api/token/',
    {
      id: 'key-1',
      project_id: 'project-a',
      expected_version: 3,
      product: 'codex',
      vendor_groups: { openai: 'cheap' },
    },
    expect.objectContaining({ params: { connect_only: true } }),
  );
});
