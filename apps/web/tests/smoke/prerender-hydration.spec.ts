import { expect, test } from '@playwright/test';

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
]) {
  test(`cold homepage has no runtime or hydration errors at ${viewport.width}px`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    page.on('console', (message) => {
      if (
        message.type() === 'error' &&
        /hydration|server html|did not match|Minified React error/i.test(
          message.text(),
        )
      ) {
        errors.push(message.text());
      }
    });
    await page.setViewportSize(viewport);
    await page.goto('/');
    await expect(page.locator('#root')).toHaveAttribute('data-hydrate', 'true');
    await expect(page.locator('.lazu-landing__headline')).toBeVisible();
    await page.getByRole('contentinfo').scrollIntoViewIfNeeded();
    await expect(page.getByRole('contentinfo')).toBeVisible();
    expect(errors).toEqual([]);
  });
}

test('marketing HTML contains the real React landing page', async ({
  request,
}) => {
  const response = await request.get('/');
  const html = await response.text();

  expect(response.ok()).toBe(true);
  expect(html).toContain('data-prerender-route="/"');
  expect(html).toContain('lazu-landing__headline');
  expect(html).not.toContain('lazu-landing-fallback');
});

test('matching prerender hydrates without React mismatch errors', async ({
  page,
}) => {
  const hydrationErrors: string[] = [];
  page.on('console', (message) => {
    if (
      message.type() === 'error' &&
      /hydration|server html|did not match/i.test(message.text())
    ) {
      hydrationErrors.push(message.text());
    }
  });
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en');
    localStorage.setItem('lazu.theme', 'light');
  });

  await page.goto('/');
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();
  const footer = page.getByRole('contentinfo');
  await expect(footer).toBeVisible();
  await expect(footer.locator('a[href="/privacy"]')).toBeVisible();
  await expect(footer.locator('a[href="/terms"]')).toBeVisible();
  await expect(
    footer.getByRole('link', { name: 'Lazu', exact: true }),
  ).toHaveAttribute('href', '/');
  await expect(page.locator('#root')).toHaveAttribute('data-hydrate', 'true');
  expect(hydrationErrors).toEqual([]);
});

test('legacy cached footer settings do not disturb the public prerender', async ({
  page,
}) => {
  const hydrationErrors: string[] = [];
  page.on('console', (message) => {
    if (
      message.type() === 'error' &&
      /hydration|server html|did not match/i.test(message.text())
    ) {
      hydrationErrors.push(message.text());
    }
  });
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en');
    localStorage.setItem('lazu.theme', 'light');
    localStorage.setItem('system_name', 'Self-hosted Lazu');
    localStorage.setItem('footer_html', '<p>Self-hosted footer</p>');
  });

  await page.goto('/');
  await expect(page.locator('#root')).toHaveAttribute('data-hydrate', 'true');
  // The current public footer uses the locked Brand chrome, not the old
  // console footer_html setting. Stale preferences must not break hydration.
  await expect(
    page
      .getByRole('contentinfo')
      .getByRole('link', { name: 'Lazu', exact: true }),
  ).toBeVisible();
  expect(hydrationErrors).toEqual([]);
});

test('legacy cached links do not replace canonical public footer URLs', async ({
  page,
}) => {
  const hydrationErrors: string[] = [];
  page.on('console', (message) => {
    if (
      message.type() === 'error' &&
      /hydration|server html|did not match/i.test(message.text())
    ) {
      hydrationErrors.push(message.text());
    }
  });
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en');
    localStorage.setItem('lazu.theme', 'light');
    localStorage.setItem(
      'status',
      JSON.stringify({
        docs_link: 'https://docs.example.com',
        privacy_link: '/self-hosted-privacy',
        terms_link: '/self-hosted-terms',
      }),
    );
  });

  await page.goto('/');
  await expect(page.locator('#root')).toHaveAttribute('data-hydrate', 'true');
  await expect(page.locator('footer a[href="/privacy"]')).toBeVisible();
  await expect(page.locator('footer a[href="/terms"]')).toBeVisible();
  expect(hydrationErrors).toEqual([]);
});

