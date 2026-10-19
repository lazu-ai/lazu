// @ts-nocheck
import { expect, test } from '@playwright/test';

type PasswordResetRequestBody = {
  email: string;
  password: string;
  session_version: number;
  token: string;
  user_id: number;
};

const isPasswordResetRequestBody = (
  value: unknown,
): value is PasswordResetRequestBody => {
  if (value === null || typeof value !== 'object') return false;
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.email === 'string' &&
    typeof candidate.password === 'string' &&
    typeof candidate.session_version === 'number' &&
    typeof candidate.token === 'string' &&
    typeof candidate.user_id === 'number'
  );
};

const mockedStatus = {
  setup: true,
  version: 'smoke-test',
  server_address: 'http://127.0.0.1:4173',
  demo_site_enabled: false,
  self_use_mode_enabled: false,
  announcements: [],
  docs_link: '',
  user_agreement_enabled: true,
  privacy_policy_enabled: true,
  email_verification: true,
};

const mockedStatusResponse = {
  success: true,
  data: mockedStatus,
};

const trackUnexpectedErrors = (page) => {
  const pageErrors = [];
  const consoleErrors = [];

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  page.on('console', (message) => {
    if (message.type() === 'error') {
      consoleErrors.push(message.text());
    }
  });

  return { pageErrors, consoleErrors };
};

test.beforeEach(async ({ page }) => {
  await page.route('**/api/auth/oidc/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          enabled: false,
          email_code_auth: true,
          password_login: true,
          break_glass: false,
          providers: [],
        },
      }),
    });
  });

  await page.route('**/api/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(mockedStatusResponse),
    });
  });

  await page.addInitScript((status) => {
    if (!localStorage.getItem('status')) {
      localStorage.setItem('status', JSON.stringify(status));
    }
    localStorage.setItem('i18nextLng', 'zh');
    localStorage.setItem('system_name', 'Lazu');
    localStorage.setItem('logo', '/logo.png');
  }, mockedStatus);
});

test('login page renders without startup errors', async ({ page }) => {
  const { pageErrors, consoleErrors } = trackUnexpectedErrors(page);

  await page.goto('/login');
  await expect(page).toHaveTitle('Lazu');
  // The door has no visible heading: the wordmark names the site and the
  // credential switch names both ways in. The h1 stays for screen readers,
  // and `.sr-only` keeps a 1x1 box, which Playwright still reports as
  // visible — so presence is asserted, and the absence of a drawn title.
  await expect(page.getByRole('heading', { name: '进入 Lazu' })).toBeAttached();
  await expect(page.locator('.auth-title')).toHaveCount(0);
  await expect(page.getByRole('textbox', { name: '邮箱' })).toBeVisible();
  await expect(page.getByRole('textbox', { name: /密码/i })).toHaveCount(0);
  // Signing in is one step, so the three-step progress bar is gone, and so is
  // the provider orbit that used to fill the right half of the page.
  await expect(page.getByRole('list', { name: '登录进度' })).toHaveCount(0);
  await expect(page.locator('.auth-provider-orbit')).toHaveCount(0);
  // One centred, cardless column on the Yunmu ground.
  await expect(page.locator('.auth-page-shell')).toHaveCSS('display', 'flex');
  await expect(page.locator('.auth-page-card-wrap')).toHaveCSS(
    'display',
    'grid',
  );
  const column = page.locator('.auth-column');
  await expect(column).toBeVisible();
  const columnBox = await column.boundingBox();
  const viewport = page.viewportSize();
  if (columnBox && viewport) {
    const centre = columnBox.x + columnBox.width / 2;
    expect(Math.abs(centre - viewport.width / 2)).toBeLessThanOrEqual(2);
  }
  await page.waitForLoadState('networkidle');

  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
});

