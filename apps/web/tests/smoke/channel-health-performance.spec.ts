import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

type FormatProbe = Window & { healthDateFormats: number };

test('inspecting a slice does not reformat historical labels across the board', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await page.addInitScript(() => {
    const probe = window as unknown as FormatProbe;
    probe.healthDateFormats = 0;
    // Count both native Date entrypoints and reused Intl formatters, so the
    // assertion survives a change in the formatting implementation.
    for (const method of [
      'toLocaleDateString',
      'toLocaleTimeString',
    ] as const) {
      const original = Date.prototype[method];
      Date.prototype[method] = function (...args: Parameters<typeof original>) {
        probe.healthDateFormats++;
        return original.apply(this, args);
      };
    }
    const descriptor = Object.getOwnPropertyDescriptor(
      Intl.DateTimeFormat.prototype,
      'format',
    );
    const getFormat = descriptor?.get;
    if (getFormat) {
      Object.defineProperty(Intl.DateTimeFormat.prototype, 'format', {
        ...descriptor,
        get() {
          const format: (value?: number | Date) => string =
            getFormat.call(this);
          return (value?: number | Date) => {
            probe.healthDateFormats++;
            return format(value);
          };
        },
      });
    }
  });
  await expectRouteReady(page, '/console/health');
  const row = page.locator('.chealth-lane').first();
  const bars = row.locator('button.chealth-bar');
  await expect(bars).toHaveCount(84);
  await expect
    .poll(() =>
      page.evaluate(() => (window as unknown as FormatProbe).healthDateFormats),
    )
    .toBeGreaterThan(84);

  for (const index of [40, 41]) {
    await page.evaluate(() => {
      (window as unknown as FormatProbe).healthDateFormats = 0;
    });
    await bars.nth(index).hover();
    await expect(row.locator('.chealth-tip')).toBeVisible();
    await expect(bars.nth(index)).toHaveClass(/is-hovered/);
    expect(
      await page.evaluate(
        () => (window as unknown as FormatProbe).healthDateFormats,
      ),
    ).toBeLessThanOrEqual(6);
    expect(await bars.nth(index).getAttribute('title')).toBe(
      await bars.nth(index).getAttribute('aria-label'),
    );
  }
  await bars.nth(42).focus();
  await expect(row.locator('.chealth-tip')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(row.locator('.chealth-tip')).toHaveCount(0);
});

test('cached slice labels follow a language change without reloading the page', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console/health');
  const bar = page
    .locator('.chealth-lane')
    .first()
    .locator('button.chealth-bar')
    .nth(40);
  const before = await bar.getAttribute('aria-label');
  await page.locator('.console-topbar button', { hasText: 'ZH' }).click();
  await page.getByRole('menuitem', { name: 'English' }).click();
  await expect(bar).not.toHaveAttribute('aria-label', before!);
  await expect(bar).toHaveAttribute('aria-label', /Sep/);
  expect(await bar.getAttribute('title')).toBe(
    await bar.getAttribute('aria-label'),
  );
  await bar.focus();
  await expect(page.locator('.chealth-tip__when')).toContainText('Sep');
});
