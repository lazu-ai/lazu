// @ts-nocheck
/**
 * Visual parity check for the G1 console (brand/BRAND.md, locked 2026-10-06;
 * reference: lazu-ops:brand/explorations/positioning-team-stability-2026-10/).
 *
 * These assertions describe the signature elements the brand decision named,
 * so a regression here means the implementation drifted from the locked
 * design rather than merely looking different.
 */
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('console ground is one flat neutral grey with no ambient light or grid', async ({
  page,
}) => {
  await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  const ground = await page.evaluate(() => {
    const shell = document.querySelector('.app-shell-v2');
    if (!shell) return null;
    const style = getComputedStyle(shell);
    const before = getComputedStyle(shell, '::before');
    return {
      color: style.backgroundColor,
      image: style.backgroundImage,
      gridImage: before.backgroundImage,
    };
  });

  expect(ground).not.toBeNull();
  expect(ground.color).toBe('rgb(245, 245, 246)');
  expect(ground.image).toBe('none');
  expect(ground.gridImage).toBe('none');
});

test('sidebar is flush to the edge on the page ground, without a product rail', async ({
  page,
}) => {
  await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  await expect(page.locator('.sidebar-v2__product-rail')).toHaveCount(0);
  await expect(page.locator('.sidebar-v2--split')).toHaveCount(0);
  await expect(page.locator('.sidebar-v2__scope-tabs')).toHaveCount(0);

  const sidebar = await page.evaluate(() => {
    const column = document.querySelector('.app-shell-sidebar');
    const node = document.querySelector('.app-shell-sidebar > *');
    const active = document.querySelector('.sidebar-v2__item.is-active');
    if (!column || !node) return null;
    const columnStyle = getComputedStyle(column);
    const style = getComputedStyle(node);
    return {
      activeBg: active ? getComputedStyle(active).backgroundColor : null,
      activeColor: active ? getComputedStyle(active).color : null,
      background: columnStyle.backgroundColor,
      borderRight: columnStyle.borderRightWidth,
      padding: columnStyle.padding,
      radius: style.borderRadius,
      shadow: style.boxShadow,
    };
  });
  expect(sidebar).not.toBeNull();
  expect(sidebar.radius).toBe('0px');
  expect(sidebar.shadow).toBe('none');
  expect(sidebar.padding).toBe('0px');
  expect(sidebar.borderRight).toBe('1px');
  expect(sidebar.background).toBe('rgb(245, 245, 246)');
  // The current item is a teal wash with teal type — never a rail.
  if (sidebar.activeColor !== null) {
    expect(sidebar.activeColor).toBe('rgb(31, 111, 107)');
    expect(sidebar.activeBg).toBe('rgba(31, 111, 107, 0.11)');
  }
});

test('primary action is ink on paper rather than a brand fill', async ({
  page,
}) => {
  await setupMockedApp(page);
  await expectRouteReady(page, '/console');

  const tokens = await page.evaluate(() => {
    const style = getComputedStyle(document.documentElement);
    return {
      action: style.getPropertyValue('--lazu-action').trim(),
      chart: style.getPropertyValue('--lazu-chart-1').trim(),
      evidence: style.getPropertyValue('--lazu-evidence').trim(),
      onAction: style.getPropertyValue('--lazu-on-action').trim(),
    };
  });

  expect(tokens.action.toLowerCase()).toBe('#202329');
  expect(tokens.onAction.toLowerCase()).toBe('#f2f3f5');
  expect(tokens.evidence.toLowerCase()).toBe('#1f6f6b');
  expect(tokens.chart.toLowerCase()).toBe('#1f6f6b');
  // Charts must never carry the retired warm orange.
  expect(tokens.chart.toLowerCase()).not.toBe('#f27828');
});
