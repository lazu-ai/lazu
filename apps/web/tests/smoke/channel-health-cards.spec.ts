import { expect, test } from '@playwright/test';
import {
  expectRouteReady,
  mockedChannelHealth,
  setupMockedApp,
} from './helpers/mock-app';
import type { ChannelHealthRow } from '../../src/hooks/health/useChannelHealthQuery';

test('one tab stop per timeline, with arrow and boundary navigation', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  const timelines = page.locator('.chealth-bars');
  const bars = timelines.first().locator('button');
  await expect(timelines.first()).toHaveAccessibleName(
    /claude-sonnet-5 · 稳定/,
  );
  await expect(page.locator('.chealth-bars button[tabindex="0"]')).toHaveCount(
    await timelines.count(),
  );
  await bars.first().focus();
  await page.keyboard.press('ArrowLeft');
  await expect(bars.first()).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await expect(bars.nth(1)).toBeFocused();
  await expect(bars.nth(1)).toHaveClass(/is-hovered/);
  await page.keyboard.press('End');
  await expect(bars.last()).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await expect(bars.last()).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('.chealth-tip')).toHaveCount(0);
  await page.keyboard.press('Home');
  await expect(bars.first()).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(timelines.nth(1).locator('button').first()).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(bars.first()).toBeFocused();
  await app.expectNoUnexpectedErrors();
});

test('a lane filter does not relabel a dual-lane model as discount-only', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console/health');
  await page.getByRole('combobox', { name: '线路', exact: true }).click();
  await page.getByRole('option', { name: '仅 Agent 专线' }).click();
  const sonnet = page.locator('.chealth-mcard', { hasText: 'claude-sonnet-5' });
  await expect(sonnet.locator('.chealth-lane')).toHaveCount(1);
  await expect(sonnet.locator('.chealth-lane__label')).toHaveText('Agent');
  await expect(sonnet.locator('.chealth-mcard__tag')).toHaveCount(0);
  await expect(
    page
      .locator('.chealth-mcard', { hasText: 'deepseek-v4-pro' })
      .locator('.chealth-mcard__tag'),
  ).toHaveText('仅 Agent 专线');
});

test('operator routes sharing a public lane retain separate names and state', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) => {
      if (!url.includes('/api/channel_health')) return undefined;
      const rows = structuredClone(
        mockedChannelHealth.rows,
      ) as ChannelHealthRow[];
      const direct = rows[0];
      direct.lane_slug = 'direct';
      rows.push({
        ...direct,
        lane_slug: 'turbo',
        status: 'outage',
        ttft_p50_ms: 1250,
      });
      return { success: true, data: { ...mockedChannelHealth, rows } };
    },
  });
  await expectRouteReady(page, '/console/health');
  const card = page.locator('.chealth-mcard', { hasText: 'claude-sonnet-5' });
  await expect(card.locator('.chealth-lane__label')).toHaveText([
    '稳定',
    'turbo',
    'Agent',
  ]);
  const turbo = card.locator('.chealth-lane', { hasText: 'turbo' });
  await expect(turbo).toHaveAttribute('data-status', 'outage');
  await expect(turbo.locator('.lazu-status-dot--danger')).toBeVisible();
  await expect(turbo.locator('.chealth-lane__figures')).toContainText('1.25s');
  await expect(page.locator('.chealth-banner')).toContainText('turbo');
  await page.getByPlaceholder('搜索模型', { exact: true }).fill('gemini');
  await expect(card).toHaveCount(0);
  await page.getByPlaceholder('搜索模型', { exact: true }).clear();
  await expect(turbo).toHaveAttribute('data-status', 'outage');
  await app.expectNoUnexpectedErrors();
});

for (const language of ['zh', 'en']) {
  for (const width of [1280, 1110, 390]) {
    test(`complete card metrics at ${width}px in ${language}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 });
      const app = await setupMockedApp(page, {
        language,
        consoleScope: 'platform:operations',
        payloadFor: (url: string) => {
          if (!url.includes('/api/channel_health')) return undefined;
          const rows = structuredClone(
            mockedChannelHealth.rows,
          ) as ChannelHealthRow[];
          const eco = rows[1];
          eco.fallback_active = true;
          eco.ttft_p50_ms = 12340;
          eco.ttft_p95_ms = 56780;
          eco.output_tps = 1234;
          rows.push({
            ...rows[0],
            model_name: 'claude-very-long-model-identifier-2026-09-27',
          });
          return { success: true, data: { ...mockedChannelHealth, rows } };
        },
      });
      await expectRouteReady(page, '/console/health');
      await expect(
        page.locator('.chealth-lane__figures .chealth-tone--warn'),
      ).toContainText('3%');
      const clipped = await page
        .locator('.chealth-mcard')
        .evaluateAll((cards) => {
          const failures: string[] = [];
          for (const card of cards) {
            const box = card.getBoundingClientRect();
            for (const element of card.querySelectorAll(
              '.chealth-mcard__name, .chealth-lane__head, .chealth-lane__figures, .chealth-lane__figures > span',
            )) {
              const rect = element.getBoundingClientRect();
              if (
                element.scrollWidth > element.clientWidth + 1 ||
                rect.right > box.right + 1 ||
                rect.left < box.left - 1
              ) {
                failures.push(element.textContent || element.className);
              }
            }
          }
          return failures;
        });
      expect(clipped).toEqual([]);
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > window.innerWidth,
      );
      expect(overflow).toBe(false);
      await page.screenshot({
        path: `../../.local-results/screenshots/pr-291/${language}-${width}.png`,
        fullPage: true,
      });
      await app.expectNoUnexpectedErrors();
    });
  }
}
