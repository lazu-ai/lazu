import { expect, test } from '@playwright/test';
import { setupMockedApp, fail } from './helpers/mock-app';

const LAST_CREDENTIAL_KEY = 'lazu.lastLoginCredential.v1';

for (const username of ['grace321', 'another-member']) {
  test(`ordinary login discards inaccessible return path: ${username}`, async ({
    page,
  }) => {
    const user = { id: username, username, role: 1, token: 'test-session' };
    await setupMockedApp(page, {
      language: 'en',
      user,
      payloadFor: (raw: string) => {
        const path = new URL(raw).pathname;
        if (path === '/api/auth/oidc/status')
          return {
            success: true,
            data: { password_login: true, email_code_auth: false },
          };
        if (path === '/api/user/login') return { success: true, data: user };
      },
    });
    await page.addInitScript(() => localStorage.removeItem('user'));
    await page.goto('/login?next=%2Fconsole%2Forganizations');
    await expect(page.locator('#auth-identifier')).toHaveAttribute(
      'name',
      'username',
    );
    await expect(page.locator('#auth-password')).toHaveAttribute(
      'name',
      'password',
    );
    await page.locator('#auth-identifier').fill(username);
    await page.locator('#auth-password').fill('test-password');
    await page.locator('button[type="submit"]').click();
    await expect(page).toHaveURL(/\/console$/);
    await expect
      .poll(() =>
        page.evaluate(
          () => JSON.parse(localStorage.getItem('user') || '{}').role,
        ),
      )
      .toBe(1);
  });
}

test('successful password login offers the credential to the browser', async ({
  page,
}) => {
  const user = {
    id: 'member',
    username: 'member',
    role: 1,
    token: 'test-session',
  };
  await setupMockedApp(page, {
    language: 'en',
    user,
    payloadFor: (raw: string) => {
      const path = new URL(raw).pathname;
      if (path === '/api/auth/oidc/status')
        return {
          success: true,
          data: { password_login: true, email_code_auth: false },
        };
      if (path === '/api/user/login') return { success: true, data: user };
    },
  });
  await page.addInitScript(() => {
    const offeredIds: string[] = [];
    Object.defineProperty(window, '__offeredPasswordIds', {
      value: offeredIds,
    });
    Object.defineProperty(window, 'PasswordCredential', {
      configurable: true,
      value: class {
        id: string;
        origin: string;
        password: string;

        constructor(data: { id: string; origin: string; password: string }) {
          this.id = data.id;
          this.origin = data.origin;
          this.password = data.password;
        }
      },
    });
    Object.defineProperty(navigator, 'credentials', {
      configurable: true,
      value: {
        store: async (credential: { id: string }) => {
          offeredIds.push(credential.id);
          return credential;
        },
      },
    });
    localStorage.removeItem('user');
  });

  await page.goto('/login');
  await page.locator('#auth-identifier').fill('member');
  await page.locator('#auth-password').fill('test-password');
  await page.locator('button[type="submit"]').click();
  await expect(page).toHaveURL(/\/console$/);
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __offeredPasswordIds?: string[] })
            .__offeredPasswordIds,
      ),
    )
    .toEqual(['member']);
});

