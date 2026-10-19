import { expect, test, type Page, type Request } from '@playwright/test';
import { fail as failWith, setupMockedApp } from './helpers/mock-app';
import {
  mockProjectsClient,
  mockSelfProjects,
} from '../../src/features/projects/mock';
import type {
  ProjectDetail,
  ProjectMember,
  ProjectToken,
  ProjectSummary,
} from '../../src/features/projects/types';

const ok = (data: unknown) => ({ success: true, data });
let detail: ProjectDetail;
let members: ProjectMember[];
let keys: ProjectToken[];
let projects: ProjectSummary[];
test.beforeAll(async () => {
  detail = await mockProjectsClient.getProject('p_orbit');
  members = await mockProjectsClient.listMembers('p_orbit');
  keys = await mockProjectsClient.listProjectTokens('p_orbit');
  projects = [
    ...mockSelfProjects(),
    {
      ...mockSelfProjects()[0],
      id: 'p_second',
      name: 'Second project',
      is_default: false,
    },
  ];
});
async function setup(
  page: Page,
  override?: (url: URL, request: Request) => unknown,
  status = 'active',
  role: ProjectSummary['role'] = 'owner',
  siteRole = 1,
) {
  const summaries = projects.map((p) => ({ ...p, status, role }));
  const user = {
    id: 'u_ada',
    username: 'Ada',
    display_name: 'Ada',
    role: siteRole,
    features: { projects: true },
    projects: summaries,
  };
  return setupMockedApp(page, {
    language: 'en',
    user,
    consoleScope: 'workspace:p_orbit',
    payloadFor: (raw: string, request: Request) => {
      const url = new URL(raw);
      const custom = override?.(url, request);
      if (custom !== undefined) return custom;
      if (url.pathname === '/api/user/payments/email-document')
        return ok({
          id: 'email-document',
          project_id: 'p_orbit',
          project_name: 'Orbit',
          paid_at: 1789000000,
          created_at: 1788999000,
          paid_amount_microusd: 5000000,
          credited_microusd: 5000000,
          payment_status: 'paid',
          payment_method: 'stripe',
          invoice_status: 'ready',
          invoice_number: 'INV-2026-001',
          invoice_ready: true,
          receipt_ready: true,
        });
      if (url.pathname === '/api/user/self') return ok(user);
      if (url.pathname === '/api/project/p_orbit')
        return ok({
          ...detail,
          status,
          settings: {
            ...detail.settings,
            default_member_budget_microusd: null,
          },
        });
      if (url.pathname.endsWith('/members'))
        return ok({
          items: members,
          total: members.length,
          page: 1,
          page_size: 100,
        });
      if (/\/members\/[^/]+$/.test(url.pathname)) {
        const member = members.find(
          (m) => m.user_id === url.pathname.split('/').pop(),
        );
        return ok({
          ...member,
          keys: keys.filter((k) => k.owner?.id === member?.user_id),
          budget_history: [],
          usage_7d: [],
        });
      }
      if (
        url.pathname.endsWith('/invites') ||
        url.pathname.endsWith('/budget-requests')
      )
        return ok({ items: [], total: 0, page: 1, page_size: 100 });
      if (url.pathname.endsWith('/tokens') || url.pathname === '/api/token/')
        return ok({ items: keys, total: keys.length, page: 1, page_size: 100 });
      if (url.pathname.endsWith('/attention'))
        return ok({
          pending_requests: 0,
          pending_invites: 0,
          near_budget_members: 0,
          rejected_today: 0,
          rejections: { items: [], total: 0, page: 1, page_size: 20 },
          balance_days_left: null,
        });
      if (url.pathname.endsWith('/my-budget'))
        return ok({
          budget_microusd: 100000000,
          spent_microusd: 9000000,
          remaining_microusd: 91000000,
          daily_avg_microusd: 1000000,
          resets_at: 1800000000,
          warning_pct: 80,
          version: 1,
          project_cap: null,
          balance_days_left: null,
          history: [],
        });
      if (url.pathname.endsWith('/wallet'))
        return ok({
          balance_microusd: 100000000,
          daily_avg_microusd: 0,
          days_left: null,
          capabilities: { can_topup: true, can_manage_billing: true },
        });
      if (url.pathname.endsWith('/topups'))
        return ok({ items: [], total: 0, page: 1, page_size: 20 });
      if (url.pathname.endsWith('/auto-reload')) return ok(autoReloadOff);
      if (url.pathname === '/api/user/models') return ok(['gpt-6-astra']);
      if (url.pathname === '/api/pricing')
        return {
          ...ok([
            {
              model_name: 'gpt-6-astra',
              vendor_id: 2,
              supported_endpoint_types: ['openai', 'openai-response'],
              modality_output: ['text'],
            },
          ]),
          vendors: [{ id: 2, name: 'OpenAI' }],
        };
      return undefined;
    },
  });
}

/* The owner's auto-reload before it has ever been touched: off, no card. */
const autoReloadOff = {
  configured: false,
  enabled: false,
  threshold_usd: 0,
  amount_usd: 0,
  monthly_cap_usd: 0,
  card: null,
  setup_pending: false,
  paused_reason: '',
  paused_at: 0,
  last_failure: '',
  last_failed_at: 0,
  last_run_at: 0,
  last_charge_at: 0,
  last_reload: null,
  month_spent_usd: 0,
  min_amount_usd: 5,
  max_amount_usd: 10000,
};

test('root project switch survives the platform home and key navigation', async ({
  page,
}) => {
  await setup(
    page,
    (url) =>
      url.pathname === '/api/project/p_second'
        ? ok({ ...detail, id: 'p_second', name: 'Second project' })
        : undefined,
    'active',
    'owner',
    100,
  );
  await page.goto('/console/token');
  await page.locator('button.pj-space, button.pj-crumb').click();
  await page
    .locator('[data-project-item]')
    .filter({ hasText: 'Second project' })
    .click();
  await expect(page).toHaveURL(/\/console$/);
  await expect(page.locator('button.pj-space, button.pj-crumb')).toContainText(
    'Second project',
  );
  await expect(page.locator('.ov-page')).toBeVisible();
  await page.locator('.sidebar-v2__item[href="/console/token"]').click();
  await expect(page.locator('button.pj-space, button.pj-crumb')).toContainText(
    'Second project',
  );
  await expect
    .poll(() =>
      page.evaluate(() => localStorage.getItem('lazu:console-scope:u_ada')),
    )
    .toBe('workspace:p_second');
});

test('settings rename failure keeps the dialog and its draft open', async ({
  page,
}) => {
  let attempts = 0;
  await setup(page, (url, request) => {
    if (url.pathname === '/api/project/p_orbit' && request.method() === 'PUT') {
      attempts++;
      return failWith(409, 'version_conflict', 'conflict_error');
    }
  });
  await page.goto('/console/project/settings');
  await page.getByRole('button', { name: 'Change', exact: true }).click();
  const dialog = page.getByRole('dialog');
  const name = dialog.getByRole('textbox', { name: 'Name', exact: true });
  await name.fill('Unsaved name');
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => attempts).toBe(1);
  await expect(dialog).toBeVisible();
  await expect(name).toHaveValue('Unsaved name');
  await expect(dialog.getByRole('alert')).toBeVisible();
  await expect(page).toHaveURL(/project\/settings/);
});

test('Key edit preserves full policy and updates monthly cap', async ({
  page,
}) => {
  let update: Record<string, unknown> | undefined;
  await setup(page, (url, request) => {
    if (url.pathname === '/api/token/' && request.method() === 'PUT') {
      update = request.postDataJSON() as Record<string, unknown>;
      expect(url.searchParams.has('policy_only')).toBe(false);
      return ok({ ...keys[0], name: update.name });
    }
    if (url.pathname === `/api/token/${keys[0].id}`) {
      expect(url.searchParams.get('project_id')).toBe('p_orbit');
      return ok({
        ...keys[0],
        cap_microusd: 20000000,
        expired_time: 1893456000,
        model_limits_enabled: true,
        model_limits: [],
        model_scope_vendors: [2],
        usage_alert_percent: 80,
        rate_limits_enabled: true,
        rpm_limit: 7,
        ip_whitelist_enabled: true,
        allow_ips: '127.0.0.1',
        search_limits_enabled: true,
        search_providers: ['tavily'],
        note: 'original note',
      });
    }
  });
  await page.goto('/console/token');
  await page
    .getByRole('button', { name: `More for ${keys[0].name}`, exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Edit', exact: true }).click();
  await page.locator('#token-name').fill('Updated key');
  await expect(page.locator('#token-quota')).toHaveValue('20');
  await page.locator('#token-quota').fill('30');
  await page
    .getByRole('button', { name: 'More settings', exact: true })
    .click();
  await page
    .getByPlaceholder('e.g. Claude Code on my Mac')
    .fill('updated note');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Save changes', exact: true })
    .click();
  await expect.poll(() => update?.name).toBe('Updated key');
  expect(update).toMatchObject({
    project_id: 'p_orbit',
    expected_version: keys[0].version,
    cap_microusd: 30000000,
    expired_time: 1893456000,
    model_limits_enabled: true,
    model_scope_vendors: ['2'],
    usage_alert_percent: 80,
    rpm_limit: 7,
    allow_ips: '127.0.0.1',
    search_providers: ['tavily'],
    note: 'updated note',
  });
  await expect(page.locator('.pj-bars')).toHaveCount(0);
});

test('Stripe return lands in the wallet and pins the payment record', async ({
  page,
}) => {
  let polled = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/top-review') {
      polled++;
      return ok({
        id: 'top-review',
        project_id: 'p_orbit',
        project_name: '',
        project_shared: false,
        paid_at: 1800000000,
        created_at: 1799999900,
        paid_amount_microusd: 10000000,
        credited_microusd: 10000000,
        payment_status: 'paid',
        payment_method: 'wechat_pay',
        invoice_status: 'ready',
        invoice_number: 'INV-1',
        invoice_ready: true,
        receipt_ready: true,
      });
    }
  });
  await page.goto('/payment/success?project_id=p_orbit&top_up_id=top-review');
  await expect(page).toHaveURL(/\/console\/wallet\?.*top_up_id=top-review/);
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Added to your Lazu account');
  await expect(record).toContainText('WeChat Pay');
  await expect(record).toContainText('INV-1');
  await expect(
    record.getByRole('link', { name: 'View receipt', exact: true }),
  ).toHaveAttribute('href', /document=receipt/);
  await expect(
    record.getByRole('link', { name: 'View on Stripe', exact: true }),
  ).toHaveAttribute('href', /document=invoice_page/);
  await expect(page.locator('.pj-wallet-page')).toBeVisible();
  await page.waitForTimeout(3_500);
  expect(polled).toBe(1);
});

