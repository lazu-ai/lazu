// @ts-nocheck
import { expect, test } from '@playwright/test';
import {
  expectRouteReady,
  mockedChannelHealth,
  setupMockedApp,
  fail,
} from './helpers/mock-app';

/* 通道健康 is the console half of the stability surface. It reads one week
   two ways: a by-upstream ledger (admin only) and a by-model status board,
   drawn the way public status pages are.

   The assertions below defend the properties that make the page honest —
   that a lane with evidence is never painted the same as a lane with none,
   that probes count as samples while their provenance stays an operator
   detail, that an unreadable aggregate never looks like a healthy one, and
   that the two views of the same window agree. */

const openModelView = async (page) => {
  await page.getByRole('tab', { name: '按模型' }).click();
};

/* One card per model, one block per lane inside it. A test that means "the
   stable lane of this model" says so rather than take whichever block comes
   first — and a single-lane card names no lane at all, so the block is found
   by the lane it serves, not by its label. */
const modelCard = (page, model) =>
  page
    .locator('.chealth-mcard')
    .filter({
      has: page.locator('.chealth-mcard__name', { hasText: model }),
    })
    .first();

const laneRow = (page, model, lane) =>
  modelCard(page, model)
    .locator(
      `.chealth-lane[data-lane="${lane === 'Agent' ? 'eco' : 'stable'}"]`,
    )
    .first();

const openProviderView = async (page) => {
  await page.getByRole('tab', { name: '按供应商' }).click();
};

test('the provider ledger cards one row per upstream with its own evidence', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');

  // The locked primary surface is the model-by-lane heat wall.
  await expect(page.getByRole('tab', { name: '按模型' })).toHaveAttribute(
    'aria-selected',
    'true',
  );

  await page.getByRole('tab', { name: '按供应商' }).click();

  const cards = page.locator('.chealth-provider');
  await expect(cards).toHaveCount(3);

  // Worst first — the ledger is opened during incidents.
  await expect(cards.first()).toContainText('relay-blossom');
  await expect(cards.first()).toContainText('97.8%');
  await expect(cards.first()).toContainText('聚合');
  await expect(cards.first()).toContainText('承载 40%');

  // A card is a third of the page wide, so it draws the week's 84 slices
  // three at a time.
  await expect(cards.first().locator('.chealth-cell')).toHaveCount(28);

  await app.expectNoUnexpectedErrors();
});

test('the provider panel names the routes an upstream carries and how it was measured', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openProviderView(page);

  const panel = page.locator('.chealth-panel');
  await expect(panel).toContainText('relay-blossom');
  await expect(panel).toContainText('claude-sonnet-5');
  await expect(panel).toContainText('实测');

  // A probe-backed upstream must say plainly that a reachability check is not
  // a load measurement, and report its newest check verbatim.
  await page.locator('.chealth-provider', { hasText: 'relay-corsair' }).click();
  await expect(panel).toContainText('抽测');
  await expect(panel).toContainText('不代表真实负载');
  await expect(panel).toContainText('1.20s');

  await app.expectNoUnexpectedErrors();
});

