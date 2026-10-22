// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('报告问题 in the Help menu sends the reader’s words and the page', async ({
  page,
}) => {
  let reported = null;
  const app = await setupMockedApp(page, {
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (
        pathname === '/api/user/support/report' &&
        request.method() === 'POST'
      ) {
        reported = request.postDataJSON();
        return { success: true, message: '', data: { sent: true } };
      }
      return undefined;
    },
  });

  await expectRouteReady(page, '/console/personal');
  await page.getByRole('button', { name: '帮助' }).click();
  await page.getByRole('menuitem', { name: '报告问题' }).click();

  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  const send = dialog.getByRole('button', { name: '发送' });
  await expect(send).toBeDisabled();
  await dialog.getByLabel('问题描述').fill('充值以后余额没有变化');
  await send.click();

  await expect.poll(() => reported).not.toBeNull();
  expect(reported).toMatchObject({
    message: '充值以后余额没有变化',
    page: '/console/personal',
    error: '',
    request_id: '',
  });
  await expect(dialog).toBeHidden();
  await expect(page.getByText('已发送。客服会回复到你的邮箱。')).toBeVisible();
  await app.expectNoUnexpectedErrors();
});
