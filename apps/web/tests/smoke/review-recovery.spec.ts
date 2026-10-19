import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

for (const [route, api] of [['/console/usage?tab=files', '/api/files']]) {
  test(`existing table ${route} retains its recovery action`, async ({
    page,
  }) => {
    await setupMockedApp(page);
    await expectRouteReady(page, route);
    const retry = page.getByRole('button', { name: '刷新', exact: true });
    await expect(retry).toBeEnabled();
    const read = page.waitForRequest(
      (r) => r.method() === 'GET' && new URL(r.url()).pathname === api,
    );
    await retry.click();
    await read;
  });
}

test('overview low-balance task opens users with the matching server filter', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/user/summary'
        ? { success: true, message: '', data: { low_balance_active: 3 } }
        : undefined,
  });
  await expectRouteReady(page, '/console');
  const request = page.waitForRequest((r) => {
    const url = new URL(r.url());
    return (
      url.pathname === '/api/user/' &&
      url.searchParams.get('filter') === 'low_balance_active'
    );
  });
  await page.getByRole('link', { name: /低余额用户仍在消费/ }).click();
  await request;
  await expect(page).toHaveURL(
    /console\/organizations\?filter=low_balance_active/,
  );
});

for (const target of [
  {
    route: 'health',
    api: '/api/channel_health',
    error: '状态读取失败',
    content: '.chealth-mcard',
  },
  {
    route: 'announcements',
    api: '/api/admin/announcements',
    error: '公告读取失败',
    content: '.ann-list__item',
  },
]) {
  test(`${target.route} has an explicit retry after an initial read failure`, async ({
    page,
  }) => {
    let failed = true;
    await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      payloadFor: (url: string) =>
        new URL(url).pathname === target.api && failed
          ? fail(500, 'internal_error')
          : new URL(url).pathname === '/api/admin/announcements'
            ? {
                success: true,
                data: {
                  items: [
                    {
                      id: 9,
                      title: 'Recovered announcement',
                      kind: 'operational',
                      content: 'Recovered',
                      created_at: 1788600000,
                    },
                  ],
                  next_before_id: null,
                },
              }
            : undefined,
    });
    await page.goto(`/console/${target.route}`);
    const error = page.getByRole('alert').filter({ hasText: target.error });
    await expect(error).toBeVisible({ timeout: 15000 });
    if (target.route === 'announcements')
      await expect(page.getByText('没有匹配的公告')).toHaveCount(0);
    failed = false;
    await error.getByRole('button', { name: '重试', exact: true }).click();
    await expect(error).toHaveCount(0);
    await expect(page.locator(target.content).first()).toBeVisible();
  });
}

test('cache-only pricing on an unpriced lane survives preview and atomic save', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console/model-ops');
  await page
    .locator('tbody tr', { hasText: 'gemini-image-4' })
    .first()
    .getByRole('button', { name: '售价与来源' })
    .click();
  const expanded = page.locator('.model-ops__row--expand');
  await expanded
    .locator('.model-ops__tier-card')
    .first()
    .getByLabel('缓存读取', { exact: true })
    .fill('0.25');
  await expanded.getByRole('button', { name: '预览改动' }).click();
  const request = page.waitForRequest(
    (r) =>
      r.method() === 'PUT' &&
      new URL(r.url()).pathname === '/api/model_ops/models/tier_config',
  );
  await page
    .getByRole('dialog')
    .getByRole('button', { name: '确认保存' })
    .click();
  expect((await request).postDataJSON()).toMatchObject({
    model: 'gemini-image-4',
    tiers: [{ lane: 'direct', price: { cache_read_per_mtok: 0.25 } }],
  });
});

test('account deletion clears canceled confirmation and stays open while a request is pending', async ({
  page,
}) => {
  await setupMockedApp(page);
  let finish: () => void = () => {};
  const pending = new Promise<void>((resolve) => {
    finish = resolve;
  });
  await page.route('**/api/user/self', async (route) => {
    if (route.request().method() !== 'DELETE') return route.fallback();
    await pending;
    await route.fulfill({
      status: 500,
      json: {
        success: false,
        error: {
          code: 'internal_error',
          type: 'api_error',
          request_id: 'req_mock',
        },
      },
    });
  });
  await expectRouteReady(page, '/console/personal');
  const trigger = page.getByRole('button', { name: '删除账号…', exact: true });
  const dialog = page.getByRole('dialog', { name: '删除账号', exact: true });
  for (const dismiss of ['escape', 'outside']) {
    await trigger.click();
    await dialog.locator('#delete-confirm').fill('admin');
    if (dismiss === 'escape') await page.keyboard.press('Escape');
    else await page.mouse.click(10, 10);
    await expect(dialog).toHaveCount(0);
  }
  await trigger.click();
  await expect(dialog.locator('#delete-confirm')).toHaveValue('');
  await expect(dialog).toHaveCSS('opacity', '1');
  await page.screenshot({
    path: '../../.local-results/screenshots/implementation/personal-delete.png',
  });
  await expect(dialog.getByRole('button', { name: '永久删除' })).toBeDisabled();
  await dialog.locator('#delete-confirm').fill('admin');
  const deletion = page.waitForRequest(
    (r) =>
      r.method() === 'DELETE' && new URL(r.url()).pathname === '/api/user/self',
  );
  await dialog.getByRole('button', { name: '永久删除' }).click();
  await deletion;
  await expect(
    dialog.getByRole('button', { name: '取消', exact: true }),
  ).toBeDisabled();
  await page.keyboard.press('Escape');
  await page.mouse.click(10, 10);
  await expect(dialog).toBeVisible();
  finish();
  await expect(
    dialog.getByRole('button', { name: '取消', exact: true }),
  ).toBeEnabled();
  await dialog.getByRole('button', { name: '取消', exact: true }).click();
  await expect(dialog).toHaveCount(0);
});
