import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

test('inline key reveal keeps the table stable and opens no dialog', async ({
  page,
}) => {
  await setupMockedApp(page, {
    payloadFor: (url: string) => {
      if (url.endsWith('/api/token/1/reveal'))
        return {
          success: true,
          data: { key: 'sk-lazu-review-fixture-1234567890' },
        };
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');
  const secret = page.locator('.lazu-inline-secret').first();
  await expect(secret).toBeVisible();
  const before = await secret.boundingBox();
  await secret.getByRole('button', { name: '显示', exact: true }).click();
  await expect(secret.locator('input')).toHaveValue(
    'sk-lazu-review-fixture-1234567890',
  );
  expect(await secret.boundingBox()).toEqual(before);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await secret.getByRole('button', { name: '隐藏', exact: true }).click();
  await expect(secret.locator('input')).not.toHaveValue(
    'sk-lazu-review-fixture-1234567890',
  );
});

test('logout uses a dismissible nearby confirmation', async ({ page }) => {
  await setupMockedApp(page);
  await expectRouteReady(page, '/console/token');
  const trigger = page.locator('.sidebar-v2__logout');
  await trigger.click();
  await expect(
    page.getByRole('button', { name: '退出', exact: true }),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(
    page.getByRole('button', { name: '退出', exact: true }),
  ).toHaveCount(0);
  await trigger.click();
  await page.mouse.click(600, 40);
  await expect(
    page.getByRole('button', { name: '退出', exact: true }),
  ).toHaveCount(0);
  await expect(page).toHaveURL(/console\/token/);
});

for (const [name, route] of Object.entries({
  keys: 'token',
  usage: 'usage',
  users: 'organizations',
  health: 'health',
  models: 'model-ops',
  search: 'search-backend',
  wallet: 'wallet',
  settings: 'setting',
  overview: '',
  personal: 'personal',
  announcements: 'announcements',
})) {
  test(`capture approved ${name} layout`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 960 });
    const app = await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      payloadFor: (url: string) => {
        const path = new URL(url).pathname;
        if (path === '/api/user/self/identities')
          return {
            success: true,
            data: { has_password: true, identities: [] },
          };
        if (name === 'overview' && path === '/api/user/summary')
          return {
            success: true,
            data: { total_users: 28, low_balance_active: 2 },
          };
        if (name === 'overview' && path === '/api/data/dashboard')
          return {
            success: true,
            data: {
              totals: {
                amount_microusd: 125000000,
                request_count: 12400,
                outcome_count: 12500,
                error_count: 100,
                active_users: 12,
                p95_ttft_ms: 850,
                ttft_sample_count: 5000,
              },
              by_user: [
                {
                  id: 7,
                  name: 'Orbit demo',
                  amount_microusd: 75000000,
                  request_count: 7400,
                },
                {
                  id: 8,
                  name: 'Review fixture',
                  amount_microusd: 50000000,
                  request_count: 5000,
                },
              ],
              time_series: Array.from({ length: 7 }, (_, i) => ({
                bucket: Math.floor(Date.now() / 1000) - (6 - i) * 86400,
                amount_microusd: [
                  12000000, 17000000, 14000000, 20000000, 16000000, 21000000,
                  25000000,
                ][i],
              })),
            },
          };
        return undefined;
      },
    });
    await expectRouteReady(page, `/console/${route}`);
    await page.screenshot({
      path: `../../.local-results/screenshots/implementation/${name}.png`,
      fullPage: true,
    });
    await app.expectNoUnexpectedErrors();
  });
}

