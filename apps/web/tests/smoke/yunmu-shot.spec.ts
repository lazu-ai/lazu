// @ts-nocheck
/** Scratch harness: renders console routes with mocked data and writes
 *  screenshots to .local-results for design comparison. Not an assertion suite. */
import { test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const routes = [
  ['console', '/console'],
  ['models', '/console/models'],
  ['usage', '/console/usage'],
  ['settings', '/console/setting'],
  ['orgs', '/console/organizations'],
  ['model-ops', '/console/model-ops'],
  ['model-ops-sources', '/console/model-ops?tab=sources'],
  ['token', '/console/token'],
];

for (const [name, path] of routes) {
  test(`shot ${name}`, async ({ page }) => {
    await page.setViewportSize({ height: 940, width: 1440 });
    await setupMockedApp(page);
    await expectRouteReady(page, path);
    await page.waitForTimeout(700);
    await page.screenshot({ path: `.local-results/yunmu-${name}.png` });
  });
}
