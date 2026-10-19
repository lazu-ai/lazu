// @ts-nocheck
/* Two rules, checked by machine on every route that has a wait.
 *
 * 1. A page must not state something it does not know yet. Every API answer is
 *    held back here, and the page is read while they are outstanding: if the
 *    text contains a count, a total or an "there is nothing here" that is only
 *    true of an empty default, the page is asserting an answer it has not been
 *    given. That is the bug the sign-in door had (a password form drawn before
 *    the server said which credentials exist), the overview had ("本区间没有请求"
 *    over a paying account) and the log page had ("共 0 条" over a loading
 *    table).
 *
 * 2. What is drawn while waiting must land where the real thing lands. The
 *    page's cumulative layout shift is measured from navigation through settle
 *    and has to stay inside Google's "good" band; /terms used to score 0.83 by
 *    throwing its footer 5,400px down the page.
 */
import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

const API_DELAY_MS = 1200;
const CLS_BUDGET = 0.1;

type Route = {
  /** Claims that would only be true of an empty default, per route. */
  forbiddenWhileLoading: RegExp[];
  mocked: boolean;
  path: string;
  role?: number;
  /** Something only the settled page can show, so the wait really ended. */
  settled: RegExp;
};

const ROUTES: Route[] = [
  {
    path: '/',
    mocked: false,
    forbiddenWhileLoading: [],
    settled: /lazu|Lazu/,
  },
  {
    path: '/models',
    mocked: false,
    forbiddenWhileLoading: [
      // The plaza's own "there is nothing here" belongs to an answered query.
      /No available models|当前没有可用模型/,
    ],
    settled: /Models|模型/,
  },
  {
    path: '/terms',
    mocked: false,
    forbiddenWhileLoading: [],
    settled: /Legal|法律/,
  },
  {
    path: '/key',
    mocked: false,
    forbiddenWhileLoading: [],
    settled: /API Key/i,
  },
  {
    path: '/console',
    mocked: true,
    role: 1,
    forbiddenWhileLoading: [
      /本区间没有请求/,
      /完成第一次请求后/,
      /\$0\.00/,
      /成功率 —/,
    ],
    settled: /\$6\.00/,
  },
  {
    path: '/console',
    mocked: true,
    role: 100,
    forbiddenWhileLoading: [/\$0\.00/, /成功率 —/],
    settled: /\$6\.00/,
  },
  {
    path: '/console/usage',
    mocked: true,
    forbiddenWhileLoading: [/共 0 条/, /总计\s*0(?!\d)/],
    settled: /用量|日志/,
  },
  {
    path: '/console/organizations',
    mocked: true,
    forbiddenWhileLoading: [/共 0 项/, /· 0(?!\d)/],
    settled: /用户|账户/,
  },
];

const installShiftMeter = async (page) => {
  await page.addInitScript(() => {
    window.__cls = 0;
    window.__worstShift = null;
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        if (entry.hadRecentInput) continue;
        window.__cls += entry.value;
        if (!window.__worstShift || entry.value > window.__worstShift.value) {
          window.__worstShift = {
            value: entry.value,
            sources: (entry.sources || [])
              .map((source) =>
                (source.node?.className || source.node?.tagName || '?')
                  .toString()
                  .slice(0, 40),
              )
              .slice(0, 3),
          };
        }
      }
    }).observe({ type: 'layout-shift', buffered: true });
  });
};

for (const route of ROUTES) {
  test(`${route.path}${route.role ? ` role ${route.role}` : ''} waits without claiming, and without moving`, async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await installShiftMeter(page);
    if (route.mocked)
      await setupMockedApp(page, {
        user: route.role ? { role: route.role } : {},
      });

    // Registered last, so it wraps whatever the mock layer already answers.
    await page.route('**/api/**', async (handler) => {
      await new Promise((resolve) => setTimeout(resolve, API_DELAY_MS));
      await handler.fallback();
    });

    // The legal routes read their text from the API; without an answer they
    // settle into the "nothing is configured" state, which is not the wait
    // this test is about.
    await page.route('**/api/user-agreement**', async (handler) => {
      await new Promise((resolve) => setTimeout(resolve, API_DELAY_MS));
      await handler.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: `# User Agreement\n\n最近更新 2026-08-27\n\n${Array.from(
            { length: 12 },
            (_unused, index) =>
              `## ${index + 1} Chapter ${index + 1}\n\n${'Long-form legal prose. '.repeat(40)}`,
          ).join('\n\n')}`,
        }),
      });
    });

    await page.goto(route.path);

    // Mid-wait: whatever is on screen may be an outline, but never an answer.
    await page.waitForTimeout(Math.round(API_DELAY_MS * 0.6));
    const waiting = await page.evaluate(
      () => document.querySelector('.app-route-stage')?.innerText || '',
    );
    for (const claim of route.forbiddenWhileLoading) {
      expect(
        claim.test(waiting),
        `${route.path} stated "${claim}" while its data was still in flight`,
      ).toBe(false);
    }

    await expect(page.locator('.app-route-stage')).toContainText(
      route.settled,
      {
        timeout: 20_000,
      },
    );
    await page.waitForTimeout(600);

    const { cls, worst } = await page.evaluate(() => ({
      cls: window.__cls,
      worst: window.__worstShift,
    }));
    expect(
      cls,
      `${route.path} moved by ${cls.toFixed(3)} (worst: ${JSON.stringify(worst)})`,
    ).toBeLessThan(CLS_BUDGET);
  });
}