test('login remembers the last successful credential after a new visit', async ({
  page,
}) => {
  const user = {
    id: 'member',
    username: 'member',
    role: 1,
    token: 'test-session',
  };
  let passwordAttempts = 0;
  await setupMockedApp(page, {
    language: 'en',
    user,
    payloadFor: (raw: string) => {
      const path = new URL(raw).pathname;
      if (path === '/api/auth/oidc/status')
        return {
          success: true,
          data: { password_login: true, email_code_auth: true },
        };
      if (path === '/api/user/login') {
        passwordAttempts += 1;
        return passwordAttempts === 1
          ? fail(500, 'internal_error')
          : { success: true, data: user };
      }
      if (path === '/api/auth/email/start') return { success: true };
      if (path === '/api/auth/email/verify')
        return { success: true, data: user };
    },
  });
  await page.addInitScript(() => localStorage.removeItem('user'));

  await page.goto('/login');
  await expect(page.getByRole('tab', { name: 'Email code' })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await page.getByRole('tab', { name: 'Password' }).click();
  await page.locator('#auth-identifier').fill('member');
  await page.locator('#auth-password').fill('test-password');
  await page.locator('button[type="submit"]').click();
  await expect.poll(() => passwordAttempts).toBe(1);
  await expect
    .poll(() =>
      page.evaluate((key) => localStorage.getItem(key), LAST_CREDENTIAL_KEY),
    )
    .toBeNull();

  await page.locator('button[type="submit"]').click();
  await expect(page).toHaveURL(/\/console$/);
  await expect
    .poll(() =>
      page.evaluate((key) => localStorage.getItem(key), LAST_CREDENTIAL_KEY),
    )
    .toBe('password');

  await page.goto('/login');
  await expect(page.getByRole('tab', { name: 'Password' })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await page.getByRole('tab', { name: 'Email code' }).click();
  await page.locator('#auth-identifier').fill('grace@example.com');
  await page.locator('button[type="submit"]').click();
  await expect(page.locator('.auth-otp__box')).toHaveCount(6);
  expect(
    await page.evaluate(
      (key) => localStorage.getItem(key),
      LAST_CREDENTIAL_KEY,
    ),
  ).toBe('password');
  await page.locator('.auth-otp__box').first().fill('123456');
  await expect(page).toHaveURL(/\/console$/);
  await expect
    .poll(() =>
      page.evaluate((key) => localStorage.getItem(key), LAST_CREDENTIAL_KEY),
    )
    .toBe('code');

  await page.goto('/login');
  await expect(page.getByRole('tab', { name: 'Email code' })).toHaveAttribute(
    'aria-selected',
    'true',
  );
});

test('remembered password falls back when password login is unavailable', async ({
  page,
}) => {
  await setupMockedApp(page, {
    language: 'en',
    payloadFor: (raw: string) => {
      if (new URL(raw).pathname === '/api/auth/oidc/status')
        return {
          success: true,
          data: {
            email_code_auth: true,
            password_login: false,
            providers: [{ id: 'google', provider: 'google' }],
          },
        };
    },
  });
  await page.addInitScript((key) => {
    localStorage.removeItem('user');
    localStorage.setItem(key, 'password');
  }, LAST_CREDENTIAL_KEY);

  await page.goto('/login');
  await expect(page.locator('#auth-identifier')).toHaveAttribute(
    'name',
    'email',
  );
  await expect(page.locator('#auth-password')).toHaveCount(0);
  await expect(page.getByRole('tablist')).toHaveCount(0);
});

test('stored ordinary session cannot resume an admin page after login', async ({
  page,
}) => {
  await setupMockedApp(page, { user: { role: 1 } });
  await page.addInitScript(() => {
    if (!sessionStorage.getItem('auth-return-tested')) {
      localStorage.removeItem('user');
      sessionStorage.setItem('auth-return-tested', '1');
    }
  });
  await page.goto('/console/organizations');
  await expect(page).toHaveURL(/\/login$/);
  await page.reload();
  await expect(page).toHaveURL(/\/console$/);
});

test('login verifies a cookie session even when the stored user has no token', async ({
  page,
}) => {
  let selfChecks = 0;
  await setupMockedApp(page, {
    user: { role: 1, token: null },
    payloadFor: (raw: string) => {
      if (new URL(raw).pathname !== '/api/user/self') return undefined;
      selfChecks += 1;
      return { success: true, data: { id: 1, username: 'member', role: 1 } };
    },
  });

  await page.goto('/login?next=%2Fconsole%2Forganizations');
  await expect(page).toHaveURL(/\/console$/);
  // The console also refreshes /self after navigation.
  expect(selfChecks).toBeGreaterThanOrEqual(1);
  await expect
    .poll(() =>
      page.evaluate(
        () => JSON.parse(localStorage.getItem('user') || '{}').role,
      ),
    )
    .toBe(1);
});

test('HTTP 401 clears a stale local user and keeps the sign-in form', async ({
  page,
}) => {
  await setupMockedApp(page, {
    language: 'en',
    user: { role: 100, token: 'stale-token' },
    payloadFor: (raw: string) => {
      const path = new URL(raw).pathname;
      if (path === '/api/auth/oidc/status')
        return {
          success: true,
          data: { password_login: true, email_code_auth: false },
        };
      return undefined;
    },
  });
  await page.route('**/api/user/self', (route) =>
    route.fulfill({
      status: 401,
      contentType: 'application/json',
      body: JSON.stringify({
        success: false,
        error: {
          code: 'session_expired',
          type: 'authentication_error',
          request_id: 'req_mock',
        },
      }),
    }),
  );

  await page.goto('/login');
  await expect(page.locator('#auth-identifier')).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('user')))
    .toBeNull();
});