test('the provider ledger links through to the model it serves', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openProviderView(page);

  // One direction only, now that the by-model wall states its figures in
  // columns instead of parking them in a panel: there is no row detail left
  // to carry a link back, and the tab does that job.
  await page
    .locator('.chealth-panel .chealth-pool__row--link', {
      hasText: 'claude-sonnet-5',
    })
    .first()
    .click();
  await expect(page.getByRole('tab', { name: '按模型' })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(modelCard(page, 'claude-sonnet-5')).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

/* A kind filter left on another section must not hide the route the
 * provider panel just pointed at. */
test('jumping from an upstream to its model clears the kind filter', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);
  await page.getByRole('tab', { name: '图片生成' }).click();
  await expect(page).toHaveURL(/kind=image/);
  await openProviderView(page);

  await page
    .locator('.chealth-panel .chealth-pool__row--link', {
      hasText: 'claude-sonnet-5',
    })
    .first()
    .click();
  await expect(page).not.toHaveURL(/kind=/);
  await expect(modelCard(page, 'claude-sonnet-5')).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

/* The by-model reading is a status page: an operator's banner that says what
 * is wrong, then one card per model, and in it one block per lane with its
 * status word, the week's availability and a week of bars. */
test('the board opens on a banner and a week of bars per lane', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  // One window, stated once; nothing to choose.
  await expect(page.getByRole('combobox', { name: '时间范围' })).toHaveCount(0);
  await expect(page.locator('.chealth-board-meta')).toContainText(
    '过去 7 天 · 每格 2 小时',
  );

  // One line: the one degraded lane by name, then the rest as a count.
  const banner = page.locator('.chealth-banner');
  await expect(banner).toHaveClass(/chealth-banner--warn/);
  await expect(banner).toHaveText(
    'claude-sonnet-5 Agent 专线降级 · 1 暂无数据 · 4 正常',
  );

  const row = laneRow(page, 'claude-sonnet-5', '稳定');
  await expect(row).toBeVisible();
  await expect(row.locator('.chealth-bars')).toHaveCount(1);
  await expect(row.locator('.chealth-bar')).toHaveCount(84);
  // A healthy lane shows its dot; the word is there for screen readers only.
  await expect(row.locator('.chealth-lane__status')).toHaveText('正常');
  await expect(row.locator('.chealth-lane__status .sr-only')).toHaveText(
    '正常',
  );
  await expect(row.locator('.chealth-lane__uptime')).toHaveText('99.90%');
  await expect(row.locator('.chealth-lane__uptime')).toHaveAttribute(
    'title',
    '过去 7 天可用',
  );

  // Summary tiles belong to the operator's ledger, not to the board.
  await expect(page.locator('.chealth-tile')).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

/* Order is fixed, as on a status page: a reader finds the model they call in
 * the same place every visit. Every family is laid out — the cards are small
 * enough that folding the healthy ones would only hide models. */
test('families keep a fixed order and every model is on the page', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  // One grid per kind, in the fixed family order; each card names its maker
  // (canvas 2026-10-14 — the per-family sub-headings are gone).
  const cards = page.locator(
    '.chealth-bsection[data-kind="llm"] .chealth-mcard',
  );
  await expect(cards).toHaveCount(3);
  await expect(cards.nth(0).locator('.chealth-mcard__maker')).toHaveText(
    'Anthropic',
  );
  await expect(cards.nth(1).locator('.chealth-mcard__maker')).toHaveText(
    'Google',
  );
  await expect(cards.nth(2).locator('.chealth-mcard__maker')).toHaveText(
    'DeepSeek',
  );
  await expect(
    page.locator('.chealth-bsection[data-kind="llm"] .chealth-family__head'),
  ).toHaveCount(0);

  // A model sold on two lanes shows two labelled blocks and no tag, and its
  // stable lane reads first, whatever its health.
  const sonnet = modelCard(page, 'claude-sonnet-5');
  await expect(sonnet.locator('.chealth-mcard__tag')).toHaveCount(0);
  await expect(sonnet.locator('.chealth-lane__label').nth(0)).toHaveText(
    '稳定',
  );
  await expect(sonnet.locator('.chealth-lane__label').nth(1)).toHaveText(
    'Agent',
  );

  // A model with one lane is one block and names no lane — unless that lane
  // is the agent lane, which the card says once, in its head.
  const gemini = modelCard(page, 'gemini-2.5-pro');
  await expect(gemini.locator('.chealth-lane')).toHaveCount(1);
  await expect(gemini.locator('.chealth-lane__label')).toHaveCount(0);
  await expect(gemini.locator('.chealth-mcard__tag')).toHaveCount(0);
  const deepseek = modelCard(page, 'deepseek-v4-pro');
  await expect(deepseek.locator('.chealth-lane')).toHaveCount(1);
  await expect(deepseek.locator('.chealth-lane__label')).toHaveCount(0);
  await expect(deepseek.locator('.chealth-mcard__tag')).toHaveText(
    '仅 Agent 专线',
  );

  await app.expectNoUnexpectedErrors();
});

/* A bar is one mark per verdict. Where a sample came from is an operator's
 * detail and lives in the tooltip; a customer's bar looks the same either
 * way. */
test('every measured bar is drawn the same way, whatever took the sample', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  const trafficBar = laneRow(page, 'claude-sonnet-5', '稳定')
    .locator('.chealth-bar--ok')
    .first();
  const probeBar = laneRow(page, 'deepseek-v4-pro', 'Agent')
    .locator('.chealth-bar--ok')
    .first();
  const color = await trafficBar.evaluate(
    (node) => getComputedStyle(node).backgroundColor,
  );
  expect(color).not.toBe('rgba(0, 0, 0, 0)');
  await expect(probeBar).toHaveCSS('background-color', color);

  // The tooltip names the slice's clock time, its verdict and, for an
  // operator, what measured it.
  await probeBar.hover();
  const tip = page.locator('.chealth-tip');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('正常');
  await expect(tip).toContainText('探测');

  // A lane with nothing measured says so in words — no row of empty cells —
  // and claims no percentage.
  const idleRow = laneRow(page, 'gemini-2.5-pro', '稳定');
  await expect(idleRow.locator('.chealth-bar')).toHaveCount(0);
  await expect(idleRow.locator('.chealth-lane__norecord')).toBeVisible();
  await expect(idleRow.locator('.chealth-lane__status')).toHaveText('暂无数据');
  await expect(idleRow.locator('.chealth-lane__uptime')).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

/* Each kind of model is measured by its own yardstick, in its own section:
 * an image model has no first token and a search backend no throughput. */
test('models are shown in one section per kind, each with its own figures', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  const sections = page.locator('.chealth-bsection');
  await expect(sections).toHaveCount(3);
  await expect(sections.nth(0)).toContainText('大模型');
  await expect(sections.nth(1)).toContainText('图片生成');
  await expect(sections.nth(2)).toContainText('搜索工具');

  const llm = page.locator('.chealth-bsection[data-kind="llm"]');
  await expect(llm.locator('.chealth-bsection__count')).toHaveText(
    '3 个模型 · 4 条线路',
  );
  await expect(llm.locator('.chealth-board-legend')).toContainText('部分失败');

  // Image generation: six-hour slices and generation time, nothing else.
  const image = page.locator('.chealth-bsection[data-kind="image"]');
  await expect(image.locator('.chealth-bsection__count')).toHaveText(
    '1 个模型 · 每格 6 小时',
  );
  const imageRow = image.locator('.chealth-mcard', { hasText: 'gpt-image-2' });
  await expect(imageRow.locator('.chealth-bar')).toHaveCount(28);
  await expect(imageRow.locator('.chealth-lane__figures')).toContainText(
    '生成 p50 8.30s · p95 10.30s',
  );

  // Search: one row per provider, no lane, response time.
  const search = page.locator('.chealth-bsection[data-kind="search"]');
  const tavily = search.locator('.chealth-mcard', { hasText: 'Tavily' });
  await expect(tavily.locator('.chealth-lane__label')).toHaveCount(0);
  await expect(tavily.locator('.chealth-lane__figures')).toContainText(
    '响应 p50 2.00s · p95 7.90s',
  );

  // The kind switch narrows the page to one section.
  await page.getByRole('tab', { name: '图片生成' }).click();
  await expect(sections).toHaveCount(1);
  await expect(page).toHaveURL(/kind=image/);
  await page.getByRole('tab', { name: '全部' }).click();
  await expect(sections).toHaveCount(3);

  await app.expectNoUnexpectedErrors();
});

/* Comparing the two lanes of one model is the question this page exists to
 * answer, so each lane states its own figures right under its own verdict,
 * and nothing has to be opened to read them. */
test('each lane states its own figures on its own line', async ({ page }) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  const stable = laneRow(page, 'claude-sonnet-5', '稳定');
  const eco = laneRow(page, 'claude-sonnet-5', 'Agent');

  // 0.4% of streams cut off is below notice, so the stable lane names no
  // rate at all rather than a reassuring zero-ish figure.
  await expect(stable.locator('.chealth-lane__figures')).toHaveText(
    '首字 420ms · p95 1.10s · 42 tok/s',
  );
  await expect(eco.locator('.chealth-lane__status')).toHaveText('降级');
  await expect(eco.locator('.chealth-lane__uptime')).toHaveText('97.8%');
  // A lane whose streams break often says so in its own colour.
  await expect(
    eco.locator('.chealth-lane__figures .chealth-tone--warn'),
  ).toContainText('截断 3%');

  await app.expectNoUnexpectedErrors();
});

/* A probe is a small streamed request, so a lane nobody called still has a
 * first token, a decode speed and a stream that either finished or did not. */
test('a lane nobody called still reports what its probes measured', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  const probed = laneRow(page, 'deepseek-v4-pro', 'Agent');
  await expect(probed.locator('.chealth-lane__figures')).toContainText(
    '首字 640ms · p95 1.30s · 55 tok/s',
  );

  await app.expectNoUnexpectedErrors();
});

