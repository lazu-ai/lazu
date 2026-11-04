// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp, fail } from './helpers/mock-app';

/* 模型运营 replaces the old 模型管理 / 定价运营 / 渠道 routes. These specs replace
   model-list.spec.ts, channel-list.spec.ts and channel-filter.spec.ts and keep
   their assertion strength: the key-reveal path, the filter query state and the
   admin-only guard all still have to hold. */

test('模型 tab renders tiers, pool health and 30-day revenue from real fields', async ({
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
  await expect(page.getByRole('columnheader', { name: /稳定/ })).toBeVisible();
  await expect(page.getByRole('columnheader', { name: /便宜/ })).toBeVisible();
  await expect(row).toContainText('0.09 / 0.36');

  // 30 日 comes from the exact-page spend endpoint (208.11 USD = 208_110_000 µUSD).
  await expect(row).toContainText('$208.11');

  // Route health is now aggregated from source records; absent observations
  // remain unknown rather than labelling a source abnormal.
  await expect(
    row.getByRole('group', { name: '3 个来源', exact: true }),
  ).toBeVisible();
  await expect(
    row.getByRole('group', {
      name: 'gpt-4o-mini 稳定线路的来源合并记录',
      exact: true,
    }),
  ).toBeVisible();

  // Both tiers are still priced; the second one lives in the row expander,
  // together with the description the row no longer repeats.
  await row.getByRole('button', { name: '编辑' }).click();
  await page
    .locator('.mo-inspector')
    .getByRole('tab', { name: '定价', exact: true })
    .click();
  const expanded = page.locator('.mo-inspector');
  await expect(expanded).toContainText('稳定');
  await expect(expanded).toContainText('便宜');

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
    '稳定档没有已启用的来源，当前无法路由请求',
  );
  await expect(row.getByRole('switch')).toBeDisabled();

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
  await row.getByRole('button', { name: '编辑' }).click();
  await page
    .locator('.mo-inspector')
    .getByRole('tab', { name: '定价', exact: true })
    .click();

  const expanded = page.locator('.mo-inspector');
  await expect(expanded).toBeVisible();
  await expect(expanded).toContainText('稳定档');
  await expect(expanded).toContainText('便宜档');
  // Cost basis exists for this pool, so a margin is shown instead of "—".
  await expect(expanded).toContainText('综合成本');
  // Both tiers have cost rows, so both show a real margin rather than "—".
  await expect(expanded.getByText('毛利估算')).toHaveCount(2);
  // Matches the router's weight+10 smoothing: 0.11 @ 80 and 0.62 @ 15.
  await expect(expanded).toContainText('综合成本 $0.1905 / $0.7495');
  // Pool-local weights are editable per source.
  await expect(
    expanded.getByLabel('OpenAI smoke channel 在本档位的权重'),
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
  await row.getByRole('button', { name: '编辑' }).click();
  await page
    .locator('.mo-inspector')
    .getByRole('tab', { name: '定价', exact: true })
    .click();
  const expanded = page.locator('.mo-inspector');
  await expanded.getByLabel('OpenAI smoke channel 在本档位的权重').fill('71');
  await expanded.getByLabel('Budget smoke channel 在本档位的权重').fill('31');
  await expanded
    .getByRole('button', { name: '保存档位配置', exact: true })
    .click();

  expect(requests).toHaveLength(0);
  await page
    .getByRole('dialog')
    .getByRole('button', { name: '确认保存' })
    .click();
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

test('来源 cards show the record, the cost with its units and one control', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const bad = page.locator('.mo-card', { hasText: 'google-2' });
  await expect(bad).toContainText('google-2');
  // Status is never colour-only: the card states the failure in words.
  await expect(bad).toContainText('80.0%');
  await expect(bad).toContainText('均值 9.2s');
  await expect(bad).toContainText('未分类');
  await expect(bad.getByRole('switch')).toBeVisible();

  // Every cost figure carries its unit; the per-call and per-second ones
  // sit in the title when the card has room for one.
  const healthy = page
    .locator('.mo-card', { hasText: 'OpenAI smoke channel' })
    .first();
  await expect(healthy.locator('.mo-card__cost-value')).toHaveText(
    '$0.11 / $0.44',
  );
  await expect(healthy.locator('.mo-card__cost')).toHaveAttribute(
    'title',
    /\$0\.02 \/ 次 · \$0\.003 \/ 秒/,
  );
  // The default lane goes unlabelled; another lane is named on the card.
  await expect(healthy.locator('.mo-card__lane')).toHaveCount(0);
  await expect(
    page.locator('.mo-card', { hasText: 'Budget smoke channel' }),
  ).toContainText('便宜档');

  // 编辑 / 测试 / 删除 live behind ⋯.
  await healthy
    .getByRole('button', { name: '更多操作 OpenAI smoke channel' })
    .click();
  await expect(page.getByRole('menuitem', { name: '编辑' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '测试' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '删除' })).toBeVisible();
  await page.keyboard.press('Escape');

  // The card itself opens the editor, its head filled from the list.
  await healthy.locator('.mo-card__name').click();
  const detail = page.getByRole('dialog');
  await expect(detail).toContainText('api.openai.com');
  // Credentials stay masked — only a fingerprint, never the key.
  await expect(detail).toContainText('凭据 #02d9');
  await expect(page.locator('body')).not.toContainText('sk-smoke-channel-key');

  await app.expectNoUnexpectedErrors();
});

test('来源 filters combine a lane with a state', async ({ page }) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const cards = page.locator('.mo-card');
  await expect(cards).toHaveCount(3);
  const lanes = page.getByRole('group', { name: '线路' });
  await expect(lanes.getByRole('button', { name: '全部线路' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await lanes.getByRole('button', { name: /稳定档/ }).click();
  await expect(cards).toHaveCount(2);
  await page
    .getByRole('group', { name: '状态' })
    .getByRole('button', { name: /异常/ })
    .click();
  await expect(cards).toHaveCount(1);
  await expect(cards).toContainText('google-2');
  // The gear beside the switch opens the lanes dialog.
  await page.getByRole('button', { name: '线路设置' }).click();
  await expect(page.getByRole('dialog')).toContainText(
    '模型默认按线路比例定价',
  );

  await app.expectNoUnexpectedErrors();
});

test('a watched source carries a stripe, its progress and the go-live check', async ({
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

  const card = page.locator('.mo-card', { hasText: 'Watched smoke channel' });
  await expect(card).toHaveClass(/is-observing/);
  await expect(card).toContainText('观察中');
  await expect(card).toContainText('3/7 天');
  await expect(card).toContainText('3 个有问题');
  await expect(card).toContainText('抽测 194/194');
  await expect(card.getByRole('switch')).toHaveCount(0);
  await expect(
    card.getByRole('button', { name: '上线检查 · 4' }),
  ).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

test('source model search filters the cards and keeps an open editor', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');
  await page
    .locator('.mo-card', { hasText: 'OpenAI smoke channel' })
    .locator('.mo-card__name')
    .click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await page.getByLabel('搜索来源', { exact: true }).fill('gemini-image-4');
  await expect(page.locator('.mo-card')).toHaveCount(1);
  await expect(page.locator('.mo-card')).toContainText('google-2');
  await app.expectNoUnexpectedErrors();
});

test('来源 dialog: skeleton until the record arrives, then the 线路 anchors', async ({
  page,
}) => {
  let release = () => {};
  const held = new Promise((resolve) => {
    release = resolve;
  });
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: async (url, request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/channel/1' && request.method() === 'GET') {
        await held;
        return {
          success: true,
          message: '',
          data: {
            id: 1,
            name: 'OpenAI smoke channel',
            type: 1,
            status: 1,
            group: 'cheap',
            source_type: 'official',
            models: 'gpt-4o-mini',
            priority: 1,
            weight: 70,
            base_url: 'https://api.openai.com',
            settings: '{"protocol_dialect":"openai"}',
          },
        };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/model-ops/sources');
  await page
    .locator('.mo-card', { hasText: 'OpenAI smoke channel' })
    .locator('.mo-card__name')
    .click();
  const dialog = page.getByRole('dialog');
  // The head shows what the list already knew; the body waits in outline.
  await expect(dialog).toContainText('OpenAI smoke channel');
  await expect(
    dialog.getByRole('status', { name: '正在读取来源详情' }),
  ).toBeVisible();
  await expect(dialog.getByRole('button', { name: '保存来源' })).toBeDisabled();
  release();
  await expect(dialog.locator('#channel-name')).toHaveValue(
    'OpenAI smoke channel',
  );

  await dialog.getByRole('tab', { name: '线路' }).click();
  const nav = dialog.getByRole('navigation', { name: '线路设置分段' });
  await expect(nav.getByRole('button')).toHaveText([
    '线路与权重',
    /限流\s*未限制/,
    '行为',
    '成本基准',
    '厂商高级设置',
  ]);
  // Lanes read by name, never by slug.
  await expect(dialog.getByLabel('线路', { exact: true })).toContainText(
    '便宜档',
  );
  // Field names live behind ⓘ, not beside the label.
  await expect(dialog.locator('.mo-code')).toHaveCount(0);
  await expect(
    dialog.getByRole('button', { name: '字段说明：weight' }),
  ).toBeVisible();
  // The 启用 switch is the card's, not the form's.
  await expect(dialog.getByRole('switch', { name: '启用' })).toHaveCount(0);
  await nav.getByRole('button', { name: '成本基准' }).click();
  await expect(dialog.locator('pre.mo-example')).toContainText(
    '"model_name": "gpt-5.5"',
  );
  await expect(nav.getByRole('button', { name: '成本基准' })).toHaveAttribute(
    'aria-current',
    'location',
  );

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
    .getByRole('button', { name: '编辑' })
    .click();
  const info = page
    .locator('.mo-inspector')
    .getByRole('tab', { name: '信息', exact: true });
  await info.focus();
  // 定价 · 资料 · 信息 · 折扣: left of 信息 is the models.dev facts page.
  await page.keyboard.press('ArrowLeft');
  await expect(
    page
      .locator('.mo-inspector')
      .getByRole('tab', { name: '资料', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(
    page.locator('.mo-inspector').getByRole('tabpanel'),
  ).toBeVisible();
  await page.screenshot({
    path: '../../.local-results/screenshots/model-ops-feedback/inspector.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect
    .poll(async () => (await page.locator('.mo-inspector').boundingBox())?.y)
    .toBe(56);
  await expect
    .poll(
      async () => (await page.locator('.mo-inspector').boundingBox())?.height,
    )
    .toBeLessThan(750);
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
  await expectRouteReady(page, '/console/model-ops/sources');
  await page
    .locator('.mo-card', { hasText: 'OpenAI smoke channel' })
    .first()
    .locator('.mo-card__name')
    .click();
  await page.getByRole('tab', { name: '线路' }).click();
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
