// @ts-nocheck
import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

const RELEASE_VERSION = 'v0.7.2';
// Ad-hoc deployments can still report a SHA, so the plate keeps an honest
// short-hash fallback instead of inventing a semantic release number.
const BUILD_SHA = 'ea8fcffa2b604266eec4962b7d84c0902c826f18';
const SHORT_SHA = 'ea8fcff';
const ANNOUNCEMENT_COPY = {
  en: {
    content: 'The announcement body follows the console language.',
    title: 'Localized release note',
  },
  ja: {
    content: 'お知らせ本文はコンソールの言語に合わせて表示されます。',
    title: 'ローカライズされたリリースノート',
  },
  zh: {
    content: '公告正文跟随控制台语言显示。',
    title: '本地化版本公告',
  },
  'zh-TW': {
    content: '公告正文會跟隨控制台語言顯示。',
    title: '本地化版本公告',
  },
};

const ok = (data = {}) => ({ data, message: '', success: true });

const ANNOUNCEMENTS = [
  {
    content:
      '焕然一新的 Lazu 控制台：独立的网关与运营工作区、更好的移动导航、更简洁的设计。',
    extra: 'peak 时段按 UTC 1–4、6–10 计价 ×2。',
    id: 20,
    isUnread: true,
    publishDate: '2026-08-20T10:24:00+08:00',
    title: '更快的控制台与新计费选项',
    type: 'release_note',
  },
  {
    content: '发布 OAuth 与支付披露文档，隐私政策与用户协议四语种同步更新。',
    id: 19,
    isUnread: true,
    publishDate: '2026-08-05T18:02:00+08:00',
    title: '法务与合规页面更新',
    type: 'success',
  },
  {
    content: 'UTC 18:00–18:30 滚动重启，API 可能出现短暂连接波动。',
    id: 18,
    isUnread: false,
    publishDate: '2026-07-22T09:40:00+08:00',
    title: '8 月 30 日凌晨数据库维护',
    type: 'warning',
  },
  {
    affected_models: ['gpt-4o-mini'],
    content: '该模型将在生效日停止服务。',
    effective_date: '2026-09-30T00:00:00Z',
    id: 17,
    isUnread: false,
    publishDate: '2026-07-20T09:40:00+08:00',
    title: '模型服务调整',
    type: 'model_update',
  },
];

/* Announcement routes with server-like memory: dismissing empties the unseen
   feed, which is what lets the test prove the plate's dot actually clears. */
const announcementRoutes = () => {
  let dismissed = false;
  return async (url) => {
    const { pathname, searchParams } = new URL(url);
    if (pathname === '/api/announcements') {
      const locale = searchParams.get('locale');
      const copy = ANNOUNCEMENT_COPY[locale] || ANNOUNCEMENT_COPY.zh;
      return ok({
        items: ANNOUNCEMENTS.map((item, index) => ({
          ...item,
          ...(index === 0 ? copy : {}),
          isUnread: dismissed ? false : item.isUnread,
        })),
      });
    }
    if (pathname === '/api/announcements/unseen') {
      return ok({
        items: dismissed ? [] : ANNOUNCEMENTS.filter((item) => item.isUnread),
      });
    }
    if (/^\/api\/announcements\/\d+\/dismiss$/.test(pathname)) {
      dismissed = true;
      return ok({});
    }
    return undefined;
  };
};

const gotoConsole = async (page) => {
  await page.goto('/console', { waitUntil: 'domcontentloaded' });
  await page
    .waitForLoadState('networkidle', { timeout: 5_000 })
    .catch(() => {});
};