test('suspended manager has no project management or Key requests', async ({
  page,
}) => {
  const forbidden: string[] = [];
  await setup(
    page,
    (url) => {
      if (
        url.pathname.endsWith('/wallet') ||
        url.pathname.endsWith('/tokens') ||
        url.pathname.endsWith('/attention')
      )
        forbidden.push(url.pathname);
    },
    'suspended',
  );
  await page.goto('/console');
  await expect(page.locator('.pj-mybudget-page')).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Members & budgets', exact: true }),
  ).toHaveCount(0);
  expect(forbidden).toEqual([]);
});

test('invitation preserves an unlimited project default', async ({ page }) => {
  let sent: Record<string, unknown> | undefined;
  await setup(page, (url, request) => {
    if (url.pathname.endsWith('/invites') && request.method() === 'POST') {
      sent = request.postDataJSON() as Record<string, unknown>;
      return ok({
        id: 'invite-review',
        ...sent,
        status: 'pending',
        expires_at: 1800000000,
      });
    }
  });
  await page.goto('/console/project/members');
  await page
    .getByRole('button', { name: 'Invite member', exact: true })
    .click();
  await page.locator('#pj-invite-email').fill('review@example.test');
  await page
    .getByRole('button', {
      name: 'Send invitation · valid 3 days',
      exact: true,
    })
    .click();
  await expect.poll(() => sent?.budget_microusd).toBe(null);
});

test('removal always stops the member Keys immediately', async ({ page }) => {
  let removed = false;
  await setup(page, (url, request) => {
    if (
      url.pathname.endsWith('/members/u_noor') &&
      request.method() === 'DELETE'
    ) {
      removed = true;
      return ok({
        removal_id: 'removal-review',
        undo_until: 1800000000,
        affected_keys: [],
        keys_disabled: 0,
      });
    }
  });
  await page.goto('/console/project/members');
  await page
    .getByRole('row')
    .filter({ hasText: 'Noor' })
    .getByRole('button', { name: 'More for Noor', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Remove…', exact: true }).click();
  const dialog = page.getByRole('dialog');
  // There is no leave policy any more: the Keys always stop at once and the
  // gate says so, with no 24 h grace wording anywhere in the dialog.
  await expect(dialog).toContainText('Stops immediately');
  await expect(dialog).toContainText('stop responding immediately');
  await expect(dialog).not.toContainText('24 h');
  await dialog.getByRole('checkbox').check();
  await dialog
    .getByRole('button', { name: 'Remove Noor', exact: true })
    .click();
  await expect.poll(() => removed).toBe(true);
  await page.screenshot({
    path: '../../.local-results/screenshots/projects-review-removal.png',
    animations: 'disabled',
    fullPage: true,
  });
});

test('a Key stopped by removal offers only delete', async ({ page }) => {
  const stopped: ProjectToken = {
    ...keys[0],
    id: 't_gone',
    name: 'left-behind',
    status: 'disabled',
    stopped_by_removal: true,
  };
  await setup(page, (url) => {
    if (url.pathname.endsWith('/tokens') || url.pathname === '/api/token/')
      return ok({
        items: [stopped, ...keys],
        total: keys.length + 1,
        page: 1,
        page_size: 100,
      });
  });
  await page.goto('/console/token');
  const row = page.getByRole('row').filter({ hasText: 'left-behind' });
  await expect(row).toContainText('Stopped when they left');
  await expect(row).toHaveAttribute('data-muted', 'true');
  await row
    .getByRole('button', { name: 'More for left-behind', exact: true })
    .click();
  await expect(
    page.getByRole('menuitem', { name: 'Delete', exact: true }),
  ).toBeVisible();
  for (const label of ['Enable', 'Disable', 'Edit', 'Rotate', 'Show']) {
    await expect(
      page.getByRole('menuitem', { name: label, exact: true }),
    ).toHaveCount(0);
  }
});

test('member edit dialog saves budget, allowed models and effort cap together', async ({
  page,
}) => {
  let body: Record<string, unknown> | null = null;
  await setup(page, (url, request) => {
    if (
      url.pathname.endsWith('/members/u_noor/budget') &&
      request.method() === 'PUT'
    ) {
      body = request.postDataJSON() as Record<string, unknown>;
      const noor = members.find((m) => m.user_id === 'u_noor')!;
      return ok({
        member: { ...noor, ...body, version: noor.version + 1 },
        allocation: detail.allocation,
      });
    }
  });
  await page.goto('/console/project/members');
  await page
    .getByRole('row')
    .filter({ hasText: 'Noor' })
    .getByRole('button', { name: 'Edit', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('Edit Noor');
  await dialog.locator('.pj-budget-picker input').fill('77');
  await dialog
    .getByRole('radiogroup', { name: 'Thinking depth cap' })
    .getByRole('radio', { name: 'low', exact: true })
    .click();
  await dialog.getByRole('button', { name: 'Allowed models' }).click();
  await page.locator('.pj-medit__opt').first().click();
  await page.keyboard.press('Escape');
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => body).not.toBeNull();
  expect(body).toMatchObject({
    budget_microusd: 77_000_000,
    reasoning_effort_cap: 'low',
    allowed_models: ['gpt-6-astra'],
  });
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('old member detail links open the edit dialog and refuse an empty budget', async ({
  page,
}) => {
  await setup(page);
  await page.goto('/console/project/members/u_noor');
  await expect(page).toHaveURL(/project\/members\?edit=u_noor/);
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('Edit Noor');
  await dialog.locator('.pj-budget-picker input').fill('');
  await expect(
    dialog.getByRole('button', { name: 'Save', exact: true }),
  ).toBeDisabled();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page).toHaveURL(/project\/members$/);
});

test('personal wallet uses real Key inventory and requested month aggregates', async ({
  page,
}) => {
  let range: URLSearchParams | undefined;
  const own = {
    ...projects.find((p) => p.id === 'p_orbit')!,
    is_default: true,
    upgraded_at: null,
    member_count: 1,
  };
  await setup(page, (url) => {
    if (url.pathname === '/api/user/self')
      return ok({
        id: 'u_ada',
        username: 'Ada',
        role: 1,
        features: { projects: true },
        projects: [own],
      });
    if (url.pathname.endsWith('/wallet')) {
      if (url.searchParams.has('start_timestamp')) range = url.searchParams;
      return ok({
        balance_microusd: 100000000,
        month_spend_microusd: 99000000,
        month_topup_microusd: 88000000,
        period_spend_microusd: 12000000,
        period_topup_microusd: 23000000,
        daily_avg_microusd: 0,
        days_left: null,
        capabilities: { can_topup: true, can_manage_billing: true },
      });
    }
    if (url.pathname === '/api/token/')
      return ok({ items: keys.slice(0, 3), total: 3, page: 1, page_size: 100 });
  });
  await page.goto('/console/wallet');
  await expect(page.locator('.pj-ledger')).toContainText('3 Key');
  await page.getByRole('tab', { name: 'Last month', exact: true }).click();
  await expect.poll(() => Boolean(range)).toBe(true);
  expect(Number(range!.get('end_timestamp'))).toBeGreaterThan(
    Number(range!.get('start_timestamp')),
  );
  await expect(page.locator('.pj-ledger')).toContainText('$12.00');
  await expect(page.locator('.pj-ledger')).toContainText('$23.00');
  await expect(page.locator('.pj-ledger')).not.toContainText('$99.00');
});

test('each created project Key opens the shared connection dialog with its own secret', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  let created = 0;
  await setup(page, (url, request) => {
    if (url.pathname === '/api/token/' && request.method() === 'POST') {
      created++;
      return ok({
        token: { ...keys[0], id: `new-${created}`, name: `key-${created}` },
        key: `fake-review-secret-${created}`,
      });
    }
  });
  await page.goto('/console/token');
  for (const n of [1, 2]) {
    if (n === 2) await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole('button', { name: /^Create key$/i }).click();
    await page.locator('#token-name').fill(`key-${n}`);
    await page
      .getByRole('dialog')
      .getByRole('button', { name: /^Create key$/i })
      .click();
    const connect = page.locator('.tok-connect');
    await expect(connect).toBeVisible();
    await page.screenshot({
      path: `../../.local-results/screenshots/team-management-latest/connect-${n === 1 ? 'desktop' : 'mobile'}.png`,
      fullPage: true,
    });
    await connect
      .getByRole('button', { name: 'Copy Key', exact: true })
      .click();
    await expect
      .poll(() => page.evaluate(() => navigator.clipboard.readText()))
      .toBe(`fake-review-secret-${n}`);
    await connect
      .getByRole('button', { name: 'Close', exact: true })
      .last()
      .click();
    await expect(connect).toHaveCount(0);
    await expect(page.locator('.pj-created')).toHaveCount(0);
  }
});

test('payment record keeps confirming until Stripe reports the order paid', async ({
  page,
}) => {
  let polls = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/delayed-review') {
      polls++;
      const paid = polls > 1;
      return ok({
        id: 'delayed-review',
        project_id: 'p_orbit',
        project_name: 'Orbit',
        project_shared: true,
        paid_at: paid ? 1800000000 : 0,
        created_at: 1799999900,
        paid_amount_microusd: paid ? 25000000 : 0,
        order_amount_microusd: 25000000,
        credited_microusd: 25000000,
        payment_status: paid ? 'paid' : 'pending',
        payment_method: paid ? 'stripe' : '',
        invoice_status: 'legacy',
        invoice_number: '',
        invoice_ready: false,
        receipt_ready: paid,
      });
    }
  });
  await page.goto(
    '/payment/success?project_id=p_orbit&top_up_id=delayed-review',
  );
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Confirming this payment with Stripe');
  await expect(record).toContainText('$25.00');
  await expect(record).toContainText('Added to Orbit', { timeout: 10_000 });
  await expect(record).toContainText('This top-up has no Stripe invoice');
});

test('payment record never names the personal project', async ({ page }) => {
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/personal-return')
      return ok({
        id: 'personal-return',
        project_id: 'p_orbit',
        project_name: '',
        project_shared: false,
        paid_at: 1800000000,
        created_at: 1799999900,
        paid_amount_microusd: 5000000,
        credited_microusd: 5000000,
        payment_status: 'paid',
        payment_method: 'stripe',
        invoice_status: 'pending',
        invoice_number: '',
        invoice_ready: false,
        receipt_ready: true,
      });
  });
  await page.goto(
    '/payment/success?project_id=p_orbit&top_up_id=personal-return',
  );
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Added to your Lazu account');
  await expect(record).not.toContainText('p_orbit');
  await expect(record).not.toContainText('grace-1');
  await expect(record).toContainText('Stripe is preparing it');
  await expect(
    record.getByRole('link', { name: 'View on Stripe', exact: true }),
  ).toHaveAttribute('aria-disabled', 'true');
});

