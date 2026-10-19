import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';
import type { AgentSessionDetail } from '../../src/hooks/usage-logs/useAgentSessions';

const detail: AgentSessionDetail = {
  first_message: '',
  models: [],
  threads: [],
  requests: [],
  requests_retained: true,
  requests_available: 2,
  requests_complete: true,
  session: {
    id: 'ses_integrity',
    session_id: 'test-session',
    source: 'codex',
    client: 'codex',
    client_version: 'test',
    title: 'Pagination example',
    token_name: 'test',
    started_at: 100,
    last_at: 110,
    request_count: 2,
    error_count: 0,
    last_error_at: 0,
    amount_microusd: 1000,
    cached_tokens: 0,
    compact_count: 0,
    compared_microusd: 0,
    completion_tokens: 20,
    official_microusd: 0,
    prompt_tokens: 100,
    review_count: 0,
    subagent_count: 0,
    subagent_requests: 0,
  },
};
const request = (id: string, at: number) => ({
  request_id: id,
  created_at: at,
  duration_seconds: 2,
  thread_id: '',
  kind: 'main',
  model_name: 'example-model',
  amount_microusd: 500,
  prompt_tokens: 50,
  completion_tokens: 10,
  cached_tokens: 0,
  failed: false,
});

for (const role of [100, 1]) {
  test(`session navigation retains authorized scope for role ${role}`, async ({
    page,
  }) => {
    const paths: string[] = [];
    const app = await setupMockedApp(page, {
      user: { role },
      consoleScope: 'workspace:default',
      payloadFor: (raw: string) => {
        const url = new URL(raw);
        if (!url.pathname.includes('/sessions')) return undefined;
        paths.push(url.pathname);
        if (url.pathname.endsWith('/sessions/ses_integrity'))
          return { success: true, data: detail };
        if (url.pathname.endsWith('/sessions'))
          return {
            success: true,
            data: {
              page: {
                items: [
                  {
                    ...detail.session,
                    models: [],
                    tokens: 120,
                    user_id: 42,
                    username: 'customer-42',
                  },
                ],
                total: 1,
                page: 1,
                page_size: 20,
              },
              summary: {
                ...detail.session,
                sessions: 1,
                tokens: 120,
                failed_sessions: 0,
                subagents: 0,
                median_amount_microusd: 1000,
                median_duration_seconds: 10,
                median_requests: 2,
              },
            },
          };
        return undefined;
      },
    });
    await page.goto('/console/usage?scope=platform&view=sessions');
    await page.locator('.sv-time').first().click();
    await expect(page).toHaveURL(
      new RegExp(
        `/console/usage/sessions/ses_integrity\\?scope=${role === 100 ? 'platform' : 'self'}$`,
      ),
    );
    const base = role === 100 ? '/api/log' : '/api/log/self';
    await expect
      .poll(() => paths.includes(`${base}/sessions/ses_integrity`))
      .toBe(true);
    expect(
      paths.some((path) =>
        role === 100
          ? path.startsWith('/api/log/self/')
          : !path.startsWith('/api/log/self/'),
      ),
    ).toBe(false);
    await page
      .locator('.ses-crumbs')
      .getByRole('link', { name: 'Agent 会话', exact: true })
      .click();
    await expect(page).toHaveURL(
      new RegExp(`scope=${role === 100 ? 'platform' : 'self'}&view=sessions`),
    );
    await expect(page.locator('.sv-time').first()).toBeVisible();
    await app.expectNoUnexpectedErrors();
  });
}

test('session details keep coverage visible until all request pages arrive', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (raw: string) => {
      const url = new URL(raw);
      if (!url.pathname.endsWith('/sessions/ses_integrity')) return undefined;
      const next = Boolean(url.searchParams.get('request_cursor'));
      return {
        success: true,
        data: {
          ...detail,
          requests: [request(next ? 'second' : 'first', next ? 110 : 100)],
          next_request_cursor: next ? undefined : '100:1',
        },
      };
    },
  });
  await expectRouteReady(page, '/console/usage/sessions/ses_integrity');
  await expect(
    page.getByText('已加载 1 次调用，可读取 2 次，会话合计 2 次。', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(page.locator('.sa-grid')).toHaveCount(0);
  await page.getByRole('button', { name: '加载更多', exact: true }).click();
  await expect(
    page.getByText('缺失或未加载的明细不用于全程分析。', { exact: false }),
  ).toHaveCount(0);
  await expect(page.locator('#requests')).toContainText('2');
  await app.expectNoUnexpectedErrors();
});

test('retained subset never becomes a complete-session analysis', async ({
  page,
}) => {
  await setupMockedApp(page, {
    payloadFor: (raw: string) =>
      new URL(raw).pathname.endsWith('/sessions/ses_integrity')
        ? {
            success: true,
            data: {
              ...detail,
              requests_available: 1,
              requests_complete: false,
              requests: [request('retained', 110)],
            },
          }
        : undefined,
  });
  await expectRouteReady(page, '/console/usage/sessions/ses_integrity');
  await expect(
    page.getByText('已加载 1 次调用，可读取 1 次，会话合计 2 次。', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: '加载更多', exact: true }),
  ).toHaveCount(0);
  await expect(page.locator('.sa-grid')).toHaveCount(0);
});
