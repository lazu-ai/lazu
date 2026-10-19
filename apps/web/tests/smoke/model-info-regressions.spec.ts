import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

test('failed review envelope is an error, never an empty successful queue', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/model_info/reviews'
        ? { success: false, message: 'review database unavailable' }
        : undefined,
  });
  await page.goto('/console/model-ops/updates');
  await expect(page.getByText('资料更新读取失败')).toBeVisible({
    timeout: 15000,
  });
  await expect(page.getByText('还没有同步过', { exact: false })).toHaveCount(0);
});

test('registration discards old prefill and ignores late lookup responses', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  let finishSlow: (() => void) | undefined;
  const slow = new Promise<void>((resolve) => {
    finishSlow = resolve;
  });
  await page.route('**/api/model_info/lookup?*', async (route) => {
    const name = new URL(route.request().url()).searchParams.get('model');
    if (name === 'gpt-slow') await slow;
    await route.fulfill({
      json: {
        success: true,
        data: {
          found: true,
          prefill: {
            model_name: name,
            name,
            maker: 'openai',
            models_dev_id: `openai/${name}`,
            release_date: name === 'gpt-final' ? '2026-09-22' : '2026-08-01',
            context_length: 400000,
            filled: ['release_date'],
            capabilities: { supports_vision: true },
          },
        },
      },
    });
  });
  await page.goto('/console/model-ops?model=new');
  const name = page.locator('#model-name');
  await name.fill('gpt-first');
  await name.blur();
  await expect(page.locator('#model-release')).toHaveValue('2026-08-01');
  await name.fill('gpt-slow');
  await expect(page.locator('#model-release')).toHaveValue('');
  await name.blur();
  await expect(
    page.getByRole('button', { name: '登记模型', exact: true }),
  ).toBeDisabled();
  await name.fill('gpt-final');
  await name.blur();
  await expect(page.locator('#model-release')).toHaveValue('2026-09-22');
  const oldResponse = page.waitForResponse((response) =>
    response.url().includes('model=gpt-slow'),
  );
  finishSlow?.();
  await oldResponse;
  await expect(page.locator('#model-release')).toHaveValue('2026-09-22');
  await expect(page.getByText('在 models.dev 找到 gpt-final')).toBeVisible();
  // Re-entering an upstream value is still an explicit manual decision.
  await page.locator('#model-release').fill('');
  await page.locator('#model-release').fill('2026-09-22');
  await name.fill('gpt-other');
  await name.blur();
  await expect(page.getByText('在 models.dev 找到 gpt-other')).toBeVisible();
  await expect(page.locator('#model-release')).toHaveValue('2026-09-22');
});

test('failed lookup is shown as a failure rather than model not found', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await page.route('**/api/model_info/lookup?*', (route) =>
    route.fulfill({
      json: {
        success: false,
        message: 'models.dev timed out',
      },
    }),
  );
  await page.goto('/console/model-ops?model=new');
  await page.locator('#model-name').fill('gpt-example');
  await page.locator('#model-name').blur();
  await expect(page.getByText('models.dev timed out')).toBeVisible();
  await expect(page.getByText('models.dev 里没有这个 ID')).toHaveCount(0);
});

for (const peak of [false, true]) {
  test(`long-context inherited prices receive the peak factor once (${peak})`, async ({
    page,
  }) => {
    await page.clock.setFixedTime(
      new Date(peak ? '2026-09-28T02:00:00Z' : '2026-09-28T06:00:00Z'),
    );
    await setupMockedApp(page, {
      user: { role: 1 },
      payloadFor: (url: string) =>
        new URL(url).pathname === '/api/user/models/catalog'
          ? {
              success: true,
              data: [
                {
                  id: 'tier-fixture',
                  family: 'OpenAI',
                  accessible: true,
                  lanes: [
                    {
                      key: 'stable',
                      available: true,
                      peak_hours_utc: '1-4',
                      peak_multiplier: 2,
                      pricing: {
                        input: 2,
                        output: 10,
                        cache_read: 0.2,
                        context_tier: { threshold: 272000, input: 4 },
                      },
                    },
                  ],
                },
              ],
            }
          : undefined,
    });
    await page.goto('/console/models');
    await page.locator('.plaza-card__open').first().click();
    const cells = page.locator('.plaza-tier__long');
    await expect(cells).toHaveCount(3);
    expect(
      (await cells.allTextContents()).map((t) =>
        Number(t.replace(/[^0-9.]/g, '')),
      ),
    ).toEqual(peak ? [8, 20, 0.4] : [4, 10, 0.2]);
  });
}