test('the lane filter narrows the large-model section only', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openModelView(page);

  // Four large-model lanes, one image model, one search source.
  const llmCount = page.locator(
    '.chealth-bsection[data-kind="llm"] .chealth-bsection__count',
  );
  await expect(llmCount).toHaveText('3 个模型 · 4 条线路');

  // Only large models are sold on two lanes; the lane filter leaves the
  // other sections alone.
  await page.getByRole('combobox', { name: '线路', exact: true }).click();
  await page.getByRole('option', { name: '仅 Agent 专线' }).click();
  await expect(llmCount).toHaveText('2 个模型 · 2 条线路');
  await expect(page.locator('.chealth-bsection')).toHaveCount(3);

  // The banner speaks for every lane, whatever the filters show.
  await expect(page.locator('.chealth-banner')).toContainText('降级');

  await app.expectNoUnexpectedErrors();
});

test('pausing an upstream names every pool it will leave before applying', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openProviderView(page);

  await page.getByRole('button', { name: '停用这个来源' }).click();

  // A routing change states exactly what it affects before it is confirmed.
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('relay-blossom');
  await expect(dialog).toContainText('claude-sonnet-5');
  await expect(dialog).toContainText('退出它服务的每个池子');

  await dialog.getByRole('button', { name: '确认停用' }).click();
  await expect(dialog).toBeHidden();

  await app.expectNoUnexpectedErrors();
});

