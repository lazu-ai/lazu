import { expect, test } from '@playwright/test';

const docsURL = process.env.DOCS_BASE_URL;
test.skip(
  !docsURL,
  'Set DOCS_BASE_URL to a built Docs preview, including the /docs base path.',
);

test('Copy page copies the current Markdown; the menu dismisses outside or with Escape', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto(`${docsURL}/zh/quickstart/`);
  const more = page.locator('.lazu-split-more');
  const menu = page.locator('.lazu-menu');
  await more.click();
  await expect(menu).toBeVisible();
  const response = page.waitForResponse((response) =>
    response.url().endsWith('/zh/quickstart.md'),
  );
  await menu.locator('[data-action="markdown"]').click();
  expect((await response).ok()).toBeTruthy();
  await expect(page.locator('.lazu-toast')).toContainText('已复制');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
    'source: https://lazu.ai/docs/zh/quickstart',
  );
  await more.click();
  await page.keyboard.press('Escape');
  await expect(menu).not.toBeVisible();
  await more.click();
  await page.mouse.click(5, 5);
  await expect(menu).not.toBeVisible();
  const block = page.locator('.vocs_CodeBlock').first();
  const toolbarCopy = block.locator('.vocs_CopyButton');
  const blockBox = await block.boundingBox();
  const copyBox = await toolbarCopy.boundingBox();
  expect(blockBox).not.toBeNull();
  expect(copyBox).not.toBeNull();
  expect(copyBox!.y - blockBox!.y).toBeLessThan(15);
});

test('Copy for agent puts the page link and the agent prompt on the clipboard', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto(`${docsURL}/endpoints/chat/`);
  await page.locator('.lazu-split-more').click();
  await page.locator('.lazu-menu [data-action="agent"]').click();
  const prompt = await page.evaluate(() => navigator.clipboard.readText());
  expect(prompt).toContain('https://lazu.ai/docs/endpoints/chat.md');
  expect(prompt).toContain('$LAZU_API_KEY');
  expect(prompt).toContain('supported_endpoints');
  expect(prompt).not.toContain('supported_endpoint_types');
  expect(prompt.trimEnd().endsWith('My task:')).toBeTruthy();
});

test('Docs API fields show nested details and fit narrow screens', async ({
  page,
}) => {
  await page.goto(`${docsURL}/endpoints/chat/`);
  await expect(page.locator('.lazu-field').first()).toBeVisible();
  await expect(page.locator('.lazu-field-children').first()).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - innerWidth,
    ),
  ).toBe(0);
});

test('Docs Markdown copying falls back without the Clipboard API', async ({
  page,
}) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    });
  });
  await page.goto(`${docsURL}/zh/quickstart/`);
  await page.locator('.lazu-split-main').click();
  await expect(page.locator('.lazu-toast')).toContainText('已复制');
});

for (const [prefix, label] of [
  ['', 'Copy page'],
  ['/zh', '复制页面'],
  ['/zh-TW', '複製頁面'],
  ['/ja', 'ページをコピー'],
]) {
  test(`Page actions speak the route locale ${prefix || 'en'}`, async ({
    page,
  }) => {
    await page.goto(`${docsURL}${prefix}/endpoints/chat/`);
    await expect(page.locator('.lazu-split-main')).toContainText(label);
  });
}

for (const prefix of ['', '/zh', '/zh-TW', '/ja']) {
  test(`Docs home links to API Explorer in ${prefix || 'en'}`, async ({
    page,
  }) => {
    await page.goto(`${docsURL}${prefix}/`);
    await expect(
      page.locator(`.lazu-home-cols a[href="${prefix}/api-reference"]`),
    ).toBeVisible();
  });
}

