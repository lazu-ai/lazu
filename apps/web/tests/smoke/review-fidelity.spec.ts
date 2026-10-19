import { existsSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { resolve, extname } from 'node:path';
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';
import { reviewPayload } from './helpers/review-fixtures';

// The locked reference HTML is a design exploration kept in the private ops
// repository (lazu-ops:brand/explorations/). Point LAZU_BRAND_REFERENCE_DIR at
// a checkout of lazu-ops/brand to run this comparison; it skips otherwise.
const brand = resolve(
  process.env.LAZU_BRAND_REFERENCE_DIR ?? resolve(process.cwd(), '../../brand'),
);
const referencePage = 'explorations/ui-redesign-2026-09/model-ops.html';

test('model operations geometry is compared against the locked HTML, not an old app screenshot', async ({
  page,
  context,
}) => {
  test.skip(
    !existsSync(resolve(brand, referencePage)),
    'Reference HTML lives in lazu-ops; set LAZU_BRAND_REFERENCE_DIR to run.',
  );
  await page.setViewportSize({ width: 1440, height: 960 });
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: reviewPayload,
  });
  await expectRouteReady(page, '/console/model-ops');
  const reference = await context.newPage();
  await reference.setViewportSize({ width: 1440, height: 960 });
  await reference.route('http://reference.test/**', async (route) => {
    const file = resolve(brand, '.' + new URL(route.request().url()).pathname);
    if (!file.startsWith(brand + '/')) return route.abort();
    const mime: Record<string, string> = {
      '.html': 'text/html',
      '.css': 'text/css',
      '.js': 'text/javascript',
      '.svg': 'image/svg+xml',
      '.woff2': 'font/woff2',
    };
    try {
      await route.fulfill({
        body: await readFile(file),
        contentType: mime[extname(file)] || 'application/octet-stream',
      });
    } catch {
      await route.fulfill({ status: 404, body: '' });
    }
  });
  await reference.goto(`http://reference.test/${referencePage}`);
  // Remove only prototype-review chrome, never restyle the selected page.
  await reference.addStyleTag({
    content: '.review-strip{display:none}.sidebar{top:18px}',
  });
  await page.evaluate(() => document.fonts.ready);
  await reference.evaluate(() => document.fonts.ready);
  const expected = await reference
    .locator('#ops-models .restore-filter > label')
    .evaluateAll((nodes) =>
      nodes.map((node) => node.getBoundingClientRect().toJSON()),
    );
  const actual = await page
    .locator('.model-ops__filters--models > label')
    .evaluateAll((nodes) =>
      nodes.map((node) => node.getBoundingClientRect().toJSON()),
    );
  expect(actual).toHaveLength(4);
  for (let i = 0; i < 4; i++)
    for (const property of ['x', 'y', 'width', 'height'] as const) {
      expect(
        Math.abs(actual[i][property] - expected[i][property]),
        `filter ${i} ${property}`,
      ).toBeLessThanOrEqual(8);
    }
  await expect(page.locator('.lazu-tabs-list--line')).toHaveCSS(
    'border-radius',
    '0px',
  );
  await expect(page.getByRole('heading', { level: 1 })).toHaveCSS(
    'font-size',
    await reference
      .locator('h1')
      .evaluate((node) => getComputedStyle(node).fontSize),
  );
  await expect(page.locator('.model-ops__lane-prices > div').first()).toHaveCSS(
    'white-space',
    'nowrap',
  );
  await page
    .getByRole('button', { name: '售价与来源', exact: true })
    .first()
    .click();
  const expanded = page.locator('.model-ops__row--expand');
  for (const card of await expanded.locator('.model-ops__tier-card').all()) {
    const cardBox = await card.boundingBox();
    const rowBox = await expanded.boundingBox();
    expect(cardBox!.x + cardBox!.width).toBeLessThanOrEqual(
      rowBox!.x + rowBox!.width,
    );
  }
  await page.getByText('售价与折扣说明', { exact: true }).click();
  await expect(page.locator('.console-review-help')).toHaveAttribute(
    'open',
    '',
  );
  await page.getByRole('tab', { name: '来源', exact: true }).click();
  await expect(
    page.getByRole('tab', { name: '来源', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await page.getByRole('tab', { name: '模型', exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  const modelColumn = await page
    .locator('.model-ops__table--models th')
    .nth(1)
    .boundingBox();
  expect(modelColumn!.width).toBeGreaterThan(200);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - innerWidth,
    ),
  ).toBe(0);
  await reference.close();
});

test('review tables retain visible actions and never grow when a long key is revealed', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 960 });
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: reviewPayload,
  });
  for (const route of ['search-backend', 'token']) {
    await expectRouteReady(page, `/console/${route}`);
    const secret = page.locator('.lazu-inline-secret').first();
    await expect(secret).toBeVisible();
    const table = page.locator('table').first();
    const before = await table.boundingBox();
    await secret.getByRole('button', { name: '显示', exact: true }).click();
    await expect(secret.locator('input')).toHaveValue(/visual-fixture-only/);
    expect(await table.boundingBox()).toEqual(before);
    await expect(page.getByRole('dialog')).toHaveCount(0);
    const actions = page
      .getByRole('row')
      .nth(1)
      .getByRole('button', { name: '删除', exact: true });
    await expect(actions).toBeInViewport();
  }
});

test('wallet amount choices keep their surface and selected border after the global button reset', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: reviewPayload,
  });
  await expectRouteReady(page, '/console/wallet');
  const choices = page.locator('.lazu-topup-bar__preset');
  await expect(choices.first()).toHaveCSS('border-top-width', '1px');
  await choices.nth(1).click();
  await expect(choices.nth(1)).toHaveAttribute('aria-pressed', 'true');
  await expect(choices.nth(1)).toHaveCSS('border-top-width', '1px');
});

test('overview charts expose every bucket and its values as an accessible list', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console');
  const chart = page.locator('.lz-combo').first();
  const values = chart.getByRole('list').getByRole('listitem');
  await expect(values).toHaveCount(24);
  await expect(values.first()).toContainText('成功率');
  await expect(chart).not.toHaveAttribute('role', 'img');
});

test('selected model actions form their own wrapping row at tablet widths', async ({
  page,
}) => {
  await page.setViewportSize({ width: 800, height: 900 });
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: reviewPayload,
  });
  await expectRouteReady(page, '/console/model-ops');
  await page
    .locator('.model-ops__table--models .lazu-checkbox')
    .first()
    .click();

  const filters = page.locator('.model-ops__filters--models > label');
  const actions = page.locator('.model-ops__selection-actions');
  await expect(actions).toBeVisible();
  const filterBounds = await filters.evaluateAll((nodes) =>
    nodes.map((node) => node.getBoundingClientRect().toJSON()),
  );
  const actionBounds = await actions.boundingBox();
  expect(actionBounds).not.toBeNull();
  expect(actionBounds!.y).toBeGreaterThanOrEqual(
    Math.max(...filterBounds.map((bound) => bound.y + bound.height)),
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - innerWidth,
    ),
  ).toBe(0);
});