test('the weight action says plainly that it does not stop traffic', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');
  await openProviderView(page);

  await page.getByRole('button', { name: '把它的权重降到最低' }).click();

  // The picker gives every live route a base weight, so weight 0 keeps a
  // share. An operator reaching for this during an incident has to be told,
  // or they will believe they stopped the traffic when they did not.
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('不会停止接流量');
  await expect(dialog).toContainText('w70 → w0');
  await expect(dialog).toContainText('停用这个来源');

  await dialog.getByRole('button', { name: '确认降权' }).click();
  await expect(dialog).toBeHidden();

  await app.expectNoUnexpectedErrors();
});

test('a member never sees the by-upstream view', async ({ page }) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'workspace:default',
    payloadFor: (url) => {
      if (!url.includes('/api/channel_health')) return undefined;
      // What the server actually sends a member: no providers key at all, and
      // no provider counters. Lanes are the user-facing unit; which vendor
      // served them is an operations fact they do not get.
      const payload = JSON.parse(JSON.stringify(mockedChannelHealth));
      delete payload.providers;
      delete payload.summary.provider_count;
      delete payload.summary.provider_degraded_count;
      delete payload.summary.provider_outage_count;
      payload.is_admin = false;
      payload.rows = payload.rows.map((row) => {
        const projected = { ...row };
        delete projected.sources;
        return projected;
      });
      return { success: true, message: '', data: payload };
    },
  });
  await expectRouteReady(page, '/console/health');

  // No toggle, no ledger — and the matrix is what they land on.
  await expect(page.getByRole('tab', { name: '按供应商' })).toHaveCount(0);
  await expect(page.getByRole('tab', { name: '按模型' })).toHaveCount(0);
  await expect(page.locator('.chealth-provider')).toHaveCount(0);
  await expect(page.locator('.chealth-mcard').first()).toBeVisible();

  // The lane tally is an operator's alarm; a member reads each model's own
  // status in its card.
  await expect(page.locator('.chealth-banner')).toHaveCount(0);

  // The row carries the lane's own figures and names no upstream.
  const row = laneRow(page, 'claude-sonnet-5', '稳定');
  await expect(row).toContainText('99.90%');
  await expect(row).not.toContainText('relay-blossom');
  await expect(page.getByText('供应商池')).toHaveCount(0);

  // Whether a sample was a customer's or a probe is ours to know. A member
  // sees one kind of bar, with the probe's figures and no provenance.
  const probed = laneRow(page, 'deepseek-v4-pro', 'Agent');
  await expect(probed).toContainText('640ms');
  await probed.locator('.chealth-bar--ok').first().hover();
  await expect(page.locator('.chealth-tip')).toBeVisible();
  await expect(page.locator('.chealth-tip')).not.toContainText('探测');

  await app.expectNoUnexpectedErrors();
});

