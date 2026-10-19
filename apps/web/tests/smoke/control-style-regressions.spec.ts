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

    test('desktop sign-out retains its compact geometry inside the account menu', async ({
      page,
    }) => {
      await page.goto('/console/models');
      await page.locator('.console-topbar__avatar').click();
      await page
        .getByRole('menuitem', { name: 'Sign out', exact: true })
        .click();
      const popup = page
        .locator('.lazu-dropdown-content')
        .filter({ hasText: 'Sign out of this account?' });
      await expect(popup).toHaveCSS('width', '210px');
      await expect(popup).toHaveCSS('border-radius', '14px');
      const cancel = popup.getByRole('button', { name: 'Cancel', exact: true });
      await expect(cancel).toHaveCSS('height', '28px');
      await expect(cancel).toHaveCSS('border-radius', '8px');
      await expect(cancel).toBeFocused();
      await cancel.click();
      await expect(
        page.getByRole('menuitem', { name: 'Sign out', exact: true }),
      ).toBeVisible();
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
      await select.click();
      await expect(
        page.getByRole('option', { name: 'Webhook', exact: true }),
      ).toBeVisible();
      await page.keyboard.press('Escape');
      await cancel.click();
      await expect(dialog).not.toBeVisible();
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

    test('monthly report date trigger keeps its field surface', async ({
      page,
    }) => {
      await page.goto('/console/usage?tab=monthly');
      const date = page.locator('.lazu-date-trigger');
      await expect(date).toBeVisible();
      await expect(date).toHaveCSS('border-top-width', '1px');
      await expect(date).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
      await date.click();
      await expect(page.locator('.lazu-calendar-popover')).toBeVisible();
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

test('mobile sign-out retains compact styling and dismisses without signing out', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setupMockedApp(page, { language: 'en' });
  await page.goto('/console/models');
  await page.locator('.mobile-console-menu-trigger').click();
  await page.locator('.sidebar-v2__logout').click();
  const popup = page
    .locator('.lazu-popover-content')
    .filter({ hasText: 'Sign out of this account?' });
  await expect(popup).toHaveCSS('width', '210px');
  await expect(popup).toHaveCSS('border-radius', '14px');
  await expect(
    popup.getByRole('button', { name: 'Cancel', exact: true }),
  ).toHaveCSS('height', '28px');
  await page.keyboard.press('Escape');
  await expect(popup).not.toBeVisible();
  await expect(page).toHaveURL(/\/console\/models/);
});
