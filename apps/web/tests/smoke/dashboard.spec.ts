// @ts-nocheck
/* The scope-rail and Key Inspector cases were removed with the 2026-08-20
 * Yunmu decision: the console ships no scope switcher (site role and org role
 * are separate axes) and Key Inspector merged into the API Keys page tabs. */
import { expect, test, type Page } from '@playwright/test';
import {
  expectRouteReady,
  fail,
  mockOverview,
  setupMockedApp as setupConsoleApp,
} from './helpers/mock-app';

// These cases exercise the customer overview. Root's home is always the
// platform overview, even when Usage & Logs has a saved personal filter.
const setupMockedApp = (
  page: Page,
  options: Parameters<typeof setupConsoleApp>[1] = {},
) => setupConsoleApp(page, { ...options, user: { ...options.user, role: 1 } });

test('dashboard route renders with mocked account data', async ({ page }) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');
  await app.expectNoUnexpectedErrors();
});

test('first-run guide uses explicit placeholders and omits an unnecessary password step', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      if (
        new URL(url).pathname === '/api/user/self/onboarding' &&
        request.method() === 'GET'
      ) {
        return {
          success: true,
          message: '',
          data: {
            active: true,
            has_alert: false,
            has_key: false,
            has_request: false,
            needs_password: false,
            password_set: false,
          },
        };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console');

  const guide = page.locator('.onb-takeover');
  await expect(guide).toBeVisible();
  await expect(guide.locator('.onb-step')).toHaveCount(3);
  await expect(guide.getByText('设置兜底密码', { exact: true })).toHaveCount(0);
  await expect(guide.locator('.onb-code')).toContainText('YOUR_LAZU_API_KEY');
  await expect(guide.locator('.onb-code')).toContainText('YOUR_MODEL_ID');
  await expect(guide.locator('.onb-code')).toContainText('Content-Type');
  await expect(guide.getByRole('button', { name: '复制模板' })).toBeVisible();
  await expect(guide).toContainText('/api/models/catalog');
  await expect(guide).toContainText('剩余额度低于阈值时通知你');
  const takeoverAccessibility = await guide.getByRole('list').ariaSnapshot();
  expect(takeoverAccessibility.match(/待完成/g) ?? []).toHaveLength(3);
  await app.expectNoUnexpectedErrors();
});