test('configured Google and GitHub providers appear on login and registration', async ({
  page,
}) => {
  await page.route('**/api/auth/oidc/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          enabled: false,
          email_code_auth: true,
          password_login: true,
          break_glass: true,
          providers: [
            { id: 'google', provider: 'google' },
            { id: 'github', provider: 'github' },
          ],
        },
      }),
    });
  });

  await page.goto('/login');
  await expect(page.getByRole('button', { name: /Google/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /GitHub/ })).toBeVisible();
  await expect(page.locator('.auth-divider')).toHaveCount(1);

  await page.goto('/register');
  await expect(page.getByRole('button', { name: /Google/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /GitHub/ })).toBeVisible();
  await expect(page.locator('.auth-divider')).toHaveCount(1);
});

/* The door used to render a guess while it waited for /api/auth/oidc/status:
 * a username-and-password form that one round trip later was replaced by the
 * real door. Blocking the answer here holds that window open so the test can
 * look inside it. */
test('the door waits for its configuration instead of flashing a password form', async ({
  page,
}) => {
  let release: () => void = () => undefined;
  const answered = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.unroute('**/api/auth/oidc/status');
  await page.route('**/api/auth/oidc/status', async (route) => {
    await answered;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          enabled: false,
          email_code_auth: true,
          password_login: true,
          break_glass: false,
          providers: [{ id: 'google', provider: 'google' }],
        },
      }),
    });
  });

  await page.goto('/login');

  // An outline in the column's place — never a form that has to be taken back.
  await expect(page.locator('.auth-skeleton')).toBeVisible();
  await expect(page.locator('.auth-column')).toHaveCount(0);
  await expect(page.getByLabel('密码', { exact: true })).toHaveCount(0);
  await expect(
    page.getByRole('button', { name: '登录', exact: true }),
  ).toHaveCount(0);
  // The ground and the chrome do not depend on the answer, so they are up.
  await expect(page.locator('.auth-page-shell')).toBeVisible();

  release();

  await expect(page.getByRole('textbox', { name: '邮箱' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Google/ })).toBeVisible();
  await expect(page.locator('.auth-skeleton')).toHaveCount(0);
});

/* The outline is only worth drawing if the door lands exactly where it stood.
 * Checked at both widths and in both themes because that is where this kind of
 * fix rots: the consent line wraps to two lines in English and one in Chinese,
 * and an outline that guesses at it drops the column when the door arrives. */
for (const viewport of [
  { height: 900, name: 'desktop', width: 1280 },
  { height: 812, name: 'mobile', width: 375 },
]) {
  test(`the door lands exactly where its outline stood (${viewport.name})`, async ({
    page,
  }) => {
    await page.setViewportSize({
      height: viewport.height,
      width: viewport.width,
    });
    let release = () => undefined;
    const answered = new Promise((resolve) => {
      release = resolve;
    });
    await page.unroute('**/api/auth/oidc/status');
    await page.route('**/api/auth/oidc/status', async (route) => {
      await answered;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: {
            enabled: false,
            email_code_auth: true,
            password_login: true,
            break_glass: false,
            providers: [
              { id: 'google', provider: 'google' },
              { id: 'github', provider: 'github' },
            ],
          },
        }),
      });
    });

    await page.goto('/login');
    const outline = await page.locator('.auth-skeleton').boundingBox();
    release();
    await expect(page.locator('.auth-column')).toBeVisible();
    await page.waitForTimeout(400);
    const door = await page.locator('.auth-column').boundingBox();

    expect(Math.round(door.x)).toBe(Math.round(outline.x));
    expect(Math.round(door.width)).toBe(Math.round(outline.width));
    expect(Math.abs(door.y - outline.y)).toBeLessThanOrEqual(1);
    expect(Math.abs(door.height - outline.height)).toBeLessThanOrEqual(1);
  });
}

test('a second visit opens the door without waiting for the answer again', async ({
  page,
}) => {
  await page.goto('/login');
  await expect(page.getByRole('textbox', { name: '邮箱' })).toBeVisible();

  // Now hold the answer back: the remembered one has to carry the first paint.
  let release: () => void = () => undefined;
  const answered = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.unroute('**/api/auth/oidc/status');
  await page.route('**/api/auth/oidc/status', async (route) => {
    await answered;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          enabled: false,
          email_code_auth: true,
          password_login: true,
          break_glass: false,
          providers: [],
        },
      }),
    });
  });

  await page.reload();
  await expect(page.getByRole('textbox', { name: '邮箱' })).toBeVisible();
  await expect(page.locator('.auth-skeleton')).toHaveCount(0);
  release();
});