/* The console-review geometry rule used to re-add a border, a surface and the
 * 38px large control height to the input inside `.pj-search`, so the Keys and
 * Members search boxes rendered as two nested borders with the inner field
 * taller than its box. Measure the computed styles instead of trusting the
 * stylesheet order. */
for (const [label, path] of [
  ['Keys', '/console/token'],
  ['Members', '/console/project/members'],
] as const) {
  test(`${label} search box renders as one 34px control`, async ({ page }) => {
    await setup(page);
    await page.goto(path);
    const box = page.locator('.pj-search').first();
    const input = box.locator('.lazu-input');
    await expect(input).toBeVisible();
    const geometry = await input.evaluate((el) => {
      const inner = getComputedStyle(el);
      const wrap = el.parentElement!;
      const outer = getComputedStyle(wrap);
      const a = el.getBoundingClientRect();
      const b = wrap.getBoundingClientRect();
      return {
        innerBorder: inner.borderTopWidth,
        innerBackground: inner.backgroundColor,
        innerBoxShadow: inner.boxShadow,
        innerHeight: a.height,
        outerBorder: outer.borderTopWidth,
        outerHeight: b.height,
        inside: a.top >= b.top && a.bottom <= b.bottom,
      };
    });
    expect(geometry.innerBorder).toBe('0px');
    expect(geometry.innerBackground).toBe('rgba(0, 0, 0, 0)');
    expect(geometry.innerBoxShadow).toBe('none');
    expect(geometry.outerBorder).toBe('1px');
    expect(geometry.outerHeight).toBe(34);
    expect(geometry.innerHeight).toBeLessThanOrEqual(32);
    expect(geometry.inside).toBe(true);
    await page.screenshot({
      path: `.local-results/screenshots/snapshots/pj-search-${label.toLowerCase()}.png`,
      clip: { x: 0, y: 0, width: 1280, height: 420 },
    });
  });
}

test('payment return verifies the order with the rollout switch disabled', async ({
  page,
}) => {
  let polls = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/self')
      return ok({
        id: 'u_ada',
        username: 'Ada',
        role: 1,
        features: { projects: false },
        projects: [],
      });
    if (url.pathname === '/api/user/payments/flag-off') {
      polls++;
      return ok({
        id: 'flag-off',
        project_id: 'p_orbit',
        project_name: '',
        project_shared: false,
        paid_at: 0,
        created_at: 1799999900,
        paid_amount_microusd: 0,
        order_amount_microusd: 5000000,
        credited_microusd: 5000000,
        payment_status: 'pending',
        payment_method: '',
        invoice_status: 'pending',
        invoice_number: '',
        invoice_ready: false,
        receipt_ready: false,
      });
    }
  });
  await page.goto('/payment/success?project_id=p_orbit&top_up_id=flag-off');
  await expect(page.locator('.wallet-page')).toBeVisible();
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Confirming this payment with Stripe');
  await expect(record).toContainText('$5.00');
  await expect(record).not.toContainText('Added to');
  await expect.poll(() => polls, { timeout: 10_000 }).toBeGreaterThan(1);
});

test('fully discounted payment stops polling when the invoice is ready', async ({
  page,
}) => {
  let polls = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/free-return') {
      polls++;
      return ok({
        id: 'free-return',
        project_id: 'p_orbit',
        project_name: '',
        project_shared: false,
        paid_at: 1700000000,
        created_at: 1699999900,
        paid_amount_microusd: 0,
        credited_microusd: 5000000,
        payment_status: 'paid',
        payment_method: 'promotion',
        invoice_status: 'ready',
        invoice_number: 'INV-FREE',
        invoice_ready: true,
        receipt_ready: false,
      });
    }
  });
  await page.goto('/payment/success?project_id=p_orbit&top_up_id=free-return');
  const record = page.locator('.lazu-topup-record');
  await expect(
    record.getByRole('link', { name: 'View on Stripe', exact: true }),
  ).not.toHaveAttribute('aria-disabled', 'true');
  await expect(record).toContainText('Stripe issues no receipt');
  await expect.poll(() => polls).toBe(1);
  await page.waitForTimeout(3_500);
  expect(polls).toBe(1);
});

test('project settings save the new-member effort cap on click', async ({
  page,
}) => {
  let settings: Record<string, unknown> | null = null;
  await setup(page, (url, request) => {
    if (url.pathname === '/api/project/p_orbit' && request.method() === 'PUT') {
      settings = (
        request.postDataJSON() as { settings: Record<string, unknown> }
      ).settings;
      return ok({ ...detail, version: detail.version + 1 });
    }
  });
  await page.goto('/console/project/settings');
  await page
    .getByRole('radiogroup', { name: 'Thinking depth cap' })
    .getByRole('radio', { name: 'medium', exact: true })
    .click();
  await expect
    .poll(() => settings)
    .toEqual({
      default_reasoning_effort_cap: 'medium',
    });
});

test('project member log requests retain the member filter', async ({
  page,
}) => {
  const requests: URL[] = [];
  await setup(page, (url) => {
    if (url.pathname === '/api/log/self/') requests.push(url);
  });
  await page.goto('/console/usage?tab=logs&project_id=p_orbit&user_id=u_noor');
  await expect.poll(() => requests.length).toBeGreaterThan(0);
  expect(requests[0].searchParams.get('user_id')).toBe('u_noor');
  expect(requests[0].searchParams.get('project_id')).toBe('p_orbit');
});

for (const [route, suffix, empty] of [
  ['/console/project/members', '/members', 'No matching members'],
  ['/console/token', '/tokens', 'No matching Keys'],
]) {
  test(`failed project read is retryable on ${route}`, async ({ page }) => {
    let fail = true;
    await setup(page, (url) =>
      fail && url.pathname.endsWith(suffix)
        ? failWith(500, 'internal_error')
        : undefined,
    );
    await page.goto(route);
    await expect(page.getByRole('alert')).toContainText(
      'Unable to load project data',
    );
    await expect(page.getByText(empty, { exact: true })).toHaveCount(0);
    fail = false;
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(page.getByRole('alert')).toHaveCount(0);
    await expect(page.locator('table')).toBeVisible();
  });
}

test('accepted invitation survives failed session refresh and retries without accepting again', async ({
  page,
}) => {
  let accepts = 0;
  let failRefresh = false;
  await setup(page, (url, request) => {
    if (url.pathname === '/api/invite/review')
      return ok({
        project_name: 'Orbit',
        inviter: { id: 'u_ada', username: 'Ada' },
        role: 'member',
        budget_microusd: 100000000,
        expires_at: 2000000000,
        status: 'pending',
      });
    if (
      url.pathname === '/api/invite/review/accept' &&
      request.method() === 'POST'
    ) {
      accepts++;
      return ok({ project_id: 'p_orbit', role: 'member' });
    }
    if (url.pathname === '/api/user/self' && failRefresh)
      return failWith(500, 'internal_error');
  });
  await page.goto('/invite/review');
  await page
    .getByRole('button', { name: 'Accept invitation', exact: true })
    .click();
  await expect(page.getByRole('heading')).toContainText(
    'You have joined “Orbit”',
  );
  failRefresh = true;
  await page.getByRole('button', { name: 'Open “Orbit”', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('You have joined');
  await expect(page.getByRole('heading')).toContainText(
    'You have joined “Orbit”',
  );
  failRefresh = false;
  await page.getByRole('button', { name: 'Open “Orbit”', exact: true }).click();
  await expect(page).toHaveURL(/\/console$/);
  expect(accepts).toBe(1);
});

test('unavailable accepted project does not leave the open button spinning', async ({
  page,
}) => {
  await setup(page, (url) => {
    if (url.pathname === '/api/invite/review')
      return ok({
        project_name: 'Removed project',
        inviter: { id: 'u_ada', username: 'Ada' },
        role: 'member',
        budget_microusd: 100000000,
        expires_at: 2000000000,
        status: 'accepted',
      });
    if (url.pathname === '/api/invite/review/accept')
      return ok({ project_id: 'p_removed', role: 'member' });
  });
  await page.goto('/invite/review');
  await page
    .getByRole('button', { name: 'Open “Removed project”', exact: true })
    .click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Open “Removed project”', exact: true }),
  ).toBeEnabled();
});

test('wallet deep link selects the credited project and removes the one-time selector', async ({
  page,
}) => {
  await setup(page, (url) =>
    url.pathname === '/api/project/p_second'
      ? ok({ ...detail, id: 'p_second', name: 'Second project' })
      : undefined,
  );
  await page.goto('/console/wallet?project_id=p_second');
  await expect(page).toHaveURL(/\/console\/wallet$/);
  await expect(page.locator('button.pj-space, button.pj-crumb')).toContainText(
    'Second project',
  );
});

test('unavailable wallet deep link does not show the selected project balance', async ({
  page,
}) => {
  await setup(page);
  await page.goto('/console/wallet?project_id=p_missing');
  await expect(page.getByRole('alert')).toContainText(
    'Unable to open this project wallet',
  );
  await expect(page.locator('.pj-wallet-page')).toHaveCount(0);
});

test('existing project Key connects using a scoped reveal and a connect-only write', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  let revealProject = '';
  let saved: Record<string, unknown> | undefined;
  await setup(page, (url, request) => {
    if (url.pathname.endsWith('/secret')) {
      revealProject = url.searchParams.get('project_id') || '';
      return ok({ key: 'fake-project-connect', notification_status: 'queued' });
    }
    if (url.pathname === '/api/token/' && request.method() === 'PUT') {
      expect(url.searchParams.get('connect_only')).toBe('true');
      saved = request.postDataJSON();
      return ok({ ...keys[0], ...saved, version: keys[0].version + 1 });
    }
  });
  await page.goto('/console/token');
  await page
    .getByRole('button', { name: 'Connect', exact: true })
    .first()
    .click();
  const connect = page.locator('.tok-connect');
  await expect(connect).toBeVisible();
  await expect.poll(() => revealProject).toBe('p_orbit');
  await connect.getByRole('button', { name: 'Codex CLI', exact: true }).click();
  await connect
    .getByRole('button', { name: 'Copy and finish', exact: true })
    .click();
  await expect.poll(() => saved?.product).toBe('codex');
  expect(saved?.project_id).toBe('p_orbit');
  expect(saved?.expected_version).toBeDefined();
  expect(saved).not.toHaveProperty('cap_microusd');
  expect(saved).not.toHaveProperty('remain_quota');
});

