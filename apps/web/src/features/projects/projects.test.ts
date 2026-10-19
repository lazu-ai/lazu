import { describe, expect, it } from 'vitest';
import {
  canChangeRole,
  canEditBudget,
  canInviteRole,
  canManageMember,
  canRevealKey,
  canTopUp,
  isSoloOwner,
} from './access';
import {
  INVITE_VALID_DAYS,
  draftFromMicro,
  lowersBelowSpent,
  validateBudget,
  validateBudgetRequestNote,
  validateTopUpAmount,
} from './budget';
import { budgetReasonText } from './components/atoms';
import {
  budgetTone,
  formatUSD,
  formatUSDShort,
  percentOf,
  formatBudgetResetDate,
} from './money';
import {
  REJECTION_REASONS,
  rejectionActionKey,
  rejectionLabelKey,
  rejectionTone,
  rejectionUsageHref,
  effortUsageHref,
} from './rejections';
import {
  getConsoleScopes,
  getDefaultConsoleScopeId,
  projectScopeId,
} from '../../context/ConsoleScope';
import { getUsageProjectScope } from '../../hooks/usage-logs/usageProjectScope';
import type { ProjectSettings, ProjectSummary } from './types';

const project = (overrides: Partial<ProjectSummary> = {}): ProjectSummary => ({
  id: 'p_1',
  name: 'Orbit',
  role: 'owner',
  status: 'active',
  is_default: true,
  upgraded_at: null,
  owner: { id: 'u_1', username: 'Ada' },
  member_count: 1,
  balance_microusd: 18_200_000,
  ...overrides,
});

describe('ConsoleScope · project mapping', () => {
  it("keeps today's single workspace scope when no projects are given", () => {
    const scopes = getConsoleScopes({ role: 1, workspace_name: 'Acme' }, []);
    expect(scopes).toHaveLength(1);
    expect(scopes[0].id).toBe('workspace:default');
    expect(scopes[0].project).toBeUndefined();
  });

  it('maps one customer scope per project and keeps the platform plane', () => {
    const scopes = getConsoleScopes({ role: 100 }, [
      project(),
      project({
        id: 'p_2',
        name: 'Shared',
        role: 'member',
        is_default: false,
        upgraded_at: 1,
        owner: { id: 'u_9', username: 'Lin' },
      }),
    ]);
    expect(scopes.map((scope) => scope.id)).toEqual([
      projectScopeId('p_1'),
      projectScopeId('p_2'),
      'platform:operations',
    ]);
    expect(scopes[1].project).toMatchObject({
      role: 'member',
      isShared: true,
      isDefault: false,
      ownerName: 'Lin',
    });
    expect(getDefaultConsoleScopeId(scopes)).toBe(projectScopeId('p_1'));
  });

  it("labels an unshared default project like today's workspace", () => {
    const [scope] = getConsoleScopes(
      { role: 1, workspace_name: 'API 工作台' },
      [project()],
    );
    expect(scope.label).toBe('API 工作台');
    expect(scope.project?.isShared).toBe(false);
  });

  it('uses the project name once it has been shared', () => {
    const [scope] = getConsoleScopes({ role: 1 }, [
      project({ upgraded_at: 100 }),
    ]);
    expect(scope.label).toBe('Orbit');
    expect(scope.project?.isShared).toBe(true);
  });
});

describe('rejection reasons', () => {
  it('has six project codes, each with its own copy key', () => {
    expect(REJECTION_REASONS).toHaveLength(6);
    for (const code of REJECTION_REASONS) {
      expect(rejectionLabelKey(code)).toBe(`projects.reason.${code}.label`);
    }
  });

  it('never maps an unknown code onto insufficient quota', () => {
    expect(rejectionLabelKey('rate_limited')).toBe(
      'projects.reason.unknown.label',
    );
    expect(rejectionTone('rate_limited')).toBe('mute');
    expect(rejectionActionKey('rate_limited', 'member')).toBe(
      'projects.reason.unknown.action',
    );
  });

  it('offers the member and admin sides different actions', () => {
    expect(rejectionActionKey('member_budget_exceeded', 'member')).toContain(
      '.member',
    );
    expect(rejectionActionKey('member_budget_exceeded', 'admin')).toContain(
      '.admin',
    );
    expect(rejectionActionKey('insufficient_quota', 'owner')).toContain(
      '.admin',
    );
  });

  it('deep-links into usage with result and reason filters', () => {
    const href = rejectionUsageHref('p_1', 'model_not_allowed', 't_1');
    expect(href).toContain('result=rejected');
    expect(href).toContain('reason=model_not_allowed');
    expect(href).toContain('token_id=t_1');
    expect(getUsageProjectScope(href.slice(href.indexOf('?')))).toEqual({
      project_id: 'p_1',
      result: 'rejected',
      reason: 'model_not_allowed',
      token_id: 't_1',
    });
  });
});