test('a sign-in link warms the door before it is clicked', async ({ page }) => {
  const doorRequests: string[] = [];
  page.on('request', (request) => {
    const { pathname } = new URL(request.url());
    if (pathname === '/api/auth/oidc/status') doorRequests.push(pathname);
  });

  await page.goto('/');
  await page.locator('a[href="/login"]').first().hover();
  await expect.poll(() => doorRequests.length).toBeGreaterThan(0);
});

test('canonical legal routes render and legacy aliases redirect to them', async ({
  page,
}) => {
  await page.route('**/api/user-agreement**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: '# Lazu 用户协议\n\n测试内容',
      }),
    });
  });
  await page.route('**/api/privacy-policy**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: '# Lazu 隐私政策\n\n测试内容',
      }),
    });
  });

  // The reading layout carries one h1 — the route's own localized title — and
  // the document's repeated title line is dropped from the body rather than
  // printed twice. The body text is the proof the document rendered.
  await page.goto('/terms');
  await expect(page).toHaveURL(/\/terms$/);
  await expect(page.getByRole('heading', { name: '用户协议' })).toBeVisible();
  await expect(page.locator('.legal-doc')).toContainText('测试内容');

  await page.goto('/privacy');
  await expect(page).toHaveURL(/\/privacy$/);
  await expect(page.getByRole('heading', { name: '隐私政策' })).toBeVisible();
  await expect(page.locator('.legal-doc')).toContainText('测试内容');

  await page.goto('/user-agreement');
  await expect(page).toHaveURL(/\/terms$/);

  await page.goto('/privacy-policy');
  await expect(page).toHaveURL(/\/privacy$/);

  await page.goto('/ja/terms');
  await expect(page).toHaveURL(/\/ja\/terms$/);
  await expect(
    page.getByRole('heading', { name: '利用規約', exact: true }),
  ).toBeVisible();
});

test('theme changes preserve a custom favicon without reloading status', async ({
  page,
}) => {
  let statusRequests = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/status') {
      statusRequests += 1;
    }
  });
  await page.addInitScript(() => {
    localStorage.setItem('logo', '/custom-logo.png');
  });

  await page.goto('/login');
  await page.waitForLoadState('networkidle');

  const favicon = page.locator('#app-favicon');
  await expect(favicon).toHaveAttribute('href', /\/custom-logo\.png$/);
  await expect(favicon).not.toHaveAttribute('type', /.+/);
  const initialStatusRequests = statusRequests;

  // The theme switch is a two-segment radio group; pick the other side.
  const themeToggle = page.locator('.theme-pill-toggle');
  const otherTheme = themeToggle.locator(
    '[role="radio"][aria-checked="false"]',
  );
  const otherLabel = await otherTheme.getAttribute('aria-label');
  await otherTheme.click();
  await expect(
    themeToggle.getByRole('radio', { name: otherLabel!, exact: true }),
  ).toHaveAttribute('aria-checked', 'true');
  await expect(favicon).toHaveAttribute('href', /\/custom-logo\.png$/);
  await page.waitForTimeout(100);

  expect(statusRequests).toBe(initialStatusRequests);
});

test('auth page keeps its column mark legible in both themes', async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem('lazu.theme', 'light');
    localStorage.setItem('theme-mode', 'light');
  });
  await page.goto('/login');

  const primaryLogo = page.locator('.auth-column__mark .brand-logo');
  const primaryLogoBox = await primaryLogo.boundingBox();
  expect(primaryLogoBox?.width).toBeGreaterThanOrEqual(40);
  const primarySource = await primaryLogo.getAttribute('src');
  expect(decodeURIComponent(primarySource ?? '')).toContain(
    'viewBox="0 0 1024 1024"',
  );

  // The column mark is the only logo left and must follow the theme.
  const getContainerFill = () =>
    primaryLogo.evaluate((element) => {
      const source = decodeURIComponent(
        element.getAttribute('src')?.replace(/^data:image\/svg\+xml,/, '') ??
          '',
      );
      return source.match(/<circle[^>]+fill="(#[0-9a-f]+)"/i)?.[1] ?? null;
    });
  const lightFill = await getContainerFill();
  expect(lightFill).not.toBeNull();

  await page
    .locator('.theme-pill-toggle [role="radio"][aria-checked="false"]')
    .click();
  await expect.poll(getContainerFill).not.toBe(lightFill);
  await expect(primaryLogo).toHaveCSS('filter', 'none');
});