test('project Key connection without reveal permission copies configuration without secrets or writes', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  let reveals = 0;
  let writes = 0;
  await setup(page, (url, request) => {
    if (url.pathname.endsWith('/tokens'))
      return ok({
        items: [
          {
            ...keys[0],
            capabilities: {
              ...keys[0].capabilities,
              can_reveal: false,
              can_update: false,
            },
          },
        ],
        total: 1,
        page: 1,
        page_size: 100,
      });
    if (url.pathname.endsWith('/secret')) {
      reveals++;
      return failWith(403, 'access_denied', 'permission_error');
    }
    if (url.pathname === '/api/token/' && request.method() === 'PUT') {
      writes++;
      return failWith(403, 'access_denied', 'permission_error');
    }
  });
  await page.goto('/console/token');
  await page.getByRole('button', { name: 'Connect', exact: true }).click();
  const dialog = page.locator('.tok-connect');
  await dialog.getByRole('button', { name: 'Codex CLI', exact: true }).click();
  await expect(
    dialog.getByRole('button', { name: 'Copy Key', exact: true }),
  ).toBeDisabled();
  await dialog
    .getByRole('button', { name: 'Copy and finish', exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(reveals).toBe(0);
  expect(writes).toBe(0);
});

test('mobile project members keep the invite action reachable', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setup(page);
  await page.goto('/console/project/members');
  await page
    .getByRole('button', { name: 'Invite member', exact: true })
    .click();
  await expect(page.locator('#pj-invite-email')).toBeVisible();
});

test('member Key budget read failures can be retried', async ({ page }) => {
  let fail = true;
  await setup(
    page,
    (url) =>
      fail && url.pathname.endsWith('/my-budget')
        ? failWith(500, 'internal_error')
        : undefined,
    'active',
    'member',
  );
  await page.goto('/console/token');
  await expect(page.getByRole('alert')).toContainText(
    'Unable to load project data',
  );
  await expect(page.getByRole('button', { name: /^Create key$/i })).toHaveCount(
    0,
  );
  fail = false;
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveCount(0);
  await expect(
    page.getByRole('button', { name: /^Create key$/i }),
  ).toBeVisible();
});

test('background member read failure and retry preserve the dirty budget', async ({
  page,
  context,
}) => {
  let fail = false;
  await page.clock.install();
  await setup(page, (url) =>
    fail && url.pathname.endsWith('/members')
      ? failWith(500, 'internal_error')
      : undefined,
  );
  await page.goto('/console/project/members');
  await page
    .getByRole('row')
    .filter({ hasText: 'Noor' })
    .getByRole('button', { name: 'Edit', exact: true })
    .click();
  const amount = page.getByRole('dialog').locator('.pj-budget-picker input');
  await amount.fill('77');
  fail = true;
  await page.clock.fastForward(31_000);
  await context.setOffline(true);
  await expect.poll(() => page.evaluate(() => navigator.onLine)).toBe(false);
  await context.setOffline(false);
  await expect(amount).toHaveValue('77');
  fail = false;
  await page.clock.fastForward(31_000);
  await expect(amount).toHaveValue('77');
});

for (const code of ['invite_email_mismatch', 'state_conflict']) {
  test(`accepted link preserves ${code} instead of claiming membership`, async ({
    page,
  }) => {
    await setup(page, (url) => {
      if (url.pathname === '/api/invite/review')
        return ok({
          project_name: 'Orbit',
          inviter: { id: 'u_ada', username: 'Ada' },
          email: 'invitee@example.com',
          role: 'member',
          budget_microusd: 100000000,
          expires_at: 2000000000,
          status: 'accepted',
        });
      if (url.pathname === '/api/invite/review/accept')
        return code === 'state_conflict'
          ? failWith(409, code, 'conflict_error')
          : failWith(403, code, 'permission_error');
    });
    await page.goto('/invite/review');
    await expect(page.getByRole('heading')).toHaveText(
      'This invitation has been accepted',
    );
    await expect(
      page.getByRole('heading', {
        name: 'You have joined “Orbit”',
        exact: true,
      }),
    ).toHaveCount(0);
    await page
      .getByRole('button', { name: 'Open “Orbit”', exact: true })
      .click();
    if (code === 'invite_email_mismatch') {
      // A wrong mailbox gets its own page naming both addresses.
      await expect(
        page.getByRole('heading', {
          name: 'This invitation went to another address',
        }),
      ).toBeVisible();
      await expect(page.getByText('invitee@example.com').first()).toBeVisible();
    } else {
      await expect(page.getByRole('alert')).toBeVisible();
    }
    await expect(
      page.getByRole('heading', {
        name: 'You have joined “Orbit”',
        exact: true,
      }),
    ).toHaveCount(0);
  });
}

test('new Key stays copyable when the connection catalog fails', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  let created = false;
  await setup(page, (url, request) => {
    if (created && url.pathname === '/api/pricing')
      return failWith(500, 'internal_error');
    if (url.pathname === '/api/token/' && request.method() === 'POST') {
      created = true;
      return ok({
        token: { ...keys[0], id: 'new-offline', name: 'Offline catalog' },
        key: 'fake-offline-catalog-secret',
      });
    }
  });
  await page.goto('/console/token');
  await page.getByRole('button', { name: /^Create key$/i }).click();
  await page.locator('#token-name').fill('Offline catalog');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: /^Create key$/i })
    .click();
  await expect(page.getByRole('dialog').getByRole('alert')).toBeVisible();
  await expect(
    page.getByText('fake-offline-catalog-secret', { exact: true }),
  ).toBeVisible();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Copy', exact: true })
    .click();
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toBe('fake-offline-catalog-secret');
});

test('flag-off failed payment lands in the legacy wallet without a project notice', async ({
  page,
}) => {
  await setup(page, (url) => {
    if (url.pathname === '/api/user/self')
      return ok({
        id: 'u_ada',
        username: 'Ada',
        role: 1,
        features: { projects: false },
        projects: [],
      });
    if (url.pathname === '/api/user/payments/flag-off-wallet')
      return ok({
        id: 'flag-off-wallet',
        project_id: 'p_orbit',
        project_name: '',
        project_shared: false,
        paid_at: 0,
        created_at: 1799999900,
        paid_amount_microusd: 0,
        order_amount_microusd: 5000000,
        credited_microusd: 5000000,
        payment_status: 'failed',
        payment_method: '',
        invoice_status: 'pending',
        invoice_number: '',
        invoice_ready: false,
        receipt_ready: false,
      });
  });
  await page.goto(
    '/payment/success?project_id=p_orbit&top_up_id=flag-off-wallet',
  );
  await expect(page.locator('.wallet-page')).toBeVisible();
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Nothing was charged.');
  await expect(record).toContainText('No documents.');
  await expect(
    page.getByText('Unable to open this project wallet', { exact: true }),
  ).toHaveCount(0);
  await record.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(page.locator('.lazu-topup-record')).toHaveCount(0);
  await expect(page).not.toHaveURL(/top_up_id/);
});

for (const [document, destination] of [
  ['invoice', 'https://pay.stripe.com/invoice/current/pdf'],
  ['invoice_page', 'https://invoice.stripe.com/i/current'],
  ['receipt', 'https://pay.stripe.com/receipts/current'],
] as const) {
  test(`payment email opens the ${document} directly in Stripe`, async ({
    page,
  }) => {
    await setup(page);
    await page.route(
      '**/api/project/p_orbit/topups/email-document/document*',
      async (route) => {
        expect(
          new URL(route.request().url()).searchParams.get('document'),
        ).toBe(document);
        await route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify(ok({ url: destination })),
        });
      },
    );
    for (const host of [
      'https://pay.stripe.com/**',
      'https://invoice.stripe.com/**',
    ])
      await page.context().route(host, async (route) => {
        await route.fulfill({
          contentType: 'text/html',
          body: '<h1>Stripe document</h1>',
        });
      });
    await page.goto(
      `/payment/document?project_id=p_orbit&top_up_id=email-document&document=${document}`,
    );
    await expect(page).toHaveURL(/stripe\.com/);
    await expect(
      page.getByRole('heading', { name: 'Stripe document' }),
    ).toBeVisible();
  });
}

test('payment document keeps a viewport while Stripe link resolves', async ({
  page,
}) => {
  await setup(page);
  let releaseRequest: () => void = () => {};
  const requestGate = new Promise<void>((resolve) => {
    releaseRequest = resolve;
  });
  await page.route(
    '**/api/project/p_orbit/topups/email-document/document*',
    async (route) => {
      await requestGate;
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({
          success: false,
          error: {
            code: 'payment_provider_unavailable',
            type: 'api_error',
            request_id: 'req_mock',
          },
        }),
      });
    },
  );
  try {
    await page.goto(
      '/payment/document?project_id=p_orbit&top_up_id=email-document&document=invoice',
    );
    const viewport = page.locator('main[aria-busy="true"]');
    await expect(viewport).toBeVisible();
    await expect(
      page.getByRole('status', { name: 'Opening payment document' }),
    ).toBeVisible();
    expect((await viewport.boundingBox())?.height).toBeGreaterThan(600);
  } finally {
    releaseRequest();
  }
  await expect(page).toHaveURL(/document_error=unavailable/);
});

test('former payer can open their payment without current project membership', async ({
  page,
}) => {
  const user = {
    id: 'u_payer',
    username: 'Payer',
    role: 1,
    features: { projects: true },
    projects: [],
  };
  await setupMockedApp(page, {
    language: 'en',
    user,
    payloadFor: (raw: string) => {
      const url = new URL(raw);
      if (url.pathname === '/api/user/self') return ok(user);
      if (url.pathname === '/api/user/payments/email-document')
        return ok({
          id: 'email-document',
          project_id: 'p_orbit',
          project_name: 'Orbit',
          paid_at: 1789000000,
          paid_amount_microusd: 5000000,
          credited_microusd: 5000000,
          payment_status: 'paid',
          payment_method: 'stripe',
          invoice_status: 'ready',
          invoice_number: 'INV-2026-001',
          invoice_ready: true,
          receipt_ready: true,
        });
      return undefined;
    },
  });
  await page.goto(
    '/console/wallet?project_id=p_orbit&top_up_id=email-document',
  );
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Added to your Lazu account');
  await expect(record).toContainText('INV-2026-001');
  await expect(page).toHaveURL(/top_up_id=email-document/);
});

for (const [status, label] of [
  ['pending', 'Confirming this payment with Stripe'],
  ['paid', 'Added to your Lazu account'],
  ['expired', 'This payment expired; nothing was charged.'],
  ['failed', 'Nothing was charged. Stripe did not complete this payment.'],
] as const) {
  test(`payment record shows ${status} state`, async ({ page }) => {
    await setup(
      page,
      (url) => {
        if (url.pathname === '/api/user/payments/email-document')
          return ok({
            id: 'email-document',
            project_id: 'p_orbit',
            project_name: '',
            project_shared: false,
            paid_at: status === 'paid' ? 1789000000 : 0,
            created_at: 1788999000,
            paid_amount_microusd: status === 'paid' ? 5000000 : 0,
            order_amount_microusd: 5000000,
            credited_microusd: 5000000,
            payment_status: status,
            payment_method: 'stripe',
            invoice_status: 'pending',
            invoice_number: '',
            invoice_ready: false,
            receipt_ready: status === 'paid',
          });
      },
      'active',
      'owner',
      10,
    );
    await page.goto(
      '/console/wallet?project_id=p_orbit&top_up_id=email-document',
    );
    const record = page.locator('.lazu-topup-record');
    await expect(record).toContainText(label);
    await expect(record).toContainText('$5.00');
    await expect(record.locator('dt').first()).toHaveText(
      status === 'paid' ? 'Credited' : 'Requested credit',
    );
    if (status === 'expired' || status === 'failed') {
      await expect(record).toContainText('Not paid');
      await expect(record).toContainText('No documents.');
    } else {
      await expect(record).toContainText('Documents');
      await expect(record).not.toContainText('Not paid');
    }
  });
}

