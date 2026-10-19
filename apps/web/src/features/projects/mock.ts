/* In-memory mock of the team-projects contract for local development
 * (`VITE_PROJECTS_MOCK=1`). It implements the same client interface as the
 * real API so pages cannot tell the difference, keeps state across calls so
 * every S-board state can be reached by hand, and never leaves the browser.
 *
 * Scenario (localStorage `lazu:projects-mock-scenario`):
 *   solo   — one unshared default project (the console as today)
 *   owner  — owner of a shared project, plus nothing else
 *   admin  — admin of a shared project + own default project (switcher)
 *   member — member of a shared project + own default project (switcher)
 */
import { projectError } from './errors';
import { readMockScenario, type MockScenario } from './flag';
import { MICRO } from './money';
import {
  type Attention,
  type BudgetHistoryEntry,
  type BudgetRequest,
  type ProjectDetail,
  type ProjectInvite,
  type ProjectMember,
  type ProjectRole,
  type ProjectSummary,
  type ProjectToken,
  type ProjectTopup,
  type RejectionReason,
  type UserRef,
} from './types';
import type { ProjectsClient } from './api';

const now = () => Math.floor(Date.now() / 1000);
const DAY = 86_400;
const usd = (value: number) => Math.round(value * MICRO);

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const latency = () => sleep(180 + Math.random() * 220);

let counter = 100;
const nextId = (prefix: string) => `${prefix}_${(counter += 1).toString(36)}`;

type MockUser = UserRef & { email: string };

const USERS: Record<string, MockUser> = {
  u_ada: { id: 'u_ada', username: 'Ada', email: 'ada@orbit.dev' },
  u_ravi: { id: 'u_ravi', username: 'Ravi', email: 'ravi@orbit.dev' },
  u_noor: { id: 'u_noor', username: 'Noor', email: 'noor@orbit.dev' },
  u_kai: { id: 'u_kai', username: 'Kai', email: 'kai@orbit.dev' },
};

const ref = (id: string): UserRef => ({ id, username: USERS[id].username });

type MemberRow = ProjectMember & {
  removal?: { id: string; until: number; keys: string[] };
};

type State = {
  viewer: string;
  shared: {
    detail: ProjectDetail;
    members: MemberRow[];
    invites: ProjectInvite[];
    tokens: ProjectToken[];
    requests: BudgetRequest[];
    history: Record<string, BudgetHistoryEntry[]>;
    topups: ProjectTopup[];
  } | null;
  own: {
    detail: ProjectDetail;
    tokens: ProjectToken[];
    topups: ProjectTopup[];
  };
  secrets: Record<string, string>;
  models: string[];
};

const MODELS = [
  'claude-sonnet-5',
  'claude-opus-5',
  'gpt-5.2',
  'gpt-5.2-mini',
  'deepseek-v4-flash',
  'gemini-3-pro',
];

const secretFor = (id: string) =>
  `sk-lazu-${id.replace(/\W/g, '').padEnd(6, 'x').slice(0, 6)}K1wq8Nf0BzXaP4hLmT3cQ9${id.slice(-2)}Mg`;
const mask = (secret: string) => `${secret.slice(0, 12)}…${secret.slice(-4)}`;

const caps = (
  overrides: Partial<ProjectToken['capabilities']> = {},
): ProjectToken['capabilities'] => ({
  can_reveal: true,
  can_update: true,
  can_disable: true,
  can_delete: true,
  can_rotate: true,
  ...overrides,
});

const token = (
  id: string,
  name: string,
  ownerId: string | null,
  ownerRole: ProjectRole,
  spend: number,
  extra: Partial<ProjectToken> = {},
): ProjectToken => {
  const secret = secretFor(id);
  return {
    id,
    name,
    masked_key: mask(secret),
    status: 'enabled',
    secret_available: true,
    owner: ownerId ? { ...ref(ownerId), role: ownerRole } : null,
    version: 1,
    capabilities: caps(),
    month_spend_microusd: usd(spend),
    cap_microusd: null,
    model_limits: [],
    effective_limit: { kind: 'project', remaining_microusd: usd(290.8) },
    rejected_today: [],
    created_at: now() - 90 * DAY,
    last_used_at: now() - 3600,
    ...extra,
  };
};

const topup = (
  id: string,
  daysAgo: number,
  amount: number,
  payer: string,
  number: string,
  billTo: string,
  invoiceStatus: ProjectTopup['invoice_status'] = 'ready',
): ProjectTopup => ({
  id,
  created_at: now() - daysAgo * DAY,
  paid_amount_microusd: usd(amount),
  credited_microusd: usd(amount),
  paid_by: ref(payer),
  method: 'stripe · Visa ···4242',
  payment_status: 'paid',
  invoice_status: invoiceStatus,
  invoice:
    invoiceStatus === 'ready'
      ? {
          number,
          hosted_url: 'https://invoice.stripe.com/i/example',
          pdf_url: 'https://pay.stripe.com/invoice/example/pdf',
          bill_to: {
            name: billTo,
            address: {
              line1: 'Friedrichstraße 12',
              line2: null,
              city: 'Berlin',
              state: null,
              postal_code: '10117',
              country: 'DE',
            },
            tax_ids: [{ type: 'eu_vat', value: 'DE123456789' }],
          },
        }
      : null,
  receipt_url: 'https://pay.stripe.com/receipts/example',
});