test('protected client navigation preserves the active destination for login', async ({
  page,
}) => {
  await page.goto('/login');
  await page.evaluate(() => {
    window.history.pushState({}, '', '/console/wallet?range=30');
    window.dispatchEvent(new PopStateEvent('popstate'));
  });

  await expect(page).toHaveURL('/login');
  const from = await page.evaluate(() => window.history.state?.usr?.from);
  expect(from).toMatchObject({
    pathname: '/console/wallet',
    search: '?range=30',
  });
});

test('email-code sign-in advances to the six-digit verification step', async ({
  page,
}) => {
  let startBody: unknown;
  await page.route('**/api/auth/email/start', async (route) => {
    startBody = route.request().postDataJSON();
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true }),
    });
  });

  await page.goto('/login');
  await page.getByRole('textbox', { name: '邮箱' }).fill('grace@example.com');
  await page.getByRole('button', { name: '继续', exact: true }).click();

  await expect(page.getByRole('heading', { name: '输入验证码' })).toBeVisible();
  await expect(page.locator('.auth-otp__box')).toHaveCount(6);
  expect(startBody).toEqual({ email: 'grace@example.com' });
});

test('disabled email-code auth falls back to password without exposing a dead form', async ({
  page,
}) => {
  await page.unroute('**/api/auth/oidc/status');
  await page.route('**/api/auth/oidc/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          enabled: false,
          email_code_auth: false,
          password_login: true,
          break_glass: false,
          providers: [],
        },
      }),
    });
  });

  await page.goto('/login');
  await expect(page.getByRole('textbox', { name: '邮箱' })).toHaveCount(0);
  await expect(page.getByRole('tablist', { name: '登录方式' })).toHaveCount(0);
  await expect(
    page.getByRole('textbox', { name: /用户名或邮箱/i }),
  ).toBeVisible();
  await expect(page.getByLabel('密码', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
});

test('password reset confirmation accepts a 64-character password', async ({
  page,
}) => {
  let requestBody: PasswordResetRequestBody | undefined;
  const selectedPassword = 'L'.repeat(64);

  await page.route('**/api/user/reset/v2', async (route) => {
    const body = route.request().postDataJSON() as unknown;
    if (!isPasswordResetRequestBody(body)) {
      throw new Error('Password reset request body did not match v2 contract');
    }
    requestBody = body;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true }),
    });
  });

  await page.goto(
    '/user/reset?email=user%40example.com&user_id=42&session_version=0&token=smoke-reset-token',
  );
  await expect(page.getByRole('heading', { name: '设置新密码' })).toBeVisible();
  await expect(page.getByLabel('邮箱', { exact: true })).toHaveAttribute(
    'autocomplete',
    'username',
  );
  await expect(page.getByLabel('邮箱', { exact: true })).toHaveAttribute(
    'readonly',
    '',
  );

  await page.getByLabel('新密码', { exact: true }).fill(selectedPassword);
  await page.getByLabel('确认密码', { exact: true }).fill(selectedPassword);
  await page.getByRole('button', { name: '更新密码' }).click();

  await expect(page.getByRole('heading', { name: '密码已更新' })).toBeVisible();
  expect(requestBody).toEqual({
    email: 'user@example.com',
    user_id: 42,
    session_version: 0,
    token: 'smoke-reset-token',
    password: selectedPassword,
  });
});

