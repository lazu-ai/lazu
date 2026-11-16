import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';
import type { Request } from '@playwright/test';

for (const email of ['', 'old@example.com']) {
  test(`email binding uses POST proofs with ${email ? 'an existing' : 'no'} mailbox`, async ({
    page,
  }) => {
    const requests: Array<{ path: string; method: string; body: unknown }> = [];
    await setupMockedApp(page, {
      language: 'en',
      user: { id: 42, username: 'member', role: 1, email },
      payloadFor: (raw: string, request: Request) => {
        const path = new URL(raw).pathname;
        if (path === '/api/user/self')
          return {
            success: true,
            data: { id: 42, username: 'member', role: 1, email },
          };
        if (path.startsWith('/api/user/email/bind')) {
          requests.push({
            path,
            method: request.method(),
            body: request.postDataJSON(),
          });
          return {
            success: true,
            data: { current_email_required: Boolean(email) },
          };
        }
        return undefined;
      },
    });
    await expectRouteReady(page, '/console/personal');
    await page.locator('#personal-email-row').getByRole('button').click();
    await page.locator('#bind-email').fill('new@example.com');
    await page.getByRole('button', { name: 'Send code', exact: true }).click();
    await expect.poll(() => requests.length).toBe(1);
    await page.locator('#bind-email-code').fill('123456');
    if (email) await page.locator('#bind-current-email-code').fill('654321');
    else await expect(page.locator('#bind-current-email-code')).toHaveCount(0);
    await page
      .locator('form')
      .filter({ has: page.locator('#bind-email') })
      .locator('button[type="submit"]')
      .click();
    await expect.poll(() => requests.length).toBe(2);
    expect(requests).toEqual([
      {
        path: '/api/user/email/bind/start',
        method: 'POST',
        body: { email: 'new@example.com' },
      },
      {
        path: '/api/user/email/bind',
        method: 'POST',
        body: {
          email: 'new@example.com',
          code: '123456',
          current_code: email ? '654321' : '',
        },
      },
    ]);
  });
}
