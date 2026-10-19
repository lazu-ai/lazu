import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

for (const path of ['/console/personal', '/console/wallet']) {
  test(`${path} shares the shell account request`, async ({ page }) => {
    await setupMockedApp(page);
    const requests: string[] = [];
    page.on('request', (request) =>
      requests.push(new URL(request.url()).pathname),
    );
    await expectRouteReady(page, path);
    expect(requests.filter((p) => p === '/api/user/self')).toHaveLength(1);
  });
}

test('hover starts catalog data before navigation and click reuses it', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console');
  const requests: string[] = [];
  page.on('request', (request) =>
    requests.push(new URL(request.url()).pathname),
  );
  const link = page.locator('a[href="/console/models"]').first();
  await link.hover();
  await expect
    .poll(() => requests.filter((p) => p === '/api/user/models/catalog').length)
    .toBe(1);
  expect(new URL(page.url()).pathname).toBe('/console');
  await link.click();
  await expect(page.locator('.plaza-card').first()).toBeVisible();
  await page.waitForLoadState('networkidle');
  expect(requests.filter((p) => p === '/api/user/models/catalog')).toHaveLength(
    1,
  );
});

test('model editor options wait until the inspector is opened', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  const requests: string[] = [];
  page.on('request', (request) =>
    requests.push(new URL(request.url()).pathname),
  );
  await expectRouteReady(page, '/console/model-ops');
  expect(requests).not.toContain('/api/prefill_group');
  await page
    .locator('.mo-ledger__row')
    .filter({ hasText: 'gpt-4o-mini' })
    .first()
    .click();
  await expect
    .poll(() => requests.filter((p) => p === '/api/prefill_group').length)
    .toBe(2);
});

test('prefetched model rows still save both source weights atomically', async ({
  page,
}) => {
  const writes: { path: string; data: unknown }[] = [];
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  page.on('request', (request) => {
    if (
      request.method() === 'PUT' &&
      new URL(request.url()).pathname === '/api/model_ops/models/tier_config'
    ) {
      writes.push({
        path: new URL(request.url()).pathname,
        data: request.postDataJSON(),
      });
    }
  });
  await expectRouteReady(page, '/console/model-ops');
  await page
    .locator('.mo-ledger__row')
    .filter({ hasText: 'gpt-4o-mini' })
    .first()
    .click();
  const editor = page.getByRole('dialog');
  await editor.getByRole('button', { name: '2 个来源', exact: true }).click();
  await editor.getByRole('button', { name: '1 个来源', exact: true }).click();
  await editor.getByLabel('OpenAI smoke channel 在本线路的权重').fill('71');
  await editor.getByLabel('Budget smoke channel 在本线路的权重').fill('31');
  await editor.getByRole('button', { name: '保存', exact: true }).click();
  expect(writes).toHaveLength(0);
  await page
    .getByRole('dialog')
    .last()
    .getByRole('button', { name: '确认保存', exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0].data).toMatchObject({
    model: 'gpt-4o-mini',
    tiers: [
      { lane: 'direct', weights: [{ channel_id: '1', weight: 71 }] },
      { lane: 'cheap', weights: [{ channel_id: '2', weight: 31 }] },
    ],
  });
});

test('model metadata cannot save while its deferred options are unanswered', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  let release = () => {};
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/prefill_group?*', async (route) => {
    await pending;
    await route.fallback();
  });
  try {
    await expectRouteReady(page, '/console/model-ops');
    await page
      .locator('.mo-ledger__row')
      .filter({ hasText: 'gpt-4o-mini' })
      .first()
      .click();
    await page.getByRole('tab', { name: '资料', exact: true }).click();
    const save = page
      .getByRole('dialog')
      .getByRole('button', { name: '保存', exact: true });
    await expect(save).toBeDisabled();
    release();
    await expect(save).toBeEnabled();
  } finally {
    release();
  }
});

test('background account refresh preserves a notification draft', async ({
  page,
}) => {
  await page.clock.install();
  await setupMockedApp(page);
  await expectRouteReady(page, '/console/personal');
  await page
    .locator('.set-row')
    .filter({ hasText: '余额提醒' })
    .getByRole('button', { name: '编辑', exact: true })
    .click();
  await page.locator('#warningThreshold').fill('37');
  const user = await page.evaluate(
    () =>
      JSON.parse(localStorage.getItem('user') || '{}') as Record<
        string,
        unknown
      >,
  );
  let reads = 0;
  await page.route('**/api/user/self', (route) => {
    reads++;
    return route.fulfill({
      json: {
        success: true,
        data: {
          ...user,
          quota: 777,
          setting: JSON.stringify({ quota_warning_threshold: 123000000 }),
        },
      },
    });
  });
  await page.clock.fastForward(61000);
  await page.evaluate(() =>
    window.dispatchEvent(new Event('visibilitychange')),
  );
  await expect.poll(() => reads).toBe(1);
  await expect(page.locator('#warningThreshold')).toHaveValue('37');
});