test('legacy paid payment explains the missing Stripe invoice', async ({
  page,
}) => {
  await setup(
    page,
    (url) => {
      if (url.pathname === '/api/user/payments/email-document')
        return ok({
          id: 'email-document',
          project_id: 'p_orbit',
          project_name: '',
          project_shared: false,
          paid_at: 1789000000,
          created_at: 1788999000,
          paid_amount_microusd: 5000000,
          credited_microusd: 5000000,
          payment_status: 'paid',
          payment_method: 'stripe',
          invoice_status: 'legacy',
          invoice_number: '',
          invoice_ready: false,
          receipt_ready: false,
        });
    },
    'active',
    'owner',
    10,
  );
  await page.goto(
    '/console/wallet?project_id=p_orbit&top_up_id=email-document',
  );
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Added to your Lazu account');
  await expect(record).toContainText('This top-up has no Stripe invoice');
  await expect(
    record.getByRole('link', { name: 'View on Stripe', exact: true }),
  ).toHaveAttribute('aria-disabled', 'true');
  await expect(record).not.toContainText('Chinese VAT');
});

test('payment server error offers retry without suggesting account switch', async ({
  page,
}) => {
  await setup(page);
  await page.route('**/api/user/payments/email-document', async (route) => {
    await route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({
        success: false,
        error: {
          code: 'payment_provider_unavailable',
          type: 'api_error',
          request_id: 'req_mock',
        },
      }),
    });
  });
  await page.goto(
    '/console/wallet?project_id=p_orbit&top_up_id=email-document',
  );
  await expect(
    page.getByText('This payment cannot be loaded right now.'),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Switch Lazu account' }),
  ).toHaveCount(0);
});

test('admin wallet exposes dispute review and old invoice reconciliation', async ({
  page,
}) => {
  let resolutions = 0;
  await setup(
    page,
    (url, request) => {
      if (url.pathname === '/api/user/payment-disputes')
        return ok({
          items: [
            {
              top_up_id: 'topup-old',
              dispute_id: 'dp_open',
              project_id: 1,
              paid_amount: 5,
              disputed_amount: 2,
            },
          ],
        });
      if (
        url.pathname === '/api/user/payment-disputes/topup-old/resolve' &&
        request.method() === 'POST'
      ) {
        resolutions += 1;
        return ok({ top_up_id: 'topup-old', outcome: 'lost' });
      }
      if (
        url.pathname === '/api/user/payments/topup-old/reconcile-documents' &&
        request.method() === 'POST'
      )
        return { success: true };
    },
    'active',
    'owner',
    10,
  );
  await page.goto('/console/wallet');
  await page.getByRole('button', { name: 'Payment disputes' }).click();
  await expect(page.getByText('topup-old · $2.00 disputed')).toBeVisible();
  await expect(page.getByText('Original payment: $5.00')).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'View dispute in Stripe' }),
  ).toHaveAttribute('href', 'https://dashboard.stripe.com/disputes/dp_open');
  await page.getByRole('button', { name: 'Mark lost' }).click();
  await expect(page.getByText('Disputed amount: $2.00')).toBeVisible();
  await expect(page.getByText(/may leave a negative balance/)).toBeVisible();
  expect(resolutions).toBe(0);
  await page.getByRole('button', { name: 'Cancel' }).click();
  expect(resolutions).toBe(0);
  await page.getByRole('button', { name: 'Mark lost' }).click();
  await page.getByRole('button', { name: 'Confirm lost dispute' }).click();
  await expect(page.getByText('Payment dispute resolved.')).toBeVisible();
  expect(resolutions).toBe(1);
  await page
    .getByRole('dialog', { name: 'Open payment disputes' })
    .locator('.lazu-modal-close')
    .click();
  await page.getByRole('button', { name: 'Reconcile invoice' }).click();
  await page.getByLabel('Payment reference').fill('topup-old');
  await page.getByRole('button', { name: 'Reconcile documents' }).click();
  await expect(page.getByText('Payment documents reconciled.')).toBeVisible();
});

test('payment document failure returns to the wallet record', async ({
  page,
}) => {
  await setup(page);
  await page.route(
    '**/api/project/p_orbit/topups/email-document/document*',
    async (route) => {
      await route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({
          success: false,
          error: {
            code: 'access_denied',
            type: 'permission_error',
            request_id: 'req_mock',
          },
        }),
      });
    },
  );
  await page.goto(
    '/payment/document?project_id=p_orbit&top_up_id=email-document&document=invoice_page&source=wallet',
  );
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText('Document unavailable');
  await expect(record).toContainText('INV-2026-001');
  await expect(page).toHaveURL(/document_error=unavailable/);
});

test('missing payment offers account switch in the wallet', async ({
  page,
}) => {
  await setup(page);
  await page.route(
    '**/api/project/p_orbit/topups/email-document/document*',
    async (route) => {
      await route.fulfill({
        status: 404,
        contentType: 'application/json',
        body: JSON.stringify({
          success: false,
          error: {
            code: 'invoice_unavailable',
            type: 'not_found_error',
            request_id: 'req_mock',
          },
        }),
      });
    },
  );
  await page.route('**/api/user/payments/email-document', async (route) => {
    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({
        success: false,
        error: {
          code: 'payment_not_found',
          type: 'not_found_error',
          request_id: 'req_mock',
        },
      }),
    });
  });
  await page.goto(
    '/payment/document?project_id=p_orbit&top_up_id=email-document&document=receipt&source=wallet',
  );
  await expect(
    page.getByText(
      'This payment was not found or is unavailable to this account.',
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Switch Lazu account' }),
  ).toBeVisible();
  await page.route('**/login?next=*', async (route) => {
    await route.fulfill({ contentType: 'text/html', body: '<h1>Sign in</h1>' });
  });
  await page.getByRole('button', { name: 'Switch Lazu account' }).click();
  /* The gate may already have folded project_id into the current project
     before the click; the resume target must still name the record. */
  await expect(page).toHaveURL(
    /\/login\?next=%2Fconsole%2Fwallet%3F.*top_up_id%3Demail-document/,
  );
});

for (const failingPath of ['/api/pricing', '/api/user/models']) {
  test(`connection preserves selection and avoids repeat reveal after ${failingPath} refetch failure`, async ({
    page,
    context,
  }) => {
    let fail = false;
    let reveals = 0;
    await page.clock.install();
    await setup(page, (url) => {
      if (fail && url.pathname === failingPath)
        return failWith(500, 'internal_error');
      if (url.pathname.endsWith('/secret')) {
        reveals++;
        return ok({
          key: 'fake-refetch-secret',
          notification_status: 'queued',
        });
      }
    });
    await page.goto('/console/token');
    await page
      .getByRole('button', { name: 'Connect', exact: true })
      .first()
      .click();
    const dialog = page.locator('.tok-connect');
    const selected = dialog.getByRole('button', {
      name: 'Cursor',
      exact: true,
    });
    await selected.click();
    await expect.poll(() => reveals).toBe(1);
    fail = true;
    await page.clock.fastForward(31_000);
    await context.setOffline(true);
    await expect.poll(() => page.evaluate(() => navigator.onLine)).toBe(false);
    await context.setOffline(false);
    await expect(dialog.getByRole('alert')).toContainText(
      'Unable to load project data',
    );
    await expect(selected).toHaveAttribute('aria-current', 'true');
    expect(reveals).toBe(1);
    fail = false;
    await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(dialog.getByRole('alert')).toHaveCount(0);
    await expect(selected).toHaveAttribute('aria-current', 'true');
    expect(reveals).toBe(1);
  });
}

for (const newlyCreated of [false, true]) {
  test(`${newlyCreated ? 'created' : 'revealed'} project connection secret remasks after 30 seconds`, async ({
    page,
    context,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.clock.install();
    let reveals = 0;
    const secret = 'fake-timed-connection-secret';
    await setup(page, (url, request) => {
      if (url.pathname === '/api/token/' && request.method() === 'POST')
        return ok({ token: { ...keys[0], id: 'timed-created' }, key: secret });
      if (url.pathname.endsWith('/secret')) {
        reveals++;
        return ok({ key: secret, notification_status: 'queued' });
      }
    });
    await page.goto('/console/token');
    if (newlyCreated) {
      await page.getByRole('button', { name: /^Create key$/i }).click();
      await page.locator('#token-name').fill('Timed key');
      await page
        .getByRole('dialog')
        .getByRole('button', { name: /^Create key$/i })
        .click();
    } else
      await page
        .getByRole('button', { name: 'Connect', exact: true })
        .first()
        .click();
    const dialog = page.locator('.tok-connect');
    await dialog.getByRole('button', { name: 'Cursor', exact: true }).click();
    await expect(dialog.locator('pre')).toContainText(secret);
    await page.clock.fastForward(31_000);
    await expect(dialog.locator('pre')).not.toContainText(secret);
    await expect(dialog.locator('pre')).toContainText('YOUR_LAZU_KEY');
    await dialog.getByRole('button', { name: 'Copy', exact: true }).click();
    await expect
      .poll(() => page.evaluate(() => navigator.clipboard.readText()))
      .not.toContain(secret);
    await dialog
      .getByRole('button', { name: 'Codex CLI', exact: true })
      .click();
    await expect(
      dialog.getByRole('button', { name: 'Copy Key', exact: true }),
    ).toBeDisabled();
    expect(reveals).toBe(newlyCreated ? 0 : 1);
  });
}

test('catalog retry cannot resurrect an expired newly created secret', async ({
  page,
}) => {
  await page.clock.install();
  let fail = true;
  let reveals = 0;
  let created = false;
  await setup(page, (url, request) => {
    if (created && fail && url.pathname === '/api/pricing')
      return failWith(500, 'internal_error');
    if (url.pathname === '/api/token/' && request.method() === 'POST') {
      created = true;
      return ok({
        token: { ...keys[0], id: 'delayed-created' },
        key: 'fake-delayed-secret',
      });
    }
    if (url.pathname.endsWith('/secret')) {
      reveals++;
      return ok({ key: 'fake-delayed-secret' });
    }
  });
  await page.goto('/console/token');
  await page.getByRole('button', { name: /^Create key$/i }).click();
  await page.locator('#token-name').fill('Delayed key');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: /^Create key$/i })
    .click();
  await expect(
    page.getByText('fake-delayed-secret', { exact: true }),
  ).toBeVisible();
  await page.clock.fastForward(31_000);
  await expect(
    page.getByText('fake-delayed-secret', { exact: true }),
  ).toHaveCount(0);
  fail = false;
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Retry', exact: true })
    .click();
  const dialog = page.locator('.tok-connect');
  await dialog.getByRole('button', { name: 'Cursor', exact: true }).click();
  await expect(dialog.locator('pre')).toContainText('YOUR_LAZU_KEY');
  await expect(dialog.locator('pre')).not.toContainText('fake-delayed-secret');
  expect(reveals).toBe(0);
});

