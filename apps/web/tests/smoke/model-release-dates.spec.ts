import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

test('missing release dates remain actionable without pending reviews', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/model_info/reviews'
        ? {
            success: true,
            data: {
              items: [],
              pending_count: 0,
              last_run_at: 1,
              missing_release_dates: [
                { model_id: 42, model_name: 'gpt-6-astra', maker: 'openai' },
              ],
            },
          }
        : undefined,
  });
  await page.goto('/console/model-ops/updates');
  await expect(
    page.getByRole('heading', { name: '在售模型缺少发布日期' }),
  ).toBeVisible();
  await expect(page.getByText('gpt-6-astra', { exact: true })).toBeVisible();
  await expect(page.getByText('资料都是最新的，没有要处理的事。')).toHaveCount(
    0,
  );
  await page.getByRole('button', { name: '核对发布日期' }).click();
  await expect(page).toHaveURL(/model=42&panel=info/);
});

test('new release rows keep maker and date aligned with their headers', async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/model_info/reviews'
        ? {
            success: true,
            data: {
              items: [
                {
                  id: 1,
                  kind: 'new_release',
                  status: 'pending',
                  model_name: 'gpt-test',
                  maker: 'openai',
                  upstream_value: '2026-09-22',
                  payload: JSON.stringify({
                    context_length: 1000000,
                    official_input_per_mtok: 2,
                    official_output_per_mtok: 10,
                  }),
                },
              ],
              pending_count: 1,
              last_run_at: 1,
              missing_release_dates: [],
            },
          }
        : undefined,
  });
  await page.goto('/console/model-ops/updates');
  const rows = page.locator('.mo-updates__grid--fresh .mo-updates__row');
  const cells = rows.nth(1).locator(':scope > span');
  await expect(cells).toHaveCount(6);
  await expect(cells.nth(1)).toHaveText('OpenAI');
  await expect(cells.nth(2)).toHaveText('2026-09-22');
  await expect(cells.nth(3)).toHaveText('1M');
  await expect(cells.nth(4)).toHaveText('2 / 10');
  for (let column = 1; column < 6; column++) {
    const header = await rows
      .nth(0)
      .locator(':scope > span')
      .nth(column)
      .boundingBox();
    const cell = await cells.nth(column).boundingBox();
    expect(header).not.toBeNull();
    expect(cell).not.toBeNull();
    expect(Math.abs(header!.x - cell!.x)).toBeLessThan(1);
  }
});
