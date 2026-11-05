// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

/* 模型运营 replaces the old 模型管理 / 定价运营 / 渠道 routes. These specs replace
   model-list.spec.ts, channel-list.spec.ts and channel-filter.spec.ts and keep
   their assertion strength: the key-reveal path, the filter query state and the
   admin-only guard all still have to hold. */

test('模型 tab renders both lanes and pooled source health from real fields', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');

  const row = page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .first();
  await expect(row).toBeVisible();

  // Both lane prices remain visible in the approved compact table.
  await expect(row).toContainText('0.15 / 0.6');
  // Headers carry the names set in 线路设置 (the mock calls them 稳定档 and
  // 便宜档), not fixed copy.
  await expect(
    page.getByRole('columnheader', { name: '稳定档线路', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('columnheader', { name: '便宜档线路', exact: true }),
  ).toBeVisible();
  await expect(row).toContainText('0.09 / 0.36');

  // Route health is now aggregated from source records; absent observations
  // remain unknown rather than labelling a source abnormal.
  await expect(
    row.getByRole('group', { name: '3 个来源', exact: true }),
  ).toBeVisible();
  await expect(
    row.getByRole('group', {
      name: 'gpt-4o-mini 稳定档线路的来源合并记录',
      exact: true,
    }),
  ).toBeVisible();

  // The row opens the model's dialog on 定价与来源, with both lanes priced.
  await row.locator('.mo-name').first().click();
  const expanded = page.getByRole('dialog');
  await expect(
    expanded.getByRole('tab', { name: '定价与来源', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(expanded).toContainText('稳定档');
  await expect(expanded).toContainText('便宜档');

  await app.expectNoUnexpectedErrors();
});

test('上架 toggle is disabled with a stated reason when neither lane has a price', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');

  const row = page
    .locator('.mo-ledger__row', { hasText: 'gemini-image-4' })
    .first();
  await expect(row).toContainText('未定价，上架后按 $0 计费');
  await expect(row.getByRole('switch')).toHaveAccessibleName(
    '没有任何档位售价，上架后请求会按 $0 计费',
  );
  await expect(row.getByRole('switch')).toBeDisabled();

  await app.expectNoUnexpectedErrors();
});

test('上架 toggle is disabled when the priced stable tier has no enabled source', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');

  const row = page
    .locator('.mo-ledger__row', { hasText: 'priced-without-stable-pool' })
    .first();
  await expect(row.getByRole('switch')).toHaveAccessibleName(
    '稳定档线路没有已启用的来源，当前无法路由请求',
  );
  await expect(row.getByRole('switch')).toBeDisabled();

  await app.expectNoUnexpectedErrors();
});

/* The registry's own seed names. A name that already says 专线 is not given a
   second 线路, in the ledger or in the dialog. */
test('模型 tab follows a lane renamed in 线路设置', async ({ page }) => {
  const lane = (slug, name, sort) => ({
    slug,
    name,
    enabled: true,
    builtin: true,
    ever_enabled: true,
    sort,
    sources: 1,
    priced: 1,
  });
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) =>
      new URL(url).pathname === '/api/model_ops/lanes'
        ? {
            success: true,
            message: '',
            data: {
              items: [
                lane('direct', '官方直连', 0),
                lane('cheap', 'Agent 专线', 1),
              ],
            },
          }
        : undefined,
  });
  await expectRouteReady(page, '/console/model-ops');

  await expect(
    page.getByRole('columnheader', { name: '官方直连线路', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('columnheader', { name: 'Agent 专线', exact: true }),
  ).toBeVisible();

  const gpt = page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .first();
  await expect(gpt.locator('.mo-lanes__label').first()).toHaveText('官方直连');
  await expect(
    gpt.getByRole('group', {
      name: 'gpt-4o-mini Agent 专线的来源合并记录',
      exact: true,
    }),
  ).toBeVisible();

  const blocked = page
    .locator('.mo-ledger__row', { hasText: 'priced-without-stable-pool' })
    .first();
  await expect(blocked.getByRole('switch')).toHaveAccessibleName(
    '官方直连线路没有已启用的来源，当前无法路由请求',
  );

  await gpt.locator('.mo-name').first().click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('Agent 专线');
  await expect(dialog).not.toContainText('专线线路');

  await app.expectNoUnexpectedErrors();
});

test('expanding a model reveals both tier cards with their pools and weights', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');

  const row = page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .first();
  await row.locator('.mo-name').first().click();

  const expanded = page.getByRole('dialog');
  await expect(expanded).toBeVisible();
  const stable = expanded.getByRole('row', { name: /稳定档/ });
  const cheap = expanded.getByRole('row', { name: /便宜档/ });
  // Cost basis exists for both pools, so each shows a real margin, not "—".
  // Matches the router's weight+10 smoothing: 0.11 @ 80 and 0.62 @ 15.
  await expect(stable).toContainText('$0.1905');
  await expect(stable).toContainText('-25.3%');
  await expect(cheap).toContainText('$0.06');
  await expect(cheap).toContainText('33.3%');
  // Pool-local weights are editable per source, behind the source count.
  await stable.getByRole('button', { name: '2 个来源' }).click();
  await expect(
    expanded.getByLabel('OpenAI smoke channel 在本线路的权重'),
  ).toHaveValue('70');

  await app.expectNoUnexpectedErrors();
});

