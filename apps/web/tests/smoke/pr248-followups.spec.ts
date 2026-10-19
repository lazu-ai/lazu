import { expect, test, type Page, type Request } from '@playwright/test';
import { setupMockedApp } from './helpers/mock-app';
import {
  mockProjectsClient,
  mockSelfProjects,
} from '../../src/features/projects/mock';

const ok = (data: unknown) => ({ success: true, data });
async function setupProject(page: Page, shared: boolean) {
  const base = await mockProjectsClient.getProject('p_orbit');
  const summary = {
    ...mockSelfProjects().find((project) => project.id === 'p_orbit')!,
    is_default: true,
    upgraded_at: shared ? base.upgraded_at : null,
    name: 'Review project',
    role: 'owner' as const,
  };
  let detail = {
    ...base,
    name: summary.name,
    upgraded_at: summary.upgraded_at,
    settings: { ...base.settings, default_member_budget_microusd: 75_000_000 },
  };
  let releaseDetail!: () => void;
  const gate = new Promise<void>((resolve) => {
    releaseDetail = resolve;
  });
  let sent: Record<string, unknown> | undefined;
  const user = {
    id: 'u_ada',
    username: 'Ada',
    display_name: 'Ada',
    role: 1,
    features: { projects: true },
    projects: [summary],
  };
  await setupMockedApp(page, {
    language: 'en',
    user,
    consoleScope: 'workspace:p_orbit',
    payloadFor: async (raw: string, request: Request) => {
      const path = new URL(raw).pathname;
      if (path === '/api/user/self') return ok(user);
      if (path === '/api/project/p_orbit') {
        if (request.method() === 'PUT') {
          const body = request.postDataJSON() as { name: string };
          summary.name = body.name;
          detail = { ...detail, name: body.name, version: detail.version + 1 };
        }
        await gate;
        return ok(detail);
      }
      if (path.endsWith('/members'))
        return ok({
          items: await mockProjectsClient.listMembers('p_orbit'),
          total: 1,
          page: 1,
          page_size: 100,
        });
      if (path.endsWith('/invites') && request.method() === 'POST') {
        sent = request.postDataJSON() as Record<string, unknown>;
        return ok({
          id: 'review-invite',
          ...sent,
          status: 'pending',
          expires_at: 1800000000,
        });
      }
      if (/\/(invites|tokens|budget-requests)$/.test(path))
        return ok({ items: [], total: 0, page: 1, page_size: 100 });
      if (path.endsWith('/attention'))
        return ok({
          pending_requests: 0,
          pending_invites: 0,
          near_budget_members: 0,
          rejected_today: 0,
        });
      return undefined;
    },
  });
  return { releaseDetail, sent: () => sent };
}

test('solo invitation survives project rename', async ({ page }) => {
  const fixture = await setupProject(page, false);
  fixture.releaseDetail();
  await page.goto('/console/project/members');
  const email = page.locator('.pj-roster__email');
  await email.fill('teammate@example.test');
  await page
    .locator('.pj-roster__caps')
    .getByRole('button', { name: '$100', exact: true })
    .click();
  await page.locator('.pj-rename').click();
  await page.locator('.pj-rename__edit input').fill('Renamed project');
  await page.locator('.pj-rename__edit input').press('Enter');
  await expect(page.locator('.pj-rename')).toHaveText('Renamed project');
  await expect(email).toHaveValue('teammate@example.test');
  await expect(page.locator('.pj-roster__caps button[data-on]')).toHaveText(
    '$100',
  );
  await page
    .locator('.pj-roster__caps')
    .getByRole('button', { name: '$75', exact: true })
    .click();
  await page
    .locator('.pj-roster__invite')
    .getByRole('button', { name: 'Invite', exact: true })
    .click();
  await expect.poll(() => fixture.sent()?.budget_microusd).toBe(75_000_000);
  expect(fixture.sent()?.project_name).toBeUndefined();
});

test('shared invitation waits for settings and resets on reopening', async ({
  page,
}) => {
  const fixture = await setupProject(page, true);
  await page.goto('/console/project/members');
  await page
    .getByRole('button', { name: 'Invite member', exact: true })
    .click();
  await expect(page.locator('#pj-invite-email')).toBeDisabled();
  fixture.releaseDetail();
  await page.locator('#pj-invite-email').fill('teammate@example.test');
  await page.getByRole('button', { name: /Send invitation/ }).click();
  await expect.poll(() => fixture.sent()?.budget_microusd).toBe(75_000_000);
  await expect(page.locator('#pj-invite-email')).not.toBeVisible();
  await page
    .getByRole('button', { name: 'Invite member', exact: true })
    .click();
  await expect(page.locator('#pj-invite-email')).toHaveValue('');
});

test('desktop sign-out requires confirmation and supports cancel', async ({
  page,
}) => {
  let logouts = 0;
  await setupMockedApp(page, {
    language: 'en',
    payloadFor: (raw: string) => {
      if (new URL(raw).pathname === '/api/user/logout') {
        logouts++;
        return ok({});
      }
      return undefined;
    },
  });
  await page.goto('/console/models');
  await page.locator('.console-topbar__avatar').click();
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
  await expect(
    page.getByText('Sign out of this account?', { exact: true }),
  ).toBeVisible();
  expect(logouts).toBe(0);
  await expect(
    page.getByRole('button', { name: 'Cancel', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await page.getByRole('menuitem', { name: 'Sign out', exact: true }).click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect.poll(() => logouts).toBe(1);
  await expect(page).toHaveURL(/\/login/);
});

test('empty revealed password is masked after fast credential switching', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem('i18nextLng', 'en'));
  await page.route('**/api/**', (route) =>
    route.fulfill({
      json: ok({
        setup: true,
        enabled: false,
        email_code_auth: true,
        password_login: true,
        providers: [],
      }),
    }),
  );
  await page.goto('/login');
  await page.getByRole('tab', { name: 'Password', exact: true }).click();
  await page
    .getByRole('button', { name: 'Show password', exact: true })
    .click();
  await expect(page.locator('#auth-password')).toHaveAttribute('type', 'text');
  await page
    .getByRole('tab', { name: 'Email code', exact: true })
    .evaluate((element) => (element as HTMLElement).click());
  await page
    .getByRole('tab', { name: 'Password', exact: true })
    .evaluate((element) => (element as HTMLElement).click());
  await expect(page.locator('#auth-password')).toHaveAttribute(
    'type',
    'password',
  );
});
