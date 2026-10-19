// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('workspace member does not receive an admin-only model registration action', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    user: { role: 1 },
  });

  await expectRouteReady(page, '/console/models');
  // The model plaza is read-only for members: the catalog renders, but with no
  // registration affordance and no admin-only pricing tab.
  await expect(page.getByText('gpt-4o-mini').first()).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Register an allowed model' }),
  ).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

test('模型运营 is admin-only; a workspace member is bounced off it', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    user: { role: 1 },
  });

  await page.goto('/console/model-ops', { waitUntil: 'domcontentloaded' });
  // AdminRoute sends non-admins away rather than rendering the control layer.
  await expect(page).not.toHaveURL(/\/console\/model-ops/);
  await app.expectNoUnexpectedErrors();
});