test('a failed health request shows an error, not an empty healthy page', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) =>
      url.includes('/api/channel_health')
        ? fail(500, 'internal_error')
        : undefined,
  });
  await expectRouteReady(page, '/console/health');

  // "No channel data" and "we cannot read any channel's health" must never
  // look the same on a monitoring surface.
  await expect(page.getByText('状态读取失败')).toBeVisible();
  await expect(page.getByText('暂时没有数据')).toBeHidden();
  await expect(page.locator('.chealth-mcard')).toHaveCount(0);
  await expect(page.locator('.chealth-banner')).toHaveCount(0);
  await expect(page.locator('.chealth-provider')).toHaveCount(0);
  await expect(page.locator('.chealth-tile')).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

/* 「通道」 is operator vocabulary. A customer opens this page to answer "is the
 * model I call healthy", so the nav entry, the page title and the empty and
 * error states must speak their language, while an operator keeps the
 *原 wording and the by-upstream lens. */
test('a customer sees model status, an operator sees channel health', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'workspace:self',
    user: { role: 1 },
    payloadFor: (url) => {
      if (!url.includes('/api/channel_health')) return undefined;
      const payload = JSON.parse(JSON.stringify(mockedChannelHealth));
      // What a customer's own request returns: the by-upstream lens is gated
      // server-side, so the page must not depend on the local role for it.
      payload.is_admin = false;
      return { success: true, message: '', data: payload };
    },
  });
  await expectRouteReady(page, '/console/health');

  await expect(
    page.getByRole('heading', { name: '模型状态', exact: true }),
  ).toBeVisible();
  // Not just the title: the lane filter and the lane names both used to say
  // 通道 to whoever opened the page.
  await expect(page.getByText('通道')).toHaveCount(0);
  await expect(page.getByText('全部线路')).toBeVisible();
  // How many lanes are down is the operator's banner, not the customer's.
  await expect(page.locator('.chealth-banner')).toHaveCount(0);
  await expect(page.locator('.chealth-lane__label').first()).toHaveText('稳定');
  // An accessible name is not text, so getByText above cannot see it: the lane
  // filter's aria-label was the last place the operator term survived.
  await expect(page.getByLabel('线路', { exact: true })).toBeVisible();
  await expect(page.getByLabel('通道', { exact: true })).toHaveCount(0);
  // The by-upstream lens stays an operator tool.
  await expect(page.getByRole('tab', { name: '按供应商' })).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

