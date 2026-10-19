import { expect, test } from '@playwright/test';
import {
  expectRouteReady,
  mockOverview,
  setupMockedApp,
} from './helpers/mock-app';

for (const savedScope of [null, 'workspace:removed', 'workspace:default']) {
  test(`root opens platform usage with ${savedScope ?? 'no saved scope'}`, async ({
    page,
  }) => {
    const overviewScopes: Array<string | null> = [];
    const app = await setupMockedApp(page, {
      consoleScope: savedScope,
      user: { username: 'root', role: 100 },
      payloadFor: (url: string) => {
        const request = new URL(url);
        if (request.pathname !== '/api/data/overview') return undefined;
        const scope = request.searchParams.get('scope');
        overviewScopes.push(scope);
        const data = mockOverview();
        data.totals.amount_microusd =
          scope === 'platform' ? 120_000_000 : 3_000_000;
        return { data, message: '', success: true };
      },
    });

    await expectRouteReady(page, '/console');
    await expect(
      page.getByRole('heading', { name: '平台总览', exact: true }),
    ).toBeVisible();
    await expect(page.locator('.ov-groups .ov-money').first()).toContainText(
      '120',
    );
    expect(overviewScopes).toEqual(['platform']);

    const exportRequest = page.waitForRequest(
      (request) => new URL(request.url()).pathname === '/api/data/export',
    );
    await page.getByRole('button', { name: '导出', exact: true }).click();
    await page.getByRole('menuitem', { name: 'CSV', exact: true }).click();
    const exported = new URL((await exportRequest).url());
    expect(exported.searchParams.get('scope')).toBe('platform');
    expect(exported.searchParams.has('project_id')).toBe(false);
    await app.expectNoUnexpectedErrors();
  });
}