test('Endpoint workbench clears a previous receipt and preserves streaming on retry', async ({
  page,
}) => {
  let attempts = 0;
  let releaseRetry: (() => void) | undefined;
  await page.route('https://api.lazu.ai/v1/chat/completions', async (route) => {
    if (route.request().method() === 'OPTIONS') {
      await route.fulfill({
        status: 204,
        headers: {
          'access-control-allow-origin': '*',
          'access-control-allow-methods': 'POST, OPTIONS',
          'access-control-allow-headers': 'authorization, content-type',
        },
      });
      return;
    }
    expect(route.request().postDataJSON().stream).toBe(true);
    expect(route.request().postDataJSON().stream_options).toEqual({
      include_usage: true,
    });
    attempts += 1;
    if (attempts === 2)
      await new Promise<void>((resolve) => {
        releaseRetry = resolve;
      });
    await route.fulfill({
      status: 200,
      headers: {
        'access-control-allow-origin': '*',
        'access-control-expose-headers':
          'x-lazu-request-id, x-lazu-cost-usd, x-lazu-lane',
        'content-type': 'text/event-stream',
        'x-lazu-request-id': `req_test_${attempts}`,
        'x-lazu-cost-usd': '0.0001',
        'x-lazu-lane': 'stable',
      },
      body:
        'data: {"choices":[],"usage":null}\n\n' +
        'data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":3}}\n\n' +
        'data: [DONE]\n\n',
    });
  });

  await page.goto(`${docsURL}/endpoints/chat/`);
  await page.locator('#key-chat').fill('sk-test-1234');
  await page.locator('.lazu-send-stream input').check();
  await page.locator('.lazu-send-button').click();
  const live = page.locator('.lazu-receipt[data-live="true"]');
  await expect(live).toContainText('req_test_1');
  await expect(live).toContainText('gpt-6-luna');
  await expect(live).toContainText('stable');
  await expect(live).toContainText('11 / 3');
  await expect(live).toContainText('••••1234');

  await page.locator('.lazu-send-button').click();
  await expect(page.locator('.lazu-receipt[aria-busy="true"]')).toBeVisible();
  await expect(live).toHaveCount(0);
  releaseRetry?.();
  await expect(live).toContainText('req_test_2');
  await expect(live).toContainText('11 / 3');
  expect(attempts).toBe(2);
});

test('Phone header keeps Console visible and headings at the display floor', async ({
  page,
}) => {
  for (const width of [320, 390, 600]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`${docsURL}/zh/endpoints/chat/`);
    const consoleLink = page.locator('.vocs_MobileTopNav .dub-console-cta');
    await expect(consoleLink).toBeVisible();
    await expect(consoleLink).toHaveAttribute(
      'href',
      'https://lazu.ai/console',
    );
    const box = await consoleLink.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
    expect(
      await page
        .locator('.lazu-endpoint-main h1')
        .evaluate((element) =>
          Number.parseFloat(getComputedStyle(element).fontSize),
        ),
    ).toBeGreaterThanOrEqual(42);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBe(width);
  }
});

test('Non-sendable endpoints keep their examples inline in a reading column', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const path of ['/endpoints/files/', '/endpoints/usage-requests/']) {
    await page.goto(`${docsURL}${path}`);
    const article = page.locator('.lazu-endpoint-main');
    const examples = page.locator('.lazu-endpoint-examples--inline');
    await expect(examples).toBeVisible();
    await expect(page.locator('.lazu-send')).toHaveCount(0);
    const articleBox = await article.boundingBox();
    const examplesBox = await examples.boundingBox();
    expect(articleBox).not.toBeNull();
    expect(examplesBox).not.toBeNull();
    expect(examplesBox!.y).toBeGreaterThan(articleBox!.y);
    expect(examplesBox!.x).toBeGreaterThanOrEqual(articleBox!.x);
    expect(examplesBox!.width).toBeLessThanOrEqual(760);
    expect(
      await examples.evaluate((element) => getComputedStyle(element).position),
    ).toBe('static');
  }
  await page.goto(`${docsURL}/endpoints/chat/`);
  await expect(page.locator('.lazu-endpoint-examples--live')).toBeVisible();
  expect(
    await page
      .locator('.lazu-endpoint-examples--live')
      .evaluate((element) => getComputedStyle(element).position),
  ).toBe('fixed');
});