test('review count reserves its final box while pending and prefetches updates on intent', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  let release: (() => void) | undefined;
  const responseGate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/model_info/reviews', async (route) => {
    await responseGate;
    await route.fulfill({
      json: {
        success: true,
        data: { items: [], last_run_at: 0, pending_count: 17 },
      },
    });
  });
  await page.goto('/console/model-ops');
  const link = page.getByRole('link', { name: /资料更新/ });
  const badge = link.locator('.mo-pagenav__badge');
  await expect(badge).toBeVisible();
  await expect(badge).not.toHaveText('0');
  const pending = await badge.boundingBox();
  const chunk = page.waitForRequest((request) =>
    /\/UpdatesPage-[^/]+\.js/.test(request.url()),
  );
  await link.hover();
  await chunk;
  expect(new URL(page.url()).pathname).toBe('/console/model-ops');
  release?.();
  await expect(badge).toHaveText('17');
  const settled = await badge.boundingBox();
  expect(pending).not.toBeNull();
  expect(settled).toEqual(pending);
});

test('one-hour cache write prices can be inspected, changed and cleared in the long-context editor', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await page.route('**/api/model_info/lookup?*', (route) =>
    route.fulfill({
      json: {
        success: true,
        data: {
          found: true,
          prefill: {
            model_name: 'gpt-4o-mini',
            filled: [],
            cache_write_per_mtok: 1,
            context_tier: { threshold: 200000, cache_write_5m_per_mtok: 2 },
          },
        },
      },
    }),
  );
  await page.goto('/console/model-ops');
  await page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .first()
    .getByRole('button', { name: '编辑' })
    .click();
  const inspector = page.locator('.mo-inspector');
  await inspector.getByRole('tab', { name: '定价', exact: true }).click();
  await inspector.getByRole('switch', { name: '按输入长度分段收费' }).check();
  const base = inspector.getByLabel(/稳定档 缓存写入 1 小时 .* 以内/);
  const high = inspector.getByLabel(/稳定档 缓存写入 1 小时 .* 以上/);
  await base.fill('0.8');
  await high.fill('1.6');
  await expect(high).toHaveValue('1.6');
  await high.fill('');
  await expect(high).toHaveValue('');
  await high.fill('1.7');
  await inspector
    .getByRole('button', { name: '保存档位配置', exact: true })
    .click();
  const request = page.waitForRequest(
    (req) =>
      new URL(req.url()).pathname === '/api/model_ops/models/tier_config' &&
      req.method() !== 'GET',
  );
  await page
    .getByRole('dialog')
    .getByRole('button', { name: '确认保存' })
    .click();
  const payload: unknown = (await request).postDataJSON();
  expect(payload).toMatchObject({
    model: 'gpt-4o-mini',
    tiers: expect.arrayContaining([
      expect.objectContaining({
        lane: 'direct',
        price: expect.objectContaining({
          cache_write_1h_per_mtok: 0.8,
          context_tier_cache_write_1h_per_mtok: 1.7,
        }),
      }),
    ]),
  });
});

for (const tier of [{ output: 15 }, { input: 4 }]) {
  test(`card shows inherited prices for partial tier ${JSON.stringify(tier)}`, async ({
    page,
  }) => {
    await page.clock.setFixedTime(new Date('2026-09-28T02:00:00Z'));
    await setupMockedApp(page, {
      user: { role: 1 },
      payloadFor: (url: string) =>
        new URL(url).pathname === '/api/user/models/catalog'
          ? {
              success: true,
              data: [
                {
                  id: 'partial-tier',
                  family: 'OpenAI',
                  accessible: true,
                  lanes: [
                    {
                      key: 'stable',
                      available: true,
                      peak_hours_utc: '1-4',
                      peak_multiplier: 2,
                      pricing: {
                        input: 3,
                        output: 12,
                        context_tier: { threshold: 200000, ...tier },
                      },
                      your_price: {
                        input: 2,
                        output: 10,
                        context_tier: { threshold: 200000, ...tier },
                      },
                    },
                  ],
                },
              ],
            }
          : undefined,
    });
    await page.goto('/console/models');
    await expect(page.locator('.plaza-laneblock__tier')).toContainText(
      'output' in tier ? '$4.00 / $30.00' : '$8.00 / $20.00',
    );
  });
}

