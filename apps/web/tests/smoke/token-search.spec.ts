// @ts-nocheck
import { test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('token search route accepts query state', async ({ page }) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/token?keyword=smoke');
  await app.expectNoUnexpectedErrors();
});
