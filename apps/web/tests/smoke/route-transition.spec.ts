// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('console navigation keeps the shell stable without a page-entry flash', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console');

  await expect(page.locator('.ov-page')).toBeVisible();

  const sidebar = page.locator('.app-shell-sidebar');
  await sidebar.evaluate((element) => {
    element.setAttribute('data-transition-anchor', 'stable');
  });

  let delayedLazyChunk = false;
  await page.route(/\/assets\/.*\.js(?:\?.*)?$/, async (route) => {
    if (!delayedLazyChunk) {
      delayedLazyChunk = true;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    await route.continue();
  });

  await page.evaluate(() => {
    const content = document.querySelector('.app-shell-content');
    const probe = {
      emptyFrames: 0,
      fullScreenFallbacks: 0,
      animatedPageFrames: 0,
    };
    const sample = () => {
      if (!content?.firstElementChild) probe.emptyFrames += 1;
      if (document.querySelector('.fixed.inset-0.z-50')) {
        probe.fullScreenFallbacks += 1;
      }
      const pageShell = document.querySelector('.console-page-shell');
      if (pageShell && getComputedStyle(pageShell).animationName !== 'none') {
        probe.animatedPageFrames += 1;
      }
    };
    const observer = new MutationObserver(sample);
    if (content) {
      observer.observe(content, { childList: true, subtree: true });
    }
    sample();
    window.__lazuRouteTransitionProbe = {
      observer,
      probe,
    };
  });

  await page
    .locator('.sidebar-v2__content')
    .getByRole('link', { name: '模型运营', exact: true })
    .click();
  await expect(page.getByRole('heading', { name: '模型运营' })).toBeVisible();

  const probe = await page.evaluate(() => {
    window.__lazuRouteTransitionProbe?.observer.disconnect();
    return window.__lazuRouteTransitionProbe?.probe;
  });

  expect(probe).toEqual({
    emptyFrames: 0,
    fullScreenFallbacks: 0,
    animatedPageFrames: 0,
  });
  await expect(
    page.locator('.app-shell-sidebar[data-transition-anchor="stable"]'),
  ).toBeVisible();
  await app.expectNoUnexpectedErrors();
});