describe('budget validation', () => {
  it('accepts amounts between the bounds and rejects the rest', () => {
    expect(validateBudget({ mode: 'amount', input: '80' })).toEqual({
      ok: true,
      micro: 80_000_000,
    });
    expect(validateBudget({ mode: 'amount', input: '0.5' })).toEqual({
      ok: false,
      error: 'below_min',
    });
    expect(validateBudget({ mode: 'amount', input: '-5' })).toEqual({
      ok: false,
      error: 'invalid',
    });
    expect(validateBudget({ mode: 'amount', input: '100001' })).toEqual({
      ok: false,
      error: 'above_max',
    });
    expect(validateBudget({ mode: 'amount', input: '' })).toEqual({
      ok: false,
      error: 'empty',
    });
  });

  it('treats unlimited as an explicit choice, never as zero', () => {
    expect(validateBudget({ mode: 'unlimited' })).toEqual({
      ok: true,
      micro: null,
    });
    expect(validateBudget({ mode: 'amount', input: '0' })).toEqual({
      ok: true,
      micro: 0,
    });
    expect(draftFromMicro(null)).toEqual({ mode: 'unlimited' });
  });

  it('flags a cap lowered under month-to-date spend', () => {
    expect(lowersBelowSpent(30_000_000, 50_000_000)).toBe(true);
    expect(lowersBelowSpent(null, 50_000_000)).toBe(false);
  });

  it('requires a four-character rejection note and bounded top-ups', () => {
    expect(validateBudgetRequestNote('太少')).toBe(false);
    expect(validateBudgetRequestNote('下月初再申请')).toBe(true);
    expect(validateTopUpAmount(3, 5, 500)).toBe('below_min');
    expect(validateTopUpAmount(50, 5, 500)).toBe('ok');
  });

  it('formats money and tones', () => {
    expect(formatUSD(1_284_600_000)).toBe('$1,284.60');
    expect(formatUSD(null)).toBe('—');
    expect(formatUSDShort(50_000_000)).toBe('$50');
    expect(percentOf(41_000_000, 50_000_000)).toBe(82);
    expect(budgetTone(41_000_000, 50_000_000)).toBe('warn');
    expect(budgetTone(50_000_000, 50_000_000)).toBe('bad');
    expect(budgetTone(10_000_000, null)).toBe('ok');
  });
});

describe('access matrix', () => {
  const settings: ProjectSettings = {
    who_can_topup: 'owner',
    default_member_budget_microusd: null,
    warning_pct: 80,
    project_cap_microusd: null,
  };

  it('lets the owner manage admins and members but never themselves', () => {
    expect(canManageMember('owner', 'admin')).toBe(true);
    expect(canManageMember('owner', 'member')).toBe(true);
    expect(canManageMember('owner', 'owner')).toBe(false);
    expect(canManageMember('owner', 'member', true)).toBe(false);
  });

  it('restricts admins to ordinary members', () => {
    expect(canManageMember('admin', 'member')).toBe(true);
    expect(canManageMember('admin', 'admin')).toBe(false);
    expect(canManageMember('admin', 'owner')).toBe(false);
    expect(canEditBudget('admin', 'member', true)).toBe(false);
    expect(canInviteRole('admin', 'admin')).toBe(false);
    expect(canInviteRole('admin', 'member')).toBe(true);
    expect(canInviteRole('owner', 'admin')).toBe(true);
    expect(canChangeRole('admin', 'member')).toBe(false);
    expect(canChangeRole('owner', 'admin')).toBe(true);
  });

  it('reveals keys by rank and never across an unshared project', () => {
    expect(canRevealKey('member', 'member', true, true)).toBe(true);
    expect(canRevealKey('member', 'member', false, true)).toBe(false);
    expect(canRevealKey('admin', 'owner', false, true)).toBe(false);
    expect(canRevealKey('owner', 'admin', false, true)).toBe(true);
    expect(canRevealKey('owner', 'member', false, false)).toBe(false);
  });

  it('follows who_can_topup', () => {
    expect(canTopUp('admin', settings)).toBe(false);
    expect(
      canTopUp('admin', { ...settings, who_can_topup: 'owner_admin' }),
    ).toBe(true);
    expect(
      canTopUp('member', { ...settings, who_can_topup: 'owner_admin' }),
    ).toBe(false);
    expect(canTopUp('owner', settings)).toBe(true);
  });
});