test('HTTP 200 self failure preserves the stored session', async ({ page }) => {
  await setupMockedApp(page, {
    language: 'en',
    user: { role: 100, token: 'existing-session' },
    payloadFor: (raw: string) => {
      const path = new URL(raw).pathname;
      if (path === '/api/user/self') return fail(500, 'internal_error');
      if (path === '/api/auth/oidc/status')
        return {
          success: true,
          data: { password_login: true, email_code_auth: false },
        };
      return undefined;
    },
  });

  await page.goto('/login');
  await expect(page.locator('#auth-identifier')).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
  await expect
    .poll(() =>
      page.evaluate(
        () => JSON.parse(localStorage.getItem('user') || '{}').token,
      ),
    )
    .toBe('existing-session');
});

test('a login link prefetches the stored session with its current identity', async ({
  page,
}) => {
  await setupMockedApp(page, {
    language: 'en',
    payloadFor: (raw: string) =>
      new URL(raw).pathname === '/api/auth/oidc/status'
        ? {
            success: true,
            data: { password_login: true, email_code_auth: false },
          }
        : undefined,
  });
  // The API module loaded while this tab was signed out. Another tab then
  // writes the user that the login link will probe.
  await page.addInitScript(() => localStorage.removeItem('user'));
  let release: () => void = () => undefined;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  const selfHeaders: Array<string | undefined> = [];
  await page.route('**/api/user/self', async (route) => {
    selfHeaders.push(route.request().headers()['x-lazu-user']);
    await pending;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: { id: 42, username: 'member', role: 1 },
      }),
    });
  });

  await page.goto('/');
  const loginLink = page.locator('a[href="/login"]').first();
  await loginLink.hover();
  await page.mouse.move(0, 0);
  await page.evaluate(() => {
    localStorage.setItem(
      'user',
      JSON.stringify({ id: 42, username: 'member', role: 1 }),
    );
  });
  await loginLink.hover();
  await expect
    .poll(() => selfHeaders.filter((id) => id === '42').length)
    .toBe(1);
  expect(selfHeaders).toContain('42');
  await loginLink.click();
  await expect(page.locator('.auth-skeleton')).toBeVisible();
  expect(selfHeaders.filter((id) => id === '42')).toHaveLength(1);
  release();
  await expect(page).toHaveURL(/\/console$/);
});

