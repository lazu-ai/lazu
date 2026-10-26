// @ts-nocheck
import { expect, test } from '@playwright/test';
import { fail, setupMockedApp } from './helpers/mock-app';

const ok = (data = {}) => ({ data, message: '', success: true });

const ITEMS = [
  {
    affected_models: ['gpt-4o-mini'],
    body_i18n: { zh: ['上游停止服务，**9 月 30 日**后自动路由到替代模型。'] },
    created_at: '2026-08-25T10:00:00+08:00',
    delivery: 'critical',
    effective_date: '2026-09-30T00:00:00Z',
    id: 20,
    kind: 'model_update',
    published_at: null,
    title: 'gpt-4o-mini 将于 9 月 30 日下线',
    title_i18n: { zh: 'gpt-4o-mini 将于 9 月 30 日下线' },
  },
  {
    body_i18n: { zh: ['9 月 1 日起单笔充值满 $100 返 5%。'] },
    created_at: '2026-08-22T10:00:00+08:00',
    delivery: 'notice',
    id: 19,
    kind: 'operational',
    published_at: '2026-08-22T10:00:00+08:00',
    summary_i18n: { zh: '九月充值活动精选摘要' },
    title: '九月充值返赠活动',
    title_i18n: { zh: '九月充值返赠活动' },
  },
  {
    body_i18n: { zh: ['独立的网关与运营工作区。'] },
    created_at: '2026-08-20T10:24:00+08:00',
    delivery: 'changelog',
    id: 18,
    kind: 'release_note',
    published_at: '2026-08-20T10:24:00+08:00',
    title: '更快的控制台与新计费选项',
    title_i18n: { zh: '更快的控制台与新计费选项' },
  },
];

/* Captures what the editor actually PUTs so the assertions can prove the
   Markdown round-trip and the model-update fields, not just the pixels. */
const adminRoutes = () => {
  const saved = [];
  const handler = async (url, request) => {
    const { pathname } = new URL(url);
    if (
      pathname === '/api/admin/announcements' &&
      request?.method() === 'GET'
    ) {
      return ok({ items: ITEMS });
    }
    if (/^\/api\/admin\/announcements\/\d+$/.test(pathname)) {
      saved.push(JSON.parse(request.postData() || '{}'));
      return ok({ id: 20 });
    }
    if (pathname === '/api/admin/announcements') {
      saved.push(JSON.parse(request.postData() || '{}'));
      return ok({ id: 21 });
    }
    return undefined;
  };
  handler.saved = saved;
  return handler;
};

const gotoPage = async (page) => {
  await page.goto('/console/announcements', { waitUntil: 'domcontentloaded' });
  await page
    .waitForLoadState('networkidle', { timeout: 5_000 })
    .catch(() => {});
};

