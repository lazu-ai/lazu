// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

/* Guards for the shared-component fixes from the 2026-10-29 UI audit:
   dialogs name themselves and do not open on their close button, form errors
   are wired to their inputs, and three phone layouts stay inside the screen. */

const openKeyEditor = async (page) => {
  await expectRouteReady(page, '/console/token');
  await page.getByRole('button', { name: '编辑', exact: true }).first().click();
  const modal = page.locator('.lazu-modal-content');
  await expect(modal).toBeVisible();
  // On a phone the editor slides up as a sheet; measure once it has landed.
  await modal.evaluate((node) =>
    Promise.all(node.getAnimations().map((animation) => animation.finished)),
  );
  return modal;
};

test('dialogs are labelled by their title and open on content, not the close button', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await openKeyEditor(page);

  const dialog = page.getByRole('dialog', { name: /编辑密钥/ });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.lazu-modal-close')).toHaveAttribute(
    'aria-label',
    '关闭',
  );
  await expect
    .poll(() => page.evaluate(() => document.activeElement?.className ?? ''))
    .not.toContain('lazu-modal-close');
  await expect(page.locator('#token-name')).toBeFocused();
  await app.expectNoUnexpectedErrors();
});

test('a form error is announced through the input it belongs to', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  const modal = await openKeyEditor(page);

  const name = modal.locator('#token-name');
  await name.fill('');
  await modal.getByRole('button', { name: '保存修改' }).click();
  await expect(name).toHaveAttribute('aria-invalid', 'true');
  await expect(name).toHaveAttribute('aria-describedby', 'token-name-error');
  await expect(modal.locator('#token-name-error')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test.describe('phone layouts', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('usage search keeps a usable width next to the filter button', async ({
    page,
  }) => {
    const app = await setupMockedApp(page);
    await expectRouteReady(page, '/console/usage');
    const input = page.locator('.logv2-search input');
    await expect(input).toBeVisible();
    const box = await input.boundingBox();
    expect(box.width).toBeGreaterThan(120);
    await app.expectNoUnexpectedErrors();
  });

  test('key editor keeps the $ prefix inside the quota field', async ({
    page,
  }) => {
    const app = await setupMockedApp(page);
    const modal = await openKeyEditor(page);
    const money = modal
      .locator('.tok-create__quota .tok-create__money')
      .first();
    const prefix = await money.locator('> span').boundingBox();
    const input = await money.locator('input').boundingBox();
    expect(prefix.y).toBeGreaterThanOrEqual(input.y);
    expect(prefix.y + prefix.height).toBeLessThanOrEqual(
      input.y + input.height + 1,
    );
    const presets = modal.locator(
      '.tok-create__quota .tok-create__presets > *',
    );
    const tops = await presets.evaluateAll((nodes) =>
      nodes.map((node) => Math.round(node.getBoundingClientRect().top)),
    );
    expect(new Set(tops).size).toBe(1);
    await app.expectNoUnexpectedErrors();
  });

  test('model detail does not scroll sideways', async ({ page }) => {
    const app = await setupMockedApp(page);
    await page.goto('/console/models');
    await page.locator('.plaza-card__open').first().click();
    const modal = page.locator('.lazu-modal-content');
    await expect(modal.locator('.plaza-detail__stats')).toBeVisible();
    const [scroll, client] = await modal.evaluate((node) => [
      node.scrollWidth,
      node.clientWidth,
    ]);
    expect(scroll).toBeLessThanOrEqual(client);
    await app.expectNoUnexpectedErrors();
  });
});