test('same-account login after logout reads fresh session data immediately', async ({
  page,
}) => {
  const user = {
    id: 1,
    username: 'review-user',
    role: 100,
    quota: 100,
    projects_enabled: false,
  };
  let signedOut = false;
  let selfReads = 0;
  await setupMockedApp(page, {
    language: 'en',
    user,
    payloadFor: (raw: string) => {
      const path = new URL(raw).pathname;
      if (path === '/api/auth/oidc/status')
        return {
          success: true,
          data: { password_login: true, email_code_auth: false },
        };
      if (path === '/api/user/logout') {
        signedOut = true;
        return { success: true };
      }
      // Login is intentionally the same identity. Its payload need not include
      // the latest memberships/flags: the session's self read supplies those.
      if (path === '/api/user/login') return { success: true, data: user };
      if (path === '/api/user/self') {
        selfReads++;
        return {
          success: true,
          data: { ...user, quota: signedOut ? 777 : 100 },
        };
      }
    },
  });
  await expectRouteReady(page, '/console/personal');
  await expect.poll(() => selfReads).toBe(1);
  await page.locator('.console-topbar__avatar').click();
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.locator('#auth-identifier').fill('review-user');
  await page.locator('#auth-password').fill('test-password');
  await page.locator('button[type="submit"]').click();
  await expect(page).toHaveURL(/\/console/);
  await expect.poll(() => selfReads).toBe(2);
  await expect
    .poll(() =>
      page.evaluate(
        () => JSON.parse(localStorage.getItem('user') || '{}').quota,
      ),
    )
    .toBe(777);
});

test('notification editing waits for account settings and retries a failed read', async ({
  page,
}) => {
  const stored = {
    notify_type: 'webhook',
    quota_warning_threshold: 37000000,
    webhook_url: 'https://example.com/test-hook',
    webhook_secret: 'test-secret',
  };
  const user = { id: 1, username: 'review-user', role: 100, quota: 100 };
  await setupMockedApp(page, { user });
  let release = () => {};
  const hold = new Promise<void>((resolve) => {
    release = resolve;
  });
  let fail = true;
  const writes: unknown[] = [];
  await page.route('**/api/user/self', async (route) => {
    await hold;
    if (fail)
      return route.fulfill({
        status: 503,
        json: { success: false, message: 'Unavailable' },
      });
    return route.fulfill({
      json: {
        success: true,
        data: { ...user, setting: JSON.stringify(stored) },
      },
    });
  });
  await page.route('**/api/user/setting', async (route) => {
    writes.push(route.request().postDataJSON());
    await route.fulfill({ json: { success: true } });
  });
  try {
    await expectRouteReady(page, '/console/personal');
    const row = page.locator('.set-row').filter({ hasText: '余额提醒' });
    await expect(
      row.getByRole('button', { name: '编辑', exact: true }),
    ).toBeDisabled();
    expect(writes).toHaveLength(0);
    release();
    await expect(
      row.getByRole('button', { name: '重试', exact: true }),
    ).toBeVisible();
    fail = false;
    await row.getByRole('button', { name: '重试', exact: true }).click();
    await row.getByRole('button', { name: '编辑', exact: true }).click();
    await expect(page.locator('#warningThreshold')).toHaveValue('37');
    await expect(page.locator('#webhookUrl')).toHaveValue(stored.webhook_url);
    await expect(page.locator('#webhookSecret')).toHaveValue(
      stored.webhook_secret,
    );
    await page.locator('#warningThreshold').fill('38');
    await page
      .locator('form')
      .filter({ has: page.locator('#warningThreshold') })
      .getByRole('button', { name: '保存', exact: true })
      .click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0]).toMatchObject({
      ...stored,
      quota_warning_threshold: 38000000,
    });
  } finally {
    release();
  }
});

test('personal settings reserve notification space before revealing a slow read', async ({
  page,
}) => {
  await page.clock.install();
  await page.clock.pauseAt(new Date());
  await setupMockedApp(page);
  let release = () => {};
  const hold = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/user/self', async (route) => {
    await hold;
    await route.fallback();
  });
  try {
    await page.goto('/console/personal');
    const row = page
      .locator('.set-row')
      .filter({ has: page.getByText('余额提醒', { exact: true }) });
    const placeholder = row.locator('.lazu-pending');
    await expect(placeholder).toBeAttached();
    await expect(placeholder).toHaveCSS('visibility', 'hidden');
    const waiting = await row.boundingBox();
    await page.clock.runFor(299);
    await expect(placeholder).toHaveCSS('visibility', 'hidden');
    await page.clock.runFor(1);
    await expect(placeholder).toBeVisible();
    release();
    await expect(
      row.getByRole('button', { name: '编辑', exact: true }),
    ).toBeEnabled();
    const ready = await row.boundingBox();
    expect(waiting?.height).toBe(ready?.height);
  } finally {
    release();
  }
});

test('digest previews start their own reveal delay after the route has settled', async ({
  page,
}) => {
  await page.clock.install();
  await page.clock.pauseAt(new Date());
  await setupMockedApp(page);
  let release = () => {};
  const hold = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/user/self/digest-preview?*', async (route) => {
    await hold;
    await route.fulfill({
      json: {
        success: true,
        data: { subject: 'Preview', html: '<p>Preview content</p>' },
      },
    });
  });
  try {
    await page.goto('/console/personal');
    await expect(
      page.getByRole('button', { name: '预览', exact: true }),
    ).toBeVisible();
    await page.clock.runFor(1000);
    await page.getByRole('button', { name: '预览', exact: true }).click();
    const pending = page.getByRole('dialog').locator('.lazu-pending-block');
    await expect(pending).toHaveCSS('visibility', 'hidden');
    await page.clock.runFor(299);
    await expect(pending).toHaveCSS('visibility', 'hidden');
    await page.clock.runFor(1);
    await expect(pending).toBeVisible();
    await page.keyboard.press('Escape');
    await page.clock.runFor(500);
    await page.getByRole('button', { name: '预览月报', exact: true }).click();
    await expect(pending).toHaveCSS('visibility', 'hidden');
    await page.clock.runFor(300);
    await expect(pending).toBeVisible();
    release();
    await page.clock.resume();
    await expect(page.getByRole('dialog').locator('iframe')).toBeVisible();
  } finally {
    release();
  }
});
