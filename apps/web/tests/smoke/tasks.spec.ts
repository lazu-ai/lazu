// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const now = Math.floor(Date.now() / 1000);

const tasks = {
  items: [
    {
      id: 'mjob_running',
      kind: 'video',
      model: 'seedance-2.5',
      status: 'running',
      source: 'media_job',
      created_at: now - 102,
      started_at: now - 99,
      request_id: 'req_lazu_running',
      token_name: 'video-prod',
      prompt: '海边日落延时，镜头缓慢右移',
      spec: { seconds: 5, resolution: '720p', aspect_ratio: '16:9' },
      hold_microusd: 1155000,
      charge_microusd: 0,
      refund_microusd: 0,
    },
    {
      id: 'mjob_done',
      kind: 'video',
      model: 'seedance-2.5',
      status: 'succeeded',
      source: 'media_job',
      created_at: now - 3600,
      started_at: now - 3598,
      completed_at: now - 3430,
      request_id: 'req_lazu_done',
      token_name: 'video-prod',
      prompt: '按参考视频的运镜，纸船漂过池塘',
      spec: {
        seconds: 4,
        resolution: '480p',
        aspect_ratio: 'adaptive',
        reference_videos: 1,
        audio: false,
      },
      hold_microusd: 3502000,
      charge_microusd: 491235,
      refund_microusd: 3010765,
      artifact: {
        file_id: 'file-lazu-done',
        url: '/__test__/clip.mp4',
        mime_type: 'video/mp4',
        bytes: 2031309,
        expires_at: now + 18 * 3600 + 1800,
      },
    },
    {
      id: 'mjob_failed',
      kind: 'video',
      model: 'seedance-2.5',
      status: 'failed',
      source: 'media_job',
      created_at: now - 7200,
      completed_at: now - 7112,
      key_deleted: true,
      prompt: '舞者跟着鼓点转身',
      spec: { seconds: 10, resolution: '720p', reference_images: 1 },
      hold_microusd: 2310000,
      charge_microusd: 0,
      refund_microusd: 2310000,
      error: { code: 'video_provider_failed', message: 'rejected' },
    },
  ],
  total: 3,
  page: 1,
  page_size: 20,
  summary: {
    tasks: 3,
    spend_microusd: 491235,
    active: 1,
    active_hold_microusd: 1155000,
    failed: 1,
    refunded_microusd: 5320765,
    expiring_soon: 1,
  },
};

test('the Tasks tab reads like the log and opens a job under its row', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    payloadFor: (url) =>
      new URL(url).pathname === '/api/log/self/tasks'
        ? { data: tasks, message: '', success: true }
        : undefined,
  });
  await page.route('**/__test__/clip.mp4', (route) =>
    route.fulfill({ status: 200, contentType: 'video/mp4', body: '' }),
  );
  await expectRouteReady(page, '/console/usage?tab=tasks');

  const summary = page.locator('.tasks-summary');
  await expect(summary).toContainText('$0.4912');
  await expect(summary).toContainText('1 · $1.155');
  const rows = page.locator('.tasks-table tbody tr.logv2-row');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText('生成中');
  await expect(rows.nth(0)).toContainText('预扣 $1.155');
  await expect(rows.nth(1)).toContainText('参考视频 1');
  await expect(rows.nth(1)).toContainText('18 小时');
  await expect(rows.nth(2)).toContainText('已删除的 Key');
  await expect(rows.nth(2)).toContainText('已退款');

  await rows.nth(1).click();
  const detail = page.locator('.logv2-detail-row');
  await expect(detail).toContainText('退回 $3.0108');
  await expect(detail).toContainText('mjob_done');
  await expect(detail.locator('video')).toHaveCount(1);
  if (process.env.TASKS_SCREENSHOT) {
    await page.screenshot({
      fullPage: true,
      path: process.env.TASKS_SCREENSHOT,
    });
  }

  await app.expectNoUnexpectedErrors();
});

test('administrators see every account in the platform view, with the user', async ({
  page,
}) => {
  const paths = [];
  const withUsers = {
    ...tasks,
    items: tasks.items.map((item, index) => ({
      ...item,
      user_id: 10 + index,
      username: ['alice', 'bob', 'carol'][index],
    })),
  };
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url) => {
      const { pathname } = new URL(url);
      if (pathname.startsWith('/api/log/')) paths.push(pathname);
      return pathname === '/api/log/tasks'
        ? { data: withUsers, message: '', success: true }
        : undefined;
    },
  });
  await page.route('**/__test__/clip.mp4', (route) =>
    process.env.TASKS_VIDEO
      ? route.fulfill({
          path: process.env.TASKS_VIDEO,
          contentType: 'video/mp4',
        })
      : route.fulfill({ status: 200, contentType: 'video/mp4', body: '' }),
  );
  await expectRouteReady(page, '/console/usage?tab=tasks');

  await expect.poll(() => paths).toContain('/api/log/tasks');
  expect(paths).not.toContain('/api/log/self/tasks');
  await expect(page.getByRole('columnheader', { name: '用户' })).toBeVisible();
  const rows = page.locator('.tasks-table tbody tr.logv2-row');
  await expect(rows.nth(1)).toContainText('bob');
  await expect(rows.nth(1)).toContainText('#11');
  await expect(page.getByRole('tab', { name: '生成任务' })).toBeVisible();

  await rows.nth(1).click();
  const detail = page.locator('.tasks-detail');
  await expect(detail.locator('.tasks-detail__prompt')).toContainText('纸船');
  await expect(detail.locator('.tasks-bill')).toContainText('$3.502');
  await expect(detail.locator('.tasks-id')).toHaveCount(2);
  if (process.env.TASKS_SCREENSHOT) {
    await page.waitForTimeout(500);
    await page.screenshot({
      fullPage: true,
      path: process.env.TASKS_SCREENSHOT,
    });
  }

  await app.expectNoUnexpectedErrors();
});
