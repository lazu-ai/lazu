import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

for (const path of ['/models', '/models/', '/MODELS']) {
  test(`${path} retains one application header and the original catalogue type`, async ({
    page,
  }) => {
    await setupMockedApp(page);
    await page.goto(path);
    await expect(page.locator('.app-shell-header')).toHaveCount(1);
    await expect(page.locator('.lazu-public-header')).toHaveCount(0);
    await expect(page.locator('.plaza-card').first()).toBeVisible();
    const type = await page
      .locator('.plaza-laneblock__now')
      .first()
      .evaluate((el) => getComputedStyle(el).fontFamily);
    expect(type).not.toContain('Instrument Serif');
    await expect(page.locator('.plaza-page h1')).toHaveCSS(
      'font-family',
      /Geist/,
    );
  });
}
for (const path of ['/invite', '/invite/a/b', '/login/typo', '/payment/typo']) {
  test(`${path} has only public chrome`, async ({ page }) => {
    await page.goto(path);
    await expect(page.locator('.lazu-status-page__title')).toBeVisible();
    await expect(page.locator('.lazu-public-header')).toHaveCount(1);
    await expect(page.locator('.app-shell-header')).toHaveCount(0);
    await expect(page.locator('.app-shell-sidebar')).toHaveCount(0);
  });
}
test('a signed-in permission failure retains account navigation', async ({
  page,
}) => {
  await setupMockedApp(page, { user: { role: 1 } });
  await page.goto('/console/model-ops');
  await expect(page).toHaveURL(/\/forbidden/);
  await expect(page.locator('.lazu-status-page__title')).toBeVisible();
  // The public header, with the way back to the console for a signed-in
  // visitor instead of sign-in.
  await expect(page.locator('.lazu-public-header')).toHaveCount(1);
  await expect(
    page.locator('.lazu-public-header [data-chrome-account="member"]'),
  ).toBeVisible();
  await expect(
    page.locator('.lazu-public-header [data-chrome-account="guest"]').first(),
  ).toBeHidden();
});
test('reduced motion disables the computed quota transition', async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/');
  await expect(page.locator('.lazu-landing__meter i')).toHaveCount(3);
  for (const meter of await page.locator('.lazu-landing__meter i').all()) {
    await expect(meter).toHaveCSS('transition-duration', '0s');
  }
});
test('platform admin links sit in the sidebar beside workspace navigation', async ({
  page,
}) => {
  await setupMockedApp(page);
  await page.goto('/console');
  const nav = page.locator('.sidebar-v2__content');
  await expect(nav.getByText('平台管理', { exact: true })).toBeVisible();
  await expect(
    nav.getByRole('link', { name: 'API Keys', exact: true }),
  ).toBeVisible();
  await expect(
    nav.getByRole('link', { name: '模型运营', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: '平台管理' })).toHaveCount(0);
});
test('members never receive the platform section', async ({ page }) => {
  await setupMockedApp(page, { user: { role: 1 } });
  await page.goto('/console');
  await expect(page.locator('.console-topbar')).toBeVisible();
  const nav = page.locator('.sidebar-v2__content');
  await expect(
    nav.getByRole('link', { name: 'API Keys', exact: true }),
  ).toBeVisible();
  await expect(nav.getByText('平台管理', { exact: true })).toHaveCount(0);
  await expect(
    nav.getByRole('link', { name: '模型运营', exact: true }),
  ).toHaveCount(0);
});

test('public chrome paints while the cold 404 chunk is held back', async ({
  page,
}) => {
  const manifest: Record<string, { file: string }> = JSON.parse(
    readFileSync('dist/.vite/manifest.json', 'utf8'),
  );
  const file = manifest['src/pages/NotFound/index.tsx'].file;
  let release = () => {};
  const blocked = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/${file}`, async (route) => {
    await blocked;
    await route.continue();
  });
  try {
    await page.goto('/not-a-route', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('.lazu-public-header')).toBeVisible();
    await expect(page.locator('.lazu-status-page__title')).toHaveCount(0);
  } finally {
    release();
  }
  await expect(page.locator('.lazu-status-page__title')).toBeVisible();
});

test('cold door exits carry their layout before auth CSS arrives', async ({
  page,
}) => {
  let release = () => {};
  const blocked = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(/\/assets\/AuthLayout-.*\.css/, async (route) => {
    await blocked;
    await route.continue();
  });
  await page.route('**/api/auth/oidc/status', (route) =>
    route.fulfill({
      json: {
        success: true,
        data: {
          enabled: false,
          email_code_auth: true,
          password_login: true,
          providers: [],
        },
      },
    }),
  );
  await page.route('**/api/status', (route) =>
    route.fulfill({
      json: { success: true, data: { email_verification: true } },
    }),
  );
  try {
    await page.goto('/login', { waitUntil: 'domcontentloaded' });
    const exits = page.locator('.auth-skeleton .auth-column__exits');
    await expect(exits).toBeVisible();
    await expect(exits).toHaveCSS('display', 'flex');
    await expect(exits).toHaveCSS('gap', '18px');
    await expect(exits).toHaveCSS('margin-top', '44px');
    const box = await exits.boundingBox();
    expect(box?.height).toBeGreaterThan(0);
  } finally {
    release();
  }
  await expect(
    page.locator('.auth-column > .auth-column__exits'),
  ).toBeVisible();
});

for (const width of [768, 900, 1100, 1280]) {
  test(`Japanese console controls do not collide at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    await setupMockedApp(page, { language: 'ja' });
    await page.goto('/console/model-ops');
    const where = page.locator('.console-topbar__where');
    const tools = page.locator('.console-topbar__tools');
    await expect(where).toBeVisible();
    await expect(tools).toBeVisible();
    const a = await where.boundingBox();
    const b = await tools.boundingBox();
    expect(a && b).toBeTruthy();
    expect(a!.x + a!.width <= b!.x || a!.y + a!.height <= b!.y).toBe(true);
    expect(b!.x + b!.width).toBeLessThanOrEqual(width);
    for (const label of await tools
      .locator('.console-topbar__tool--labelled > span')
      .all()) {
      await expect(label).toBeVisible();
    }
  });
}

for (const width of [390, 1440]) {
  test(`cold key lookup preserves form geometry at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    const manifest: Record<string, { file: string }> = JSON.parse(
      readFileSync('dist/.vite/manifest.json', 'utf8'),
    );
    const file = manifest['src/pages/KeyInspector/index.tsx'].file;
    let release = () => {};
    const blocked = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route(`**/${file}`, async (route) => {
      await blocked;
      await route.continue();
    });
    let outline;
    try {
      await page.goto('/key', { waitUntil: 'domcontentloaded' });
      await expect(page.locator('[data-key-loading]')).toBeVisible();
      outline = await page.locator('.key-lookup__form').boundingBox();
      await expect(page.locator('.console-route-loading')).toHaveCount(0);
    } finally {
      release();
    }
    await expect(page.locator('#inspect-key')).toBeVisible();
    const complete = await page.locator('.key-lookup__form').boundingBox();
    expect(outline).toBeTruthy();
    for (const dimension of ['x', 'y', 'width', 'height'] as const) {
      expect(
        Math.abs(outline![dimension] - complete![dimension]),
      ).toBeLessThanOrEqual(1);
    }
  });
}
