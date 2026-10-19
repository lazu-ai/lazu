import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Request } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const imagePricing = {
  output_usd: 0.045,
  pixel_threshold: 2610000,
  high_resolution_output_usd: 0.09,
  layer_usd: 0.0225,
  high_resolution_layer_usd: 0.045,
  reference_usd: 0.003,
  free_references: 1,
};
const ok = (data: unknown) => ({ data, message: '', success: true });
const screenshots = resolve(
  process.cwd(),
  '../../.local-results/screenshots/seedream-latest',
);
test.beforeAll(async () => {
  await mkdir(screenshots, { recursive: true });
});

test('Seedream model detail shows the icon and all billing tiers', async ({
  page,
}) => {
  const model = {
    id: 'seedream-5.0-pro',
    name: 'Seedream 5.0 Pro',
    maker: 'bytedance',
    vendor: 'ByteDance',
    family: 'Seedream',
    capabilities: { supports_vision: true },
    modality: { input: ['text', 'image'], output: ['image'] },
    supported_endpoint_types: ['image-generation'],
    supported_endpoints: [
      {
        type: 'image-generation',
        path: '/v1/images/generations',
        method: 'POST',
      },
    ],
    pricing: { request: 0.045, image_pricing: imagePricing },
    lanes: [
      {
        key: 'stable',
        available: true,
        pricing: { request: 0.045, image_pricing: imagePricing },
      },
    ],
  };
  const app = await setupMockedApp(page, {
    payloadFor: (url: string) =>
      ['/api/user/models/catalog', '/models.json'].includes(
        new URL(url).pathname,
      )
        ? ok([model])
        : undefined,
  });
  await expectRouteReady(page, '/console/models');
  const card = page
    .locator('.plaza-card')
    .filter({ hasText: 'Seedream 5.0 Pro' });
  await expect(card).toBeVisible();
  await expect(
    page.locator('[data-provider-logo="seed"]').first(),
  ).toBeVisible();
  await card
    .getByRole('button', { name: 'seedream-5.0-pro', exact: true })
    .click({ position: { x: 12, y: 12 } });
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText('$0.0225', { exact: false })).toBeVisible();
  await expect(dialog.getByText(/额外图片/)).toContainText('$0.003');
  await expect(
    dialog.getByText('> 2,610,000px:', { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: resolve(screenshots, 'model-pricing.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await app.expectNoUnexpectedErrors();
});
