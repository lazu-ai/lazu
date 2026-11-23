import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';
import { reviewPayload } from './helpers/review-fixtures';

test('key actions remain visible when the key details expand', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 960 });
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: reviewPayload,
  });
  await expectRouteReady(page, '/console/token');
  const row = page
    .getByRole('row')
    .filter({
      has: page.getByRole('button', { name: '删除 production', exact: true }),
    });
  const table = page.getByRole('table').first();
  const before = await table.boundingBox();
  await row.getByRole('button', { name: '展开详情' }).click();
  await expect(row.getByRole('button', { name: '收起详情' })).toBeVisible();
  expect((await table.boundingBox())?.width).toEqual(before?.width);
  await expect(
    row.getByRole('button', { name: '删除 production' }),
  ).toBeInViewport();
});

test('wallet amount choices keep their surface and selected border after the global button reset', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: reviewPayload,
  });
  await expectRouteReady(page, '/console/wallet');
  const choices = page.locator('.wal-tile');
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
    .getByRole('checkbox', { name: '选择 gpt-4o-mini', exact: true })
    .first()
    .click();

  const filters = page.locator('.mo-toolbar');
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
