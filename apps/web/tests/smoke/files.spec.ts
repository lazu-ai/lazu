// @ts-nocheck
import { test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('files route renders with mocked file data', async ({ page }) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/usage?tab=files');
  await app.expectNoUnexpectedErrors();
});
