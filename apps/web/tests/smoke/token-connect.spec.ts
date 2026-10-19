import { expect, test, type Page } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

/** The catalog rows the dialog reads, with the lane shapes that decide what a
 *  model's lane control may offer:
 *  - claude-fable-5-1: an agent lane anyone can use.
 *  - gpt-6-astra / gpt-5.6-luna: an agent lane only Codex may use.
 *  - gemini-flash-3.8: no agent lane at all. */
const models = [
  {
    model_name: 'gemini-flash-3.8',
    vendor_id: 3,
    supported_endpoint_types: ['gemini', 'openai'],
    input_per_mtok: 0.75,
    output_per_mtok: 3.75,
    lanes: [
      {
        channel_group: 'direct',
        available: true,
        input_per_mtok: 0.75,
        output_per_mtok: 3.75,
      },
    ],
  },
  {
    model_name: 'claude-fable-5-1',
    vendor_id: 1,
    supported_endpoint_types: ['anthropic'],
    input_per_mtok: 5,
    output_per_mtok: 25,
    lanes: [
      {
        channel_group: 'direct',
        available: true,
        input_per_mtok: 5,
        output_per_mtok: 25,
      },
      {
        channel_group: 'cheap',
        available: true,
        input_per_mtok: 0.9,
        output_per_mtok: 4.5,
      },
    ],
  },
  {
    model_name: 'gpt-6-astra',
    vendor_id: 2,
    supported_endpoint_types: ['openai', 'openai-response'],
    input_per_mtok: 10,
    output_per_mtok: 50,
    lanes: [
      {
        channel_group: 'direct',
        available: true,
        input_per_mtok: 10,
        output_per_mtok: 50,
      },
      {
        channel_group: 'cheap',
        available: true,
        client_only: 'codex',
        input_per_mtok: 1.8,
        output_per_mtok: 9,
      },
    ],
  },
  {
    model_name: 'gpt-5.6-luna',
    vendor_id: 2,
    supported_endpoint_types: ['openai', 'openai-response'],
    input_per_mtok: 0.2,
    output_per_mtok: 1.2,
    lanes: [
      {
        channel_group: 'direct',
        available: true,
        input_per_mtok: 0.2,
        output_per_mtok: 1.2,
      },
      {
        channel_group: 'cheap',
        available: true,
        client_only: 'codex',
        input_per_mtok: 0.036,
        output_per_mtok: 0.216,
      },
    ],
  },
];