test.describe('What’s New version plate', () => {
  test('sidebar plate opens the announcement panel and clears unread', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      payloadFor: announcementRoutes(),
      status: { version: RELEASE_VERSION },
    });
    await gotoConsole(page);

    // Formal releases show the semantic version published by the deploy.
    const plate = page.locator('.sidebar-v2__brand-row .version-plate');
    await expect(plate).toBeVisible();
    await expect(plate).toHaveText(RELEASE_VERSION);
    await expect(plate).toHaveClass(/has-unread/);

    await plate.click();

    const panel = page.locator('.whats-new-panel');
    await expect(panel).toBeVisible();
    await expect(panel.locator('.whats-new-panel__title')).toContainText(
      '最新动态',
    );
    await expect(panel.locator('.lazu-modal-close')).toHaveAccessibleName(
      '关闭',
    );
    // Header meta restates the build so the panel and its entry agree.
    await expect(panel.locator('.whats-new-panel__meta')).toContainText(
      RELEASE_VERSION,
    );

    await panel.evaluate((el) =>
      Promise.all(el.getAnimations().map((animation) => animation.finished)),
    );
    const box = await panel.boundingBox();
    const viewport = page.viewportSize();
    expect(Math.round(box.x + box.width / 2)).toBe(
      Math.round(viewport.width / 2),
    );
    expect(Math.round(box.y + box.height / 2)).toBe(
      Math.round(viewport.height / 2),
    );

    const cards = panel.locator('.announcement-card');
    await expect(cards).toHaveCount(ANNOUNCEMENTS.length);
    // Deploy-generated release notes get their own type rather than "默认".
    await expect(cards.first()).toHaveAttribute('data-tone', 'feature');
    await expect(cards.first().locator('.announcement-card__type')).toHaveText(
      '功能上新',
    );
    await expect(cards.nth(1)).toHaveAttribute('data-tone', 'success');
    await expect(cards.nth(2)).toHaveAttribute('data-tone', 'notice');
    await expect(cards.nth(3)).toHaveAttribute('data-tone', 'success');
    await expect(
      cards.nth(3).locator('.announcement-card__metadata'),
    ).toContainText('gpt-4o-mini');
    await expect(cards.nth(3).locator('time')).toHaveText('2026-09-30');
    await expect(panel.locator('.announcement-card__unread')).toHaveCount(2);

    // Marking read is the completion action: it closes the panel and the plate
    // drops its dot.
    await panel.locator('.whats-new-panel__mark-read').click();
    await expect(panel).toBeHidden();
    await expect(plate).toHaveClass(/is-read/);

    await app.expectNoUnexpectedErrors();
  });

  test('panel closes on escape without marking anything read', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      payloadFor: announcementRoutes(),
      status: { version: BUILD_SHA },
    });
    await gotoConsole(page);

    const plate = page.locator('.sidebar-v2__brand-row .version-plate');
    await plate.click();
    await expect(page.locator('.whats-new-panel')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('.whats-new-panel')).toBeHidden();
    // Glancing is not reading — the unread state has to survive a dismiss.
    await expect(plate).toHaveClass(/has-unread/);

    await app.expectNoUnexpectedErrors();
  });

  test('mobile topbar carries the plate as a bell beside the page name', async ({
    page,
  }) => {
    await page.setViewportSize({ height: 780, width: 390 });
    const app = await setupMockedApp(page, {
      payloadFor: announcementRoutes(),
      status: { version: BUILD_SHA },
    });
    await gotoConsole(page);

    // A bell: the version number is not drawn next to the page name on a
    // phone (it pushed the name into an ellipsis) but stays in the label.
    const plate = page.locator('.mobile-console-actions .version-plate');
    await expect(plate).toBeVisible();
    await expect(plate).not.toContainText(SHORT_SHA);
    await expect(plate).toHaveAttribute('aria-label', new RegExp(SHORT_SHA));

    await plate.click();
    const panel = page.locator('.whats-new-panel');
    await expect(panel).toBeVisible();
    // Bottom sheet: the panel sits on the viewport floor, not beside a sidebar
    // that does not exist at this width. Let the sheet finish rising first —
    // measuring mid-animation reads the translateY offset as real geometry.
    await panel.evaluate((el) =>
      Promise.all(el.getAnimations().map((animation) => animation.finished)),
    );
    const box = await panel.boundingBox();
    const viewport = page.viewportSize();
    expect(Math.round(box.y + box.height)).toBe(viewport.height);
    expect(Math.round(box.x)).toBe(0);
    await expect(panel.locator('.whats-new-panel__grab')).toBeVisible();

    await app.expectNoUnexpectedErrors();
  });

  test('plate stays hidden when the gateway reports no build', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      payloadFor: announcementRoutes(),
      status: { version: '' },
    });
    await gotoConsole(page);

    await expect(page.locator('.version-plate')).toHaveCount(0);
    await app.expectNoUnexpectedErrors();
  });

  test('plate falls back to a short SHA for an ad-hoc deployment', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      payloadFor: announcementRoutes(),
      status: { version: BUILD_SHA },
    });
    await gotoConsole(page);

    await expect(
      page.locator('.sidebar-v2__brand-row .version-plate'),
    ).toHaveText(SHORT_SHA);
    await app.expectNoUnexpectedErrors();
  });

  test('critical announcements open the update panel once without a plate click', async ({
    page,
  }) => {
    const critical = {
      ...ANNOUNCEMENTS[0],
      delivery: 'critical',
      id: 91,
      title: '需要注意的服务变更',
    };
    const app = await setupMockedApp(page, {
      payloadFor: async (url) => {
        const { pathname } = new URL(url);
        if (pathname === '/api/announcements') return ok({ items: [critical] });
        if (pathname === '/api/announcements/unseen') {
          return ok({ items: [critical] });
        }
        return undefined;
      },
      status: { version: RELEASE_VERSION },
    });
    await gotoConsole(page);

    await expect(page.locator('.whats-new-panel')).toBeVisible();
    await expect(page.locator('.whats-new-panel')).toContainText(
      critical.title,
    );
    await app.expectNoUnexpectedErrors();
  });

  test('a critical announcement beyond the unseen page still opens the panel', async ({
    page,
  }) => {
    const critical = {
      ...ANNOUNCEMENTS[0],
      delivery: 'critical',
      id: 92,
      title: '较早的紧急服务变更',
    };
    const notices = Array.from({ length: 20 }, (_, index) => ({
      ...ANNOUNCEMENTS[0],
      delivery: 'notice',
      id: index + 100,
      title: `普通公告 ${index + 1}`,
    }));
    const app = await setupMockedApp(page, {
      payloadFor: async (url) => {
        const { pathname } = new URL(url);
        if (pathname === '/api/announcements') return ok({ items: notices });
        if (pathname === '/api/announcements/unseen') {
          return ok({
            critical_item: critical,
            has_critical: true,
            items: notices,
          });
        }
        return undefined;
      },
      status: { version: RELEASE_VERSION },
    });
    await gotoConsole(page);

    await expect(page.locator('.whats-new-panel')).toBeVisible();
    await expect(page.locator('.whats-new-panel')).toContainText(
      critical.title,
    );
    await app.expectNoUnexpectedErrors();
  });

  test('announcement chrome and copy follow all four console locales', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      payloadFor: announcementRoutes(),
      status: { version: RELEASE_VERSION },
    });
    const expectations = [
      { locale: 'zh', panelTitle: '最新动态' },
      { locale: 'zh-TW', panelTitle: '最新動態' },
      { locale: 'en', panelTitle: "What's New" },
      { locale: 'ja', panelTitle: '最新情報' },
    ];

    for (const expectation of expectations) {
      await page.goto('/console', { waitUntil: 'domcontentloaded' });
      await page.evaluate((locale) => {
        localStorage.setItem('i18nextLng', locale);
      }, expectation.locale);
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.locator('.sidebar-v2__brand-row .version-plate').click();
      const panel = page.locator('.whats-new-panel');
      await expect(panel.locator('.whats-new-panel__title')).toContainText(
        expectation.panelTitle,
      );
      const copy = ANNOUNCEMENT_COPY[expectation.locale];
      await expect(panel.locator('.announcement-card').first()).toContainText(
        copy.title,
      );
      await expect(panel.locator('.announcement-card').first()).toContainText(
        copy.content,
      );
      await page.keyboard.press('Escape');
    }

    await app.expectNoUnexpectedErrors();
  });

  test('mark all read drains unread announcements beyond the visible page', async ({
    page,
  }) => {
    const pending = new Set(
      Array.from({ length: 35 }, (_, index) => index + 1),
    );
    const handler = async (url, request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/announcements') {
        return ok({
          items: Array.from(pending)
            .slice(0, 30)
            .map((id) => ({ ...ANNOUNCEMENTS[0], id, isUnread: true })),
        });
      }
      if (pathname === '/api/announcements/unseen') {
        return ok({
          items: Array.from(pending)
            .slice(0, 100)
            .map((id) => ({ id, isUnread: true })),
        });
      }
      const match = pathname.match(/^\/api\/announcements\/(\d+)\/dismiss$/);
      if (match && request?.method() === 'POST') {
        pending.delete(Number(match[1]));
        return ok({});
      }
      return undefined;
    };
    const app = await setupMockedApp(page, {
      payloadFor: handler,
      status: { version: RELEASE_VERSION },
    });
    await gotoConsole(page);
    await page.locator('.sidebar-v2__brand-row .version-plate').click();
    const action = page.locator('.whats-new-panel__mark-read');
    await expect(action).toBeEnabled();
    await action.click();
    await expect.poll(() => pending.size).toBe(0);
    await expect(page.locator('.whats-new-panel')).toBeHidden();
    await app.expectNoUnexpectedErrors();
  });
});

test('review pages preserve the original mobile announcement sheet geometry', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 780 });
  await setupMockedApp(page, {
    payloadFor: announcementRoutes(),
    status: { version: BUILD_SHA },
    consoleScope: 'platform:operations',
  });
  const measurements = [];
  for (const route of ['/console/models', '/console/model-ops']) {
    await page.goto(route);
    await page.locator('.mobile-console-actions .version-plate').click();
    const panel = page.locator('.whats-new-panel');
    await expect(panel).toBeVisible();
    measurements.push(
      await panel.evaluate((el) => {
        const style = getComputedStyle(el);
        return {
          maxHeight: style.maxHeight,
          radius: style.borderRadius,
          padding: style.padding,
          header: getComputedStyle(el.querySelector('.lazu-modal-header')!)
            .padding,
          body: getComputedStyle(el.querySelector('.lazu-modal-body')!).padding,
        };
      }),
    );
    await expect(panel).toHaveCSS('border-bottom-left-radius', '0px');
    await page.keyboard.press('Escape');
    await expect(panel).not.toBeVisible();
  }
  expect(measurements[1]).toEqual(measurements[0]);
});
