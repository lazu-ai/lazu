import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

for (const path of ['/console/routes', '/console/model-market']) {
  test(`legacy catalog ${path} preserves URL state and member access`, async ({
    page,
  }) => {
    const app = await setupMockedApp(page, { user: { role: 1 } });
    await page.goto(`${path}?source=bookmark#pricing`);
    await expect(page).toHaveURL(/\/console\/models\?source=bookmark#pricing$/);
    await expect(page.getByText('gpt-4o-mini').first()).toBeVisible();
    await app.expectNoUnexpectedErrors();
  });

  test(`legacy catalog ${path} remains available to Key Pass`, async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      user: { role: 1, key_name: 'test-key' },
      consoleScope: 'key:test-key',
    });
    await page.goto(`${path}?source=key#pricing`);
    await expect(page).toHaveURL(/\/console\/models\?source=key#pricing$/);
    await expect(page.getByText('gpt-4o-mini').first()).toBeVisible();
    await app.expectNoUnexpectedErrors();
  });

  test(`legacy catalog ${path} survives the login redirect`, async ({
    page,
  }) => {
    await setupMockedApp(page, { user: { role: 1 } });
    // First visit is unauthenticated. Reload simulates the stored authenticated
    // session while retaining router history, exercising the login session check.
    await page.addInitScript(() => {
      if (!sessionStorage.getItem('catalog-login-tested')) {
        localStorage.removeItem('user');
        sessionStorage.setItem('catalog-login-tested', '1');
      }
    });
    await page.goto(`${path}?source=login#pricing`);
    await expect(page).toHaveURL(/\/login$/);
    await page.reload();
    await expect(page).toHaveURL(/\/console\/models\?source=login#pricing$/);
    await expect(page.getByText('gpt-4o-mini').first()).toBeVisible();
  });
}

for (const peak of [false, true]) {
  test(`extra prices apply ${peak ? 'peak' : 'base'} lane pricing to every dimension`, async ({
    page,
  }) => {
    await page.clock.setFixedTime(
      new Date(peak ? '2026-09-05T02:00:00Z' : '2026-09-05T06:00:00Z'),
    );
    const dimensions = {
      cache_read_per_million: 1,
      cache_write_5m_per_million: 2,
      cache_write_1h_per_million: 3,
      image_input: 4,
      image_output: 5,
      audio_input: 6,
      audio_output: 7,
    };
    const catalog = [
      {
        id: 'price-fixture',
        family: 'OpenAI',
        accessible: true,
        pricing: { input_per_million: 10, output_per_million: 20 },
        lanes: [
          {
            key: 'stable',
            available: true,
            peak_hours_utc: '1-4',
            peak_multiplier: 2,
            pricing: dimensions,
          },
          {
            key: 'eco',
            available: true,
            peak_hours_utc: '1-4',
            peak_multiplier: 3,
            pricing: dimensions,
            your_price: {
              ...dimensions,
              cache_read_per_million: 0.5,
              audio_output: 0.25,
            },
          },
        ],
      },
    ];
    const app = await setupMockedApp(page, {
      user: { role: 1 },
      payloadFor: (url: string) =>
        new URL(url).pathname === '/api/user/models/catalog'
          ? { success: true, data: catalog }
          : undefined,
    });
    await page.goto('/console/models');
    await page
      .locator('.plaza-card__open')
      .first()
      .click({ position: { x: 12, y: 12 } });
    const prices = page.locator('.plaza-xchip b');
    const stableFactor = peak ? 2 : 1;
    const ecoFactor = peak ? 3 : 1;
    const values = [
      ...[4, 5, 6, 7].map((value) => value * stableFactor),
      ...[4, 5, 6, 0.25].map((value) => value * ecoFactor),
    ];
    await expect(prices).toHaveCount(values.length);
    const displayed = (await prices.allTextContents()).map((text) =>
      Number(text.replace(/[^0-9.]/g, '')),
    );
    expect(displayed).toEqual(values);
    // Cache prices live on each lane block's cache line, not in extra chips.
    const cacheLines = page.locator('.plaza-laneblock__cache');
    await expect(cacheLines.nth(0)).toContainText(
      `$${stableFactor.toFixed(2)}`,
    );
    await expect(cacheLines.nth(0)).toContainText(
      `5m $${(2 * stableFactor).toFixed(2)}`,
    );
    const yourCachePrice = 0.5 * ecoFactor;
    await expect(cacheLines.nth(1)).toContainText(
      `$${yourCachePrice.toFixed(yourCachePrice < 1 ? 3 : 2)}`,
    );
    await app.expectNoUnexpectedErrors();
  });
}