test('saving both tiers sends one atomic row request', async ({ page }) => {
  const requests: Array<{ pathname: string; payload: unknown }> = [];
  page.on('request', (request) => {
    const pathname = new URL(request.url()).pathname;
    if (
      request.method() !== 'GET' &&
      [
        '/api/model_ops/models/tier_config',
        '/api/model_ops/pool_weight',
        '/api/pricing/sell_prices',
      ].includes(pathname)
    ) {
      requests.push({ pathname, payload: request.postDataJSON() });
    }
  });
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');

  const row = page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .first();
  await row.locator('.mo-name').first().click();
  const expanded = page.getByRole('dialog');
  await expanded.getByRole('button', { name: '2 个来源' }).click();
  await expanded.getByRole('button', { name: '1 个来源' }).click();
  await expanded.getByLabel('OpenAI smoke channel 在本线路的权重').fill('71');
  await expanded.getByLabel('Budget smoke channel 在本线路的权重').fill('31');
  await expanded.getByRole('button', { name: '保存', exact: true }).click();

  expect(requests).toHaveLength(0);
  await page.getByRole('button', { name: '确认保存' }).click();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0].pathname).toBe('/api/model_ops/models/tier_config');
  expect(requests[0].payload).toMatchObject({
    model: 'gpt-4o-mini',
    tiers: [
      {
        lane: 'direct',
        weights: [{ channel_id: '1', weight: 71 }],
      },
      {
        lane: 'cheap',
        weights: [{ channel_id: '2', weight: 31 }],
      },
    ],
  });
  await app.expectNoUnexpectedErrors();
});

test('来源 list is a table with state, upstream change and health columns', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const table = page.getByRole('table', { name: '来源' });
  await expect(table.getByRole('columnheader')).toContainText([
    '来源',
    '状态',
    '上游变化',
    '模型',
  ]);
  const bad = page.getByRole('row', { name: 'google-2' });
  // Status is never colour-only: the row states the failure in words.
  await expect(bad).toContainText('80.0%');
  await expect(bad).toContainText('未分类');
  await expect(bad).toContainText('正式');

  const healthy = page.getByRole('row', { name: 'OpenAI smoke channel' });
  await expect(healthy).toContainText('+1');
  await expect(healthy).toContainText('−1');
  await expect(healthy).toContainText('待核对');
  // The default lane goes unlabelled; another lane is named on the row.
  await expect(
    page.getByRole('row', { name: 'Budget smoke channel' }),
  ).toContainText('便宜档');

  // 编辑 / 测试 / 停用 / 删除 live behind ⋯.
  await healthy
    .getByRole('button', { name: '更多操作 OpenAI smoke channel' })
    .click();
  await expect(page.getByRole('menuitem', { name: '编辑' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '测试' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '停用' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '删除' })).toBeVisible();
  await page.keyboard.press('Escape');

  // The row opens the source's own page.
  await healthy.click();
  await expect(page).toHaveURL(/\/console\/model-ops\/sources\/1$/);
  await expect(page.locator('body')).not.toContainText('sk-smoke-channel-key');

  await app.expectNoUnexpectedErrors();
});

test('来源 filters by state and searches names, models and lanes', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const rows = page.locator('.src-srow:not(.src-srow--head)');
  await expect(rows).toHaveCount(3);
  await page
    .getByRole('group', { name: '状态' })
    .getByRole('button', { name: /上游有变化/ })
    .click();
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText('OpenAI smoke channel');
  await page
    .getByRole('group', { name: '状态' })
    .getByRole('button', { name: /全部/ })
    .click();
  await page.getByLabel('搜索来源', { exact: true }).fill('gemini-image-4');
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText('google-2');
  await page.getByLabel('搜索来源', { exact: true }).fill('便宜档');
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText('Budget smoke channel');

  await app.expectNoUnexpectedErrors();
});

