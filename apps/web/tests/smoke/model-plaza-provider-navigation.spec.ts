import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const SCREENSHOT_DIR = resolve(
  process.cwd(),
  '../../.local-results/screenshots/model-plaza-latest',
);

test.beforeAll(async () => {
  await mkdir(SCREENSHOT_DIR, { recursive: true });
});

test('model plaza filters by the actual model company', async ({ page }) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/models');

  const anthropic = page.getByRole('button', { name: /Anthropic/ });
  await expect(anthropic).toBeVisible();
  await expect(page.getByRole('button', { name: /OpenAI/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /DeepSeek/ })).toBeVisible();
  await expect(
    anthropic.locator('[data-provider-logo="anthropic"]'),
  ).toBeVisible();
  await expect(
    anthropic.locator('[data-provider-logo="anthropic"] svg'),
  ).toBeVisible();

  await anthropic.click();

  await expect(anthropic).toHaveAttribute('aria-pressed', 'true');
  // One grid, no maker headings: the maker rides on each card.
  await expect(page.locator('.plaza-card')).toHaveCount(1);
  await expect(page.locator('.plaza-card__maker')).toHaveText(/Anthropic/);
  await expect(
    page.getByText('claude-sonnet-4-6', { exact: true }),
  ).toBeVisible();
  const claudeCard = page
    .locator('.plaza-card')
    .filter({ hasText: 'claude-sonnet-4-6' });
  // Each lane is the health page's lane block: status and uptime, the price,
  // then the week's 84 two-hour bars — but a week with no record (this mock
  // sends no buckets) draws no row of empty cells (canvas 2026-10-14).
  const compactLane = claudeCard.locator('.plaza-laneblock').first();
  await expect(compactLane).toBeVisible();
  await expect(compactLane.locator('.lazu-status-dot')).toBeVisible();
  await expect(compactLane.locator('.plaza-laneblock__now')).toBeVisible();
  await expect(
    compactLane.locator('.lazu-status-bars__row > .lazu-status-bar'),
  ).toHaveCount(0);
  await expect(compactLane.locator('.plaza-laneblock__figures')).toBeVisible();
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (value: string) => {
          sessionStorage.setItem('test.clipboard', value);
        },
      },
    });
  });
  const cardCopy = claudeCard.locator('.plaza-copy--card');
  await expect(cardCopy).toBeVisible();
  await cardCopy.click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect
    .poll(() => page.evaluate(() => sessionStorage.getItem('test.clipboard')))
    .toBe('claude-sonnet-4-6');
  // The card opens from anywhere but its bars, which inspect a slice.
  await claudeCard
    .getByRole('button', { name: 'claude-sonnet-4-6', exact: true })
    .click({ position: { x: 12, y: 12 } });
  const dialog = page.getByRole('dialog');
  const titleCopy = dialog.locator('.plaza-copy--title');
  await expect(titleCopy).toBeVisible();
  const detailLane = dialog.locator('.plaza-laneblock--detail').first();
  await expect(detailLane).toBeVisible();
  // The detail follows the card: an unrecorded week draws no empty bars.
  await expect(detailLane.locator('.lazu-status-bars__row')).toHaveCount(0);
  await page.evaluate(() => sessionStorage.removeItem('test.clipboard'));
  await titleCopy.click();
  await expect
    .poll(() => page.evaluate(() => sessionStorage.getItem('test.clipboard')))
    .toBe('claude-sonnet-4-6');
  await page.getByRole('dialog').getByRole('button').last().click();
  await expect(page.getByText('gpt-4o-mini', { exact: true })).toHaveCount(0);

  await page.locator('.plaza-provider-chip').first().click();
  await expect(page.locator('.plaza-card')).toHaveCount(4);
  await app.expectNoUnexpectedErrors();
});

test('provider logo navigation stays usable in dark and narrow layouts', async ({
  page,
}) => {
  await page.setViewportSize({ height: 940, width: 1440 });
  const app = await setupMockedApp(page);
  await page.addInitScript(() => {
    localStorage.setItem('lazu.theme', 'dark');
  });
  await expectRouteReady(page, '/console/models');

  await expect(page.locator('body')).toHaveAttribute('theme-mode', 'dark');
  await expect(page.locator('.plaza-provider-nav')).toBeVisible();
  await page.screenshot({
    path: resolve(SCREENSHOT_DIR, 'yunmu-models-dark.png'),
  });

  await page.setViewportSize({ height: 900, width: 430 });
  await expect(page.locator('.plaza-provider-nav')).toBeVisible();
  await expect
    .poll(() =>
      page.locator('.plaza-provider-nav').evaluate((nav) => ({
        clientWidth: nav.clientWidth,
        scrollWidth: nav.scrollWidth,
      })),
    )
    .toMatchObject({
      clientWidth: expect.any(Number),
      scrollWidth: expect.any(Number),
    });
  expect(
    await page
      .locator('.plaza-provider-nav')
      .evaluate((nav) => nav.scrollWidth > nav.clientWidth),
  ).toBe(true);
  await expect(page.locator('.plaza-card')).toHaveCount(4);
  await page.screenshot({
    fullPage: true,
    path: resolve(SCREENSHOT_DIR, 'yunmu-models-dark-narrow.png'),
  });
  await app.expectNoUnexpectedErrors();
});