test('landing facts start loading after the prerender hydrates', async ({
  page,
}) => {
  await page.route('**/api/pricing', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: [
          {
            has_price: true,
            input_per_mtok: 1,
            model_name: 'hydrated-smoke-model',
            output_per_mtok: 2,
            vendor_id: 'smoke-vendor',
          },
        ],
        vendors: [{ id: 'smoke-vendor', name: 'Smoke Vendor' }],
      }),
    }),
  );
  await page.route('**/api/public/status', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          model_families: [{ available_count: 1, model_count: 1 }],
        },
      }),
    }),
  );

  await page.goto('/');

  // The full smoke suite renders several screenshot matrices in parallel;
  // leave enough time for the hydrated query to be scheduled under load.
  await expect(page.getByText('hydrated-smoke-model')).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByText('Smoke Vendor')).toBeVisible({
    timeout: 15_000,
  });
});

test('cookie locale takes precedence before hydration', async ({
  baseURL,
  context,
  page,
}) => {
  await context.addCookies([
    {
      name: 'i18next',
      value: 'zh',
      url: baseURL,
    },
  ]);
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en');
    localStorage.setItem('lazu.theme', 'light');
  });

  await page.goto('/');
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();
  await expect(page.locator('#root')).not.toHaveAttribute(
    'data-hydrate',
    'true',
  );
  await expect(page.locator('.lazu-landing__headline')).toContainText(
    '一个账户，一张账单',
  );
});

test('query locale takes precedence over cookie before hydration', async ({
  baseURL,
  context,
  page,
}) => {
  await context.addCookies([
    {
      name: 'i18next',
      value: 'zh',
      url: baseURL,
    },
  ]);
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'zh');
    localStorage.setItem('lazu.theme', 'light');
  });

  await page.goto('/?lng=en');
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();
  await expect(page.locator('#root')).toHaveAttribute('data-hydrate', 'true');
});

test('SPA fallback discards a marketing prerender for non-marketing routes', async ({
  page,
  request,
}) => {
  const html = await (await request.get('/login')).text();
  const mainModule = html.match(
    /<script type="module" crossorigin src="([^"]+)"/,
  )?.[1];
  expect(html).toContain('data-prerender-route="/"');
  expect(mainModule).toBeTruthy();

  let releaseModule: (() => void) | undefined;
  const moduleGate = new Promise<void>((resolve) => {
    releaseModule = resolve;
  });
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en');
    localStorage.setItem('lazu.theme', 'light');
  });
  await page.route('**/api/status', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: { setup: true, system_name: 'Lazu' },
      }),
    }),
  );
  await page.route('**/api/auth/oidc/status', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          enabled: false,
          email_code_auth: true,
          password_login: true,
          providers: [],
        },
      }),
    }),
  );
  await page.route(`**${mainModule}`, async (route) => {
    await moduleGate;
    await route.continue();
  });

  await page.goto('/login', { waitUntil: 'commit' });
  await expect(page.locator('#root')).toBeAttached();
  await expect(page.locator('#root')).toBeEmpty();
  await expect(page.locator('#root')).not.toHaveAttribute(
    'data-hydrate',
    'true',
  );

  releaseModule?.();
  await expect(page.getByRole('textbox', { name: 'Email' })).toBeVisible();
});

test('non-matching locale holds geometry without flashing English before startup', async ({
  page,
  request,
}) => {
  const html = await (await request.get('/')).text();
  const mainModule = html.match(
    /<script type="module" crossorigin src="([^"]+)"/,
  )?.[1];
  expect(mainModule).toBeTruthy();

  let releaseModule: (() => void) | undefined;
  const moduleGate = new Promise<void>((resolve) => {
    releaseModule = resolve;
  });
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'zh');
    localStorage.setItem('lazu.theme', 'light');
  });
  await page.route(`**${mainModule}`, async (route) => {
    await moduleGate;
    await route.continue();
  });

  const navigation = page.goto('/', { waitUntil: 'commit' });
  await navigation;
  await expect(page.locator('#root')).toBeAttached();
  await expect(page.locator('#root')).toHaveCSS('visibility', 'hidden');
  expect((await page.locator('#root').boundingBox())?.height).toBeGreaterThan(
    500,
  );
  await expect(page.locator('.lazu-landing__headline')).not.toBeVisible();

  releaseModule?.();
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();
});