for (const method of ['manual', 'official']) {
  test(`adding an agent-lane tier preserves the shared threshold (${method})`, async ({
    page,
  }) => {
    await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      payloadFor: (url: string) =>
        new URL(url).pathname === '/api/pricing/sell_prices'
          ? {
              success: true,
              data: [
                {
                  model_name: 'gpt-4o-mini',
                  channel_group: 'direct',
                  active: true,
                  input_per_mtok: 0.15,
                  output_per_mtok: 0.6,
                  context_tier_threshold: 272000,
                  context_tier_input_per_mtok: 0.3,
                },
                {
                  model_name: 'gpt-4o-mini',
                  channel_group: 'cheap',
                  active: true,
                  input_per_mtok: 0.09,
                  output_per_mtok: 0.36,
                },
              ],
            }
          : undefined,
    });
    await page.route('**/api/model_info/lookup?*', (route) =>
      route.fulfill({
        json: {
          success: true,
          data: {
            found: true,
            prefill: {
              model_name: 'gpt-4o-mini',
              filled: [],
              official_input_per_mtok: 1,
              official_output_per_mtok: 2,
              context_tier: {
                threshold: 272000,
                input_per_mtok: 2,
                output_per_mtok: 4,
              },
            },
          },
        },
      }),
    );
    await page.goto('/console/model-ops');
    await page
      .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
      .first()
      .getByRole('button', { name: '编辑' })
      .click();
    const inspector = page.locator('.mo-inspector');
    await inspector.getByRole('tab', { name: '定价', exact: true }).click();
    await expect(inspector.locator('#mo-ctier-threshold')).toHaveValue(
      '272000',
    );
    if (method === 'official') {
      await inspector
        .getByRole('button', { name: '按官方比例填高段价' })
        .click();
    } else {
      await inspector
        .getByLabel('便宜档 输出 272K 以上', { exact: true })
        .fill('0.7');
    }
    await inspector
      .getByRole('button', { name: '保存档位配置', exact: true })
      .click();
    const request = page.waitForRequest(
      (req) =>
        new URL(req.url()).pathname === '/api/model_ops/models/tier_config' &&
        req.method() !== 'GET',
    );
    await page
      .getByRole('dialog')
      .getByRole('button', { name: '确认保存' })
      .click();
    const payload: unknown = (await request).postDataJSON();
    expect(payload).toMatchObject({
      tiers: expect.arrayContaining([
        expect.objectContaining({
          lane: 'cheap',
          price: expect.objectContaining({
            context_tier_threshold: 272000,
            context_tier_output_per_mtok: method === 'official' ? 0.72 : 0.7,
          }),
        }),
      ]),
    });
  });
}

test('release and tier suggestions can be dismissed and their badge updates', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  const dismissed = new Set<number>();
  const rows = [
    {
      id: 9001,
      model_name: 'new-release-fixture',
      kind: 'new_release',
      note: 'lab',
      model_id: 0,
      field: 'release',
    },
    {
      id: 9002,
      model_name: 'tier-gap-fixture',
      kind: 'tier_gap',
      note: '200000',
      model_id: 1,
      field: 'context_tier',
    },
  ].map((row) => ({
    ...row,
    maker: 'openai',
    status: 'pending',
    sync_run: 0,
    created_time: 0,
    local_value: '',
    upstream_value: '',
  }));
  await page.route('**/api/model_info/reviews', (route) =>
    route.fulfill({
      json: {
        success: true,
        data: {
          items: rows.filter((row) => !dismissed.has(row.id)),
          pending_count: rows.length - dismissed.size,
          last_run_at: 0,
        },
      },
    }),
  );
  await page.route('**/api/model_info/reviews/*/keep', async (route) => {
    dismissed.add(
      Number(new URL(route.request().url()).pathname.split('/').slice(-2)[0]),
    );
    await route.fulfill({ json: { success: true } });
  });
  await page.goto('/console/model-ops/updates');
  const badge = page.locator('.mo-pagenav__badge');
  await expect(badge).toHaveText('2');
  for (const [index, name] of [
    'new-release-fixture',
    'tier-gap-fixture',
  ].entries()) {
    const row = page.locator('.mo-updates__row', { hasText: name });
    const dismiss = row.getByRole('button', { name: '忽略此建议' });
    await expect(dismiss).toBeVisible();
    await dismiss.click();
    await expect(row).toHaveCount(0);
    expect(dismissed.size).toBe(index + 1);
  }
  await expect(badge).toBeHidden();
});

for (const peak of [false, true]) {
  test(`long-context details disclose both cache-write durations (${peak})`, async ({
    page,
  }) => {
    await page.clock.setFixedTime(
      new Date(peak ? '2026-09-28T02:00:00Z' : '2026-09-28T06:00:00Z'),
    );
    await setupMockedApp(page, {
      user: { role: 1 },
      payloadFor: (url: string) =>
        new URL(url).pathname === '/api/user/models/catalog'
          ? {
              success: true,
              data: [
                {
                  id: 'cache-tier-model',
                  family: 'OpenAI',
                  accessible: true,
                  lanes: [
                    {
                      key: 'stable',
                      available: true,
                      peak_hours_utc: '1-4',
                      peak_multiplier: 2,
                      pricing: {
                        input: 2,
                        output: 10,
                        cache_write_5m: 3,
                        cache_write_1h: 5,
                        context_tier: {
                          threshold: 200000,
                          input: 4,
                          cache_write_1h: 8,
                        },
                      },
                    },
                  ],
                },
              ],
            }
          : undefined,
    });
    await page.goto('/console/models');
    await page.locator('.plaza-card__open').first().click();
    const table = page.locator('.plaza-tier__table');
    await expect(table.getByRole('row', { name: /缓存写入 5m/ })).toContainText(
      peak ? '$6.00' : '$3.00',
    );
    const oneHour = table.getByRole('row', { name: /缓存写入 1h/ });
    await expect(oneHour.locator('.plaza-tier__base')).toHaveText(
      peak ? '$10.00' : '$5.00',
    );
    await expect(oneHour.locator('.plaza-tier__long')).toHaveText(
      peak ? '$16.00' : '$8.00',
    );
  });
}