test('project connection retries a failed reveal without discarding selection', async ({
  page,
}) => {
  let reveals = 0;
  await setup(page, (url) => {
    if (url.pathname.endsWith('/secret')) {
      reveals++;
      return reveals === 1
        ? failWith(500, 'internal_error')
        : ok({ key: 'fake-retried-secret', notification_status: 'queued' });
    }
  });
  await page.goto('/console/token');
  await page
    .getByRole('button', { name: 'Connect', exact: true })
    .first()
    .click();
  const dialog = page.locator('.tok-connect');
  await expect(dialog.getByRole('alert')).toBeVisible();
  const tool = dialog.getByRole('button', { name: 'Cursor', exact: true });
  await tool.click();
  await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveCount(0);
  await expect(dialog.locator('pre')).toContainText('fake-retried-secret');
  await expect(tool).toHaveAttribute('aria-current', 'true');
  expect(reveals).toBe(2);
});

test('connection conflict refreshes the list before reopening with the current version', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  let changed = false;
  const versions: unknown[] = [];
  let reads = 0;
  await setup(page, (url, request) => {
    if (url.pathname.endsWith('/tokens')) {
      reads++;
      return ok({
        items: [{ ...keys[0], version: changed ? 42 : 41 }],
        total: 1,
        page: 1,
        page_size: 100,
      });
    }
    if (url.pathname.endsWith('/secret'))
      return ok({ key: changed ? 'fake-fresh-secret' : 'fake-old-secret' });
    if (url.pathname === '/api/token/' && request.method() === 'PUT') {
      const body = request.postDataJSON();
      versions.push(body.expected_version);
      if (!changed) {
        changed = true;
        return failWith(409, 'version_conflict', 'conflict_error');
      }
      return ok({ ...keys[0], ...body, version: 43 });
    }
  });
  await page.goto('/console/token');
  await page.getByRole('button', { name: 'Connect', exact: true }).click();
  const dialog = page.locator('.tok-connect');
  await dialog.getByRole('button', { name: 'Codex CLI', exact: true }).click();
  await dialog
    .getByRole('button', { name: 'Copy and finish', exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(versions).toEqual([41]);
  expect(reads).toBeGreaterThanOrEqual(2);
  await page.getByRole('button', { name: 'Connect', exact: true }).click();
  await dialog.getByRole('button', { name: 'Cursor', exact: true }).click();
  await expect(dialog.locator('pre')).toContainText('fake-fresh-secret');
  await dialog.getByRole('button', { name: 'Codex CLI', exact: true }).click();
  await dialog
    .getByRole('button', { name: 'Copy and finish', exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(versions).toEqual([41, 42]);
});

test('accepted invitation verifies the account before showing success even past expiry', async ({
  page,
}) => {
  let accepts = 0;
  let failRefresh = false;
  await setup(page, (url) => {
    if (url.pathname === '/api/invite/review')
      return ok({
        project_name: 'Orbit',
        inviter: { id: 'u_ada', username: 'Ada' },
        role: 'member',
        budget_microusd: 100000000,
        expires_at: 1,
        status: 'accepted',
      });
    if (url.pathname === '/api/invite/review/accept') {
      accepts++;
      failRefresh = true;
      return ok({ project_id: 'p_orbit', role: 'member' });
    }
    if (url.pathname === '/api/user/self' && failRefresh)
      return failWith(500, 'internal_error');
  });
  await page.goto('/invite/review');
  await expect(page.getByRole('heading')).toHaveText(
    'This invitation has been accepted',
  );
  expect(accepts).toBe(0);
  await page.getByRole('button', { name: 'Open “Orbit”', exact: true }).click();
  await expect(page.getByRole('heading')).toHaveText('You have joined “Orbit”');
  await expect(page.getByRole('alert')).toContainText('You have joined');
  failRefresh = false;
  await page.getByRole('button', { name: 'Open “Orbit”', exact: true }).click();
  await expect(page).toHaveURL(/\/console$/);
  expect(accepts).toBe(1);
});

test('expired fallback secret cannot be copied before the timer resumes', async ({
  page,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.clock.install();
  let created = false;
  await setup(page, (url, request) => {
    if (created && url.pathname === '/api/pricing')
      return failWith(500, 'internal_error');
    if (url.pathname === '/api/token/' && request.method() === 'POST') {
      created = true;
      return ok({
        token: { ...keys[0], id: 'expired-copy' },
        key: 'fake-expired-copy-secret',
      });
    }
  });
  await page.goto('/console/token');
  await page.getByRole('button', { name: /^Create key$/i }).click();
  await page.locator('#token-name').fill('Expired copy');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: /^Create key$/i })
    .click();
  await expect(
    page.getByText('fake-expired-copy-secret', { exact: true }),
  ).toBeVisible();
  await page.evaluate(() => navigator.clipboard.writeText('unchanged'));
  // Move wall time without firing timers, as after a suspended tab resumes.
  await page.clock.setSystemTime(
    (await page.evaluate(() => Date.now())) + 31_000,
  );
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Copy', exact: true })
    .click();
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toBe('unchanged');
  await expect(
    page.getByText('fake-expired-copy-secret', { exact: true }),
  ).toHaveCount(0);
});

for (const action of ['key', 'snippet', 'import'] as const) {
  test(`connection ${action} checks the secret deadline before timers resume`, async ({
    page,
    context,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.clock.install();
    let writes = 0;
    await setup(page, (url, request) => {
      if (url.pathname.endsWith('/secret'))
        return ok({
          key: 'fake-expired-export-secret',
          notification_status: 'queued',
        });
      if (url.pathname === '/api/token/' && request.method() === 'PUT') {
        writes++;
        return ok(keys[0]);
      }
    });
    await page.goto('/console/token');
    await page
      .getByRole('button', { name: 'Connect', exact: true })
      .first()
      .click();
    const dialog = page.locator('.tok-connect');
    await dialog.getByRole('button', { name: 'Cursor', exact: true }).click();
    await expect(dialog.locator('pre')).toContainText(
      'fake-expired-export-secret',
    );
    if (action !== 'snippet')
      await dialog
        .getByRole('button', { name: 'Codex CLI', exact: true })
        .click();
    await page.evaluate(() => navigator.clipboard.writeText('unchanged'));
    await page.clock.setSystemTime(
      (await page.evaluate(() => Date.now())) + 31_000,
    );
    const button =
      action === 'key'
        ? dialog.getByRole('button', { name: 'Copy Key', exact: true })
        : action === 'snippet'
          ? dialog.getByRole('button', { name: 'Copy', exact: true })
          : dialog.locator('.tok-connect__quick button').first();
    await button.click();
    await expect
      .poll(() => page.evaluate(() => navigator.clipboard.readText()))
      .toBe('unchanged');
    await expect(dialog.locator('pre')).not.toContainText(
      'fake-expired-export-secret',
    );
    expect(writes).toBe(0);
    if (action === 'import') await expect(button).toBeDisabled();
  });
}

for (const failed of ['members', 'tokens']) {
  test(`dissolution recovers after ${failed} prerequisite fails`, async ({
    page,
  }) => {
    let attempts = 0;
    await setup(page, (url) => {
      if (url.pathname.endsWith(`/${failed}`) && ++attempts === 1)
        return failWith(500, 'internal_error');
    });
    await page.goto('/console/project/settings');
    const dissolve = page.locator('.pj-settings-dissolve');
    await expect(dissolve).toBeDisabled();
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(dissolve).toBeEnabled();
    await dissolve.click();
    await expect(page.getByRole('dialog')).toBeVisible();
    expect(attempts).toBeGreaterThan(1);
  });
}

test('member catalog reserves space until models are known', async ({
  page,
}) => {
  await setup(page);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/user/models*', async (route) => {
    await gate;
    await route.fulfill({ json: ok(['gpt-6-astra', 'gpt-5.2']) });
  });
  await page.goto('/console/project/members');
  await page
    .getByRole('row')
    .filter({ hasText: 'Noor' })
    .getByRole('button', { name: 'Edit', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  const trigger = dialog.getByRole('button', {
    name: 'Allowed models',
    exact: true,
  });
  await expect(trigger.locator('.lazu-pending')).toHaveCount(1);
  await expect(trigger).not.toContainText('0');
  await dialog.evaluate((el) =>
    el.getAnimations({ subtree: true }).forEach((a) => {
      if (Number.isFinite(a.effect?.getComputedTiming().endTime)) a.finish();
    }),
  );
  const before = await trigger.boundingBox();
  await trigger.click();
  const list = page.locator('.pj-medit__list');
  await expect(list.locator('.lazu-pending-block')).toHaveCount(1);
  await page.locator('.pj-medit__menu').evaluate((el) =>
    el.getAnimations({ subtree: true }).forEach((a) => {
      if (Number.isFinite(a.effect?.getComputedTiming().endTime)) a.finish();
    }),
  );
  const listBefore = await list.boundingBox();
  release();
  await expect(page.locator('.pj-medit__opt')).toHaveCount(2);
  await expect(trigger).toContainText('2');
  const after = await trigger.boundingBox();
  const listAfter = await list.boundingBox();
  expect(after?.width).toBe(before?.width);
  expect(after?.height).toBe(before?.height);
  expect(listAfter?.width).toBe(listBefore?.width);
  expect(listAfter?.height).toBe(listBefore?.height);
});

for (const fails of [false, true]) {
  test(`effort default stays unknown while settings ${fails ? 'fail' : 'load'}`, async ({
    page,
  }) => {
    await setup(page);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route('**/api/project/p_orbit', async (route) => {
      await gate;
      await route.fulfill({
        json: fails
          ? failWith(500, 'internal_error')
          : ok({
              ...detail,
              settings: {
                ...detail.settings,
                default_reasoning_effort_cap: 'low',
              },
            }),
      });
    });
    await page.goto('/console/project/settings');
    const field = page.getByTestId('project-effort-setting');
    await expect(field.locator('.lazu-pending-block')).toBeVisible();
    await expect(field.getByRole('radiogroup')).toHaveCount(0);
    const before = await field.boundingBox();
    release();
    if (fails) {
      await expect(
        page.getByRole('button', { name: 'Retry', exact: true }),
      ).toBeVisible();
      await expect(field.getByRole('radiogroup')).toHaveCount(0);
    } else {
      await expect(
        field.getByRole('radio', { name: 'low', exact: true }),
      ).toHaveAttribute('aria-checked', 'true');
      const after = await field.boundingBox();
      expect(after?.width).toBe(before?.width);
      expect(after?.height).toBe(before?.height);
    }
  });
}

test('suspended attention action selects the row with Enable instead of editing policy', async ({
  page,
}) => {
  let restored = false;
  await setup(page, (url, request) => {
    const member = members.find((m) => m.user_id === 'u_noor')!;
    if (url.pathname.endsWith('/attention'))
      return ok({
        rejections: {
          items: [
            {
              actor: { id: member.user_id, username: member.username },
              token: { id: keys[0].id, name: keys[0].name },
              reason: 'project_access_suspended',
              count: 1,
              last_at: 1800000000,
            },
          ],
          total: 1,
          page: 1,
          page_size: 20,
        },
        pending_invites_count: 0,
        pending_budget_requests_count: 0,
        balance_days_left: null,
      });
    if (url.pathname.endsWith('/members'))
      return ok({
        total: members.length,
        page: 1,
        page_size: 100,
        items: members.map((m) =>
          m.user_id === member.user_id
            ? { ...m, status: restored ? 'active' : 'suspended' }
            : m,
        ),
      });
    if (
      url.pathname.endsWith('/members/u_noor/status') &&
      request.method() === 'PUT'
    ) {
      restored = true;
      return ok({ ...member, status: 'active' });
    }
  });
  await page.goto('/console');
  await page
    .getByRole('link', { name: 'Open this member', exact: true })
    .click();
  await expect(page).toHaveURL(/members\?member=u_noor/);
  const row = page.getByRole('row').filter({ hasText: 'Noor' });
  await expect(row).toHaveAttribute('data-selected', 'true');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await row.getByRole('button', { name: 'Enable', exact: true }).click();
  await expect.poll(() => restored).toBe(true);
});

test('narrow members retain role suspend remove and restore actions', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  let restored = false;
  await setup(page, (url, request) => {
    if (url.pathname.endsWith('/members'))
      return ok({
        total: members.length,
        page: 1,
        page_size: 100,
        items: members.map((m) =>
          m.user_id === 'u_noor'
            ? { ...m, status: restored ? 'active' : 'suspended' }
            : m,
        ),
      });
    if (
      url.pathname.endsWith('/members/u_noor/status') &&
      request.method() === 'PUT'
    ) {
      restored = true;
      return ok({
        ...members.find((m) => m.user_id === 'u_noor'),
        status: 'active',
      });
    }
  });
  await page.goto('/console/project/members?member=u_noor');
  const narrow = page.locator('.pj-narrow');
  const suspended = narrow
    .locator('.pj-mlist__row')
    .filter({ hasText: 'Noor' });
  await expect(suspended).toHaveAttribute('data-selected', 'true');
  await suspended.getByRole('button', { name: 'Enable', exact: true }).click();
  await expect.poll(() => restored).toBe(true);
  const row = narrow.locator('.pj-mlist__row').filter({ hasText: 'Kai' });
  await row.getByRole('button', { name: 'More for Kai' }).click();
  await expect(
    page.getByRole('menuitem', { name: 'Make admin', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('menuitem', { name: 'Suspend', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('menuitem', { name: 'Remove…', exact: true }),
  ).toBeVisible();
  await page.getByRole('menuitem', { name: 'Suspend', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await row.getByRole('button', { name: 'More for Kai' }).click();
  await page.getByRole('menuitem', { name: 'Make admin', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await row.getByRole('button', { name: 'More for Kai' }).click();
  await page.getByRole('menuitem', { name: 'Remove…', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
});

test('allocation breakdown reserves its space until members arrive', async ({
  page,
}) => {
  await setup(page);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/project/p_orbit/members?*', async (route) => {
    await gate;
    await route.fulfill({
      json: ok({
        total: members.length,
        page: 1,
        page_size: 100,
        items: members,
      }),
    });
  });
  await page.goto('/console/project/members');
  const pending = page.getByTestId('allocation-pending');
  await expect(pending).toBeVisible();
  await expect(page.locator('.pj-allocstack__legend')).not.toContainText(
    'Ravi',
  );
  const before = await pending.boundingBox();
  release();
  await expect(page.locator('.pj-allocstack__legend')).toContainText('Ravi');
  const after = await page.locator('.pj-allocstack').boundingBox();
  expect(after?.height).toBe(before?.height);
  expect(after?.width).toBe(before?.width);
});

const pendingPayment = {
  id: 'review-payment',
  project_id: 'p_orbit',
  project_name: 'Orbit',
  project_shared: true,
  paid_at: 0,
  created_at: 1788999000,
  paid_amount_microusd: 0,
  order_amount_microusd: 10_000_000_000,
  credited_microusd: 10_000_000_000,
  payment_status: 'pending',
  payment_method: '',
  invoice_status: 'pending',
  invoice_number: '',
  invoice_ready: false,
  receipt_ready: false,
};

test('demoted payer sees the payment record above their budget', async ({
  page,
}) => {
  await setup(page, undefined, 'active', 'member');
  await page.goto(
    '/console/wallet?project_id=p_orbit&top_up_id=email-document',
  );
  await expect(page.locator('.pj-mybudget-page')).toBeVisible();
  await expect(page.locator('.lazu-topup-record')).toContainText(
    'INV-2026-001',
  );
  await expect(page.locator('.lazu-topup-bar')).toHaveCount(0);
});

test('payment lookup reserves the maximum amount without claiming a status', async ({
  page,
}) => {
  await setup(page);
  await page.clock.install();
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/user/payments/review-payment', async (route) => {
    await gate;
    await route.fulfill({ json: ok(pendingPayment) });
  });
  await page.goto('/console/wallet?top_up_id=review-payment');
  const record = page.locator('.lazu-topup-record');
  await expect(record).toHaveAttribute('data-state', 'loading');
  await expect(record).not.toContainText('CONFIRMING');
  const amount = record.locator('.lazu-topup-record__amount');
  const before = await amount.boundingBox();
  await page.clock.runFor(301);
  await expect(amount.locator('.lazu-pending')).toBeVisible();
  release();
  await expect(amount).toHaveText('$10,000.00');
  const after = await amount.boundingBox();
  expect(after?.width).toBeCloseTo(before!.width, 0);
  expect(after?.height).toBeCloseTo(before!.height, 0);
  expect(after?.x).toBeCloseTo(before!.x, 0);
  expect(after?.y).toBeCloseTo(before!.y, 0);
});

test('payment stops polling and busy indicators at the time budget', async ({
  page,
}) => {
  let lookups = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/review-payment') {
      lookups++;
      return ok(pendingPayment);
    }
  });
  await page.clock.install();
  await page.goto('/console/wallet?top_up_id=review-payment');
  const record = page.locator('.lazu-topup-record');
  await expect(record).toHaveAttribute('data-state', 'pending');
  await page.clock.fastForward(121_000);
  await expect(record).not.toHaveAttribute('aria-busy', 'true');
  await expect(record.locator('.lazu-loading__spinner')).toHaveCount(0);
  await expect(record).not.toContainText('Not paid');
  for (const label of [
    'Paid on',
    'Payment method',
    'Invoice number',
    'Bill to',
  ]) {
    const value = record
      .locator('.lazu-topup-record__fact')
      .filter({ has: page.getByText(label, { exact: true }) })
      .locator('dd');
    await expect(value).not.toContainText('—');
    const unknown = value.getByRole('status', { name: 'Not yet confirmed' });
    await expect(unknown).toBeVisible();
    await expect(unknown).toHaveCSS('animation-name', 'none');
  }

  const stoppedAt = lookups;
  await page.clock.fastForward(60_000);
  expect(lookups).toBe(stoppedAt);
  await record.getByRole('button', { name: 'Try again' }).click();
  await expect.poll(() => lookups).toBe(stoppedAt + 1);
});

test('paid transition refreshes the project wallet and topups', async ({
  page,
}) => {
  let paid = false;
  let walletReads = 0;
  let topupReads = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/review-payment')
      return ok({
        ...pendingPayment,
        payment_status: paid ? 'paid' : 'pending',
        paid_amount_microusd: paid ? 10_000_000_000 : 0,
        invoice_status: paid ? 'ready' : 'pending',
        invoice_ready: paid,
        receipt_ready: paid,
      });
    if (url.pathname === '/api/project/p_orbit/wallet') walletReads++;
    if (url.pathname === '/api/project/p_orbit/topups') topupReads++;
  });
  await page.clock.install();
  await page.goto('/console/wallet?top_up_id=review-payment');
  await expect(page.locator('.lazu-topup-record')).toHaveAttribute(
    'data-state',
    'pending',
  );
  await expect.poll(() => walletReads).toBeGreaterThan(0);
  await expect.poll(() => topupReads).toBeGreaterThan(0);
  const beforeWallet = walletReads,
    beforeTopups = topupReads;
  paid = true;
  await page.clock.fastForward(3_100);
  await expect(page.locator('.lazu-topup-record')).toHaveAttribute(
    'data-state',
    'paid',
  );
  await expect.poll(() => walletReads).toBeGreaterThan(beforeWallet);
  await expect.poll(() => topupReads).toBeGreaterThan(beforeTopups);
});

test('legacy payment without documents is terminal', async ({ page }) => {
  let lookups = 0;
  await setup(page, (url) => {
    if (url.pathname === '/api/user/payments/review-payment') {
      lookups++;
      return ok({
        ...pendingPayment,
        payment_status: 'paid',
        paid_amount_microusd: 5_000_000,
        invoice_status: 'legacy',
      });
    }
  });
  await page.clock.install();
  await page.goto('/console/wallet?top_up_id=review-payment');
  const record = page.locator('.lazu-topup-record');
  await expect(record).toContainText(
    'No receipt is available for this historical payment.',
  );
  await expect(record).not.toHaveAttribute('aria-busy', 'true');
  await expect(record.locator('.lazu-loading__spinner')).toHaveCount(0);
  await page.clock.fastForward(10_000);
  expect(lookups).toBe(1);
});

test('topup amount rejects decimals and negative values without rewriting them', async ({
  page,
}) => {
  await setup(page);
  await page.goto('/console/wallet');
  const bar = page.locator('.lazu-topup-bar');
  const input = bar.locator('input');
  // The deployment floor is $5 unless /api/status says otherwise (the
  // operator's MinTopUpUSD); the mocked status says nothing, so $4 is under it.
  for (const value of ['1.5', '-5', '1e2', '0', '4', '10001']) {
    await input.fill(value);
    await expect(input).toHaveValue(value);
    await expect(bar.locator('.lazu-topup-bar__pay')).toBeDisabled();
    await expect(bar.getByRole('alert')).toBeVisible();
  }
  await input.fill('5');
  await expect(bar.locator('.lazu-topup-bar__pay')).toBeEnabled();
  await expect(bar.locator('.lazu-topup-bar__pay')).toContainText('$5');
});

test('payment lookup starts while the project bootstrap is unresolved', async ({
  page,
}) => {
  await setup(page);
  let lookupStarted = false;
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/user/self', async (route) => {
    await gate;
    await route.fallback();
  });
  await page.route('**/api/user/payments/email-document', async (route) => {
    lookupStarted = true;
    await route.fallback();
  });
  try {
    await page.goto(
      '/console/wallet?project_id=p_orbit&top_up_id=email-document',
    );
    await expect.poll(() => lookupStarted).toBe(true);
  } finally {
    release();
  }
  await expect(page.locator('.lazu-topup-record')).toHaveAttribute(
    'data-state',
    'paid',
  );
});

test('topup bar holds unknown balance slots until the wallet responds', async ({
  page,
}) => {
  await setup(page);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/project/p_orbit/wallet', async (route) => {
    await gate;
    await route.fallback();
  });
  await page.clock.install();
  await page.goto('/console/wallet');
  const bar = page.locator('.lazu-topup-bar');
  const slots = bar.locator('.lazu-topup-bar__balance-slot');
  await expect(slots).toHaveCount(2);
  await expect(slots.nth(0)).not.toContainText('—');
  await expect(slots.nth(1)).not.toContainText('—');
  const before = await slots.evaluateAll((nodes) =>
    nodes.map((node) => {
      const r = node.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height };
    }),
  );
  await page.clock.runFor(301);
  await expect(slots.nth(0).locator('.lazu-pending')).toBeVisible();
  release();
  await expect(slots.nth(0)).toContainText('$100.00');
  await expect(slots.nth(1)).toContainText('$120.00');
  const after = await slots.evaluateAll((nodes) =>
    nodes.map((node) => {
      const r = node.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height };
    }),
  );
  expect(after).toEqual(before);
});