test('a watched source shows its badge, progress and problems in the status column', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      const { pathname } = new URL(url);
      if (pathname !== '/api/model_ops/sources') return undefined;
      return {
        success: true,
        message: '',
        data: {
          items: [
            {
              id: '9',
              internal_id: 9,
              name: 'Watched smoke channel',
              type: 14,
              type_name: 'Anthropic',
              source_type: 'reverse',
              needs_classification: false,
              lane: 'cheap',
              status: 1,
              endpoint: 'watch.example',
              key_fingerprint: 'w9w9',
              model_count: 4,
              cost_input_per_mtok: null,
              cost_output_per_mtok: null,
              model_names: ['claude-opus-4-8'],
              health: {
                source: 'probe',
                request_count: 194,
                error_count: 0,
                success_rate: 1,
                avg_latency_ms: 1200,
                buckets: [1, 1, 1, 1].map((rate, index) => ({
                  start_at: new Date(
                    Date.now() - (4 - index) * 3600_000,
                  ).toISOString(),
                  end_at: new Date(
                    Date.now() - (3 - index) * 3600_000,
                  ).toISOString(),
                  request_count: 40,
                  error_count: 0,
                  success_rate: rate,
                })),
              },
              observation: {
                checks: [],
                complete: false,
                day: 3,
                days_required: 7,
                live_routes: 0,
                models: 4,
                ready: false,
                routes: 4,
                samples: 194,
                failures: 0,
                started_at: new Date(Date.now() - 2 * 86400_000).toISOString(),
                success_rate: 1,
                success_state: 'pass',
                summary: {
                  all_passed: 1,
                  pending: 0,
                  problem_count: 3,
                  problems: [
                    { model: 'claude-opus-4-8', kind: 'probe' },
                    { model: 'claude-opus-5-5', kind: 'probe' },
                    { model: 'claude-fable-5-1', kind: 'probe' },
                  ],
                  ready: 1,
                },
              },
            },
          ],
          rules: null,
        },
      };
    },
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const row = page.getByRole('row', { name: 'Watched smoke channel' });
  // The state is a badge in its own column, never a stripe down the row.
  await expect(row).toContainText('观察中');
  await expect(row).toContainText('3/7 天');
  await expect(row).toContainText('3 个有问题');
  await expect(row).toContainText('抽测 194/194');
  await expect(row.getByRole('img', { name: '观察进度 3/7 天' })).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