describe('usage deep links from a project', () => {
  it('carries the member a rejection chip points at', () => {
    /* The "被拒 N 次" link on a member row passes user_id; dropping it showed
       the whole project's rejections under one person's name. */
    const scope = getUsageProjectScope(
      '?tab=logs&project_id=prj_1&user_id=usr_2&result=rejected&reason=member_budget_exceeded&token_id=tok_3',
    );
    expect(scope).toEqual({
      project_id: 'prj_1',
      reason: 'member_budget_exceeded',
      result: 'rejected',
      token_id: 'tok_3',
      user_id: 'usr_2',
    });
  });

  it('keeps an unrelated usage page unscoped', () => {
    expect(getUsageProjectScope('?tab=logs')).toEqual({});
  });
});

describe('budget history reasons', () => {
  const t = (key: string) => key;

  it('names the three enum forms and passes anything else through', () => {
    expect(budgetReasonText('invitation', t)).toBe(
      'projects.member.reason.invitation',
    );
    expect(budgetReasonText('manual', t)).toBe('projects.member.reason.manual');
    expect(budgetReasonText('request:breq_01', t)).toBe(
      'projects.member.reason.request',
    );
    expect(budgetReasonText('something-new', t)).toBe('something-new');
  });
});

describe('invitation dates', () => {
  it('derives the send date from the 3-day validity, not 7', () => {
    /* The row used to subtract a week from expires_at and show a date four
       days before the invitation was actually sent. */
    const sentAt = 1_700_000_000;
    const expiresAt = sentAt + INVITE_VALID_DAYS * 86_400;
    expect(expiresAt - INVITE_VALID_DAYS * 86_400).toBe(sentAt);
  });
});

it('formats the next UTC budget reset in the active language', () => {
  const now = new Date('2026-12-31T23:59:00Z');
  expect(formatBudgetResetDate('en', now)).toBe('January 1');
  expect(formatBudgetResetDate('zh', now)).toBe('1月1日');
  expect(formatBudgetResetDate('ja', now)).toBe('1月1日');
  expect(formatBudgetResetDate('zh-TW', now)).toBe('1月1日');
});

/* The three "start a team" entries share this one rule, so a mistake here
   shows up as an entry the reader should not have — or, worse, as the
   feature being invisible again. */
describe('isSoloOwner', () => {
  const scope = {
    enabled: true,
    isShared: false,
    role: 'owner' as const,
    status: 'active',
  };

  it('is the owner of a project nobody else has joined', () => {
    expect(isSoloOwner(scope)).toBe(true);
  });

  it('stops offering once the project is shared', () => {
    expect(isSoloOwner({ ...scope, isShared: true })).toBe(false);
  });

  it('never offers to a member or an admin', () => {
    expect(isSoloOwner({ ...scope, role: 'member' })).toBe(false);
    expect(isSoloOwner({ ...scope, role: 'admin' })).toBe(false);
  });

  it('stays hidden while the switch is off', () => {
    expect(isSoloOwner({ ...scope, enabled: false })).toBe(false);
  });

  it('stays hidden for a membership that is not active', () => {
    expect(isSoloOwner({ ...scope, status: 'suspended' })).toBe(false);
  });
});

describe('lowered effort evidence links', () => {
  it('carries the exact UTC month and lowered-only scope', () => {
    const href = effortUsageHref(
      'p_1',
      't_1',
      new Date('2026-09-27T03:00:00Z'),
    );
    const params = new URLSearchParams(href.split('?')[1]);
    expect(Number(params.get('start_timestamp'))).toBe(
      Date.parse('2026-09-01T00:00:00Z') / 1000,
    );
    expect(Number(params.get('end_timestamp'))).toBe(
      Date.parse('2026-09-27T03:00:00Z') / 1000,
    );
    expect(getUsageProjectScope(href.slice(href.indexOf('?')))).toEqual({
      project_id: 'p_1',
      token_id: 't_1',
      effort_clamped: 'true',
    });
  });
});