test('first-run guide adds the fallback-password step for social accounts', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      if (
        new URL(url).pathname === '/api/user/self/onboarding' &&
        request.method() === 'GET'
      ) {
        return {
          success: true,
          message: '',
          data: {
            active: true,
            has_alert: false,
            has_key: false,
            has_request: false,
            needs_password: true,
            password_set: false,
          },
        };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console');

  const guide = page.locator('.onb-takeover');
  await expect(guide.locator('.onb-step')).toHaveCount(4);
  await expect(
    guide.locator('.onb-step__label').filter({ hasText: /^设置兜底密码/ }),
  ).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

/** What a browser fires when the reader comes back to this tab. */
const returnToTab = (page: Page) =>
  page.evaluate(() => {
    document.dispatchEvent(new Event('visibilitychange'));
  });

test('first-run guide polls until an external request completes the step', async ({
  page,
}) => {
  let onboardingReads = 0;
  let allowCompletion = false;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      if (
        new URL(url).pathname === '/api/user/self/onboarding' &&
        request.method() === 'GET'
      ) {
        onboardingReads += 1;
        return {
          success: true,
          message: '',
          data: {
            active: true,
            has_alert: false,
            has_key: true,
            has_request: allowCompletion,
            needs_password: false,
            password_set: false,
          },
        };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console');
  await expect(page.locator('.onb-takeover')).toBeVisible();

  const readsBeforeCompletion = onboardingReads;
  allowCompletion = true;
  // The step is finished in another tab or tool; coming back to this tab
  // re-reads the guide at once instead of waiting for the slow clock.
  await returnToTab(page);
  await expect
    .poll(() => onboardingReads, { timeout: 7_000 })
    .toBeGreaterThan(readsBeforeCompletion);
  await expect(page.locator('.onb-strip')).toBeVisible();
  await expect(page.locator('.onb-takeover')).toHaveCount(0);
  const stripAccessibility = await page.locator('.onb-strip').ariaSnapshot();
  expect(stripAccessibility.match(/已完成/g) ?? []).toHaveLength(2);
  expect(stripAccessibility.match(/待完成/g) ?? []).toHaveLength(1);
  await app.expectNoUnexpectedErrors();
});

test('first-run guide restores itself and reports a localized dismissal failure', async ({
  page,
}) => {
  let dismissWrites = 0;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      const pathname = new URL(url).pathname;
      if (
        pathname === '/api/user/self/onboarding' &&
        request.method() === 'GET'
      ) {
        return {
          success: true,
          message: '',
          data: {
            active: true,
            has_alert: false,
            has_key: false,
            has_request: false,
            needs_password: false,
            password_set: false,
          },
        };
      }
      if (
        pathname === '/api/user/self/onboarding/dismiss' &&
        request.method() === 'POST'
      ) {
        dismissWrites += 1;
        return fail(500, 'internal_error');
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console');
  const guide = page.locator('.onb-takeover');
  await expect(guide).toBeVisible();
  await guide.getByRole('button', { name: '跳过引导' }).click();

  await expect.poll(() => dismissWrites).toBe(1);
  await expect(guide).toBeVisible();
  await expect(
    page.getByText('无法保存此选择。引导已恢复，请重试。').first(),
  ).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('a stale onboarding refresh cannot restore a successfully dismissed guide', async ({
  page,
}) => {
  let onboardingReads = 0;
  let dismissWrites = 0;
  let releaseStaleRefresh = () => undefined;
  let markStaleRefreshStarted = () => undefined;
  const staleRefreshGate = new Promise((resolve) => {
    releaseStaleRefresh = resolve;
  });
  const staleRefreshStarted = new Promise((resolve) => {
    markStaleRefreshStarted = resolve;
  });
  const activeState = {
    active: true,
    has_alert: false,
    has_key: false,
    has_request: false,
    needs_password: false,
    password_set: false,
  };
  const app = await setupMockedApp(page, {
    payloadFor: async (url, request) => {
      const pathname = new URL(url).pathname;
      if (
        pathname === '/api/user/self/onboarding' &&
        request.method() === 'GET'
      ) {
        onboardingReads += 1;
        if (onboardingReads === 2) {
          markStaleRefreshStarted();
          await staleRefreshGate;
        }
        return { success: true, message: '', data: activeState };
      }
      if (
        pathname === '/api/user/self/onboarding/dismiss' &&
        request.method() === 'POST'
      ) {
        dismissWrites += 1;
        return { success: true, message: '' };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console');
  const guide = page.locator('.onb-takeover');
  await expect(guide).toBeVisible();
  await returnToTab(page);
  await staleRefreshStarted;

  await guide.getByRole('button', { name: '跳过引导' }).click();
  await expect.poll(() => dismissWrites).toBe(1);
  await expect(guide).toHaveCount(0);

  releaseStaleRefresh();
  await page.waitForTimeout(250);
  await expect(guide).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('Gateway shell keeps the single Yunmu column and the four core entries', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  // Yunmu ships one floating column: the product rail and the scope rail were
  // retired with the 2026-08-20 brand decision.
  await expect(page.locator('.sidebar-v2__product-rail')).toHaveCount(0);
  await expect(page.locator('.sidebar-v2__scope-tabs')).toHaveCount(0);
  await expect(page.locator('.sidebar-v2__item[href="/console"]')).toHaveCount(
    1,
  );
  await expect(
    page.locator('.sidebar-v2__item[href="/console/token"]'),
  ).toHaveCount(1);
  await expect(
    page.locator('.sidebar-v2__item[href="/console/models"]'),
  ).toHaveCount(1);
  await expect(
    page.locator('.sidebar-v2__item[href="/console/usage"]'),
  ).toHaveCount(1);
  await expect(page.locator('.ov-page')).toBeVisible();
  // Healthy operation shows no status verdict at all — the line under the
  // title carries the resolved range and freshness, and nothing else.
  await expect(page.locator('.ov-head__meta')).toContainText('同步于');
  await expect(page.locator('.lazu-inline-notice')).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('Overview names failed requests in the needs line', async ({ page }) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      if (new URL(url).pathname === '/api/data/overview') {
        const data = mockOverview();
        data.totals = {
          ...data.totals,
          outcome_count: 40,
          error_count: 12,
          success_rate: 0.7,
        };
        return { data, message: '', success: true };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console');

  // A success rate under 99% is something to act on: it goes on the one quiet
  // needs line with the failing count and a way to reach the requests.
  const needs = page.locator('.ov-needs');
  await expect(needs).toContainText('12 次请求失败');
  await expect(needs.locator('a[href^="/console/usage?type=5"]')).toBeVisible();
  await expect(page.locator('.lazu-inline-notice')).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});
test('Overview spend compares against the previous equal range', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      if (new URL(url).pathname !== '/api/data/overview') return undefined;
      const data = mockOverview();
      data.previous = { ...data.previous, amount_microusd: 3_000_000 };
      return { data, message: '', success: true };
    },
  });
  await expectRouteReady(page, '/console');

  const delta = page.locator('.ov-delta').first();
  await expect(delta).toHaveAttribute('data-dir', 'up');
  await expect(delta).toContainText('+100.0%');
  await expect(page.getByText(/较昨天同时段/).first()).toBeVisible();
  await app.expectNoUnexpectedErrors();
});
test('Gateway compact range picker stays inside a narrow viewport', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  await page.locator('.ov-seg .dashv3-tp-btn--compact').click();
  const popover = page.locator('.ov-seg .dashv3-tp-popover');
  await expect(popover).toBeVisible();
  const bounds = await popover.boundingBox();
  const visibleMonths = await popover
    .locator('.dashv3-tp-cal-month')
    .evaluateAll(
      (months) =>
        months.filter(
          (month) => window.getComputedStyle(month).display !== 'none',
        ).length,
    );
  expect(bounds).not.toBeNull();
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(390);
  expect(bounds.y).toBeGreaterThanOrEqual(0);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(844);
  expect(visibleMonths).toBe(1);
  const months = popover.locator('.dashv3-tp-cal-month');
  await expect(months.nth(0)).toBeHidden();
  await expect(months.nth(1)).toBeVisible();
  await months.nth(1).locator('[data-today="true"]').click();
  await expect(popover.locator('.dashv3-tp-foot')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});

test('Gateway compact range picker preserves its desktop width', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  await page.locator('.ov-seg .dashv3-tp-btn--compact').click();
  const bounds = await page.locator('.ov-seg .dashv3-tp-popover').boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds.width).toBeGreaterThanOrEqual(500);
  expect(bounds.width).toBeLessThanOrEqual(540);
  expect(bounds.x).toBeGreaterThanOrEqual(16);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(1424);
  await app.expectNoUnexpectedErrors();
});

test('Gateway compact range picker remains usable in a short landscape viewport', async ({
  page,
}) => {
  await page.setViewportSize({ width: 844, height: 390 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  await page.locator('.ov-seg .dashv3-tp-btn--compact').click();
  const popover = page.locator('.ov-seg .dashv3-tp-popover');
  const bounds = await popover.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds.y).toBeGreaterThanOrEqual(16);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(374);
  await expect(popover).toHaveCSS('overflow-y', 'auto');

  await popover.locator('[data-today="true"]').click();
  const footer = popover.locator('.dashv3-tp-foot');
  await footer.scrollIntoViewIfNeeded();
  await expect(footer).toBeVisible();
  const footerBounds = await footer.boundingBox();
  expect(footerBounds).not.toBeNull();
  expect(footerBounds.y + footerBounds.height).toBeLessThanOrEqual(390);
  await app.expectNoUnexpectedErrors();
});

test('Gateway overview shortcuts follow workspace module visibility', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    status: {
      SidebarModulesAdmin: JSON.stringify({
        workspace: {
          enabled: true,
          model_market: false,
          token: false,
          usage: false,
        },
      }),
    },
    user: {
      sidebar_modules: {
        workspace: {
          enabled: true,
          model_market: false,
          token: false,
          usage: false,
        },
      },
    },
  });
  await expectRouteReady(page, '/console');

  await expect(
    page.locator(
      'main a[href="/console/models"], main a[href="/console/token"], main a[href^="/console/usage"]',
    ),
  ).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('workspace docs stay visible when every workspace module is disabled', async ({
  page,
}) => {
  const workspaceDisabled = {
    enabled: true,
    detail: false,
    access: false,
    model_market: false,
    token: false,
    files: false,
    usage: false,
    members: false,
    key_inspector: false,
  };
  const app = await setupMockedApp(page, {
    status: {
      SidebarModulesAdmin: JSON.stringify({
        workspace: workspaceDisabled,
      }),
    },
    user: {
      sidebar_modules: {
        workspace: workspaceDisabled,
      },
    },
  });
  await expectRouteReady(page, '/console/wallet');

  const navigation = page.getByRole('navigation');
  await expect(navigation.getByRole('link', { name: /文档/ })).toBeVisible();
  await expect(
    navigation.locator(
      'a[href="/console"], a[href="/console/models"], a[href="/console/token"], a[href="/console/usage"]',
    ),
  ).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('mobile navigation closes through the scrim and restores focus', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  const trigger = page.locator('.mobile-console-menu-trigger');
  await expect(trigger).toHaveCount(1);
  await trigger.click();
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');

  const scrim = page.locator('.app-shell-sidebar__scrim');
  await expect(scrim).toBeVisible();
  await scrim.click({ position: { x: 350, y: 200 } });

  await expect(scrim).toBeHidden();
  await expect(trigger).toHaveAttribute('aria-expanded', 'false');
  await expect(trigger).toBeFocused();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      ),
    )
    .toBeLessThanOrEqual(1);
  await app.expectNoUnexpectedErrors();
});

test('console navigation resets push scroll, restores pop scroll, and focuses content', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  const content = page.locator('.app-shell-content');
  await page.addStyleTag({
    content: '.app-route-stage:has(.gateway-page) { min-height: 2500px; }',
  });
  await content.evaluate((element) => {
    window.scrollTo({ top: 560 });
  });
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(560);

  await page.locator('.sidebar-v2__item[href="/console/wallet"]').click();
  await expect(page).toHaveURL(/\/console\/wallet$/);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await expect(content).toBeFocused();
  const walletHeading = await content.locator('h1').first().innerText();
  await expect(page.locator('p.sr-only[aria-live="polite"]')).toHaveText(
    await content.getAttribute('aria-label'),
  );
  await expect(content).toHaveAttribute(
    'aria-label',
    new RegExp(walletHeading),
  );
  await expect(
    page.locator('.sidebar-v2__item[href="/console"]'),
  ).not.toHaveClass(/is-active/);
  await expect(
    page.locator('.sidebar-v2__item[href="/console/wallet"]'),
  ).toHaveClass(/is-active/);

  await page.goBack();
  await expect(page).toHaveURL(/\/console$/);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(560);
  const restoredAnnouncement = await content.getAttribute('aria-label');
  await expect(page.locator('p.sr-only[aria-live="polite"]')).toHaveText(
    restoredAnnouncement,
  );
  await app.expectNoUnexpectedErrors();
});

test('mobile Models stays inside console and drawer navigation focuses the destination', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  // The Models tab follows the sidebar to the model plaza (/console/models);
  // the legacy marketplace route still exists but is no longer navigation.
  await page
    .locator('.mobile-console-tabbar a[href="/console/models"]')
    .click();
  await expect(page).toHaveURL(/\/console\/models$/);
  await expect(page.locator('.mobile-console-topbar')).toBeVisible();
  await expect(page.locator('.mobile-console-tabbar')).toBeVisible();

  const trigger = page.locator('.mobile-console-menu-trigger');
  await trigger.click();
  await page
    .locator('.app-shell-sidebar .sidebar-v2__item[href="/console/wallet"]')
    .click();
  await expect(page).toHaveURL(/\/console\/wallet$/);
  await expect(page.locator('.app-shell-content')).toBeFocused();
  await expect(trigger).not.toBeFocused();
  await app.expectNoUnexpectedErrors();
});