test('root returns to platform home without overwriting personal log selection', async ({
  page,
}) => {
  const app = await setupMockedApp(page, { consoleScope: null });
  await expectRouteReady(page, '/console');
  await expect(
    page.getByRole('heading', { name: '平台总览', exact: true }),
  ).toBeVisible();

  await page.locator('.sidebar-v2__item[href="/console/usage"]').click();
  // The operator's scope switch lives in the 筛选 popover; a chip above the
  // table names it while it is set to 仅我自己.
  await page.getByRole('button', { name: /^筛选/ }).click();
  await page.getByRole('button', { name: '仅我自己', exact: true }).click();
  await expect(page.getByText('范围：仅我自己')).toBeVisible();
  await page.locator('.sidebar-v2__item[href="/console"]').click();
  await expect(
    page.getByRole('heading', { name: '平台总览', exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole('heading', { name: '平台总览', exact: true }),
  ).toBeVisible();
  await page.locator('.sidebar-v2__item[href="/console/usage"]').click();
  await expect(page.getByText('范围：仅我自己')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('platform user drilldowns carry their own scope without changing the saved project', async ({
  page,
}) => {
  const requests: URL[] = [];
  await setupMockedApp(page, {
    consoleScope: 'workspace:default',
    payloadFor: (raw: string) => {
      const url = new URL(raw);
      if (url.pathname.startsWith('/api/log/')) requests.push(url);
      if (url.pathname === '/api/data/overview')
        return {
          success: true,
          data: mockOverview({
            users: [
              {
                id: 42,
                name: 'customer-42',
                amount_microusd: 6_000_000,
                gross_margin_microusd: 2_000_000,
              },
            ],
          }),
        };
      return undefined;
    },
  });
  await expectRouteReady(page, '/console');
  const href = await page
    .getByRole('link', { name: 'customer-42', exact: true })
    .getAttribute('href');
  expect(href).toContain('scope=platform');
  await page.goto(href!);
  await page.getByRole('button', { name: /^筛选/ }).click();
  await expect(
    page.getByRole('button', { name: '平台全局', exact: true }),
  ).toHaveAttribute('aria-pressed', 'true');
  await expect
    .poll(() =>
      requests.some(
        (url) =>
          url.pathname === '/api/log/' &&
          url.searchParams.get('user_id') === '42',
      ),
    )
    .toBe(true);
  expect(requests.some((url) => url.pathname.startsWith('/api/log/self'))).toBe(
    false,
  );
  await page.getByRole('button', { name: '仅我自己', exact: true }).click();
  await expect(page.getByText('范围：仅我自己')).toBeVisible();
  await expect
    .poll(() => requests.some((url) => url.pathname === '/api/log/self/'))
    .toBe(true);
});

for (const savedScope of [null, 'platform:operations']) {
  test(`root project log links stay project-scoped with ${savedScope ?? 'no saved scope'}`, async ({
    page,
  }) => {
    const requests: URL[] = [];
    await setupMockedApp(page, {
      consoleScope: savedScope,
      payloadFor: (raw: string) => {
        const url = new URL(raw);
        if (url.pathname.startsWith('/api/log/')) requests.push(url);
        return undefined;
      },
    });
    await page.goto('/console/usage?project_id=p_orbit');
    await expect
      .poll(() =>
        requests.some(
          (url) =>
            url.pathname === '/api/log/self/' &&
            url.searchParams.get('project_id') === 'p_orbit',
        ),
      )
      .toBe(true);
    await expect
      .poll(() =>
        requests.some(
          (url) =>
            url.pathname === '/api/log/self/stat' &&
            url.searchParams.get('project_id') === 'p_orbit',
        ),
      )
      .toBe(true);
    expect(
      requests.some(
        (url) =>
          url.pathname === '/api/log/' || url.pathname === '/api/log/stat',
      ),
    ).toBe(false);
  });
}

for (const [cachedRole, refreshedRole] of [
  [100, 1],
  [1, 100],
]) {
  test(`home follows refreshed role ${refreshedRole} instead of cached role ${cachedRole}`, async ({
    page,
  }) => {
    let releaseSelf: () => void;
    const gate = new Promise<void>((resolve) => {
      releaseSelf = resolve;
    });
    const scopes: Array<string | null> = [];
    await page.setViewportSize({ width: 390, height: 844 });
    await setupMockedApp(page, {
      user: { id: 1, role: cachedRole },
      payloadFor: async (raw: string) => {
        const url = new URL(raw);
        if (url.pathname === '/api/user/self') {
          await gate;
          return {
            success: true,
            data: { id: 1, username: 'root', role: refreshedRole },
          };
        }
        if (url.pathname === '/api/data/overview')
          scopes.push(url.searchParams.get('scope'));
        return undefined;
      },
    });
    await page.goto('/console');
    await expect.poll(() => scopes.length).toBeGreaterThan(0);
    releaseSelf!();
    await expect
      .poll(() => scopes[scopes.length - 1])
      .toBe(refreshedRole === 100 ? 'platform' : 'self');
    await expect(
      page.getByRole('heading', { name: '平台总览', exact: true }),
    ).toHaveCount(refreshedRole === 100 ? 1 : 0);
    if (refreshedRole === 100)
      await expect(page.locator('.mobile-console-title')).toContainText(
        '管理后台',
      );
    else
      await expect(page.locator('.mobile-console-title')).not.toContainText(
        '管理后台',
      );
    await page.setViewportSize({ width: 1440, height: 1000 });
    const accounts = page.locator(
      '.sidebar-v2__item[href="/console/organizations"]',
    );
    if (refreshedRole === 100) {
      await expect(accounts).toBeVisible();
      await accounts.click();
      await expect(page).toHaveURL(/\/console\/organizations$/);
      await expect(page.locator('.orgs-ledger')).toBeVisible();
    } else {
      await expect(accounts).toHaveCount(0);
      await page.goto('/console/organizations');
      await expect(page).toHaveURL(/\/forbidden$/);
    }
  });
}

test('mobile root home agrees with the platform overview despite saved personal scope', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page, { consoleScope: 'workspace:default' });
  await expectRouteReady(page, '/console');
  await expect(
    page.getByRole('heading', { name: '平台总览', exact: true }),
  ).toBeVisible();
  await expect(page.locator('.mobile-console-title')).toContainText('管理后台');
  await app.expectNoUnexpectedErrors();
});

for (const savedScope of [null, 'platform:operations']) {
  test(`ordinary users keep personal usage with ${savedScope ?? 'no saved scope'}`, async ({
    page,
  }) => {
    const scopes: Array<string | null> = [];
    const logPaths: string[] = [];
    const app = await setupMockedApp(page, {
      consoleScope: savedScope,
      user: { id: 2, username: 'customer', role: 1 },
      payloadFor: (url: string) => {
        const request = new URL(url);
        if (request.pathname === '/api/data/overview')
          scopes.push(request.searchParams.get('scope'));
        if (request.pathname.startsWith('/api/log/'))
          logPaths.push(request.pathname);
        return undefined;
      },
    });
    await expectRouteReady(page, '/console');
    await expect(page.locator('.ov-kpis')).toBeVisible();
    expect(scopes).toEqual(['self']);
    await expect(
      page.getByRole('heading', { name: '平台总览', exact: true }),
    ).toHaveCount(0);
    await page.goto('/console/usage?scope=platform');
    await expect.poll(() => logPaths.includes('/api/log/self/')).toBe(true);
    expect(logPaths.includes('/api/log/')).toBe(false);
    await app.expectNoUnexpectedErrors();
  });
}