test('known password door keeps its place during a session check', async ({
  page,
}) => {
  await setupMockedApp(page, {
    language: 'en',
    payloadFor: (raw: string) =>
      new URL(raw).pathname === '/api/auth/oidc/status'
        ? {
            success: true,
            data: { password_login: true, email_code_auth: false },
          }
        : undefined,
  });
  let release: () => void = () => undefined;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/user/self', async (route) => {
    await pending;
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({
        success: false,
        error: {
          code: 'internal_error',
          type: 'api_error',
          request_id: 'req_mock',
        },
      }),
    });
  });

  await page.goto('/login');
  const outline = page.locator('.auth-skeleton');
  await expect(outline).toBeVisible();
  await expect(outline.locator('.auth-skeleton__group')).toHaveCount(0);
  await expect(outline.locator('.auth-skeleton__field')).toHaveCount(2);
  const before = await outline.boundingBox();
  release();
  await expect(page.locator('#auth-password')).toBeVisible();
  await page.waitForTimeout(400);
  const after = await page.locator('.auth-column').boundingBox();
  expect(before).not.toBeNull();
  expect(after).not.toBeNull();
  expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(1);
  expect(Math.abs(after!.height - before!.height)).toBeLessThanOrEqual(1);
});

test('a stalled session check releases the sign-in form', async ({ page }) => {
  await page.clock.install();
  await setupMockedApp(page, {
    language: 'en',
    payloadFor: (raw: string) =>
      new URL(raw).pathname === '/api/auth/oidc/status'
        ? {
            success: true,
            data: { password_login: true, email_code_auth: false },
          }
        : undefined,
  });
  let release: () => void = () => undefined;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  let selfChecks = 0;
  await page.route('**/api/user/self', async (route) => {
    selfChecks += 1;
    await pending;
    await route
      .fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, data: { role: 100 } }),
      })
      .catch(() => undefined);
  });

  await page.goto('/login');
  await expect.poll(() => selfChecks).toBeGreaterThanOrEqual(1);
  await expect(page.locator('.auth-skeleton')).toBeVisible();
  await page.clock.fastForward(8_100);
  await expect(page.locator('#auth-identifier')).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
  release();
});

for (const outcome of ['success', 'rejection'] as const) {
  test(`session ${outcome} cannot overwrite a newer login in another tab`, async ({
    page,
  }) => {
    await setupMockedApp(page, {
      language: 'en',
      payloadFor: (raw: string) =>
        new URL(raw).pathname === '/api/auth/oidc/status'
          ? {
              success: true,
              data: { password_login: true, email_code_auth: false },
            }
          : undefined,
    });
    let release: () => void = () => undefined;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    let selfChecks = 0;
    await page.route('**/api/user/self', async (route) => {
      selfChecks += 1;
      await pending;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          outcome === 'success'
            ? { success: true, data: { id: 1, username: 'old', role: 1 } }
            : fail(500, 'internal_error'),
        ),
      });
    });

    await page.goto('/login');
    await expect.poll(() => selfChecks).toBeGreaterThanOrEqual(1);
    // The route can request /self before the lazy login form has mounted.
    // Wait for the form's own pending state before simulating another tab.
    await expect(page.locator('.auth-skeleton')).toBeVisible();
    await page.evaluate(() => {
      localStorage.setItem(
        'user',
        JSON.stringify({ id: 2, username: 'new', role: 100 }),
      );
    });
    release();
    await expect(page.locator('#auth-identifier')).toBeVisible();
    await expect(page).toHaveURL(/\/login$/);
    await expect
      .poll(() =>
        page.evaluate(
          () => JSON.parse(localStorage.getItem('user') || '{}').username,
        ),
      )
      .toBe('new');
  });
}

test('ordinary users still cannot open platform organizations directly', async ({
  page,
}) => {
  await setupMockedApp(page, { user: { role: 1 } });
  await page.goto('/console/organizations');
  await expect(page).toHaveURL(/\/forbidden$/);
});

test('platform administrators can still open organizations', async ({
  page,
}) => {
  await setupMockedApp(page, { user: { role: 100 } });
  await page.goto('/console/organizations');
  await expect(page).toHaveURL(/\/console\/organizations$/);
  await expect(
    page.getByRole('heading', { name: '用户', exact: true }),
  ).toBeVisible();
});