test('search credentials stay inline and delete can be canceled', async ({
  page,
}) => {
  let deletes = 0;
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string, request: import('@playwright/test').Request) => {
      const path = new URL(url).pathname;
      if (path === '/api/search/backends')
        return {
          success: true,
          data: [
            {
              id: 7,
              name: 'Jina',
              provider: 'jina',
              type: 102,
              status: 1,
              masked_key: 'jina-de••••••tail',
              last_used_at: 1788624000,
              capabilities: ['web_search'],
              priority: 10,
            },
          ],
        };
      if (path === '/api/search/backends/7/reveal')
        return { success: true, data: { key: 'jina-demo-review-key-tail' } };
      if (path === '/api/search/backends/7' && request.method() === 'DELETE') {
        deletes++;
        return { success: true };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/search-backend');
  const row = page.getByRole('row').filter({ hasText: 'Jina' });
  const field = row.locator('.lazu-inline-secret input');
  await expect(field).toHaveValue('jina-de••••••tail');
  const box = await row.boundingBox();
  await row.getByRole('button', { name: '显示', exact: true }).click();
  await expect(field).toHaveValue('jina-demo-review-key-tail');
  expect(await row.boundingBox()).toEqual(box);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.screenshot({
    path: '../../.local-results/screenshots/implementation/search-inline.png',
    animations: 'disabled',
  });
  await row.getByRole('button', { name: '删除', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.screenshot({
    path: '../../.local-results/screenshots/implementation/search-delete.png',
    animations: 'disabled',
  });
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(deletes).toBe(0);
});

test('personal settings offer no IP switch and editors dismiss without writing', async ({
  page,
}) => {
  const writes: unknown[] = [];
  await setupMockedApp(page, {
    payloadFor: (url: string, request: import('@playwright/test').Request) => {
      if (url.includes('/api/user/self/identities'))
        return { success: true, data: { has_password: true, identities: [] } };
      if (
        new URL(url).pathname === '/api/user/setting' &&
        request.method() === 'PUT'
      ) {
        writes.push(request.postDataJSON());
        return { success: true };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/personal');
  // Every request records the caller's IP; there is no switch to offer.
  await expect(page.getByRole('switch', { name: '记录请求 IP' })).toHaveCount(
    0,
  );
  await expect(page.locator('.personal-preferences-save')).toHaveCount(0);
  const passwordRow = page
    .locator('.set-row')
    .filter({ has: page.getByText('密码', { exact: true }) });
  await passwordRow.getByRole('button').click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.locator('#old-password')).toHaveAttribute(
    'autocomplete',
    'current-password',
  );
  await expect(page.locator('#new-password')).toHaveAttribute(
    'autocomplete',
    'new-password',
  );
  await expect(page.locator('#confirm-password')).toHaveAttribute(
    'autocomplete',
    'new-password',
  );
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(writes).toHaveLength(0);
});

test('announcement publication needs explicit confirmation and retains a rejected draft', async ({
  page,
}) => {
  let writes = 0;
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string, request: import('@playwright/test').Request) => {
      if (
        new URL(url).pathname === '/api/admin/announcements' &&
        request.method() === 'POST'
      ) {
        writes++;
        return fail(500, 'internal_error');
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/announcements');
  await page.getByPlaceholder('一句话说明这次更新').fill('模型更新审核');
  await page.locator('.ann-md__input').fill('测试公告内容');
  await page.getByRole('button', { name: '发布', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('模型更新审核');
  expect(writes).toBe(0);
  await page.mouse.click(4, 4);
  await expect(dialog).toHaveCount(0);
  await page.getByRole('button', { name: '发布', exact: true }).click();
  await dialog.getByRole('button', { name: '发布', exact: true }).click();
  await expect.poll(() => writes).toBe(1);
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByPlaceholder('一句话说明这次更新')).toHaveValue(
    '模型更新审核',
  );
});

test('editing a key reveals it in the existing dialog without submitting edits', async ({
  page,
}) => {
  let writes = 0;
  await setupMockedApp(page, {
    payloadFor: (url: string, request: import('@playwright/test').Request) => {
      if (url.endsWith('/api/token/1/reveal'))
        return {
          success: true,
          data: { key: 'sk-example-edit-dialog-fixture-1234567890' },
        };
      if (
        new URL(url).pathname === '/api/token/1' &&
        request.method() === 'GET'
      )
        return {
          success: true,
          data: {
            id: 1,
            name: 'Review API Key',
            remain_quota: 5000000,
            unlimited_quota: false,
            status: 1,
          },
        };
      if (new URL(url).pathname === '/api/token/' && request.method() === 'PUT')
        writes++;
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');
  await page
    .locator('tbody tr')
    .first()
    .getByRole('button', { name: '编辑', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toHaveCount(1);
  const field = dialog.locator('.lazu-inline-secret');
  await expect(field).toBeVisible();
  await field
    .getByRole('button', { name: '显示', exact: true })
    .click({ trial: true });
  const before = await field.boundingBox();
  await field.getByRole('button', { name: '显示', exact: true }).click();
  await expect(field.locator('input')).toHaveValue(
    'sk-example-edit-dialog-fixture-1234567890',
  );
  expect(await field.boundingBox()).toEqual(before);
  await expect(dialog).toHaveCount(1);
  expect(writes).toBe(0);
  await expect(dialog.locator('.lazu-modal-header')).toBeInViewport({
    ratio: 1,
  });
  await expect(dialog.locator('.lazu-modal-footer')).toBeInViewport({
    ratio: 1,
  });
  await page.screenshot({
    path: '../../.local-results/screenshots/implementation/keys-edit.png',
    animations: 'disabled',
  });
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  expect(writes).toBe(0);
});
