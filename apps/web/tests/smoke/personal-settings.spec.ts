// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('personal settings route renders for an authenticated user', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/personal');
  await app.expectNoUnexpectedErrors();
});

test('setting the first password refreshes the connected sign-in methods', async ({
  page,
}) => {
  let identityReads = 0;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (
        pathname === '/api/user/self/identities' &&
        request.method() === 'GET'
      ) {
        identityReads += 1;
        return {
          success: true,
          message: '',
          data: {
            has_password: identityReads > 1,
            identities: [],
          },
        };
      }
      if (pathname === '/api/user/self' && request.method() === 'PUT') {
        return { success: true, message: '', data: {} };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console/personal');

  const identitySection = page
    .locator('.set-card')
    .filter({ has: page.locator('.set-card__t', { hasText: /^账号$/ }) });
  await expect(identitySection).toContainText('未设置');
  await identitySection.getByRole('button', { name: '设置密码' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page
    .getByRole('dialog')
    .getByLabel('新密码', { exact: true })
    .fill('Lazu-2026-password');
  await page
    .getByRole('dialog')
    .getByLabel('确认密码', { exact: true })
    .fill('Lazu-2026-password');
  await page.getByRole('dialog').getByRole('button', { name: '提交' }).click();

  await expect.poll(() => identityReads).toBeGreaterThan(1);
  await expect(
    identitySection.getByText('已设置', { exact: true }),
  ).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('personal settings rejects passwords beyond the bcrypt byte limit locally', async ({
  page,
}) => {
  let passwordUpdates = 0;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (
        pathname === '/api/user/self/identities' &&
        request.method() === 'GET'
      ) {
        return {
          success: true,
          message: '',
          data: { has_password: false, identities: [] },
        };
      }
      if (pathname === '/api/user/self' && request.method() === 'PUT') {
        passwordUpdates += 1;
        return { success: true, message: '', data: {} };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console/personal');
  const identitySection = page
    .locator('.set-card')
    .filter({ has: page.locator('.set-card__t', { hasText: /^账号$/ }) });
  await identitySection.getByRole('button', { name: '设置密码' }).click();
  await page
    .getByRole('dialog')
    .getByLabel('新密码', { exact: true })
    .fill('😀'.repeat(19));
  await page
    .getByRole('dialog')
    .getByLabel('确认密码', { exact: true })
    .fill('😀'.repeat(19));
  await page.getByRole('dialog').getByRole('button', { name: '提交' }).click();

  await expect(
    page.getByText('密码的 UTF-8 编码不得超过 72 字节'),
  ).toBeVisible();
  expect(passwordUpdates).toBe(0);
  await app.expectNoUnexpectedErrors();
});

test('identity state fails closed when the response is incomplete', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (
        pathname === '/api/user/self/identities' &&
        request.method() === 'GET'
      ) {
        return { success: true, message: '', data: {} };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console/personal');
  const identitySection = page
    .locator('.set-card')
    .filter({ has: page.locator('.set-card__t', { hasText: /^账号$/ }) });
  await expect(identitySection).toContainText('加载用户数据失败。');
  await expect(
    identitySection.getByRole('button', { name: '重试' }),
  ).toBeVisible();
  await expect(identitySection).not.toContainText('已设置');
  await app.expectNoUnexpectedErrors();
});

test('external identities stay read-only until secure unlinking exists', async ({
  page,
}) => {
  let identityDeletes = 0;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (
        pathname === '/api/user/self/identities' &&
        request.method() === 'GET'
      ) {
        return {
          success: true,
          message: '',
          data: {
            has_password: true,
            identities: [
              {
                email: 'grace@example.com',
                label: 'Google',
                linked_at: 1_700_000_000,
                provider: 'google',
                // The frontend must remain fail closed even against an older or
                // inconsistent API response that still advertises removability.
                removable: true,
              },
            ],
          },
        };
      }
      if (
        pathname === '/api/user/self/identities/google' &&
        request.method() === 'DELETE'
      ) {
        identityDeletes += 1;
        return { success: true, message: '' };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console/personal');
  const identitySection = page
    .locator('.set-card')
    .filter({ has: page.locator('.set-card__t', { hasText: /^账号$/ }) });
  await expect(identitySection).toContainText('暂不支持解除已绑定的登录方式。');
  await expect(
    identitySection.getByRole('button', { name: '解除绑定' }),
  ).toHaveCount(0);
  expect(identityDeletes).toBe(0);
  await app.expectNoUnexpectedErrors();
});

test('personal settings stay within a narrow mobile viewport', async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 720 });
  const app = await setupMockedApp(page);

  await expectRouteReady(page, '/console/personal');
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth);

  const firstRow = page.locator('.set-row').first();
  await expect(firstRow).toBeVisible();
  const columns = await firstRow.evaluate(
    (element) => getComputedStyle(element).gridTemplateColumns,
  );
  expect(columns.trim().split(/\s+/)).toHaveLength(1);
  await app.expectNoUnexpectedErrors();
});

test('Telegram reminders link the account through a one-time Telegram link', async ({
  page,
}) => {
  let linked = false;
  let linkRequests = 0;
  const app = await setupMockedApp(page, {
    user: {
      setting: JSON.stringify({
        notify_type: 'telegram',
        quota_warning_threshold: 5,
      }),
    },
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/user/telegram' && request.method() === 'GET') {
        return {
          success: true,
          message: '',
          data: linked
            ? { available: true, linked: true, linked_at: 1761100000 }
            : { available: true, linked: false },
        };
      }
      if (
        pathname === '/api/user/telegram/link' &&
        request.method() === 'POST'
      ) {
        linkRequests += 1;
        return {
          success: true,
          message: '',
          data: {
            url: 'https://t.me/lazu_help_bot?start=link_smoke',
            expires_in: 600,
          },
        };
      }
      return undefined;
    },
  });
  await page.route('https://t.me/**', (route) =>
    route.fulfill({ status: 200, body: 'telegram' }),
  );

  await expectRouteReady(page, '/console/personal');
  await expect(page.getByText('通过Telegram提醒')).toBeVisible();
  const row = page.locator('.set-row').filter({ hasText: '通过Telegram提醒' });
  await row.getByRole('button', { name: '编辑' }).click();

  const link = page.getByRole('button', { name: '绑定 Telegram' });
  await expect(link).toBeVisible();
  const popup = page.waitForEvent('popup');
  await link.click();
  expect((await popup).url()).toBe(
    'https://t.me/lazu_help_bot?start=link_smoke',
  );
  expect(linkRequests).toBe(1);
  await expect(
    page.getByText('在 Telegram 里点「开始」完成绑定…'),
  ).toBeVisible();

  // The bot completes the link; the field notices on its next poll.
  linked = true;
  await expect(page.getByText(/已绑定 Telegram/)).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByRole('button', { name: '解绑' })).toBeVisible();
  await app.expectNoUnexpectedErrors();
});