function seed(scenario: MockScenario): State {
  const viewer =
    scenario === 'member'
      ? 'u_noor'
      : scenario === 'admin'
        ? 'u_ravi'
        : 'u_ada';
  const ownDetail = (
    ownerId: string,
    name: string,
    balance: number,
  ): ProjectDetail => ({
    id: `p_${ownerId}`,
    name,
    owner: ref(ownerId),
    upgraded_at: null,
    role: 'owner',
    status: 'active',
    member_count: 1,
    pending_invites: 0,
    balance_microusd: usd(balance),
    month_spend_microusd: usd(3.1),
    project_cap_microusd: null,
    version: 1,
    allocation: {
      member_caps_microusd: 0,
      unlimited_members: 1,
    },
    settings: {
      who_can_topup: 'owner_admin',
      default_member_budget_microusd: usd(100),
      warning_pct: 80,
      project_cap_microusd: null,
    },
  });

  const state: State = {
    viewer,
    shared: null,
    own: {
      detail: ownDetail(
        viewer,
        `${USERS[viewer].username} 的项目`,
        scenario === 'solo' ? 1284.6 : 18.2,
      ),
      tokens: [
        token('t_own_main', 'main', viewer, 'owner', 18.2, {
          effective_limit: { kind: 'balance', remaining_microusd: usd(18.2) },
        }),
      ],
      topups: [
        topup(
          'tu_own_1',
          11,
          20,
          viewer,
          '3B9E0K2M-0002',
          `${USERS[viewer].username}`,
        ),
      ],
    },
    secrets: {},
    models: MODELS,
  };

  if (scenario === 'solo') {
    state.own.detail.balance_microusd = usd(1284.6);
    return state;
  }

  const spent = { u_noor: 41, u_ada: 18.2, u_ravi: 37.6, u_kai: 50 };
  const members: MemberRow[] = [
    {
      user_id: 'u_ada',
      username: 'Ada',
      email: USERS.u_ada.email,
      role: 'owner',
      status: 'active',
      joined_at: now() - 200 * DAY,
      invited_by: null,
      budget_microusd: null,
      warning_pct: 80,
      version: 1,
      month_spend_microusd: usd(spent.u_ada),
      rejected_count_today: 0,
      key_count: 1,
      last_active_at: now() - 1800,
      allowed_models: [],
      reasoning_effort_cap: '',
      effort_downed_month: 0,
    },
    {
      user_id: 'u_ravi',
      username: 'Ravi',
      email: USERS.u_ravi.email,
      role: 'admin',
      status: 'active',
      joined_at: now() - 120 * DAY,
      invited_by: ref('u_ada'),
      budget_microusd: usd(100),
      warning_pct: 80,
      version: 1,
      month_spend_microusd: usd(spent.u_ravi),
      rejected_count_today: 0,
      key_count: 2,
      last_active_at: now() - 4800,
      allowed_models: [],
      reasoning_effort_cap: '',
      effort_downed_month: 0,
    },
    {
      user_id: 'u_noor',
      username: 'Noor',
      email: USERS.u_noor.email,
      role: 'member',
      status: 'active',
      joined_at: now() - 90 * DAY,
      invited_by: ref('u_ravi'),
      budget_microusd: usd(50),
      warning_pct: 80,
      version: 1,
      month_spend_microusd: usd(spent.u_noor),
      rejected_count_today: 0,
      key_count: 2,
      last_active_at: now() - 600,
      allowed_models: [],
      reasoning_effort_cap: 'high',
      effort_downed_month: 0,
    },
    {
      user_id: 'u_kai',
      username: 'Kai',
      email: USERS.u_kai.email,
      role: 'member',
      status: 'active',
      joined_at: now() - 40 * DAY,
      invited_by: ref('u_ada'),
      budget_microusd: usd(50),
      warning_pct: 80,
      version: 1,
      month_spend_microusd: usd(spent.u_kai),
      rejected_count_today: 12,
      key_count: 1,
      last_active_at: now() - 300,
      allowed_models: [],
      reasoning_effort_cap: 'high',
      effort_downed_month: 37,
    },
  ];

  const tokens: ProjectToken[] = [
    token('t_laptop', 'laptop', 'u_noor', 'member', 28.4, {
      member_effort_cap: 'high',
      effective_limit: { kind: 'member', remaining_microusd: usd(9) },
      created_at: now() - 90 * DAY,
    }),
    token('t_notebook', 'notebook', 'u_noor', 'member', 12.6, {
      member_effort_cap: 'high',
      model_limits: ['claude-sonnet-5', 'gpt-5.2-mini', 'deepseek-v4-flash'],
      effective_limit: { kind: 'member', remaining_microusd: usd(9) },
      last_used_at: now() - 12 * 3600,
    }),
    token('t_cli', 'cli', 'u_ravi', 'admin', 37.6, {
      secret_available: false,
      effective_limit: { kind: 'member', remaining_microusd: usd(62.4) },
    }),
    token('t_kai_cli', 'kai-cli', 'u_kai', 'member', 50, {
      model_limits: ['gpt-5.2-mini'],
      reasoning_effort_cap: 'medium',
      member_effort_cap: 'high',
      effort_downed_month: 37,
      effective_limit: { kind: 'member', remaining_microusd: 0 },
      rejected_today: [
        { reason: 'member_budget_exceeded', count: 12 },
        { reason: 'model_not_allowed', count: 2 },
      ],
    }),
    token('t_main', 'main', 'u_ada', 'owner', 18.2, {
      effective_limit: { kind: 'project', remaining_microusd: usd(290.8) },
    }),
    token('t_ci', 'ci-runner', 'u_ravi', 'admin', 162.4, {
      cap_microusd: usd(200),
      reasoning_effort_cap: 'low',
      model_limits: ['gpt-5.2-mini'],
      effective_limit: { kind: 'key', remaining_microusd: usd(37.6) },
      rejected_today: [{ reason: 'key_budget_exceeded', count: 1 }],
      created_at: now() - 70 * DAY,
    }),
  ];

  const detail: ProjectDetail = {
    id: 'p_orbit',
    name: 'Orbit',
    owner: ref('u_ada'),
    upgraded_at: now() - 120 * DAY,
    role: members.find((m) => m.user_id === viewer)!.role,
    status: 'active',
    member_count: 4,
    pending_invites: 1,
    balance_microusd: usd(1284.6),
    month_spend_microusd: usd(309.2),
    project_cap_microusd: usd(600),
    version: 3,
    allocation: {
      member_caps_microusd: usd(200),
      unlimited_members: 1,
    },
    settings: {
      who_can_topup: 'owner_admin',
      default_member_budget_microusd: usd(100),
      warning_pct: 80,
      project_cap_microusd: usd(600),
      default_reasoning_effort_cap: 'high',
    },
  };

  state.shared = {
    detail,
    members,
    invites: [
      {
        id: 'inv_yu',
        email: 'yu@orbit.dev',
        role: 'member',
        budget_microusd: usd(100),
        status: 'pending',
        expires_at: now() + 4 * DAY,
        invited_by: ref('u_ada'),
        resent_at: null,
      },
    ],
    tokens,
    requests: [
      {
        id: 'br_old',
        user: ref('u_noor'),
        requested_microusd: usd(200),
        reason: '想跑一轮更大的评测',
        status: 'rejected',
        created_at: now() - 21 * DAY,
        handled_by: ref('u_ada'),
        handled_note: '这个月项目上限快到了，下月初再申请',
        handled_at: now() - 20 * DAY,
      },
      {
        id: 'br_kai',
        user: ref('u_kai'),
        requested_microusd: usd(100),
        reason: '这周要跑评测集',
        status: 'pending',
        created_at: now() - 3600,
        handled_by: null,
        handled_note: null,
        handled_at: null,
      },
    ],
    history: {
      u_noor: [
        {
          id: 'bh_2',
          created_at: now() - 27 * DAY,
          changed_by: ref('u_ravi'),
          old_budget_microusd: usd(30),
          new_budget_microusd: usd(50),
          reason: '你申请的',
        },
        {
          id: 'bh_1',
          created_at: now() - 90 * DAY,
          changed_by: ref('u_ravi'),
          old_budget_microusd: null,
          new_budget_microusd: usd(30),
          reason: '接受邀请时设的',
        },
      ],
      u_kai: [
        {
          id: 'bh_3',
          created_at: now() - 40 * DAY,
          changed_by: ref('u_ada'),
          old_budget_microusd: null,
          new_budget_microusd: usd(50),
          reason: '接受邀请时设的',
        },
      ],
      u_ravi: [
        {
          id: 'bh_4',
          created_at: now() - 120 * DAY,
          changed_by: ref('u_ada'),
          old_budget_microusd: null,
          new_budget_microusd: usd(100),
          reason: '接受邀请时设的',
        },
      ],
    },
    topups: [
      topup('tu_3', 4, 1000, 'u_ada', 'F7A2C1D9-0004', 'Orbit Labs GmbH'),
      topup('tu_2', 9, 1000, 'u_ada', 'F7A2C1D9-0003', 'Orbit Labs GmbH'),
      topup('tu_1', 40, 2500, 'u_ravi', 'F7A2C1D9-0002', 'Orbit Labs GmbH'),
    ],
  };
  return state;
}

