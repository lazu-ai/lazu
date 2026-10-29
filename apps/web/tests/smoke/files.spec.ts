// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('files route renders with mocked file data', async ({ page }) => {
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/usage?tab=files');
  await app.expectNoUnexpectedErrors();
});

test('deleting a file waits behind an undo and 撤销 sends nothing', async ({
  page,
}) => {
  let deletions = 0;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      if (
        request.method() === 'DELETE' &&
        new URL(url).pathname.startsWith('/api/files/')
      ) {
        deletions++;
        return { id: 'file-smoke123', object: 'file', deleted: true };
      }
    },
  });
  await expectRouteReady(page, '/console/usage?tab=files');
  const row = page.getByRole('row').filter({ hasText: 'smoke.txt' });
  await row.getByRole('button', { name: '删除', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(row).toContainText('删除中');
  await page.getByRole('button', { name: '撤销', exact: true }).click();
  await expect(row).not.toContainText('删除中');
  await expect(
    row.getByRole('button', { name: '删除', exact: true }),
  ).toBeVisible();
  expect(deletions).toBe(0);
  await app.expectNoUnexpectedErrors();
});