test('late registration lookup preserves typed and deliberately cleared fields', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  let release: (() => void) | undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/model_info/lookup?*', async (route) => {
    await gate;
    await route.fulfill({
      json: {
        success: true,
        data: {
          found: true,
          prefill: {
            model_name: 'gpt-manual',
            maker: 'openai',
            release_date: '2026-09-01',
            official_input_per_mtok: 2,
            knowledge_cutoff: '2026-04',
            context_length: 400000,
            filled: ['release_date', 'context_length'],
          },
        },
      },
    });
  });
  await page.goto('/console/model-ops?model=new');
  await page.locator('#model-name').fill('gpt-manual');
  await page.locator('#model-name').blur();
  await page.locator('#model-release').fill('2026-08-01');
  await page.locator('#model-official-in').fill('7');
  await page.locator('#model-knowledge').fill('2026-01');
  await page.locator('#model-knowledge').fill('');
  release?.();
  await expect(
    page.getByRole('button', { name: '登记模型', exact: true }),
  ).toBeEnabled();
  await expect(page.locator('#model-release')).toHaveValue('2026-08-01');
  await expect(page.locator('#model-official-in')).toHaveValue('7');
  await expect(page.locator('#model-knowledge')).toHaveValue('');
  const saved = page.waitForRequest(
    (request) =>
      new URL(request.url()).pathname === '/api/models/' &&
      request.method() === 'POST',
  );
  await page.getByRole('button', { name: '登记模型', exact: true }).click();
  const payload: unknown = (await saved).postDataJSON();
  expect(payload).toMatchObject({
    release_date: '2026-08-01',
    official_input_price: 0.000007,
    knowledge_cutoff: '',
    manual_fields: expect.arrayContaining([
      'release_date',
      'official_price',
      'knowledge_cutoff',
    ]),
    prefilled_fields: expect.arrayContaining(['context_length']),
  });
});

test('batch acceptance submits only displayed proposal revisions', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/model_info/reviews'
        ? {
            success: true,
            data: {
              pending_count: 0,
              last_run_at: 0,
              items: [
                {
                  id: 700,
                  kind: 'first_run',
                  status: 'pending',
                  model_name: 'gpt-fixture',
                  maker: 'openai',
                  field: 'release_date',
                  local_value: '2026-08-01',
                  upstream_value: '2026-09-01',
                  note: '31',
                },
              ],
            },
          }
        : undefined,
  });
  await page.goto('/console/model-ops/updates');
  const saved = page.waitForRequest((request) =>
    request.url().includes('/model_info/reviews/first_run/accept'),
  );
  await page.getByRole('button', { name: /全部采用上游/ }).click();
  expect((await saved).postDataJSON()).toEqual({ ids: [700] });
});

test('ledger preserves custom maker order and within-maker release order', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      ['/api/models/', '/api/models/search'].includes(new URL(url).pathname)
        ? {
            success: true,
            data: {
              items: [
                {
                  id: 71,
                  model_name: 'gpt-new',
                  maker_resolved: 'openai',
                  release_date: '2026-09-22',
                  status: 0,
                },
                {
                  id: 72,
                  model_name: 'gpt-old',
                  maker_resolved: 'openai',
                  release_date: '2026-08-01',
                  status: 0,
                },
                {
                  id: 73,
                  model_name: 'claude-new',
                  maker_resolved: 'anthropic',
                  release_date: '2026-09-23',
                  status: 0,
                },
              ],
              total: 3,
              page: 1,
              page_size: 100,
              vendor_counts: {},
            },
          }
        : undefined,
  });
  await page.goto('/console/model-ops');
  await expect(page.locator('.mo-ledger__group b')).toHaveText([
    'OpenAI',
    'Anthropic',
  ]);
  const rows = page.locator('.mo-ledger__row');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('gpt-new');
  await expect(rows.nth(1)).toContainText('gpt-old');
  await expect(rows.nth(2)).toContainText('claude-new');
});