let state: State = seed(readMockScenario());
let seededScenario: MockScenario = readMockScenario();

const ensureScenario = () => {
  const current = readMockScenario();
  if (current !== seededScenario) {
    seededScenario = current;
    state = seed(current);
  }
};

export const resetMock = () => {
  seededScenario = readMockScenario();
  state = seed(seededScenario);
};

const fail = (
  code: string,
  message: string,
  details: Record<string, unknown> = {},
  status = 400,
): never => {
  throw projectError(code, details, status, message);
};

const project = (id: string) => {
  ensureScenario();
  if (state.shared && state.shared.detail.id === id)
    return { kind: 'shared' as const, ...state.shared };
  if (state.own.detail.id === id)
    return {
      kind: 'own' as const,
      detail: state.own.detail,
      tokens: state.own.tokens,
      topups: state.own.topups,
    };
  return fail('project_not_found', '项目不存在或你不在其中', {}, 404);
};

const shared = (id: string) => {
  const p = project(id);
  if (p.kind !== 'shared')
    return fail('project_not_found', '这个项目还没有共享', {}, 404);
  return p;
};

const viewerRole = (id: string): ProjectRole => project(id).detail.role;

const requireManager = (id: string) => {
  if (viewerRole(id) === 'member') fail('access_denied', '没有权限', {}, 403);
};

const recalcAllocation = (p: NonNullable<State['shared']>) => {
  const memberCaps = p.members
    .filter((m) => m.status !== 'removed' && m.role !== 'owner')
    .reduce((sum, m) => sum + (m.budget_microusd ?? 0), 0);
  p.detail.allocation = {
    member_caps_microusd: memberCaps,
    unlimited_members: p.members.filter(
      (m) => m.status !== 'removed' && m.budget_microusd === null,
    ).length,
  };
  p.detail.member_count = p.members.filter(
    (m) => m.status !== 'removed',
  ).length;
  p.detail.pending_invites = p.invites.filter(
    (i) => i.status === 'pending',
  ).length;
};

/** `/api/user/self` → `projects[]` for the current mock viewer. */
export const mockSelfProjects = (): ProjectSummary[] => {
  ensureScenario();
  const own: ProjectSummary = {
    id: state.own.detail.id,
    name: state.own.detail.name,
    role: 'owner',
    status: 'active',
    is_default: true,
    upgraded_at: null,
    owner: state.own.detail.owner,
    member_count: 1,
    balance_microusd: state.own.detail.balance_microusd,
  };
  if (!state.shared) return [own];
  const me = state.shared.members.find((m) => m.user_id === state.viewer);
  if (!me || me.status === 'removed') return [own];
  const sharedSummary: ProjectSummary = {
    id: state.shared.detail.id,
    name: state.shared.detail.name,
    role: me.role,
    status: me.status,
    is_default: me.role === 'owner',
    upgraded_at: state.shared.detail.upgraded_at,
    owner: state.shared.detail.owner,
    member_count: state.shared.detail.member_count,
    balance_microusd: state.shared.detail.balance_microusd,
  };
  // The owner's default project IS the shared project.
  return me.role === 'owner' ? [sharedSummary] : [sharedSummary, own];
};

