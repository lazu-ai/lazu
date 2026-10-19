/* The six project rejection reasons, each with fixed copy and one action.
 * Anything else the relay returns (invalid key, rate limit, platform budget)
 * keeps its own code and falls back to a safe generic explanation. Copy lives
 * in the locale bundles under `projects.reason.*`. */
import type { ProjectRole, RejectionReason } from './types';

export type RejectionTone = 'bad' | 'warn' | 'evi' | 'mute';

export type RejectionAction =
  | 'request_budget'
  | 'edit_key'
  | 'ask_admin'
  | 'top_up'
  | 'contact_admin'
  | 'none';

export type RejectionSpec = {
  code: RejectionReason;
  tone: RejectionTone;
  /** Which side of the project can act on it. */
  actor: 'member' | 'admin';
  action: RejectionAction;
};

export const REJECTION_REASONS: readonly RejectionReason[] = [
  'member_budget_exceeded',
  'key_budget_exceeded',
  'project_budget_exceeded',
  'model_not_allowed',
  'insufficient_quota',
  'project_access_suspended',
] as const;

const SPECS: Record<RejectionReason, RejectionSpec> = {
  member_budget_exceeded: {
    code: 'member_budget_exceeded',
    tone: 'bad',
    actor: 'member',
    action: 'request_budget',
  },
  key_budget_exceeded: {
    code: 'key_budget_exceeded',
    tone: 'warn',
    actor: 'member',
    action: 'edit_key',
  },
  project_budget_exceeded: {
    code: 'project_budget_exceeded',
    tone: 'warn',
    actor: 'admin',
    action: 'ask_admin',
  },
  model_not_allowed: {
    code: 'model_not_allowed',
    tone: 'evi',
    actor: 'member',
    action: 'edit_key',
  },
  insufficient_quota: {
    code: 'insufficient_quota',
    tone: 'warn',
    actor: 'admin',
    action: 'top_up',
  },
  project_access_suspended: {
    code: 'project_access_suspended',
    tone: 'bad',
    actor: 'admin',
    action: 'contact_admin',
  },
};

export const isProjectRejection = (code: string): code is RejectionReason =>
  Object.prototype.hasOwnProperty.call(SPECS, code);

export const rejectionSpec = (code: string): RejectionSpec | null =>
  isProjectRejection(code) ? SPECS[code] : null;

/** Locale key of the human explanation for a code. Unknown codes get the
 *  generic fallback and never masquerade as "insufficient quota". */
export const rejectionLabelKey = (code: string): string =>
  isProjectRejection(code)
    ? `projects.reason.${code}.label`
    : 'projects.reason.unknown.label';

export const rejectionEffectKey = (code: string): string =>
  isProjectRejection(code)
    ? `projects.reason.${code}.effect`
    : 'projects.reason.unknown.effect';

/** The one action the UI offers. Members and admins see different verbs for
 *  admin-side reasons (a member asks; an admin fixes). */
export const rejectionActionKey = (
  code: string,
  viewerRole: ProjectRole,
): string => {
  const spec = rejectionSpec(code);
  if (!spec) return 'projects.reason.unknown.action';
  const side = viewerRole === 'member' ? 'member' : 'admin';
  return `projects.reason.${spec.code}.action.${side}`;
};

export const rejectionTone = (code: string): RejectionTone =>
  rejectionSpec(code)?.tone ?? 'mute';

/** Deep link into 用量与日志 filtered by this reason (and optionally a Key). */
export const rejectionUsageHref = (
  projectId: string,
  code: string,
  tokenId?: string,
) => {
  const params = new URLSearchParams({
    tab: 'logs',
    project_id: projectId,
    result: 'rejected',
    reason: code,
  });
  if (tokenId) params.set('token_id', tokenId);
  return `/console/usage?${params.toString()}`;
};

/** Match the UTC month-to-date count of lowered requests on a Key. */
export const effortUsageHref = (
  projectId: string,
  tokenId: string,
  now = new Date(),
) => {
  const start = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1);
  return `/console/usage?${new URLSearchParams({
    tab: 'logs',
    project_id: projectId,
    token_id: tokenId,
    effort_clamped: 'true',
    start_timestamp: String(Math.floor(start / 1000)),
    end_timestamp: String(Math.floor(now.getTime() / 1000)),
  }).toString()}`;
};
