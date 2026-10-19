import { expect, test, type Request } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

test('platform settings remain interactive and revert to the loaded value', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url: string, request: Request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/option/' && request.method() === 'GET') {
        return {
          success: true,
          message: '',
          data: [{ key: 'RegisterEnabled', value: 'false' }],
        };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console/setting?tab=accounts');

  const registrationRow = page
    .locator('.set-row')
    .filter({ hasText: '允许注册' });
  const registrationSwitch = registrationRow.getByRole('switch');
  await expect(registrationSwitch).toHaveAttribute('aria-checked', 'false');

  await registrationSwitch.click();
  await expect(page.getByRole('button', { name: '撤销' })).toBeVisible();
  await page.getByRole('button', { name: '撤销' }).click();
  await expect(registrationSwitch).toHaveAttribute('aria-checked', 'false');
  await expect(page.getByRole('button', { name: '撤销' })).toHaveCount(0);

  // Each group is a real panel: selecting it swaps the mounted section, so the
  // check is the selected tab plus the section that came with it.
  for (const [tabName, group] of [
    ['充值与计费', 'billing'],
    ['站点与邮件', 'branding'],
    ['请求与上游', 'upstream'],
    ['日志与监控', 'observability'],
    ['注册与访问', 'accounts'],
  ]) {
    await page.getByRole('tab', { name: tabName, exact: true }).click();
    await expect(
      page.getByRole('tab', { name: tabName, exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    // Only the selected group is mounted, so its section is the proof the
    // click swapped panels rather than scrolling a long document.
    await expect(page.locator(`#settings-group-${group}`)).toBeVisible();
    await expect(page.locator('.settings-continuous-group')).toHaveCount(1);
  }

  await app.expectNoUnexpectedErrors();
});

test('one save keeps every draft in the group and retries a failed field', async ({
  page,
}) => {
  // The five groups are tabs now, so only the open one is mounted: the drafts
  // this protects are the ones inside a single group. A rejected write must
  // still leave both drafts on screen and stay retryable.
  const values: Record<string, string> = {
    PreConsumedQuota: '0',
    QuotaForNewUser: '0',
  };
  const writes: Array<{ key: string; value: string }> = [];
  let rejectQuota = true;
  await setupMockedApp(page, {
    payloadFor: (url: string, request: Request) => {
      if (new URL(url).pathname !== '/api/option/') return undefined;
      if (request.method() === 'PUT') {
        const entry = request.postDataJSON() as { key: string; value: string };
        writes.push(entry);
        if (entry.key === 'QuotaForNewUser' && rejectQuota)
          return fail(500, 'internal_error');
        values[entry.key] = entry.value;
        return { success: true };
      }
      return {
        success: true,
        data: Object.entries(values).map(([key, value]) => ({ key, value })),
      };
    },
  });
  await expectRouteReady(page, '/console/setting?tab=billing');
  await page.locator('#QuotaForNewUser').fill('12345');
  await page.locator('#PreConsumedQuota').fill('678');
  expect(writes).toHaveLength(0);
  // Unsaved work rides in a floating bar now, not a wrapper under the groups.
  const save = page
    .locator('.set-unsaved')
    .getByRole('button', { name: '保存', exact: true });
  await expect(save).toHaveCount(1);
  await save.click();
  await expect.poll(() => writes.length).toBeGreaterThan(0);
  await expect(page.locator('#QuotaForNewUser')).toHaveValue('12345');
  await expect(page.locator('#PreConsumedQuota')).toHaveValue('678');
  await expect(save).toBeEnabled();
  rejectQuota = false;
  await save.click();
  // Edited in USD, stored in microUSD: $12,345 is 12,345,000,000.
  await expect.poll(() => values.QuotaForNewUser).toBe('12345000000');
  await expect(save).toHaveCount(0);
});

test('switching group with unsaved changes asks before discarding them', async ({
  page,
}) => {
  await setupMockedApp(page, {
    payloadFor: (url: string, request: Request) =>
      new URL(url).pathname === '/api/option/' && request.method() === 'GET'
        ? { success: true, data: [{ key: 'QuotaForNewUser', value: '0' }] }
        : undefined,
  });
  await expectRouteReady(page, '/console/setting?tab=billing');
  await page.locator('#QuotaForNewUser').fill('42');
  await page.getByRole('tab', { name: '注册与访问', exact: true }).click();
  // The guard stops the switch until the reader decides what happens to the
  // draft; the panel behind it is still billing.
  const guard = page.getByRole('dialog');
  await expect(guard).toBeVisible();
  await guard.getByRole('button', { name: '留在这里' }).click();
  await expect(page.locator('#QuotaForNewUser')).toHaveValue('42');
});

test('tier navigation cannot discard unsaved platform settings', async ({
  page,
}) => {
  await setupMockedApp(page, {
    payloadFor: (url: string, request: Request) =>
      new URL(url).pathname === '/api/option/' && request.method() === 'GET'
        ? { success: true, data: [{ key: 'RegisterEnabled', value: 'false' }] }
        : undefined,
  });
  await expectRouteReady(page, '/console/setting');
  const registration = page.getByRole('switch', {
    name: '允许注册',
    exact: true,
  });
  const original = await registration.getAttribute('aria-checked');
  await registration.click();
  const changed = await registration.getAttribute('aria-checked');
  const limits = page.getByRole('link', { name: '编辑', exact: true });
  await expect(limits).toHaveAttribute('aria-disabled', 'true');
  await limits.click({ force: true });
  await expect(page).not.toHaveURL(/tab=rate-limits/);
  await expect(registration).toHaveAttribute('aria-checked', changed!);
  await expect(
    page.getByRole('status').filter({ hasText: '请先保存或撤销' }),
  ).toBeVisible();
  await page.getByRole('button', { name: '撤销', exact: true }).click();
  await expect(registration).toHaveAttribute('aria-checked', original!);
  await limits.click();
  await expect(page).toHaveURL(/tab=rate-limits/);
});

test('settings groups use second-level headings below the page title', async ({
  page,
}) => {
  await setupMockedApp(page);
  await expectRouteReady(page, '/console/setting?tab=accounts');
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
  await expect(
    page.locator('.set-card > .set-card__hd h2.set-card__t'),
  ).not.toHaveCount(0);
});

for (const [language, title, access] of [
  ['en', 'Platform settings', 'Registration & access'],
  ['zh-TW', '平台設定', '註冊與存取'],
  ['ja', 'プラットフォーム設定', '登録とアクセス'],
]) {
  test(`settings navigation is localized in ${language}`, async ({ page }) => {
    await setupMockedApp(page, { language });
    await expectRouteReady(page, '/console/setting');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(title);
    // The groups are tabs now, not scroll anchors, so the accessible role is
    // "tab" rather than "button".
    await expect(
      page.getByRole('tab', { name: access, exact: true }),
    ).toBeVisible();
  });
}