export const mockViewer = () => {
  ensureScenario();
  return USERS[state.viewer];
};

const REJECT_REASON_FOR_KEY: Record<string, RejectionReason> = {
  t_kai_cli: 'member_budget_exceeded',
};

export const mockProjectsClient: ProjectsClient = {
  async getProject(id) {
    await latency();
    const p = project(id);
    return structuredClone(p.detail);
  },
  async updateProject(id, body) {
    await latency();
    const p = project(id);
    if (p.detail.role !== 'owner')
      fail('access_denied', '只有所有者能改项目设置', {}, 403);
    if (body.expected_version !== p.detail.version)
      fail('version_conflict', '设置已被别人改过，请刷新后再改', {}, 409);
    if (body.name !== undefined)
      p.detail.name = body.name.trim() || p.detail.name;
    if (body.settings)
      p.detail.settings = { ...p.detail.settings, ...body.settings };
    p.detail.project_cap_microusd = p.detail.settings.project_cap_microusd;
    p.detail.version += 1;
    return structuredClone(p.detail);
  },
  async dissolveProject(id, confirmName) {
    await latency();
    const p = shared(id);
    if (p.detail.role !== 'owner')
      fail('access_denied', '只有所有者能解散项目', {}, 403);
    if (confirmName.trim().toLowerCase() !== p.detail.name.toLowerCase())
      fail('confirm_mismatch', '项目名不匹配');
    state.own = {
      detail: {
        ...p.detail,
        name: `${USERS[state.viewer].username} 的项目`,
        upgraded_at: null,
        member_count: 1,
        pending_invites: 0,
        allocation: {
          member_caps_microusd: 0,
          unlimited_members: 1,
        },
      },
      tokens: p.tokens.filter((t) => t.owner?.id === state.viewer),
      topups: p.topups,
    };
    state.shared = null;
  },

  async listMembers(id) {
    await latency();
    requireManager(id);
    return structuredClone(
      shared(id).members.filter((m) => m.status !== 'removed'),
    );
  },
  async getMember(id, userId) {
    await latency();
    requireManager(id);
    const p = shared(id);
    const m = p.members.find(
      (x) => x.user_id === userId && x.status !== 'removed',
    );
    if (!m) fail('project_member_not_found', '成员不存在', {}, 404);
    const keys = p.tokens.filter((t) => t.owner?.id === userId);
    const usage_7d = Array.from({ length: 7 }, (_, i) => {
      const d = new Date((now() - (6 - i) * DAY) * 1000);
      const base = m!.month_spend_microusd / 9;
      return {
        date: d.toISOString().slice(0, 10),
        spent_microusd: Math.round(
          base * [0.6, 0.8, 0.76, 0.92, 0.8, 2, 1.2][i],
        ),
        request_count: 800 + i * 90,
        rejected_count: i === 6 ? m!.rejected_count_today : 0,
      };
    });
    return structuredClone({
      ...m!,
      keys,
      budget_history: p.history[userId] ?? [],
      usage_7d,
    });
  },
  async listBudgetHistory(id, userId, page) {
    await latency();
    const p = shared(id);
    const items = p.history[userId] ?? [];
    return {
      items: structuredClone(items),
      total: items.length,
      page,
      page_size: 20,
    };
  },
  async setMemberRole(id, userId, role) {
    await latency();
    const p = shared(id);
    if (p.detail.role !== 'owner')
      fail('access_denied', '只有所有者能指定或取消管理员', {}, 403);
    const m = p.members.find((x) => x.user_id === userId)!;
    if (m.role === 'owner') fail('cannot_change_owner', '所有者不能降级');
    m.role = role;
    m.version += 1;
    return structuredClone(m);
  },
  async setMemberBudget(id, userId, body) {
    await latency();
    const p = shared(id);
    const me = p.detail.role;
    const m = p.members.find((x) => x.user_id === userId)!;
    if (userId === state.viewer)
      fail('cannot_edit_own_budget', '不能改自己的预算');
    if (
      me === 'member' ||
      (me === 'admin' && m.role !== 'member') ||
      m.role === 'owner'
    )
      fail('access_denied', '没有权限改这位成员的预算', {}, 403);
    if (body.expected_version !== m.version)
      fail('version_conflict', '这一行已经被别人改过，刷新后再改', {}, 409);
    const old = m.budget_microusd;
    m.budget_microusd = body.budget_microusd;
    m.warning_pct = body.warning_pct;
    if (body.allowed_models) m.allowed_models = [...body.allowed_models];
    if (body.reasoning_effort_cap !== undefined) {
      m.reasoning_effort_cap = body.reasoning_effort_cap;
      for (const k of p.tokens)
        if (k.owner?.id === userId)
          k.member_effort_cap = body.reasoning_effort_cap;
    }
    m.version += 1;
    (p.history[userId] ??= []).unshift({
      id: nextId('bh'),
      created_at: now(),
      changed_by: ref(state.viewer),
      old_budget_microusd: old,
      new_budget_microusd: body.budget_microusd,
      reason: '管理员修改',
    });
    recalcAllocation(p);
    return structuredClone({ member: m, allocation: p.detail.allocation });
  },
  async setMemberStatus(id, userId, status) {
    await latency();
    const p = shared(id);
    const m = p.members.find((x) => x.user_id === userId)!;
    if (
      p.detail.role === 'member' ||
      m.role === 'owner' ||
      userId === state.viewer ||
      (p.detail.role === 'admin' && m.role !== 'member')
    )
      fail('access_denied', '没有权限', {}, 403);
    m.status = status;
    m.version += 1;
    p.tokens
      .filter((t) => t.owner?.id === userId)
      .forEach((t) => {
        t.status = status === 'active' ? 'enabled' : 'disabled';
      });
    return structuredClone(m);
  },
  async removeMember(id, userId) {
    await latency();
    const p = shared(id);
    const m = p.members.find((x) => x.user_id === userId)!;
    if (
      p.detail.role === 'member' ||
      m.role === 'owner' ||
      userId === state.viewer ||
      (p.detail.role === 'admin' && m.role !== 'member')
    )
      fail('access_denied', '没有权限', {}, 403);
    const affected = p.tokens
      .filter((t) => t.owner?.id === userId && t.status === 'enabled')
      .map((t) => t.id);
    p.tokens.forEach((t) => {
      if (affected.includes(t.id)) {
        t.status = 'disabled';
        t.stopped_by_removal = true;
      }
    });
    const removalId = nextId('rm');
    m.removal = { id: removalId, until: now() + 600, keys: affected };
    m.status = 'removed';
    recalcAllocation(p);
    return { removal_id: removalId, undo_until: now() + 600 };
  },
  async undoRemove(id, userId, removalId) {
    await latency();
    const p = shared(id);
    const m = p.members.find((x) => x.user_id === userId)!;
    const removal = m.removal;
    if (!removal || removal.id !== removalId)
      return fail('state_conflict', '这次移出已经不能撤销');
    if (removal.until < now())
      return fail('undo_expired', '10 分钟已过，需要重新邀请');
    const restored: string[] = [];
    const skipped: string[] = [];
    removal.keys.forEach((keyId) => {
      const t = p.tokens.find((x) => x.id === keyId);
      if (t && t.status === 'disabled') {
        t.status = 'enabled';
        t.stopped_by_removal = false;
        restored.push(keyId);
      } else skipped.push(keyId);
    });
    m.status = 'active';
    m.removal = undefined;
    recalcAllocation(p);
    return {
      status: 'active',
      restored_key_ids: restored,
      skipped_key_ids: skipped,
    };
  },
  async leaveProject(id) {
    await latency();
    const p = shared(id);
    if (p.detail.role === 'owner')
      fail('access_denied', '所有者不能离开自己的项目', {}, 403);
    const m = p.members.find((x) => x.user_id === state.viewer)!;
    m.status = 'removed';
    p.tokens.forEach((t) => {
      if (t.owner?.id === state.viewer && t.status === 'enabled') {
        t.status = 'disabled';
        t.stopped_by_removal = true;
      }
    });
  },

  async listInvites(id) {
    await latency();
    requireManager(id);
    return structuredClone(shared(id).invites);
  },
  async createInvite(id, body) {
    await latency();
    const email = body.email.trim().toLowerCase();
    if (!email.includes('@')) fail('invite_email_invalid', '邮箱格式不对');
    const p = project(id);
    const me = p.detail.role;
    if (me === 'member' || (me === 'admin' && body.role !== 'member'))
      fail('access_denied', '管理员只能邀请成员', {}, 403);
    if (p.kind === 'own') {
      // First invitation upgrades the default project in place.
      const name =
        body.project_name?.trim() || `${USERS[state.viewer].username} 的项目`;
      const detail: ProjectDetail = {
        ...p.detail,
        name,
        upgraded_at: now(),
        member_count: 1,
        pending_invites: 1,
        version: p.detail.version + 1,
      };
      state.shared = {
        detail,
        members: [
          {
            user_id: state.viewer,
            username: USERS[state.viewer].username,
            email: USERS[state.viewer].email,
            role: 'owner',
            status: 'active',
            joined_at: now() - 200 * DAY,
            invited_by: null,
            budget_microusd: null,
            warning_pct: 80,
            version: 1,
            month_spend_microusd: p.detail.month_spend_microusd,
            rejected_count_today: 0,
            key_count: p.tokens.length,
            last_active_at: now(),
          },
        ],
        invites: [],
        tokens: p.tokens,
        requests: [],
        history: {},
        topups: p.topups,
      };
    }
    const sp = state.shared!;
    if (
      sp.members.some(
        (m) => m.status !== 'removed' && m.email.toLowerCase() === email,
      )
    )
      fail('already_member', `${email} 已经在 ${sp.detail.name} 里了`);
    if (
      sp.invites.some(
        (i) => i.status === 'pending' && i.email.toLowerCase() === email,
      )
    )
      fail('already_invited', `${email} 已经有一封待接受的邀请`);
    const invite: ProjectInvite = {
      id: nextId('inv'),
      email,
      role: body.role,
      budget_microusd:
        body.budget_microusd === undefined
          ? sp.detail.settings.default_member_budget_microusd
          : body.budget_microusd,
      status: 'pending',
      expires_at: now() + 7 * DAY,
      invited_by: ref(state.viewer),
      resent_at: null,
    };
    sp.invites.unshift(invite);
    recalcAllocation(sp);
    return structuredClone(invite);
  },
  async resendInvite(id, inviteId) {
    await latency();
    const p = shared(id);
    const invite = p.invites.find((i) => i.id === inviteId)!;
    if (invite.resent_at && now() - invite.resent_at < 60)
      fail(
        'resend_too_soon',
        '刚发过，等一分钟',
        { retry_after_seconds: 60 - (now() - invite.resent_at) },
        429,
      );
    invite.resent_at = now();
    invite.expires_at = now() + 7 * DAY;
    invite.status = 'pending';
    return structuredClone(invite);
  },
  async revokeInvite(id, inviteId) {
    await latency();
    const p = shared(id);
    const invite = p.invites.find((i) => i.id === inviteId)!;
    invite.status = 'revoked';
    recalcAllocation(p);
  },
  async previewInvite(token) {
    await latency();
    ensureScenario();
    if (token === 'expired')
      return {
        project_name: 'Orbit',
        inviter: ref('u_ada'),
        email: 'lin@beichen.dev',
        role: 'member',
        budget_microusd: usd(100),
        expires_at: now() - DAY,
        status: 'expired',
      };
    if (token === 'revoked')
      return {
        project_name: 'Orbit',
        inviter: ref('u_ada'),
        email: 'lin@beichen.dev',
        role: 'member',
        budget_microusd: usd(100),
        expires_at: now() + DAY,
        status: 'revoked',
      };
    return {
      project_name: 'Orbit',
      inviter: ref('u_ada'),
      email: 'lin@beichen.dev',
      role: 'member',
      budget_microusd: usd(100),
      expires_at: now() + 2 * DAY,
      status: 'pending',
    };
  },
  async acceptInvite(token) {
    await latency();
    if (token === 'expired') fail('invite_expired', '邀请已过期', {}, 409);
    if (token === 'revoked') fail('invite_revoked', '邀请已被撤回', {}, 409);
    if (token === 'mismatch')
      fail('invite_email_mismatch', '这封邀请发给的不是你登录的邮箱');
    return { project_id: 'p_orbit', role: 'member' };
  },

  async listMyTokens(id) {
    await latency();
    const p = project(id);
    const list = p.tokens.filter((t) => t.owner?.id === state.viewer);
    return structuredClone(list);
  },
  async listProjectTokens(id) {
    await latency();
    requireManager(id);
    return structuredClone(shared(id).tokens);
  },
  async createToken(id, body) {
    await latency();
    const p = project(id);
    const mine = p.tokens.filter((t) => t.owner?.id === state.viewer);
    if (mine.length >= 200)
      fail('key_limit_reached', '每人每个项目最多 200 把 Key');
    const tokenId = nextId('t');
    const created = token(
      tokenId,
      body.name.trim(),
      state.viewer,
      p.detail.role,
      0,
      {
        ...body,
        model_limits: body.model_limits,
        cap_microusd: body.cap_microusd,
        member_effort_cap:
          'members' in p
            ? (p.members.find((x) => x.user_id === state.viewer)
                ?.reasoning_effort_cap ?? '')
            : '',
        created_at: now(),
        last_used_at: null,
        effective_limit:
          body.cap_microusd !== null
            ? { kind: 'key', remaining_microusd: body.cap_microusd }
            : p.detail.role === 'member'
              ? { kind: 'member', remaining_microusd: usd(9) }
              : { kind: 'project', remaining_microusd: usd(290.8) },
      },
    );
    p.tokens.unshift(created);
    const secret = secretFor(tokenId);
    state.secrets[tokenId] = secret;
    return { token: structuredClone(created), key: secret };
  },
  async updateTokenConnect(tokenId, projectId, fields) {
    await latency();
    const item = project(projectId).tokens.find(
      (token) => token.id === tokenId,
    );
    if (!item || !item.capabilities.can_update)
      fail('access_denied', 'Cannot edit Key', {}, 403);
    if (
      fields.expected_version !== undefined &&
      item!.version !== fields.expected_version
    )
      fail('version_conflict', 'Key changed', {}, 409);
    if (fields.product !== undefined) item!.product = fields.product;
    if (fields.vendor_groups !== undefined)
      item!.vendor_groups = fields.vendor_groups;
    item!.version += 1;
    return structuredClone(item!);
  },
  async revealToken(tokenId, projectId) {
    await latency();
    const p = project(projectId);
    const t = p.tokens.find((x) => x.id === tokenId);
    if (!t) fail('key_not_found', 'Key 不存在', {}, 404);
    if (!t!.secret_available)
      fail('secret_unavailable', '这把 Key 只存了摘要，看不到完整密钥');
    const isOwn = t!.owner?.id === state.viewer;
    if (!isOwn && p.detail.role === 'member')
      fail('access_denied', '只有本人能看', {}, 403);
    return {
      key: state.secrets[tokenId] ?? secretFor(tokenId),
      notification_status: isOwn ? 'not_required' : 'queued',
    };
  },
  async getToken(tokenId, projectId) {
    await latency();
    const item = project(projectId).tokens.find(
      (entry) => entry.id === tokenId,
    );
    if (!item || !item.capabilities.can_update)
      fail('access_denied', 'Cannot edit Key', {}, 403);
    return structuredClone(item!);
  },
  async updateTokenFull(tokenId, projectId, body) {
    return this.updateToken(
      tokenId,
      projectId,
      body as Parameters<ProjectsClient['updateToken']>[2],
    );
  },
  async updateToken(tokenId, projectId, body) {
    await latency();
    const p = project(projectId);
    const item = p.tokens.find((x) => x.id === tokenId);
    if (!item || !item.capabilities.can_update)
      fail('access_denied', 'Cannot edit Key', {}, 403);
    if (item!.version !== body.expected_version)
      fail('version_conflict', 'Key changed', {}, 409);
    const { expected_version: _expectedVersion, ...policy } = body;
    Object.assign(item!, policy);
    item!.version += 1;
    return structuredClone(item!);
  },
  async setTokenEnabled(tokenId, projectId, enabled, expectedVersion) {
    await latency();
    const p = project(projectId);
    const t = p.tokens.find((x) => x.id === tokenId)!;
    if (t.version !== expectedVersion)
      fail('version_conflict', 'Key changed', {}, 409);
    t.status = enabled ? 'enabled' : 'disabled';
    t.version += 1;
    return structuredClone(t);
  },
  async deleteToken(tokenId, projectId) {
    await latency();
    const p = project(projectId);
    const index = p.tokens.findIndex((x) => x.id === tokenId);
    if (index >= 0) p.tokens.splice(index, 1);
    if (p.kind === 'shared') recalcAllocation(p);
  },
  async rotateToken(tokenId, projectId, oldExpires) {
    await latency();
    const p = project(projectId);
    const t = p.tokens.find((x) => x.id === tokenId)!;
    const secret = secretFor(`${tokenId}${t.version + 1}`);
    state.secrets[tokenId] = secret;
    t.masked_key = mask(secret);
    t.secret_available = true;
    t.version += 1;
    return {
      key: secret,
      old_expires_at: oldExpires === '24h' ? now() + DAY : null,
    };
  },
  async listProjectModels() {
    await latency();
    return [...state.models];
  },

  async getMyBudget(id) {
    await latency();
    const p = shared(id);
    const m = p.members.find((x) => x.user_id === state.viewer)!;
    const budget = m.budget_microusd;
    const spent = m.month_spend_microusd;
    const dayOfMonth = new Date().getUTCDate();
    const next = new Date(
      Date.UTC(new Date().getUTCFullYear(), new Date().getUTCMonth() + 1, 1),
    );
    return {
      budget_microusd: budget,
      spent_microusd: spent,
      remaining_microusd: budget === null ? null : Math.max(0, budget - spent),
      daily_avg_microusd: Math.round(spent / Math.max(1, dayOfMonth)),
      resets_at: Math.floor(next.getTime() / 1000),
      warning_pct: m.warning_pct,
      version: m.version,
      project_cap: {
        cap_microusd: p.detail.project_cap_microusd,
        spent_microusd: p.detail.month_spend_microusd,
      },
      balance_days_left: 37,
      history: structuredClone(p.history[state.viewer] ?? []),
      reasoning_effort_cap: m.reasoning_effort_cap ?? '',
    };
  },
  async listBudgetRequests(id, page) {
    await latency();
    const p = shared(id);
    const items =
      p.detail.role === 'member'
        ? p.requests.filter((r) => r.user.id === state.viewer)
        : p.requests;
    return {
      items: structuredClone(items),
      total: items.length,
      page,
      page_size: 20,
    };
  },
  async createBudgetRequest(id, body) {
    await latency();
    const p = shared(id);
    if (
      p.requests.some(
        (r) => r.user.id === state.viewer && r.status === 'pending',
      )
    )
      fail('request_pending', '已经有一个申请在等处理');
    const request: BudgetRequest = {
      id: nextId('br'),
      user: ref(state.viewer),
      requested_microusd: body.requested_microusd,
      reason: body.reason.trim(),
      status: 'pending',
      created_at: now(),
      handled_by: null,
      handled_note: null,
      handled_at: null,
    };
    p.requests.unshift(request);
    return structuredClone(request);
  },
  async withdrawBudgetRequest(id, requestId) {
    await latency();
    const p = shared(id);
    const r = p.requests.find((x) => x.id === requestId)!;
    if (r.status !== 'pending')
      fail('state_conflict', '申请已经处理过了', {}, 409);
    r.status = 'withdrawn';
    return structuredClone(r);
  },
  async approveBudgetRequest(id, requestId, budgetMicro) {
    await latency();
    requireManager(id);
    const p = shared(id);
    const r = p.requests.find((x) => x.id === requestId)!;
    if (r.status !== 'pending')
      fail('state_conflict', '申请已经处理过了', {}, 409);
    const m = p.members.find((x) => x.user_id === r.user.id)!;
    if (m.role !== 'member')
      fail('access_denied', '只能处理普通成员的申请', {}, 403);
    const old = m.budget_microusd;
    m.budget_microusd = budgetMicro;
    m.version += 1;
    r.status = 'approved';
    r.handled_by = ref(state.viewer);
    r.handled_at = now();
    r.approved_budget_microusd = budgetMicro;
    (p.history[r.user.id] ??= []).unshift({
      id: nextId('bh'),
      created_at: now(),
      changed_by: ref(state.viewer),
      old_budget_microusd: old,
      new_budget_microusd: budgetMicro,
      reason: '你申请的',
    });
    recalcAllocation(p);
    return structuredClone(r);
  },
  async rejectBudgetRequest(id, requestId, note) {
    await latency();
    requireManager(id);
    const p = shared(id);
    const r = p.requests.find((x) => x.id === requestId)!;
    if (r.status !== 'pending')
      fail('state_conflict', '申请已经处理过了', {}, 409);
    r.status = 'rejected';
    r.handled_by = ref(state.viewer);
    r.handled_at = now();
    r.handled_note = note.trim();
    return structuredClone(r);
  },

  async getAttention(id, page) {
    await latency();
    requireManager(id);
    const p = shared(id);
    const items: Attention['rejections']['items'] = [];
    p.tokens.forEach((t) => {
      t.rejected_today.forEach((rej) => {
        items.push({
          actor: t.owner
            ? { id: t.owner.id, username: t.owner.username }
            : null,
          token: { id: t.id, name: t.name },
          reason: rej.reason,
          count: rej.count,
          last_at: now() - 900,
        });
      });
    });
    const refused = new Set(
      items
        .filter((i) => i.reason === 'member_budget_exceeded' && i.actor)
        .map((i) => i.actor!.id),
    );
    const nearBudget: Attention['near_budget']['items'] = p.members
      .filter(
        (m) =>
          m.status === 'active' &&
          m.role !== 'owner' &&
          m.budget_microusd !== null &&
          m.budget_microusd > 0 &&
          m.month_spend_microusd * 100 >= m.budget_microusd * m.warning_pct &&
          !refused.has(m.user_id),
      )
      .map((m) => ({
        actor: { id: m.user_id, username: m.username },
        spent_microusd: m.month_spend_microusd,
        budget_microusd: m.budget_microusd!,
        warning_pct: m.warning_pct,
        exhausted: m.month_spend_microusd >= m.budget_microusd!,
      }))
      .sort(
        (a, b) =>
          b.spent_microusd / b.budget_microusd -
          a.spent_microusd / a.budget_microusd,
      );
    const dayStart = Math.floor(now() / DAY) * DAY;
    return {
      as_of: now(),
      window_start: dayStart,
      window_end: dayStart + DAY,
      rejected_count_today: items.reduce((sum, i) => sum + i.count, 0),
      pending_invites_count: p.invites.filter((i) => i.status === 'pending')
        .length,
      pending_budget_requests_count: p.requests.filter(
        (r) => r.status === 'pending',
      ).length,
      balance_days_left: 37,
      rejections: { items, total: items.length, page, page_size: 20 },
      near_budget: { items: nearBudget, total: nearBudget.length },
    };
  },

  async getWallet(id) {
    await latency();
    const p = project(id);
    const role = p.detail.role;
    if (role === 'member')
      return { balance_microusd: p.detail.balance_microusd, days_left: 37 };
    return {
      balance_microusd: p.detail.balance_microusd,
      month_spend_microusd: p.detail.month_spend_microusd,
      month_topup_microusd: p.topups
        .filter((t) => t.created_at > now() - 30 * DAY)
        .reduce((s, t) => s + t.credited_microusd, 0),
      daily_avg_microusd: Math.round(
        p.detail.month_spend_microusd / Math.max(1, new Date().getUTCDate()),
      ),
      days_left: p.kind === 'shared' ? 37 : 53,
      project_cap: {
        cap_microusd: p.detail.project_cap_microusd,
        spent_microusd: p.detail.month_spend_microusd,
      },
      billing_details:
        p.kind === 'shared'
          ? {
              name: 'Orbit Labs GmbH',
              address: {
                line1: 'Friedrichstraße 12',
                line2: null,
                city: 'Berlin',
                state: null,
                postal_code: '10117',
                country: 'DE',
              },
              tax_ids: [{ type: 'eu_vat', value: 'DE123456789' }],
            }
          : null,
      capabilities: {
        can_topup:
          role === 'owner' || p.detail.settings.who_can_topup === 'owner_admin',
        can_manage_billing: role === 'owner',
      },
    };
  },
  async listTopups(id, page) {
    await latency();
    const p = project(id);
    if (p.detail.role === 'member')
      fail('access_denied', '成员看不到充值记录', {}, 403);
    return {
      items: structuredClone(p.topups),
      total: p.topups.length,
      page,
      page_size: 20,
    };
  },
  async getTopup(id, topupId) {
    await latency();
    const p = project(id);
    const t = p.topups.find((x) => x.id === topupId);
    if (!t) fail('payment_not_found', '找不到这笔充值', {}, 404);
    // Simulate the invoice becoming ready a few seconds after credit.
    if (t!.invoice_status === 'pending' && now() - t!.created_at > 6) {
      t!.invoice_status = 'ready';
      t!.invoice = {
        number: `F7A2C1D9-${String(1000 + p.topups.length).slice(-4)}`,
        hosted_url: 'https://invoice.stripe.com/i/example',
        pdf_url: 'https://pay.stripe.com/invoice/example/pdf',
        bill_to: { name: 'Orbit Labs GmbH', address: null, tax_ids: [] },
      };
    }
    return structuredClone(t!);
  },
  async cancelTopup(id, topupId) {
    await latency();
    const p = project(id);
    const t = p.topups.find((x) => x.id === topupId);
    if (!t) fail('payment_not_found', '找不到这笔充值', {}, 404);
    if (t!.payment_status !== 'pending')
      fail('state_conflict', '这笔订单已经结束', {}, 409);
    t!.payment_status = 'canceled';
    delete t!.pay_link;
    delete t!.pay_by;
    return structuredClone(t!);
  },
  async getTopupDocument(id, topupId, document) {
    const topup = await this.getTopup(id, topupId);
    const url =
      document === 'invoice'
        ? topup.invoice?.pdf_url
        : document === 'invoice_page'
          ? topup.invoice?.hosted_url
          : topup.receipt_url;
    if (!url) fail('invoice_unavailable', 'Document unavailable', {}, 404);
    return { url: url! };
  },
  async startTopup(id, amountUSD) {
    await latency();
    const p = project(id);
    const t: ProjectTopup = {
      id: nextId('tu'),
      created_at: now(),
      paid_amount_microusd: usd(amountUSD),
      credited_microusd: usd(amountUSD),
      paid_by: ref(state.viewer),
      method: 'stripe · Visa ···4242',
      payment_status: 'paid',
      invoice_status: 'pending',
      invoice: null,
      receipt_url: 'https://pay.stripe.com/receipts/example',
    };
    p.topups.unshift(t);
    p.detail.balance_microusd =
      (p.detail.balance_microusd ?? 0) + t.credited_microusd;
    return {
      pay_link: `/payment/success?project_id=${id}&top_up_id=${t.id}&mock=1`,
      top_up_id: t.id,
    };
  },
  async getPortalUrl(id) {
    await latency();
    if (viewerRole(id) !== 'owner')
      fail('access_denied', '只有所有者能进账单门户', {}, 403);
    return { url: 'https://billing.stripe.com/p/session/example' };
  },
};

export const mockRejectReasonForKey = (id: string) => REJECT_REASON_FOR_KEY[id];

export function mockTokenVendorOptions() {
  const vendors = [
    { id: 'v_mock_anthropic', name: 'Anthropic' },
    { id: 'v_mock_openai', name: 'OpenAI' },
    { id: 'v_mock_deepseek', name: 'DeepSeek' },
    { id: 'v_mock_google', name: 'Google' },
  ];
  return {
    vendors,
    models: MODELS.map((model) => ({
      model,
      endpointTypes: ['openai'],
      vendorID: model.startsWith('claude')
        ? vendors[0].id
        : model.startsWith('gpt')
          ? vendors[1].id
          : model.startsWith('deepseek')
            ? vendors[2].id
            : vendors[3].id,
      lanes: [
        { key: 'direct' as const, available: true },
        { key: 'cheap' as const, available: true },
      ],
    })),
  };
}