test('Embedding picker follows advertised endpoint capabilities', async ({
  page,
}) => {
  await page.route('https://api.lazu.ai/api/pricing', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'access-control-allow-origin': '*' },
      contentType: 'application/json',
      body: JSON.stringify({
        data: [
          {
            model_name: 'bge-large',
            lanes: [{ available: true }],
            supported_endpoints: { openai: { path: '/v1/embeddings' } },
          },
          {
            model_name: 'foo-embedding',
            lanes: [{ available: true }],
            supported_endpoints: { openai: { path: '/v1/chat/completions' } },
          },
        ],
      }),
    }),
  );
  await page.goto(`${docsURL}/endpoints/embeddings/`);
  const picker = page.locator('#model-embeddings');
  await expect(picker.locator('option[value="bge-large"]')).toHaveCount(1);
  await expect(picker.locator('option[value="foo-embedding"]')).toHaveCount(0);
  await expect(
    picker.locator('option[value="text-embedding-3-small"]'),
  ).toHaveCount(0);
  await expect(picker).toHaveValue('bge-large');
});

test('Home copy does not claim success when both clipboard methods fail', async ({
  page,
}) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error('denied')) },
    });
    document.execCommand = () => false;
  });
  await page.goto(`${docsURL}/`);
  const copy = page.locator('.lazu-home-bench .lazu-cb-head .lazu-cb-copy');
  await copy.click();
  await expect(copy).toHaveText('Copy');
});

test('Stream switch updates every displayed and copied request sample', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto(`${docsURL}/endpoints/chat/`);
  await page.locator('.lazu-send-stream input').check();
  const request = page.locator('.lazu-bench .lazu-cb').first();
  for (const [tab, streamFlag] of [
    ['cURL', '"stream": true'],
    ['Python', 'stream=True'],
    ['TypeScript', 'stream: true'],
  ]) {
    await request.getByRole('button', { name: tab, exact: true }).click();
    await expect(request.locator('pre')).toContainText(streamFlag);
    await request.locator('.lazu-cb-copy').click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
      streamFlag,
    );
  }
  await request.getByRole('button', { name: 'Python', exact: true }).click();
  await expect(request.locator('pre')).toContainText('if chunk.choices:');
  await expect(request.locator('pre')).toContainText('include_usage');
});

test('Search receipt shows billable search units', async ({ page }) => {
  await page.route('https://api.lazu.ai/v1/search', async (route) => {
    if (route.request().method() === 'OPTIONS') {
      await route.fulfill({
        status: 204,
        headers: {
          'access-control-allow-origin': '*',
          'access-control-allow-methods': 'POST, OPTIONS',
          'access-control-allow-headers': 'authorization, content-type',
        },
      });
      return;
    }
    await route.fulfill({
      status: 200,
      headers: { 'access-control-allow-origin': '*' },
      contentType: 'application/json',
      body: JSON.stringify({
        results: [],
        usage: { web_search_requests: 1, web_search_billable_units: 2 },
      }),
    });
  });
  await page.goto(`${docsURL}/endpoints/search/`);
  await page.locator('#key-search').fill('sk-test-1234');
  await page.locator('.lazu-send-button').click();
  const receipt = page.locator('.lazu-receipt[data-live="true"]');
  await expect(receipt).toContainText('Search requests / billable units');
  await expect(receipt).toContainText('1 / 2');
  await expect(receipt).not.toContainText('Tokens in / out');
});

test('Page actions refresh their prose header after client navigation', async ({
  page,
}) => {
  await page.goto(`${docsURL}/quickstart/`);
  const head = page.locator('.lazu-page-head--prose');
  await expect(head).toContainText('Get started');
  await page.locator('.vocs_Sidebar a[href="/models/catalog"]').click();
  await expect(page).toHaveURL(/\/models\/catalog\/?$/);
  await expect(head).toContainText('Models');
  await expect(head).not.toContainText('Get started');
});

test('Gemini reports failed prompt copy', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error('denied')) },
    });
    document.execCommand = () => false;
  });
  await page.goto(`${docsURL}/endpoints/chat/`);
  await page.locator('.lazu-split-more').click();
  const popup = page.waitForEvent('popup');
  await page.locator('.lazu-menu [data-action="gemini"]').click();
  await (await popup).close();
  await expect(page.locator('.lazu-toast')).toContainText('Copy failed');
});

test('Mobile page-actions menus stay inside the viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const path of ['/zh/endpoints/chat/', '/zh/quickstart/']) {
    await page.goto(`${docsURL}${path}`);
    await page.locator('.lazu-split-more').click();
    const box = await page.locator('.lazu-menu').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(19);
    expect(box!.x + box!.width).toBeLessThanOrEqual(371);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBe(390);
  }
});