test('来源 page: models with the upstream diff, then 线路 and 同步 tabs', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources/1');

  await expect(page.locator('h1')).toContainText('OpenAI smoke channel');
  const banner = page.getByRole('status').filter({ hasText: '上游有变化' });
  await expect(banner).toContainText('新出现 1 个');
  await expect(banner).toContainText('已不列 1 个');

  const models = page.getByRole('table', { name: '模型' });
  const gone = models.getByRole('row').filter({ hasText: 'gpt-4o-mini' });
  await expect(gone).toHaveClass(/is-gone/);
  await expect(gone).toContainText('已不列');
  await expect(
    gone.getByRole('switch', { name: '供货 gpt-4o-mini' }),
  ).toBeChecked();
  const fresh = models.getByRole('row').filter({ hasText: 'gpt-5-preview' });
  await expect(fresh).toContainText('新出现');
  await expect(
    fresh.getByRole('switch', { name: '供货 gpt-5-preview' }),
  ).not.toBeChecked();

  await page.getByRole('button', { name: '看新出现的' }).click();
  await expect(models.getByRole('row')).toHaveCount(2);

  await page.getByRole('tab', { name: '线路' }).click();
  await expect(page).toHaveURL(/tab=routing/);
  const nav = page.getByRole('navigation', { name: '线路设置分段' });
  await expect(nav.getByRole('button')).toHaveText([
    '线路与权重',
    /限流\s*未限制/,
    '行为',
    '成本基准',
    '厂商高级设置',
  ]);

  await page.getByRole('tab', { name: '同步' }).click();
  await expect(page.getByRole('button', { name: '每 6 小时' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(
    page.getByLabel('先不供货，进「上游新增」等我核对'),
  ).toBeChecked();
  await page.getByLabel('忽略', { exact: true }).fill('text-embedding-*');
  await expect(page.locator('.src-sync')).toContainText(
    'text-embedding-3-large',
  );

  await app.expectNoUnexpectedErrors();
});

test('新建来源 is a page: type, connection test, then models to serve', async ({
  page,
}) => {
  const created = [];
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/channel/fetch_models') {
        return {
          success: true,
          message: '',
          data: ['gpt-4o-mini', 'gpt-5-preview'],
        };
      }
      if (pathname === '/api/channel/' && request.method() === 'POST') {
        created.push(request.postDataJSON());
        return { success: true, message: '', data: { ids: ['9'] } };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/model-ops/sources/new');

  await expect(page.locator('h1')).toContainText('新建来源');
  await expect(page.getByRole('radio', { name: 'OpenAI 兼容' })).toBeChecked();
  const submit = page.getByRole('button', { name: /创建来源并供货/ });
  await expect(submit).toBeDisabled();

  await page.getByPlaceholder('生产 OpenAI 池').fill('Smoke new source');
  await page
    .getByPlaceholder('https://api.example.com')
    .fill('https://api.example.com');
  await page.getByPlaceholder('sk-…').fill('sk-smoke-new');
  await page.getByRole('button', { name: '测试连接' }).click();
  await expect(page.locator('body')).toContainText('上游列出 2 个模型');

  await page.getByRole('checkbox', { name: 'gpt-5-preview' }).check();
  await expect(submit).toHaveText(/创建来源并供货 1 个/);

  await app.expectNoUnexpectedErrors();
});

test('同步上游目录 previews the diff before writing anything', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');

  await page.getByRole('button', { name: '同步', exact: true }).click();
  await page.getByRole('menuitem', { name: '同步上游目录' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('本地缺失 1');
  await expect(dialog).toContainText('gpt-5-preview');
  await expect(dialog).toContainText('字段冲突 1');
  await expect(dialog.getByRole('button', { name: /确认同步/ })).toBeEnabled();

  await app.expectNoUnexpectedErrors();
});

test('bookmarked model, pricing and channel URLs resolve to their current owners', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });

  // expectRouteReady asserts the URL is unchanged, so redirects are checked
  // with a plain navigation.
  for (const [from, to] of [
    ['/console/models', /\/console\/models$/],
    ['/console/pricing', /\/console\/model-ops$/],
    ['/console/channel', /\/console\/model-ops\/sources$/],
  ]) {
    await page.goto(from, { waitUntil: 'domcontentloaded' });
    await expect(page).toHaveURL(to);
    await expect(page.locator('#root')).toContainText(/\S/);
  }

  await app.expectNoUnexpectedErrors();
});

test('model deletion runs behind an undo and puts the row back when it fails', async ({
  page,
}) => {
  let deletions = 0;
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url, request) => {
      if (
        request.method() === 'DELETE' &&
        new URL(url).pathname.startsWith('/api/models/')
      ) {
        deletions++;
        return fail(500, 'internal_error');
      }
    },
  });
  await expectRouteReady(page, '/console/model-ops?model=1&panel=info');
  await page.getByRole('button', { name: '删除模型', exact: true }).click();
  // No confirmation: the row reads 删除中 and the toast offers 撤销.
  await expect(page.getByRole('dialog')).toHaveCount(0);
  const row = page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .first();
  await expect(row).toContainText('删除中');
  await expect(
    page.getByRole('button', { name: '撤销', exact: true }),
  ).toBeVisible();
  expect(deletions).toBe(0);
  // Nothing is sent until the countdown ends; a failure puts the row back.
  await expect.poll(() => deletions, { timeout: 15_000 }).toBe(1);
  await expect(row).not.toContainText('删除中');
  await expect(
    page.getByRole('button', { name: '重试', exact: true }),
  ).toBeVisible();
});