test('locale-prefixed routes serve their own prerendered language', async ({
  request,
}) => {
  const zh = await (await request.get('/zh/')).text();
  expect(zh).toContain('<html lang="zh"');
  expect(zh).toContain('data-prerender-route="/zh/"');
  expect(zh).toContain('data-prerender-language="zh"');
  expect(zh).toContain('rel="canonical" href="https://lazu.ai/zh/"');
  expect(zh).toContain('hreflang="x-default"');

  // Trailing slash: vite preview only resolves directory indexes with it;
  // production nginx (try_files $uri $uri/) serves both forms.
  const jaFaq = await (await request.get('/ja/faq/')).text();
  expect(jaFaq).toContain('<html lang="ja"');
  expect(jaFaq).toContain('data-prerender-route="/ja/faq"');
});

test('legal documents have canonical localized prerenders and sitemap entries', async ({
  request,
}) => {
  const privacy = await (await request.get('/privacy/')).text();
  expect(privacy).toContain('<html lang="en"');
  expect(privacy).toContain('data-prerender-route="/privacy"');
  expect(privacy).toContain('rel="canonical" href="https://lazu.ai/privacy"');
  expect(privacy).toContain(
    'hreflang="zh-TW" href="https://lazu.ai/zh-TW/privacy"',
  );

  const jaTerms = await (await request.get('/ja/terms/')).text();
  expect(jaTerms).toContain('<html lang="ja"');
  expect(jaTerms).toContain('data-prerender-route="/ja/terms"');
  expect(jaTerms).toContain('rel="canonical" href="https://lazu.ai/ja/terms"');

  const sitemap = await (await request.get('/sitemap-pages.xml')).text();
  expect(sitemap).toContain('<loc>https://lazu.ai/privacy</loc>');
  expect(sitemap).toContain('<loc>https://lazu.ai/zh/privacy</loc>');
  expect(sitemap).toContain('<loc>https://lazu.ai/zh-TW/terms</loc>');
  expect(sitemap).toContain('<loc>https://lazu.ai/ja/terms</loc>');
});

test('the URL locale outranks a stored English preference on /zh/', async ({
  page,
}) => {
  const hydrationErrors: string[] = [];
  page.on('console', (message) => {
    if (
      message.type() === 'error' &&
      /hydration|server html|did not match/i.test(message.text())
    ) {
      hydrationErrors.push(message.text());
    }
  });
  // A visitor whose stored preference is English opens the Chinese URL:
  // the URL wins — the Chinese prerender must hydrate, not be discarded.
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en');
    localStorage.setItem('lazu.theme', 'light');
  });

  await page.goto('/zh/');
  await expect(page.locator('#root')).toHaveAttribute('data-hydrate', 'true');
  await expect(page.locator('.lazu-landing__headline')).toContainText(
    '一个账户，一张账单',
  );
  await expect(page.locator('footer a[href="/zh/privacy"]')).toBeVisible();
  await expect(page.locator('footer a[href="/zh/terms"]')).toBeVisible();
  expect(hydrationErrors).toEqual([]);
});

test('the language switcher navigates between locale URLs', async ({
  page,
}) => {
  await page.goto('/');
  // The prerendered header is static markup until React hydrates — a click
  // that lands before hydration opens nothing. Retry until React is live.
  const openLangMenu = async () => {
    const menu = page.locator('.lazu-landing__lang-menu');
    await expect
      .poll(async () => {
        // Check first so a delayed menu from the previous hydration-era click
        // is not immediately toggled closed on the next retry.
        if (await menu.isVisible()) return true;
        await page.locator('.lazu-landing__lang-trigger').first().click();
        return menu.isVisible();
      })
      .toBe(true);
  };

  await openLangMenu();
  await page.getByRole('menuitemradio', { name: '简体中文' }).click();
  await expect(page).toHaveURL(/\/zh\/$/);

  // And back to the unprefixed English tree.
  await openLangMenu();
  await page.getByRole('menuitemradio', { name: 'English' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page).not.toHaveURL(/\/zh\//);
});

for (const language of ['en', 'zh-TW', 'ja']) {
  test(`dark startup keeps ${language} content and theme without hydration errors`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.addInitScript((language) => {
      localStorage.setItem('i18nextLng', language);
      localStorage.setItem('lazu.theme', 'dark');
    }, language);
    await page.goto('/');
    await expect(page.locator('.lazu-landing__headline')).toBeVisible();
    await expect(page.locator('html')).toHaveClass(/dark/);
    expect(errors).toEqual([]);
  });
}
