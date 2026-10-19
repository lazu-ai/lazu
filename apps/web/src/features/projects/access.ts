/* Actor × target × action matrix from
 * brand/foundations/team-projects-2026-09.md "Permissions". The backend is
 * the authority; this only decides what the console offers, never what it
 * allows. Everything here assumes the actor's membership is active. */
import type { ProjectRole, ProjectSettings } from './types';

const isManager = (role: ProjectRole) => role !== 'member';

/** Owner manages admins and members; admin manages ordinary members only.
 *  Nobody manages themselves through the member matrix. */
export const canManageMember = (
  actor: ProjectRole,
  target: ProjectRole,
  isSelf = false,
): boolean => {
  if (isSelf) return false;
  if (actor === 'owner') return target !== 'owner';
  if (actor === 'admin') return target === 'member';
  return false;
};

export const canInviteRole = (
  actor: ProjectRole,
  role: Exclude<ProjectRole, 'owner'>,
): boolean => {
  if (actor === 'owner') return true;
  if (actor === 'admin') return role === 'member';
  return false;
};

export const canManageInvite = (
  actor: ProjectRole,
  inviteRole: Exclude<ProjectRole, 'owner'>,
) => canInviteRole(actor, inviteRole);

export const canChangeRole = (actor: ProjectRole, target: ProjectRole) =>
  actor === 'owner' && target !== 'owner';

export const canEditBudget = (
  actor: ProjectRole,
  target: ProjectRole,
  isSelf = false,
) => canManageMember(actor, target, isSelf);

export const canRemoveMember = canManageMember;

/** A Key belongs to one member. The member always sees their own; the owner
 *  and admins see the Keys of the people they manage, and every such reveal
 *  is logged and notifies the member. */
export const canRevealKey = (
  actor: ProjectRole,
  keyOwnerRole: ProjectRole,
  isOwnKey: boolean,
  projectShared: boolean,
): boolean => {
  if (isOwnKey) return true;
  if (!projectShared) return false;
  return canManageMember(actor, keyOwnerRole);
};

export const canTopUp = (actor: ProjectRole, settings: ProjectSettings) =>
  actor === 'owner' ||
  (actor === 'admin' && settings.who_can_topup === 'owner_admin');

export const canManageBilling = (actor: ProjectRole) => actor === 'owner';

export const canHandleBudgetRequest = (
  actor: ProjectRole,
  requesterRole: ProjectRole,
) => isManager(actor) && requesterRole === 'member';

/* Whether this reader is the owner of a project that has nobody else in it.
 * One rule behind all three "start a team" entries — the sidebar group, the
 * wallet card and the fork in front of creating a Key — so a project that
 * already has people stops offering to start one everywhere at once. */
export const isSoloOwner = (scope: {
  enabled: boolean;
  isShared: boolean;
  role: ProjectRole;
  status: string;
}) =>
  scope.enabled &&
  !scope.isShared &&
  scope.status === 'active' &&
  scope.role === 'owner';