async function setupConnection(
  page: Page,
  empty = false,
  chatURL?: string,
  access?: { secret_available: boolean; can_reveal_key: boolean },
  record: Record<string, unknown> = {},
) {
  await setupMockedApp(page, {
    status: { chats: chatURL ? [{ Chat: chatURL }] : [] },
    payloadFor: (url: string) => {
      const path = new URL(url).pathname;
      if (path === '/api/user/models')
        return {
          success: true,
          data: empty ? [] : models.map((model) => model.model_name),
        };
      if (path === '/api/pricing')
        return {
          success: true,
          data: empty ? [] : models,
          vendors: [
            { id: 1, name: 'Anthropic' },
            { id: 2, name: 'OpenAI' },
            { id: 3, name: 'Google' },
          ],
        };
      if (path.endsWith('/reveal'))
        return { success: true, data: { key: 'sk-fake-connection' } };
      if (path === '/api/token/')
        return {
          success: true,
          data: {
            items: [
              {
                id: 1,
                name: 'Connection regression',
                product: 'claude-code',
                key: 'sk-fake',
                status: 1,
                status_code: 1,
                version: 7,
                expired_time: -1,
                remain_quota: 1000000,
                secret_available: true,
                can_reveal_key: true,
                vendor_groups: {},
                model_lanes: {},
                capabilities: { can_update: true },
                ...access,
                ...record,
              },
            ],
            total: 1,
            page: 1,
            page_size: 10,
          },
        };
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');
  if (access) return;
  await page.getByRole('button', { name: '接入', exact: true }).first().click();
  await expect(page.locator('.tok-connect')).toBeVisible();
}

test('each model slot carries its own lane, and only where there is one', async ({
  page,
}) => {
  await setupConnection(page);
  const dialog = page.locator('.tok-connect');
  const slot = (id: string) =>
    dialog.locator(`.tok-connect__field:has(#${id}-label)`);

  // The default model is Anthropic's, whose agent lane takes any client: both
  // sides of the switch are live and both quote their own rate.
  const main = slot('tok-connect-model');
  await expect(main.getByRole('radio', { name: /稳定/ })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(main.getByRole('radio', { name: /Agent/ })).toContainText(
    '$0.9 / $4.5',
  );
  await main.getByRole('radio', { name: /Agent/ }).click();
  await expect(main.getByRole('radio', { name: /Agent/ })).toHaveAttribute(
    'aria-checked',
    'true',
  );

  // The flagship slot holds an OpenAI model whose agent lane is Codex-only.
  // Claude Code cannot reach it, so the price shows and the switch does not.
  const opus = slot('tok-connect-opus');
  await opus.getByRole('button', { name: /gpt-6-astra|claude|gemini/ }).click();
  await page
    .getByRole('option', { name: /gpt-6-astra/ })
    .first()
    .click();
  await expect(opus.getByRole('radio', { name: /Agent/ })).toBeDisabled();
  await expect(opus).toContainText('Agent 专线只接受 Codex 客户端');
  await expect(opus.getByRole('radio', { name: /Agent/ })).toContainText(
    '$1.8 / $9',
  );

  // A model with no agent lane says so in one line instead of a dead switch.
  const light = slot('tok-connect-light');
  await light.getByRole('button', { name: /claude|gemini|gpt/ }).click();
  await page
    .getByRole('option', { name: /gemini-flash-3\.8/ })
    .first()
    .click();
  await expect(light.getByRole('radio')).toHaveCount(0);
  await expect(light).toContainText('仅稳定线路');

  // Lanes belong to models, so one slot's choice never moves another's.
  await expect(main.getByRole('radio', { name: /Agent/ })).toHaveAttribute(
    'aria-checked',
    'true',
  );
});

test('a lane the Key kept but this tool cannot use warns instead of hiding', async ({
  page,
}) => {
  // The Key routes gpt-6-astra on the agent lane, which only Codex reaches.
  // Claude Code has to say the request would be refused rather than quietly
  // showing a lane that works — that silence is what made a whole afternoon of
  // 404s look like a gateway fault.
  await setupConnection(page, false, undefined, undefined, {
    model_lanes: { 'gpt-6-astra': 'cheap' },
  });
  const dialog = page.locator('.tok-connect');
  const opus = dialog.locator(
    '.tok-connect__field:has(#tok-connect-opus-label)',
  );
  await opus.getByRole('button', { name: /gpt|claude|gemini/ }).click();
  await page
    .getByRole('option', { name: /gpt-6-astra/ })
    .first()
    .click();
  await expect(opus.getByRole('radio', { name: /Agent/ })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(opus).toContainText('这个工具的请求会被拒绝，请改为稳定');

  // The same model in Codex is simply usable.
  await dialog.getByRole('button', { name: 'Codex CLI', exact: true }).click();
  await expect(dialog).not.toContainText('这个工具的请求会被拒绝');
});

test('a tool with no model field lists the lanes on the Key instead', async ({
  page,
}) => {
  await setupConnection(page);
  const dialog = page.locator('.tok-connect');
  await dialog
    .getByRole('button', { name: 'Cherry Studio', exact: true })
    .click();
  const rows = dialog.locator('.tok-model__row');
  // Only models with a lane worth choosing are listed.
  await expect(rows.filter({ hasText: 'claude-fable-5-1' })).toHaveCount(1);
  await expect(rows.filter({ hasText: 'gemini-flash-3.8' })).toHaveCount(0);
  await expect(dialog.locator('.tok-model__group-head').first()).toContainText(
    '厂商默认',
  );
});

test('empty model catalog blocks chat configuration and import', async ({
  page,
}) => {
  await setupConnection(page, true);
  const dialog = page.locator('.tok-connect');
  for (const name of ['Cherry Studio', 'OpenCat', 'LobeChat']) {
    await dialog.getByRole('button', { name, exact: true }).click();
    await expect(
      dialog.locator('.tok-modal-foot .lazu-button--primary'),
    ).toBeDisabled();
    const quick = dialog.locator('.tok-connect__quick button');
    if (await quick.count()) await expect(quick).toBeDisabled();
  }
});

for (const viewport of [
  { width: 1280, height: 720 },
  { width: 390, height: 844 },
]) {
  test(`connection actions stay visible at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await setupConnection(page);
    const dialog = page.locator('.tok-connect');
    const footer = dialog.locator('.tok-modal-foot');
    const box = await footer.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height);
    await expect(footer.getByRole('button').last()).toBeInViewport();
    if (viewport.width < 760) {
      await expect(dialog.locator('.tok-connect__rail')).toBeHidden();
      await expect(dialog.locator('.tok-connect__mobile-tool')).toBeVisible();
    }
    await page.screenshot({
      animations: 'disabled',
      path: `../../.local-results/pr228-review/fixed-connect-${viewport.width}.png`,
    });
  });
}

test('connection saves only changed fields with the opened version', async ({
  page,
}) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await setupConnection(page);
  const requestPromise = page.waitForRequest(
    (request) =>
      request.method() === 'PUT' && request.url().includes('connect_only=true'),
  );
  await page
    .locator('.tok-connect .tok-connect__field:has(#tok-connect-model-label)')
    .getByRole('radio', { name: /Agent/ })
    .click();
  await page
    .locator('.tok-connect .tok-modal-foot')
    .getByRole('button', { name: '复制并完成' })
    .click();
  const request = await requestPromise;
  expect(request.postDataJSON()).toEqual({
    id: 1,
    expected_version: 7,
    // Only the model that was moved is pinned; the rest keep reading their
    // vendor default rather than being frozen at today's value.
    model_lanes: { 'claude-fable-5-1': 'cheap' },
  });
  await expect(page.locator('.tok-connect')).toHaveCount(0);
});

test('default create form fits a laptop viewport', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await setupConnection(page);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: '创建密钥', exact: true }).click();
  const modal = page.locator('.tok-create-modal');
  await expect(modal.locator('.tok-create__more')).toBeInViewport();
  const body = modal.locator('.lazu-modal-body');
  await page.screenshot({
    animations: 'disabled',
    path: '../../.local-results/pr228-review/fixed-create.png',
  });
  const dimensions = await body.evaluate((element) => ({
    scroll: element.scrollHeight,
    client: element.clientHeight,
  }));
  expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.client + 1);
});

/* The chat entry point was one item of the row's "…" menu, and that menu is
 * gone: copying a Key lives on the key cell, disabling on the batch bar, and
 * the row ends in 接入 · 编辑 · delete. The tests that guarded the menu's
 * chat item guarded a surface that no longer exists. The link builder and its
 * key-safety rules are still covered by chatTemplate.test.ts, so the moment
 * chat gets an entry point again, its safety is already asserted. */

for (const allowAll of [true, false]) {
  test(`empty restricted scope blocks saving until ${allowAll ? 'switching scope back off' : 'selecting a model'}`, async ({
    page,
  }) => {
    await setupConnection(page);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: '创建密钥', exact: true }).click();
    const modal = page.locator('.tok-create-modal');
    await modal.locator('#token-name').fill('Restricted regression');
    await modal.locator('.tok-create__more').click();
    // Scope is a section switch now, not a button that swaps the field: the
    // question "is this Key narrowed" reads the same as every other question
    // behind 更多设置.
    const scopeSection = modal.locator('.tok-more__sec', {
      hasText: '模型范围',
    });
    await scopeSection.locator('.tok-more__switch .lazu-switch').click();
    const create = modal.getByRole('button', {
      name: '创建并接入',
      exact: true,
    });
    const writes: string[] = [];
    page.on('request', (request) => {
      if (
        request.method() === 'POST' &&
        new URL(request.url()).pathname === '/api/token/'
      )
        writes.push(request.postData() || '');
    });
    // Narrowed but empty is the one state that must not save: it would read
    // as "everything" at the gateway while the editor says otherwise.
    await create.click();
    await expect(modal.locator('.tok-scope__warn.is-error')).toBeVisible();
    expect(writes).toHaveLength(0);

    if (allowAll) {
      await scopeSection.locator('.tok-more__switch .lazu-switch').click();
    } else {
      // Search rather than guess which vendor holds the model: the model
      // pane opens on the first vendor the search leaves.
      await scopeSection
        .getByRole('textbox', { name: /搜索模型/ })
        .fill('gpt-6-astra');
      await modal.getByRole('checkbox', { name: /^gpt-6-astra\b/ }).click();
    }
    const requestPromise = page.waitForRequest(
      (request) =>
        request.method() === 'POST' &&
        new URL(request.url()).pathname === '/api/token/',
    );
    await create.click();
    const payload = (await requestPromise).postDataJSON();
    expect(payload.model_limits_enabled).toBe(!allowAll);
    expect(payload.model_limits).toBe(allowAll ? '' : 'gpt-6-astra');
  });
}

/* Naming a vendor is a standing decision, not a snapshot of today's catalog:
 * it is the whole reason the picker is a tree rather than a list. It has to
 * reach the server as the vendor, or the Key silently freezes at whatever the
 * vendor happened to ship on the day it was created. */
test('scoping a Key to a whole vendor sends the vendor, not its models', async ({
  page,
}) => {
  await setupConnection(page);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: '创建密钥', exact: true }).click();
  const modal = page.locator('.tok-create-modal');
  await modal.locator('#token-name').fill('Vendor scope');
  await modal.locator('.tok-create__more').click();
  const scopeSection = modal.locator('.tok-more__sec', { hasText: '模型范围' });
  await scopeSection.locator('.tok-more__switch .lazu-switch').click();

  await scopeSection
    .locator('.tok-scope__vendors .lazu-checkbox')
    .first()
    .click();

  const requestPromise = page.waitForRequest(
    (request) =>
      request.method() === 'POST' &&
      new URL(request.url()).pathname === '/api/token/',
  );
  await modal.getByRole('button', { name: '创建并接入', exact: true }).click();
  const payload = (await requestPromise).postDataJSON();
  expect(payload.model_limits_enabled).toBe(true);
  expect(payload.model_scope_vendors).toEqual(['1']);
  expect(payload.model_limits).toBe('');
});

test('external import keeps the dialog open and retries without stale saves', async ({
  page,
}) => {
  await setupConnection(page);
  const writes: Array<{
    expected_version: number;
    product?: string;
    model_lanes?: Record<string, string>;
  }> = [];
  let saved = { product: 'claude-code', model_lanes: {}, version: 7 };
  await page.route('**/api/token/?connect_only=true', async (route) => {
    const body = route.request().postDataJSON();
    writes.push(body);
    expect(body.expected_version).toBe(saved.version);
    saved = { ...saved, ...body, version: saved.version + 1 };
    await route.fulfill({ json: { success: true, data: saved } });
  });
  const dialog = page.locator('.tok-connect');
  await dialog.getByRole('button', { name: 'Codex CLI', exact: true }).click();
  await dialog
    .getByRole('button', { name: '在 CC Switch 里导入', exact: true })
    .click();
  const retry = dialog.getByRole('button', {
    name: '重新发送导入',
    exact: true,
  });
  await expect(retry).toBeEnabled();
  await expect(
    dialog.getByText('请在 CC Switch 中确认导入。', { exact: false }),
  ).toBeVisible();
  expect(writes).toHaveLength(1);
  await retry.click();
  await expect(dialog).toBeVisible();
  expect(writes).toHaveLength(1);
  await page.screenshot({
    path: '../../.local-results/screenshots/ccswitch-retry-desktop.png',
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(retry).toBeInViewport();
  await expect(dialog.locator('.tok-modal-foot')).toBeInViewport();
  await page.screenshot({
    path: '../../.local-results/screenshots/ccswitch-retry-mobile.png',
  });
  await page.setViewportSize({ width: 1280, height: 720 });

  await dialog
    .getByRole('button', { name: 'Claude Code', exact: true })
    .click();
  await dialog
    .getByRole('button', { name: '在 CC Switch 里导入', exact: true })
    .click();
  await expect(retry).toBeEnabled();
  expect(writes).toHaveLength(2);
  expect(writes[1]).toMatchObject({
    expected_version: 8,
    product: 'claude-code',
  });
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: '接入', exact: true }).first().click();
  await expect(
    dialog.getByRole('button', { name: '在 CC Switch 里导入', exact: true }),
  ).toBeVisible();
});
