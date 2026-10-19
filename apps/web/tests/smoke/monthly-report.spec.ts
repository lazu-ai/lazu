// @ts-nocheck
import { test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('monthly report route renders with mocked report data', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/usage?tab=monthly');
  await app.expectNoUnexpectedErrors();
});
