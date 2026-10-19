import { test, expect } from '@playwright/test';
import { setupMockedApp, expectRouteReady } from './helpers/mock-app';

test('plan prices keep the official anchor at peak and show both context boundaries', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url: string) => {
      if (new URL(url).pathname !== '/api/user/models/catalog')
        return undefined;
      return {
        success: true,
        data: [
          {
            id: 'contract-model',
            name: 'contract-model',
            maker: 'OpenAI',
            lanes: [
              {
                key: 'stable',
                available: true,
                peak_hours_utc: '0-24',
                peak_multiplier: 2,
                plan_discount_pct: 40,
                pricing: { input: 9, output: 18 },
                your_price: {
                  input: 6,
                  output: 12,
                  peak_pricing: {
                    input: 6,
                    output: 12,
                    context_tiers: [
                      { threshold: 100000, input: 12, output: 24 },
                      { threshold: 200000, input: 15, output: 30 },
                    ],
                  },
                },
              },
            ],
          },
        ],
      };
    },
  });
  await expectRouteReady(page, '/console/models');
  const lane = page.locator('.plaza-laneblock').first();
  await expect(lane).toContainText('$6.00');
  await expect(lane).toContainText('100K');
  await expect(lane).toContainText('200K');
  await app.expectNoUnexpectedErrors();
});