test('password reset confirmation rejects links without owner claims', async ({
  page,
}) => {
  let resetRequests = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/user/reset/v2') {
      resetRequests += 1;
    }
  });

  await page.goto(
    '/user/reset?email=user%40example.com&token=legacy-reset-token',
  );
  await expect(
    page.getByText('无效的重置链接，请重新发起密码重置请求'),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: '更新密码' })).toHaveCount(0);
  expect(resetRequests).toBe(0);
});

test('password reset confirmation rejects mismatched passwords locally', async ({
  page,
}) => {
  let resetRequests = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/user/reset/v2') {
      resetRequests += 1;
    }
  });

  await page.goto(
    '/user/reset?email=user%40example.com&user_id=42&session_version=0&token=smoke-reset-token',
  );
  await page.getByLabel('新密码', { exact: true }).fill('Lazu-Reset-2026!');
  await page.getByLabel('确认密码', { exact: true }).fill('different-password');
  await page.getByRole('button', { name: '更新密码' }).click();

  await expect(page.getByText('两次输入的密码不一致')).toBeVisible();
  expect(resetRequests).toBe(0);
});

test('password reset confirmation enforces the bcrypt byte limit locally', async ({
  page,
}) => {
  let resetRequests = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/user/reset/v2') {
      resetRequests += 1;
    }
  });

  await page.goto(
    '/user/reset?email=user%40example.com&user_id=42&session_version=0&token=smoke-reset-token',
  );
  const oversizedPassword = '😀'.repeat(19);
  await page.getByLabel('新密码', { exact: true }).fill(oversizedPassword);
  await page.getByLabel('确认密码', { exact: true }).fill(oversizedPassword);
  await page.getByRole('button', { name: '更新密码' }).click();

  await expect(
    page.getByText('密码的 UTF-8 编码不得超过 72 字节'),
  ).toBeVisible();
  expect(resetRequests).toBe(0);
});

test('password reset confirmation fails safely against an old backend', async ({
  page,
}) => {
  await page.route('**/api/user/reset/v2', async (route) => {
    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ success: false, message: 'not found' }),
    });
  });

  await page.goto(
    '/user/reset?email=user%40example.com&user_id=42&session_version=0&token=smoke-reset-token',
  );
  await page.getByLabel('新密码', { exact: true }).fill('Lazu-Reset-2026!');
  await page.getByLabel('确认密码', { exact: true }).fill('Lazu-Reset-2026!');
  await page.getByRole('button', { name: '更新密码' }).click();

  await expect(
    page.getByText('密码重置服务正在升级，请刷新页面后重试'),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: '设置新密码' })).toBeVisible();
});

test('password reset confirmation never reports legacy plaintext responses as success', async ({
  page,
}) => {
  await page.route('**/api/user/reset/v2', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: 'legacy-temp-password' }),
    });
  });

  await page.goto(
    '/user/reset?email=user%40example.com&user_id=42&session_version=0&token=smoke-reset-token',
  );
  await page.getByLabel('新密码', { exact: true }).fill('Lazu-Reset-2026!');
  await page.getByLabel('确认密码', { exact: true }).fill('Lazu-Reset-2026!');
  await page.getByRole('button', { name: '更新密码' }).click();

  await expect(
    page.getByText('密码重置服务正在升级，请刷新页面后重试'),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: '设置新密码' })).toBeVisible();
});

test('auth pages recover from malformed saved status data', async ({
  page,
}) => {
  const { pageErrors } = trackUnexpectedErrors(page);

  await page.goto('/login');
  await page.evaluate(() => localStorage.setItem('status', 'not-json'));
  await page.reload();
  await expect(page.getByRole('heading', { name: '进入 Lazu' })).toBeAttached();
  await expect(page.locator('.auth-title')).toHaveCount(0);

  await page.evaluate(() => localStorage.setItem('status', '[]'));
  await page.goto('/register');
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('heading', { name: '进入 Lazu' })).toBeAttached();
  await expect(page.locator('.auth-title')).toHaveCount(0);
  await expect(page.getByRole('textbox', { name: '邮箱' })).toBeVisible();

  expect(pageErrors).toEqual([]);
});