test('legacy topup bar estimates days from account daily spend', async ({
  page,
}) => {
  await setup(page, (url) => {
    if (url.pathname === '/api/user/topup/info')
      return ok({ enable_stripe_topup: true, stripe_min_topup: 1 });
    if (url.pathname === '/api/user/self')
      return ok({
        id: 'u_ada',
        username: 'Ada',
        role: 1,
        quota: 100_000_000,
        features: { projects: false },
        projects: [],
      });
    if (url.pathname === '/api/data/dashboard/self')
      return ok({ totals: { amount_microusd: 27_000_000 }, time_series: [] });
  });
  await page.goto('/console/wallet');
  await expect(page.locator('.wallet-page')).toBeVisible();
  await expect(page.locator('.lazu-topup-bar__fact').first()).toContainText(
    /days/,
  );
});

for (const status of ['pending', 'expired', 'failed'] as const) {
  test(`payment ${status} does not present Stripe as the payment method`, async ({
    page,
  }) => {
    await setup(page, (url) =>
      url.pathname === '/api/user/payments/review-payment'
        ? ok({
            ...pendingPayment,
            payment_status: status,
            payment_method: 'stripe',
          })
        : undefined,
    );
    await page.goto('/console/wallet?top_up_id=review-payment');
    const record = page.locator('.lazu-topup-record');
    await expect(record).toHaveAttribute('data-state', status);
    const method = record
      .locator('.lazu-topup-record__fact')
      .filter({ has: page.getByText('Payment method', { exact: true }) })
      .locator('dd');
    await expect(method).not.toContainText('Stripe');
    if (status === 'pending')
      await expect(method.locator('.lazu-pending')).toBeVisible();
    else await expect(method).toHaveText('—');
  });
}