test.describe('announcement workbench', () => {
  test('history and editor share one screen; selecting loads the draft', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, { payloadFor: adminRoutes() });
    await gotoPage(page);

    const items = page.locator('.ann-list__item');
    await expect(items).toHaveCount(ITEMS.length);
    // Editor is resident, not behind a navigation step.
    await expect(page.locator('.ann-editor')).toBeVisible();

    await items.first().click();
    await expect(items.first()).toHaveClass(/is-active/);
    await expect(page.locator('.ann-editor__head h2')).toHaveText('编辑公告');
    await expect(page.locator('.ann-md__input')).toHaveValue(/9 月 30 日/);
    // model_update reveals its two extra fields.
    await expect(page.locator('.ann-model')).toHaveText(/gpt-4o-mini/);
    await expect(page.locator('.ann-date')).toHaveValue('2026-09-30');

    await app.expectNoUnexpectedErrors();
  });

  test('delivery level is visible while reviewing an announcement', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, { payloadFor: adminRoutes() });
    await gotoPage(page);

    const critical = page.locator(
      '.ann-list__item .ann-delivery[data-delivery="critical"]',
    );
    await expect(critical).toHaveText('CRITICAL');

    await page.locator('.ann-list__item').first().click();
    await expect(
      page.locator('.ann-editor__head .ann-delivery[data-delivery="critical"]'),
    ).toHaveText('CRITICAL');

    await app.expectNoUnexpectedErrors();
  });

  test('selection is marked without a leading rail or bracket', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, { payloadFor: adminRoutes() });
    await gotoPage(page);
    const first = page.locator('.ann-list__item').first();
    await first.click();

    // Yunmu rule 6: background + weight only. A left rail would show up as a
    // non-zero left border, and a bracket as ::before content.
    const marks = await first.evaluate((el) => {
      const cs = getComputedStyle(el);
      const before = getComputedStyle(el, '::before');
      return {
        before: before.content,
        borderLeft: cs.borderLeftWidth,
        background: cs.backgroundColor,
        weight: getComputedStyle(el.querySelector('.ann-list__title'))
          .fontWeight,
      };
    });
    expect(marks.borderLeft).toBe('0px');
    expect(['none', 'normal', '']).toContain(marks.before);
    expect(marks.background).not.toBe('rgba(0, 0, 0, 0)');
    expect(Number(marks.weight)).toBeGreaterThanOrEqual(700);

    await app.expectNoUnexpectedErrors();
  });

  test('markdown preview renders the body it will publish', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, { payloadFor: adminRoutes() });
    await gotoPage(page);
    await page.locator('.ann-list__item').first().click();

    await page.locator('.ann-md__view', { hasText: '预览' }).click();
    const preview = page.locator('.ann-md__preview');
    await expect(preview).toBeVisible();
    await expect(preview.locator('strong')).toHaveText('9 月 30 日');
    await expect(page.locator('.ann-md__input')).toHaveCount(0);

    await page.locator('.ann-md__view', { hasText: '编辑' }).click();
    await expect(page.locator('.ann-md__input')).toBeVisible();
    await page
      .locator('.ann-md__input')
      .fill('代码示例\n\n```\nconst value = 1;\n```');
    await page.locator('.ann-md__view', { hasText: '预览' }).click();
    const unlabeledCode = page.locator('.ann-md__preview pre code');
    await expect(unlabeledCode).toContainText('const value = 1;');
    await expect(unlabeledCode).toHaveCSS('display', 'block');

    await app.expectNoUnexpectedErrors();
  });

  test('kind switch reveals model fields and publish sends them', async ({
    page,
  }) => {
    const routes = adminRoutes();
    const app = await setupMockedApp(page, { payloadFor: routes });
    await gotoPage(page);

    // A fresh draft defaults to 运营公告, which has no model fields.
    await expect(page.locator('.ann-editor__row')).toHaveCount(0);
    await page
      .locator('.ann-editor__selectors')
      .getByRole('combobox')
      .first()
      .click();
    await page.getByRole('option', { name: '模型变更', exact: true }).click();
    await expect(page.locator('.ann-editor__row')).toBeVisible();
    await page.locator('.ann-model-add').click();
    await page
      .getByRole('textbox', { name: '输入模型名称' })
      .fill('cancelled-example');
    await page.keyboard.press('Escape');
    await expect(
      page.getByRole('textbox', { name: '输入模型名称' }),
    ).toHaveCount(0);
    await expect(page.locator('.ann-model')).toHaveCount(0);
    await page.locator('.ann-model-add').click();
    await page
      .getByRole('textbox', { name: '输入模型名称' })
      .fill('gpt-example-announcement');
    await page.getByRole('textbox', { name: '输入模型名称' }).press('Enter');
    await expect(page.locator('.ann-model')).toContainText(
      'gpt-example-announcement',
    );

    await page.locator('.ann-editor .lazu-input').first().fill('测试公告');
    await page.locator('.ann-md__input').fill('第一段\n\n第二段');
    // Scoped to the editor: list rows carry 已发布 in their accessible name.
    await page.locator('.ann-editor__foot button', { hasText: '发布' }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: '发布', exact: true })
      .click();

    await expect
      .poll(() => routes.saved.length, { timeout: 5_000 })
      .toBeGreaterThan(0);
    const payload = routes.saved.at(-1);
    expect(payload.kind).toBe('model_update');
    expect(payload.affected_models).toEqual(['gpt-example-announcement']);
    expect(payload.published).toBe(true);
    // Blank-line separated blocks become the stored paragraph array.
    expect(payload.body_i18n.zh).toEqual(['第一段', '第二段']);
    expect(payload.title_i18n.zh).toBe('测试公告');
    // The public status page prints summary verbatim, so it must be plain prose.
    expect(payload.summary_i18n.zh).toBe('第一段');

    // Saving keeps the operator on what they just wrote instead of blanking the
    // editor and losing their place.
    await expect(page.locator('.ann-editor__head h2')).toHaveText('编辑公告');

    await app.expectNoUnexpectedErrors();
  });

  test('saving an existing draft does not unpublish it', async ({ page }) => {
    const routes = adminRoutes();
    const app = await setupMockedApp(page, { payloadFor: routes });
    await gotoPage(page);
    await page.locator('.ann-list__item').first().click();
    await page
      .locator('.ann-editor__foot button', { hasText: '存草稿' })
      .click();

    await expect.poll(() => routes.saved.length).toBe(1);
    expect(routes.saved[0]).not.toHaveProperty('published');
    await app.expectNoUnexpectedErrors();
  });

  test('editing an announcement preserves its curated localized summaries', async ({
    page,
  }) => {
    const routes = adminRoutes();
    const app = await setupMockedApp(page, { payloadFor: routes });
    await gotoPage(page);
    await page.locator('.ann-list__item').nth(1).click();
    await page.locator('.ann-editor .lazu-input').first().fill('更新后的标题');
    await page
      .locator('.ann-editor__foot button', { hasText: '存草稿' })
      .click();

    await expect.poll(() => routes.saved.length).toBe(1);
    expect(routes.saved[0].summary_i18n.zh).toBe('九月充值活动精选摘要');
    await app.expectNoUnexpectedErrors();
  });

  test('preview highlights supported languages and loads formulas on demand', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, { payloadFor: adminRoutes() });
    await gotoPage(page);
    await page
      .locator('.ann-md__input')
      .fill(
        '```json\n{"model": "luna"}\n```\n\n```rust\nfn main() {}\n```\n\n面积是 $x^2$。',
      );
    await page.locator('.ann-md__view', { hasText: '预览' }).click();
    const blocks = page.locator('.ann-md__preview pre code');
    await expect(blocks.nth(0).locator('.hljs-attr')).toHaveText('"model"');
    await expect(blocks.nth(1)).toHaveText('fn main() {}');
    await expect(blocks.nth(1).locator('span')).toHaveCount(0);
    await expect(page.locator('.ann-md__preview .katex')).toHaveCount(1);
    await app.expectNoUnexpectedErrors();
  });

  test('saving Markdown preserves indentation inside fenced code blocks', async ({
    page,
  }) => {
    const routes = adminRoutes();
    const app = await setupMockedApp(page, { payloadFor: routes });
    const markdown =
      '```ts\nfunction chooseModel() {\n  const model = "luna";\n\n  return model;\n}\n```';
    await gotoPage(page);
    await page.locator('.ann-editor .lazu-input').first().fill('代码公告');
    await page.locator('.ann-md__input').fill(markdown);
    await page.locator('.ann-editor__foot button', { hasText: '发布' }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: '发布', exact: true })
      .click();

    await expect.poll(() => routes.saved.length).toBe(1);
    expect(routes.saved[0].body_i18n.zh).toEqual([markdown]);
    await app.expectNoUnexpectedErrors();
  });

  test('failed unpublish stays published and reports the server error', async ({
    page,
  }) => {
    const routes = adminRoutes();
    const handler = async (url, request) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/admin/announcements/19/unpublish') {
        return fail(500, 'internal_error');
      }
      return routes(url, request);
    };
    await setupMockedApp(page, { payloadFor: handler });
    await gotoPage(page);
    await page.locator('.ann-list__item').nth(1).click();
    await expect(page.locator('.ann-editor__head .ann-state')).toHaveText(
      '已发布',
    );
    await page.locator('.ann-editor__foot button', { hasText: '下架' }).click();

    await expect(
      page
        .getByText(
          '服务没能完成这个请求。如果反复出现，请带上请求编号联系支持。',
        )
        .last(),
    ).toBeVisible();
    await expect(page.locator('.ann-editor__head .ann-state')).toHaveText(
      '已发布',
    );
  });

  test('history loads older announcements with a stable cursor', async ({
    page,
  }) => {
    const handler = async (url) => {
      const { pathname, searchParams } = new URL(url);
      if (pathname !== '/api/admin/announcements') return undefined;
      if (searchParams.get('before_id') === '19') {
        return ok({ items: [ITEMS[2]], next_before_id: 0 });
      }
      return ok({ items: ITEMS.slice(0, 2), next_before_id: 19 });
    };
    const app = await setupMockedApp(page, { payloadFor: handler });
    await gotoPage(page);

    await expect(page.locator('.ann-list__item')).toHaveCount(2);
    await page.getByRole('button', { name: '加载更多' }).click();
    await expect(page.locator('.ann-list__item')).toHaveCount(3);
    await expect(page.getByRole('button', { name: '加载更多' })).toHaveCount(0);
    await app.expectNoUnexpectedErrors();
  });

  test('generated public summary removes markdown structure', async ({
    page,
  }) => {
    const routes = adminRoutes();
    const app = await setupMockedApp(page, { payloadFor: routes });
    await gotoPage(page);
    await page.locator('.ann-editor .lazu-input').first().fill('摘要测试');
    await page
      .locator('.ann-md__input')
      .fill('# 标题\n![图示](https://example.com/image.png) **正文**');
    await page.locator('.ann-editor__foot button', { hasText: '发布' }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: '发布', exact: true })
      .click();

    await expect.poll(() => routes.saved.length).toBe(1);
    expect(routes.saved[0].summary_i18n.zh).toBe('标题 图示 正文');
    await app.expectNoUnexpectedErrors();
  });

  test('disabled announcements module blocks the direct route', async ({
    page,
  }) => {
    const app = await setupMockedApp(page, {
      status: {
        SidebarModulesAdmin: JSON.stringify({
          admin: { announcements: false, enabled: true },
        }),
      },
    });
    await gotoPage(page);

    // The switched-off fallback: the Off mark plus a translated title.
    // The route announcer (sr-only) repeats the title, so ask for the heading.
    await expect(
      page.getByRole('heading', { name: '这项功能没有开启' }),
    ).toBeVisible();
    await expect(page.locator('.lazu-status-page__mark')).toHaveText('Off');
    await expect(page.locator('.ann-workbench')).toHaveCount(0);
    await app.expectNoUnexpectedErrors();
  });
});