/* Operators used to get their own title, their own description and their own
 * word for a lane. Two vocabularies for one page meant a support conversation
 * where each side was reading different words off the same screen, and the
 * operator's extra word bought nothing — the wall is identical either way. The
 * by-upstream lens is the only thing an operator actually gets more of, and it
 * renames itself when opened. */
test('an operator reads the same page as everyone else', async ({ page }) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await expectRouteReady(page, '/console/health');

  await expect(
    page.getByRole('heading', { name: '模型状态', exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel('线路', { exact: true })).toBeVisible();

  // The by-upstream lens is the operator's, and only it carries the old word.
  await expect(page.getByRole('tab', { name: '按供应商' })).toBeVisible();
  await page.getByRole('tab', { name: '按供应商' }).click();
  await expect(
    page.getByRole('heading', { name: '通道健康', exact: true }),
  ).toBeVisible();

  await app.expectNoUnexpectedErrors();
});

/* The mobile top bar takes its name from the route table rather than from the
 * page heading, so it has to follow the same audience rule — otherwise a
 * customer reads 通道健康 above a page titled 模型状态. */
test('the mobile top bar names the page the way the customer reads it', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'workspace:self',
    user: { role: 1 },
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expectRouteReady(page, '/console/health');

  const bar = page.locator('.mobile-console-topbar, header').first();
  await expect(bar).toContainText('模型状态');
  await expect(page.getByText('通道健康')).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

/* The error state is exactly where the operator vocabulary used to survive: the
 * description was switched but the title beside it still said 通道健康. */
test('a failed request keeps the customer wording in the title too', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'workspace:self',
    user: { role: 1 },
    payloadFor: (url) =>
      url.includes('/api/channel_health')
        ? fail(500, 'internal_error')
        : undefined,
  });
  await expectRouteReady(page, '/console/health');

  await expect(page.getByText('状态读取失败')).toBeVisible();
  await expect(page.getByText('健康数据读取失败')).toHaveCount(0);
  await expect(page.getByText('通道')).toHaveCount(0);

  await app.expectNoUnexpectedErrors();
});

for (const allUnknown of [false, true]) {
  test(`unknown evidence is not declared healthy (all unknown: ${allUnknown})`, async ({
    page,
  }) => {
    await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      payloadFor: (url) => {
        if (!url.includes('/api/channel_health')) return undefined;
        const payload = structuredClone(mockedChannelHealth);
        payload.rows = payload.rows.filter(
          (row) => row.model_name === 'claude-sonnet-5',
        );
        payload.rows.forEach((row, index) => {
          row.status = allUnknown || index === 0 ? 'unknown' : 'operational';
        });
        return { success: true, data: payload };
      },
    });
    await expectRouteReady(page, '/console/health');
    await expect(page.locator('.chealth-banner')).toContainText('暂无数据');
    await expect(page.locator('.chealth-banner')).not.toHaveClass(/--ok/);
    // The board names the unknown lane on its card; there is no per-family
    // verdict line any more.
    await expect(
      page.locator('.chealth-mcard .chealth-lane__status').first(),
    ).toContainText('暂无数据');
  });
}