test('payment resolves the actual method and keeps polling until the receipt arrives', async ({
  page,
}) => {
  let stage = 0;
  let reads = 0;
  await setup(page, (url) => {
    if (url.pathname !== '/api/user/payments/review-payment') return undefined;
    reads++;
    return ok({
      ...pendingPayment,
      payment_status: stage === 0 ? 'pending' : 'paid',
      payment_method: stage === 0 ? 'stripe' : 'Visa ···· 4242',
      paid_amount_microusd: stage === 0 ? 0 : 10_000_000_000,
      paid_at: stage === 0 ? 0 : 1789000000,
      invoice_status: stage === 0 ? 'pending' : 'ready',
      invoice_ready: stage > 0,
      receipt_ready: stage === 2,
    });
  });
  await page.clock.install();
  await page.goto('/console/wallet?top_up_id=review-payment');
  const record = page.locator('.lazu-topup-record');
  await expect(record).toHaveAttribute('data-state', 'pending');
  stage = 1;
  await page.clock.fastForward(3_100);
  await expect(record).toHaveAttribute('data-state', 'paid');
  await expect(record).toContainText('Visa ···· 4242');
  const receipt = record.getByRole('link', {
    name: 'View receipt',
    exact: true,
  });
  await expect(receipt).toHaveAttribute('aria-disabled', 'true');
  await expect(record).toHaveAttribute('aria-busy', 'true');
  stage = 2;
  await page.clock.fastForward(3_100);
  await expect(receipt).not.toHaveAttribute('aria-disabled', 'true');
  await expect(record).not.toHaveAttribute('aria-busy', 'true');
  const settledReads = reads;
  await page.clock.fastForward(10_000);
  expect(reads).toBe(settledReads);
});

test('lowered count links to exact month-to-date filtered logs and statistics', async ({
  page,
}) => {
  const requests: URL[] = [];
  await page.clock.install({ time: new Date('2026-09-27T03:00:00Z') });
  await setup(page, (url) => {
    if (url.pathname.endsWith('/tokens') || url.pathname === '/api/token/')
      return ok({
        total: keys.length,
        page: 1,
        page_size: 100,
        items: keys.map((key, index) => ({
          ...key,
          effort_downed_month: index === 0 ? 2 : 0,
        })),
      });
    if (url.pathname.startsWith('/api/log/self/')) requests.push(url);
  });
  await page.goto('/console/token');
  const link = page.getByRole('link', { name: '2 lowered', exact: true });
  await expect(link).toBeVisible();
  const href = await link.getAttribute('href');
  const params = new URLSearchParams(href?.split('?')[1]);
  expect(params.get('effort_clamped')).toBe('true');
  expect(Number(params.get('start_timestamp'))).toBe(
    Date.parse('2026-09-01T00:00:00Z') / 1000,
  );
  await link.click();
  await expect
    .poll(() => requests.some((url) => url.pathname === '/api/log/self/'))
    .toBe(true);
  await expect
    .poll(() => requests.some((url) => url.pathname === '/api/log/self/stat'))
    .toBe(true);
  for (const url of requests.filter(
    (url) =>
      url.pathname === '/api/log/self/' ||
      url.pathname === '/api/log/self/stat',
  )) {
    for (const key of [
      'effort_clamped',
      'project_id',
      'token_id',
      'start_timestamp',
      'end_timestamp',
    ]) {
      expect(url.searchParams.get(key), `${url.pathname} ${key}`).toBe(
        params.get(key),
      );
    }
  }
});

test('auto-reload is one closed row for the owner and opens the numbers with the consent line', async ({
  page,
}) => {
  const own = {
    ...projects.find((p) => p.id === 'p_orbit')!,
    is_default: true,
    upgraded_at: null,
    member_count: 1,
  };
  let setupBody: Record<string, unknown> | null = null;
  const app = await setup(page, (url, request) => {
    if (url.pathname === '/api/user/self')
      return ok({
        id: 'u_ada',
        username: 'Ada',
        role: 1,
        features: { projects: true },
        projects: [own],
      });
    if (
      url.pathname.endsWith('/auto-reload/setup') &&
      request.method() === 'POST'
    ) {
      setupBody = request.postDataJSON() as Record<string, unknown>;
      return ok({
        setup_link: '/console/wallet?auto_reload=canceled',
        session_id: 'cs_t',
      });
    }
  });
  await page.goto('/console/wallet');
  const row = page.locator('.lazu-auto-reload');
  await expect(row).toBeVisible();
  await expect(row).toContainText('Auto-reload · off');
  // Closed: no numbers, no card, one line of copy.
  await expect(row.locator('.lazu-auto-reload__field')).toHaveCount(0);
  await row.getByRole('switch', { name: 'Auto-reload' }).click();
  await expect(row.locator('.lazu-auto-reload__field')).toHaveCount(3);
  await expect(row).toContainText(
    'I agree that Lazu may charge the saved card',
  );
  await expect(row).toContainText('No card yet');
  await row.locator('.lazu-auto-reload__field input').first().fill('12');
  await row
    .getByRole('button', { name: 'Agree and save a card on Stripe' })
    .click();
  await expect.poll(() => setupBody).not.toBeNull();
  expect(setupBody).toMatchObject({
    threshold_usd: 12,
    amount_usd: 50,
    monthly_cap_usd: 300,
  });
  await app.expectNoUnexpectedErrors();
});

test('auto-reload row is hidden from a member and shows the paused notice to the owner', async ({
  page,
}) => {
  await setup(page, (url) => {
    if (url.pathname.endsWith('/auto-reload'))
      return ok({
        ...autoReloadOff,
        configured: true,
        enabled: true,
        threshold_usd: 10,
        amount_usd: 50,
        monthly_cap_usd: 300,
        card: { brand: 'visa', last4: '4242', label: 'Visa ···· 4242' },
        paused_reason: 'card_declined',
        last_failure: 'insufficient_funds',
        last_failed_at: 1760843520,
      });
  });
  await page.goto('/console/wallet');
  const notice = page.locator('.lazu-auto-reload-notice');
  await expect(notice).toContainText(
    'Auto-reload could not charge your card and is paused',
  );
  await expect(notice).toContainText('Visa ···· 4242');
  await expect(
    notice.getByRole('button', { name: 'Top up $50' }),
  ).toBeVisible();
  const row = page.locator('.lazu-auto-reload');
  await expect(row).toContainText('Auto-reload · paused');
  await expect(row.locator('.lazu-auto-reload__card-label')).toHaveText(
    'Visa ···· 4242',
  );

  // A member sees neither the notice nor the row.
  await setup(page, undefined, 'active', 'member');
  await page.goto('/console/wallet');
  await expect(page.locator('.pj-mybudget-page')).toBeVisible();
  await expect(page.locator('.lazu-auto-reload')).toHaveCount(0);
  await expect(page.locator('.lazu-auto-reload-notice')).toHaveCount(0);
});