test('inspector tabs have keyboard navigation and ledger rows stay one line on mobile', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops');
  await page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .locator('.mo-name')
    .first()
    .click();
  const dialog = page.getByRole('dialog');
  const info = dialog.getByRole('tab', { name: '资料', exact: true });
  await info.focus();
  // 定价与来源 · 资料: left of 资料 is the pricing page.
  await page.keyboard.press('ArrowLeft');
  const pricing = dialog.getByRole('tab', { name: '定价与来源', exact: true });
  await expect(pricing).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('Enter');
  await expect(info).toHaveAttribute('aria-selected', 'true');
  await page.screenshot({
    path: '../../.local-results/screenshots/model-ops-feedback/inspector.png',
    fullPage: true,
  });
  // On a phone the dialog takes the whole screen rather than overflowing it.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect
    .poll(async () => (await dialog.boundingBox())?.width)
    .toBeLessThanOrEqual(390);
  await expect
    .poll(async () => (await dialog.boundingBox())?.height)
    .toBeLessThanOrEqual(844);
  await page.screenshot({
    path: '../../.local-results/screenshots/model-ops-feedback/inspector-mobile.png',
    fullPage: true,
  });
  await page.keyboard.press('Escape');
  const row = page.locator('.mo-ledger__row').first();
  const height = await row.evaluate((el) => el.getBoundingClientRect().height);
  expect(height).toBeLessThan(70);
  const cells = await row
    .locator(':scope > span')
    .evaluateAll((els) =>
      els
        .filter((el) => getComputedStyle(el).display !== 'none')
        .map(
          (el) =>
            el.getBoundingClientRect().top +
            el.getBoundingClientRect().height / 2,
        ),
    );
  expect(Math.max(...cells) - Math.min(...cells)).toBeLessThan(2);
  await app.expectNoUnexpectedErrors();
});

test('来源表单 edits request overrides and keeps the stored ones on save', async ({
  page,
}) => {
  const saved = [];
  const detail = {
    id: 1,
    name: 'OpenAI smoke channel',
    type: 1,
    status: 1,
    group: 'direct',
    source_type: 'official',
    models: 'gpt-4o-mini',
    priority: 1,
    weight: 70,
    base_url: 'https://api.openai.com',
    settings: '{"protocol_dialect":"openai"}',
    param_override: '{"temperature":0.2}',
    header_override: '{"X-Org":"lazu"}',
    status_code_mapping: '',
  };
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url, request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/channel/1' && request.method() === 'GET') {
        return { success: true, message: '', data: detail };
      }
      if (pathname.startsWith('/api/channel') && request.method() === 'PUT') {
        saved.push(request.postDataJSON());
        return { success: true, message: '', data: detail };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/model-ops/sources/1?tab=routing');
  await page
    .getByRole('navigation', { name: '线路设置分段' })
    .getByRole('button', { name: '厂商高级设置' })
    .click();

  await expect(page.locator('#channel-param-override')).toHaveValue(
    /"temperature": 0.2/,
  );
  await expect(page.locator('#channel-header-override')).toHaveValue(
    /"X-Org": "lazu"/,
  );

  const mapping = page.locator('#channel-status-code-mapping');
  await mapping.fill('{"400": 500}');
  await page.locator('button[form="source-form"]').click();
  await expect(page.locator('#source-sect-advanced')).toContainText(
    '状态码映射必须是状态码对状态码',
  );
  expect(saved).toHaveLength(0);

  await mapping.fill('{"400": "503"}');
  await page.locator('button[form="source-form"]').click();
  await expect.poll(() => saved.length).toBe(1);
  expect(saved[0]).toMatchObject({
    param_override: '{"temperature":0.2}',
    header_override: '{"X-Org":"lazu"}',
    status_code_mapping: '{"400":"503"}',
  });
  await app.expectNoUnexpectedErrors();
});

// The undo toast sits outside the dialog; pressing it must neither be
// blocked by the dialog nor count as dismissing it.
test('undoing a lane deletion from its toast keeps the lanes dialog open', async ({
  page,
}) => {
  const deletes = [];
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url, request) => {
      const p = new URL(url).pathname;
      if (request.method() === 'DELETE') {
        deletes.push(p);
        return { success: true, data: null };
      }
      if (p === '/api/model_ops/lanes')
        return {
          success: true,
          message: '',
          data: {
            items: [
              {
                slug: 'direct',
                name: '稳定档',
                enabled: true,
                builtin: true,
                ever_enabled: true,
                sort: 0,
                sources: 2,
                priced: 2,
              },
              {
                slug: 'trial',
                name: '试用档',
                enabled: false,
                builtin: false,
                ever_enabled: false,
                sort: 5,
                sources: 0,
                priced: 0,
              },
            ],
          },
        };
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/model-ops');
  await page.getByRole('button', { name: '线路', exact: true }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('button', { name: '试用档' }).click();
  await dialog.getByRole('button', { name: '删除', exact: true }).click();
  await expect(page.locator('.lazu-undo-toast')).toBeVisible();
  await page
    .locator('.lazu-undo-toast')
    .getByRole('button', { name: '撤销' })
    .click();
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('试用档');
  expect(deletes).toHaveLength(0);
});