test.describe('accessible slice details', () => {
  test.use({ hasTouch: true });

  test('slice details are available by keyboard and tap and blocked differs from no samples', async ({
    page,
  }) => {
    await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      payloadFor: (url) => {
        if (!url.includes('/api/channel_health')) return undefined;
        const payload = structuredClone(mockedChannelHealth);
        Object.assign(payload.rows[0].buckets[83], {
          tone: 'blocked',
          source: 'blocked',
          request_count: 0,
          error_count: 0,
          success_rate: undefined,
        });
        payload.rows[0].buckets[82].tone = 'none';
        return { success: true, data: payload };
      },
    });
    await expectRouteReady(page, '/console/health');
    const blocked = page.locator('button.chealth-bar--blocked').first();
    await blocked.focus();
    await expect(page.locator('.chealth-tip')).toContainText('无法探测');
    await expect(blocked).toHaveCSS(
      'background-image',
      /repeating-linear-gradient/,
    );
    await blocked.press('Escape');
    await expect(page.locator('.chealth-tip')).toHaveCount(0);
    await page.setViewportSize({ width: 390, height: 844 });
    await blocked.tap();
    await expect(page.locator('.chealth-tip')).toContainText('无法探测');
    await expect(page.locator('.chealth-board-legend').first()).toContainText(
      '暂无数据',
    );
    await expect(page.locator('.chealth-board-legend').first()).toContainText(
      '无法抽测',
    );
  });
});

test('search narrows the board to the matching model cards', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console/health');
  const cards = page.locator(
    '.chealth-bsection[data-kind="llm"] .chealth-mcard',
  );
  await expect(cards).toHaveCount(3);
  await page
    .getByPlaceholder('搜索模型', { exact: true })
    .fill('claude-sonnet-5');
  await expect(cards).toHaveCount(1);
  await expect(laneRow(page, 'claude-sonnet-5', '稳定')).toBeVisible();
  await page.getByPlaceholder('搜索模型', { exact: true }).clear();
  await expect(cards).toHaveCount(3);
});

test('imperfect uptime never rounds to perfect availability', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      if (!url.includes('/api/channel_health')) return undefined;
      const payload = structuredClone(mockedChannelHealth);
      payload.rows[0].success_rate = 0.999999;
      payload.rows[0].buckets[0].success_rate = 0.999999;
      return { success: true, data: payload };
    },
  });
  await expectRouteReady(page, '/console/health');
  await expect(
    laneRow(page, 'claude-sonnet-5', '稳定').locator('.chealth-lane__uptime'),
  ).toHaveText('99.99%');
  await laneRow(page, 'claude-sonnet-5', '稳定')
    .locator('.chealth-bar')
    .first()
    .focus();
  await expect(page.locator('.chealth-tip')).toContainText('99.9%');
});

test('compacted provider cells describe the same evidence as their worst tone', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      if (!url.includes('/api/channel_health')) return undefined;
      const payload = structuredClone(mockedChannelHealth);
      for (const provider of payload.providers) {
        provider.buckets.slice(0, 3).forEach((bucket, index) =>
          Object.assign(
            bucket,
            index === 1
              ? {
                  tone: 'bad',
                  source: 'probe',
                  request_count: 3,
                  error_count: 3,
                  success_rate: 0,
                }
              : {
                  tone: 'ok',
                  source: 'traffic',
                  request_count: 10000,
                  error_count: 0,
                  success_rate: 1,
                },
          ),
        );
      }
      return { success: true, data: payload };
    },
  });
  await expectRouteReady(page, '/console/health');
  await openProviderView(page);
  const cell = page
    .locator('.chealth-provider')
    .first()
    .locator('.chealth-cell')
    .first();
  await expect(cell).toHaveClass(/chealth-cell--bad/);
  await expect(cell).toHaveAttribute('title', /0\.0%.*探测.*3/);
});

test('platform issue shortcut promises the weekly status page it opens', async ({
  page,
}) => {
  const windows = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.pathname === '/api/channel_health')
      windows.push(url.searchParams.get('window'));
  });
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console');
  await page
    .getByRole('link', { name: /模型状态/ })
    .first()
    .click();
  await expect(page).toHaveURL(/\/console\/health$/);
  expect(windows.length).toBeGreaterThan(0);
  expect(windows.every((window) => window === '7d')).toBe(true);
});
