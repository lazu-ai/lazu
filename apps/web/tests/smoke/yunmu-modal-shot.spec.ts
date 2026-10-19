// @ts-nocheck
/** Scratch harness: opens edit modals with mocked data and screenshots them. */
import { test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const cases = [
  ['source-edit', '/console/model-ops?tab=sources', '编辑'],
  ['token-edit', '/console/token', '编辑'],
  ['org-edit', '/console/organizations', '编辑'],
];

for (const [name, path, buttonText] of cases) {
  test(`modal ${name}`, async ({ page }) => {
    await page.setViewportSize({ height: 940, width: 1440 });
    await setupMockedApp(page);
    await expectRouteReady(page, path);
    await page.waitForTimeout(500);
    const btn = page.getByRole('button', { name: buttonText }).first();
    if (await btn.isVisible().catch(() => false)) await btn.click();
    await page.waitForTimeout(600);
    await page.screenshot({ path: `.local-results/yunmu-${name}.png` });
  });
}
