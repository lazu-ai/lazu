import { expect, test } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';

for (const theme of ['light', 'dark'] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await setupMockedApp(page, { language: 'en' });
      await page.addInitScript((value) => {
        localStorage.setItem('lazu.theme', value);
      }, theme);
    });

    test('personal settings controls retain their appearance in a portal', async ({
      page,
    }) => {
      await page.goto('/console/personal');
      await page
        .locator('.set-row')
        .filter({
          has: page.locator('.set-row__lb', { hasText: 'Low-balance alert' }),
        })
        .getByRole('button', { name: 'Edit', exact: true })
        .click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      const select = dialog.locator('.lazu-select-trigger');
      await expect(select).toHaveCSS('border-top-width', '1px');
      await expect(select).not.toHaveCSS(
        'background-color',
        'rgba(0, 0, 0, 0)',
      );
      await expect(select).toHaveCSS('min-height', '38px');
      await expect(select).toHaveCSS('padding-left', '12px');
      await expect(select).toHaveCSS('padding-right', '12px');
      const cancel = dialog.getByRole('button', {
        name: 'Cancel',
        exact: true,
      });
      await expect(cancel).toHaveCSS('border-radius', '12px');
      await expect(cancel).toHaveCSS('min-height', '30px');
      // Header and footer are pinned; only the body scrolls.
      await expect(dialog).toHaveCSS('display', 'flex');
      await expect(dialog.locator('.lazu-modal-body')).toHaveCSS(
        'overflow-y',
        'auto',
      );
      await expect(dialog.locator('.lazu-modal-header')).toHaveCSS(
        'flex-shrink',
        '0',
      );
      await select.click();
      await expect(
        page.getByRole('option', { name: 'Webhook', exact: true }),
      ).toBeVisible();
      // The list is portalled to body but keeps the page's control recipe,
      // on one sheet of paper at control radius.
      const list = page.locator('.lazu-select-content');
      await expect(list).toHaveClass(/console-review-controls/);
      await expect(list).toHaveCSS('border-radius', '12px');
      await page.keyboard.press('Escape');
      await cancel.click();
      await expect(dialog).not.toBeVisible();
    });

    test('there is one switch: 36 by 20, teal when on', async ({ page }) => {
      await page.goto('/console/personal');
      const on = page.locator('.lazu-switch[data-checked]').first();
      await expect(on).toBeVisible();
      await expect(on).toHaveCSS('width', '36px');
      await expect(on).toHaveCSS('height', '20px');
      const teal = await on.evaluate((node) =>
        getComputedStyle(node).getPropertyValue('--lazu-evidence').trim(),
      );
      expect(teal).not.toBe('');
      await expect(on.locator('.lazu-switch__thumb')).toHaveCSS(
        'width',
        '16px',
      );
    });

    test('announcement Select owns its surface outside the review page list', async ({
      page,
    }) => {
      await page.goto('/console/announcements');
      const select = page.locator('.ann-editor .lazu-select-trigger').first();
      await expect(select).toBeVisible();
      await expect(select).toHaveCSS('border-top-width', '1px');
      await expect(select).not.toHaveCSS(
        'background-color',
        'rgba(0, 0, 0, 0)',
      );
      await select.click();
      await expect(page.getByRole('listbox')).toBeVisible();
      await page.keyboard.press('Escape');
    });

    test('legacy interaction aliases resolve to current theme tokens', async ({
      page,
    }) => {
      await page.goto('/console/models');
      await expect(page.locator('body')).toHaveAttribute('theme-mode', theme);
      const colors = await page.locator('body').evaluate((body) => {
        const style = getComputedStyle(body);
        const value = (name: string) => style.getPropertyValue(name).trim();
        return {
          hover: value('--app-row-hover'),
          surface: value('--lazu-surface-muted'),
          active: value('--app-row-active'),
          accent: value('--lazu-action-soft'),
          ring: value('--app-focus-ring'),
        };
      });
      expect(colors.hover).toBe(colors.surface);
      expect(colors.active).toBe(colors.accent);
      expect(colors.ring).toBe(`0 0 0 3px ${colors.accent}`);
    });
  });
}

test('below 768px a dialog is a bottom sheet with a 44px close', async ({
  page,
}) => {
  await page.setViewportSize({ width: 740, height: 900 });
  await setupMockedApp(page, { language: 'en' });
  await page.goto('/console/personal');
  await page
    .locator('.set-row')
    .filter({
      has: page.locator('.set-row__lb', { hasText: 'Low-balance alert' }),
    })
    .getByRole('button', { name: 'Edit', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toHaveClass(/lazu-modal-content--sheet/);
  // Pinned to the bottom edge across the full width.
  await expect(dialog).toHaveCSS('bottom', '0px');
  await expect(dialog).toHaveCSS('left', '0px');
  await expect(dialog).toHaveCSS('width', '740px');
  const close = dialog.getByRole('button', { name: 'Close', exact: true });
  await expect(close).toHaveCSS('width', '44px');
  await expect(close).toHaveCSS('height', '44px');
  await expect(page.locator('.lazu-modal-grabber')).toHaveCount(0);
});

test('mobile sign-out signs out from the drawer at once', async ({ page }) => {
  let logouts = 0;
  await page.setViewportSize({ width: 390, height: 844 });
  await setupMockedApp(page, {
    language: 'en',
    payloadFor: (raw: string) => {
      if (new URL(raw).pathname === '/api/user/logout') {
        logouts++;
        return { success: true };
      }
      return undefined;
    },
  });
  await page.goto('/console/models');
  await page.locator('.mobile-console-menu-trigger').click();
  await page.locator('.sidebar-v2__logout').click();
  await expect.poll(() => logouts).toBe(1);
  await expect(page).toHaveURL(/\/login/);
});
