// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const orgs = [
  {
    id: 'usr_grace',
    internal_id: 2,
    username: 'grace',
    display_name: 'grace',
    email: 'owner@example.com',
    role: 1,
    status: 1,
    price_plan_id: 0,
    quota: 100_000_000,
    used_quota: 27_460_000,
    token_count: 6,
    recent_spend_microusd: 27_460_000,
    recent_spend_days: 30,
  },
  {
    id: 'usr_demo',
    internal_id: 4,
    username: 'demo_user',
    display_name: 'demo_user',
    email: 'demo@qq.com',
    role: 1,
    status: 1,
    price_plan_id: 3,
    price_plan_name: '合作方案 A',
    quota: 3_200_000,
    used_quota: 5_310_000,
    token_count: 1,
    recent_spend_microusd: 5_310_000,
    recent_spend_days: 30,
  },
  {
    id: 'usr_grace123',
    internal_id: 6,
    username: 'grace123',
    display_name: 'grace123',
    email: 'p123@mail.com',
    role: 1,
    status: 2,
    price_plan_id: 3,
    price_plan_name: '合作方案 A',
    quota: 0,
    used_quota: 0,
    token_count: 0,
    recent_spend_microusd: 0,
    recent_spend_days: 30,
  },
];

const listPayload = (items, total = items.length) => ({
  success: true,
  message: '',
  data: { items, page: 1, page_size: 20, total },
});

function orgPayloadFor(requestedUrls) {
  return (url) => {
    const parsed = new URL(url);
    if (parsed.pathname === '/api/price-plans') {
      return { success: true, message: '', data: [] };
    }
    if (
      parsed.pathname === '/api/user/' ||
      parsed.pathname === '/api/user/search'
    ) {
      requestedUrls.push(`${parsed.pathname}?${parsed.searchParams}`);
      if (parsed.pathname === '/api/user/search') {
        return listPayload(
          orgs.filter((org) =>
            org.username.includes(parsed.searchParams.get('keyword') || ''),
          ),
        );
      }
      // 42 total exercises the pagination footer without 42 fixtures.
      return listPayload(orgs, 42);
    }
    return undefined;
  };
}

test('organizations ledger pages and searches server-side', async ({
  page,
}) => {
  const requestedUrls = [];
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: orgPayloadFor(requestedUrls),
  });

  await expectRouteReady(page, '/console/organizations');

  const root = page.locator('#root');
  await expect(root).toContainText('owner@example.com');
  // Balance and the trailing-window spend both render as USD.
  await expect(root).toContainText('$100.00');
  await expect(root).toContainText('$27.46');
  // The ledger shows the runway from recent spend; grace123 is frozen.
  await expect(root).toContainText('≈ 18 天');
  await expect(root).toContainText('已冻结');
  // Footer summary reports the real server-side total, not the page length.
  await expect(root).toContainText('42');

  // The first load must be a single paged request — not a 20-page fan-out.
  expect(requestedUrls).toHaveLength(1);
  expect(requestedUrls[0]).toContain('page_size=20');
  expect(requestedUrls[0]).toContain('with_usage=true');

  // Searching goes to the server-side search endpoint.
  await page.getByRole('textbox', { name: '搜索', exact: true }).fill('demo');
  await page.getByRole('textbox', { name: '搜索', exact: true }).press('Enter');
  await expect(root).not.toContainText('owner@example.com');
  await expect(root).toContainText('demo@qq.com');
  expect(
    requestedUrls.some(
      (url) => url.startsWith('/api/user/search') && url.includes('demo'),
    ),
  ).toBe(true);

  await app.expectNoUnexpectedErrors();
});

test('account usage carries platform scope despite a saved personal selection', async ({
  page,
}) => {
  const logRequests = [];
  const orgPayload = orgPayloadFor([]);
  await setupMockedApp(page, {
    consoleScope: 'workspace:default',
    payloadFor: (raw) => {
      const url = new URL(raw);
      if (url.pathname.startsWith('/api/log/')) logRequests.push(url);
      return orgPayload(raw);
    },
  });
  await expectRouteReady(page, '/console/organizations');
  await page
    .locator('tbody tr', { hasText: 'owner@example.com' })
    .first()
    .click();
  const usage = page.locator('.acc-drawer a[href^="/console/usage?"]').first();
  await expect(usage).toHaveAttribute('href', /scope=platform/);
  await page.goto(await usage.getAttribute('href'));
  await expect
    .poll(() =>
      logRequests.some(
        (url) =>
          url.pathname === '/api/log/' &&
          url.searchParams.get('username') === 'grace' &&
          url.searchParams.get('user_id') === '2',
      ),
    )
    .toBe(true);
  expect(
    logRequests.some((url) => url.pathname.startsWith('/api/log/self/')),
  ).toBe(false);
});

test('organizations ledger exposes create and delete', async ({ page }) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: orgPayloadFor([]),
  });

  await expectRouteReady(page, '/console/organizations');

  await page.getByRole('button', { name: '新建用户' }).click();
  await expect(page.getByText('登录名')).toBeVisible();
  await page.getByRole('button', { name: '取消' }).click();

  // Deleting an account is an explicit drawer action with confirmation.
  await page
    .locator('tbody tr', { hasText: 'owner@example.com' })
    .first()
    .click();
  await page
    .locator('.acc-drawer')
    .getByRole('button', { name: '删除', exact: true })
    .click();
  await expect(page.getByRole('button', { name: '确认删除' })).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

test('organization balance adjustments submit an atomic signed delta', async ({
  page,
}) => {
  const requests = [];
  const detailReads = [];
  const basePayload = orgPayloadFor([]);
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url, request) => {
      const parsed = new URL(url);
      if (parsed.pathname === '/api/user/usr_grace/quota/adjust') {
        requests.push({
          body: request.postDataJSON(),
          method: request.method(),
        });
        return {
          success: true,
          message: '',
          data: { delta_microusd: -1_250_000 },
        };
      }
      if (
        request.method() === 'GET' &&
        parsed.pathname === '/api/user/usr_grace'
      ) {
        detailReads.push(parsed.pathname);
      }
      return basePayload(url, request);
    },
  });

  await expectRouteReady(page, '/console/organizations');

  const row = page
    .locator('tbody tr', { hasText: 'owner@example.com' })
    .first();
  await row.click();
  await page.locator('.orgs__balance .lazu-select-trigger').click();
  await page.getByRole('option', { name: '扣减 −' }).click();
  await page.getByPlaceholder('100.00').fill('1.25');
  await page.getByPlaceholder('例如：线下转账 #77').fill('并发安全扣减');
  await page.getByRole('button', { name: '执行调整' }).click();

  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0]).toEqual({
    body: { delta_microusd: -1_250_000, reason: '并发安全扣减' },
    method: 'POST',
  });
  expect(detailReads).toHaveLength(0);

  await app.expectNoUnexpectedErrors();
});
