import { expect, test } from '@playwright/test';

/* The landing page used to be a single viewport-tall hero with nothing after
 * it. These assertions keep the sections that were added — what the product
 * is, the four things it solves, how to start, and the FAQ — from quietly
 * disappearing again, and keep the page on the Yunmu palette. */

test('landing explains the product and keeps its sections', async ({
  page,
}) => {
  await page.goto('/');

  // One sentence saying what this is, above the fold.
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();
  await expect(page.locator('.lazu-landing__what')).toContainText(/lazu/i);

  // The hero is set in the display serif.
  const headlineFont = await page
    .locator('.lazu-landing__headline')
    .evaluate((el) => getComputedStyle(el).fontFamily);
  expect(headlineFont).toMatch(/Instrument Serif/);

  // It carries the console at its members-and-quota page: one row per member,
  // and the one without a cap draws no meter rather than a share of nothing.
  await expect(
    page.locator('.lazu-landing__shot .lazu-landing__tr'),
  ).toHaveCount(4);
  await expect(page.locator('.lazu-landing__meter i')).toHaveCount(3);

  // Four capability sections, each paired with a console panel.
  await expect(page.locator('.lazu-landing__feat')).toHaveCount(4);
  await expect(
    page.locator('.lazu-landing__feat .lazu-landing__panel'),
  ).toHaveCount(4);

  // Getting started and FAQ both survive.
  await expect(page.locator('.lazu-landing__step')).toHaveCount(3);
  await expect(page.locator('.lazu-landing__faq-q')).toHaveCount(4);

  // The page is meaningfully longer than one screen.
  const height = await page.evaluate(() => document.body.scrollHeight);
  expect(height).toBeGreaterThan(2000);
});

test('landing figures come from the API, never from hard-coded copy', async ({
  page,
}) => {
  // Keep this state deterministic even when the local production build points
  // at a reachable split-origin API through VITE_REACT_APP_SERVER_URL.
  await page.route('**/api/pricing*', (route) => route.abort());
  await page.route('**/api/public/status*', (route) => route.abort());

  // With both live sources unavailable, every figure should fall back to its
  // placeholder. If a number shows up here, it was baked into the copy.
  await page.goto('/');

  // Either the strip is hidden because the call failed outright, or every
  // figure is a placeholder. What must never happen is a number appearing
  // when there is no backend to have measured it.
  const figures = await page.locator('.lazu-landing__stat b').allTextContents();
  figures.forEach((text) => expect(text).not.toMatch(/\d/));

  // Same for the model panel: no catalogue means one plain line saying so,
  // not invented rows — and that line must actually say something.
  const wait = page.locator('.lazu-landing__row--wait');
  await expect(wait).toHaveCount(1);
  await expect(wait).not.toBeEmpty();
});

test('landing runs on the Yunmu palette, not a private landing theme', async ({
  page,
}) => {
  await page.goto('/');

  const tokens = await page.evaluate(() => {
    const style = getComputedStyle(document.documentElement);
    const landing = document.querySelector('.lazu-landing');
    const local = landing ? getComputedStyle(landing) : null;
    return {
      action: style.getPropertyValue('--lazu-action').trim(),
      // The retired per-page colour layer must stay deleted.
      legacy: style.getPropertyValue('--lazu-web-landing-action').trim(),
      localAccent: local?.getPropertyValue('--accent').trim() ?? '',
    };
  });

  expect(tokens.legacy).toBe('');
  expect(tokens.action.toLowerCase()).toBe('#202329');
  expect(tokens.localAccent.toLowerCase()).toBe('#202329');
});

test('landing sits on the flat G1 ground with no shader and vermilion only in the hero', async ({
  page,
}) => {
  await page.goto('/');
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();

  // The WebGL ground is gone: no shader canvas, no fixed ground layer.
  await expect(page.locator('.lazu-ground')).toHaveCount(0);
  await expect(page.locator('.lazu-landing canvas')).toHaveCount(0);

  const look = await page.evaluate(() => {
    const body = getComputedStyle(document.body);
    const em = document.querySelector('.lazu-landing__headline em');
    const eyebrow = document.querySelector(
      '.lazu-landing__hero .lazu-landing__eyebrow',
    );
    return {
      bodyColor: body.backgroundColor,
      bodyImage: body.backgroundImage,
      // The retired paper grain was a fixed body::before layer.
      grain: getComputedStyle(document.body, '::before').backgroundImage,
      em: em ? getComputedStyle(em).color : null,
      eyebrow: eyebrow ? getComputedStyle(eyebrow).color : null,
    };
  });
  expect(look.bodyColor).toBe('rgb(245, 245, 246)');
  expect(look.bodyImage).toBe('none');
  expect(look.grain).toBe('none');
  if (look.em !== null) expect(look.em).toBe('rgb(201, 69, 42)');
  if (look.eyebrow !== null) expect(look.eyebrow).toBe('rgb(184, 62, 36)');
});

test('cold English landing hydrates after delayed translations without errors', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.addInitScript(() => localStorage.setItem('i18nextLng', 'en'));
  await page.route('**/assets/en-*.js', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 500));
    await route.continue();
  });
  await page.goto('/', { waitUntil: 'networkidle' });
  await expect(page.locator('.lazu-landing__headline')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Change language' }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Change language' }).click();
  await expect(
    page.getByRole('button', { name: 'Change language' }),
  ).toHaveAttribute('aria-expanded', 'true');
  expect(errors).toEqual([]);
});

test('mobile marketing hydration completes without synchronous shell updates', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.addInitScript(() => localStorage.setItem('i18nextLng', 'en'));
  await page.goto('/', { waitUntil: 'networkidle' });
  await page.getByRole('button', { name: 'Change language' }).click();
  await expect(
    page.getByRole('button', { name: 'Change language' }),
  ).toHaveAttribute('aria-expanded', 'true');
  expect(errors).toEqual([]);
});
