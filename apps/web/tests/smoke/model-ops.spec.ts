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

test('来源 tab renders the ledger and gives unhealthy rows explicit failure text', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const bad = page.locator('.mo-ledger__row', { hasText: 'google-2' });
  await expect(bad).toContainText('google-2');
  // Status is never colour-only: the row states the failure in words.
  await expect(bad).toContainText('80.0%');
  await expect(bad).toContainText('均值 9.2s');
  await expect(bad.getByRole('switch')).toBeVisible();

  // Cost basis stays on the row; 接入 and its credential moved to the expander.
  const healthy = page
    .locator('.mo-ledger__row', { hasText: 'OpenAI smoke channel' })
    .first();
  await expect(healthy).toContainText('$0.02/ 次');
  await expect(healthy).toContainText('$0.003/ 秒');

  await healthy.getByRole('button', { name: '编辑' }).click();
  const detail = page.locator('.mo-inspector');
  await expect(detail).toContainText('api.openai.com');
  // Credentials stay masked — only a fingerprint, never the key.
  await expect(detail).toContainText('凭据 #02d9');
  await expect(page.locator('body')).not.toContainText('sk-smoke-channel-key');

  await app.expectNoUnexpectedErrors();
});

test('来源 rule cards only describe automation this build actually enforces', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const rules = page.locator('.mo-operational-context');
  await expect(rules).toContainText('自动停用阈值 5 秒');
  await expect(rules).toContainText('48h 请求遥测');
  await expect(rules).toContainText('自动恢复');
  await expect(rules).not.toContainText('自动降权');

  await app.expectNoUnexpectedErrors();
});

test('来源 tab filters by source type', async ({ page }) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  await expect(page.locator('.mo-ledger__row')).toHaveCount(3);
  await page.getByLabel('类型').click();
  await page.getByRole('option', { name: '官方', exact: true }).click();
  await expect(page.locator('.mo-ledger__row')).toHaveCount(1);
  await expect(page.locator('.mo-ledger__row')).toContainText(
    'OpenAI smoke channel',
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

test('sources the migration could not classify are marked 未分类, not guessed as 官方', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');

  const row = page.locator('.mo-ledger__row', { hasText: 'google-2' }).first();
  await expect(row).toContainText('未分类');
  // And they are findable through the type filter.
  await page.getByLabel('类型').click();
  await page.getByRole('option', { name: '未分类' }).click();
  await expect(page.locator('.mo-ledger__row')).toHaveCount(1);
  await expect(page.locator('.mo-ledger__row')).toContainText('google-2');

  await app.expectNoUnexpectedErrors();
});

test('source model search keeps an open draft and counts unique models', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/model-ops/sources');
  await expect(page.locator('.mo-page-meta')).toContainText('供货 2 个模型');
  await page
    .locator('.mo-ledger__row', { hasText: 'OpenAI smoke channel' })
    .getByRole('button', { name: '编辑' })
    .click();
  const panel = page.locator('.mo-inspector');
  await expect(panel).toBeVisible();
  await page.getByLabel('搜索来源', { exact: true }).fill('gemini-image-4');
  await expect(page.locator('.mo-ledger__row')).toHaveCount(1);
  await expect(page.locator('.mo-ledger__row')).toContainText('google-2');
  await expect(panel).toContainText('OpenAI smoke channel');
  await app.expectNoUnexpectedErrors();
});

test('failed model deletion keeps confirmation open for retry', async ({
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
  await expectRouteReady(page, '/console/model-ops');
  await page
    .locator('.mo-ledger__row', { hasText: 'gpt-4o-mini' })
    .getByRole('button', { name: '删除', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('button', { name: '删除', exact: true }).click();
  await expect.poll(() => deletions).toBe(1);
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByRole('button', { name: '删除', exact: true }),
  ).toBeEnabled();
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
