// Shared types and pure helpers for the customer-accounts console page.
// The page renders the platform `users` table as customer accounts; a row is
// one account, not a real multi-member organization.

type ClientTop = {
  country?: string;
  framework?: string;
};

export type Account = {
  balance_microusd?: number;
  client_top?: ClientTop | null;
  /** Unix seconds the account was deleted; absent for a live account. */
  deleted_at?: number;
  email?: string;
  has_topped_up?: boolean;
  id: number | string;
  internal_id?: number;
  key_count?: number | null;
  last_used_at?: number | null;
  login_providers?: string[];
  name: string;
  note?: string;
  owner_email?: string;
  price_plan_id?: number;
  price_plan_name?: string;
  recent_spend_days?: number;
  recent_spend_microusd?: number | null;
  request_count_recent?: number | null;
  /** Unix seconds until which signing in restores a deleted account. */
  restore_until?: number;
  role?: number;
  rpm_limit?: number;
  status?: number;
  topup_total_microusd?: number | null;
  tpm_limit?: number;
  used_quota_microusd?: number;
  username?: string;
};

/** The customer's 专属折扣, as the account summary reports it. */
type AccountPricePlan = {
  default_pct?: number | null;
  ends_at?: string | null;
  id: number;
  lanes?: string;
  live?: boolean;
  name: string;
  starts_at?: string | null;
};

/** Effective ceilings plus the account's own overrides (0 = none). */
type AccountRateLimit = {
  enabled?: boolean;
  rpm?: number;
  rpm_override?: number;
  tpm?: number;
  tpm_override?: number;
};

/** Where an account came from (internal/model/signup_attribution.go). */
type SignupAttribution = {
  campaign?: string;
  content?: string;
  country?: string;
  invite_code?: string;
  landing_path?: string;
  medium?: string;
  /** password / email_code / oidc / <oauth provider> / oauth / other */
  method?: string;
  referrer_host?: string;
  source?: string;
  term?: string;
};

export type AccountSignup = {
  attribution?: SignupAttribution | null;
  created_at?: number;
  /** "exact" when stamped at registration; anything else was inferred. */
  created_at_source?: string;
  first_request_at?: number;
  first_topup_at?: number;
  last_request_at?: number;
};

/** One account deletion, kept after the account is cleared. */
type AccountDeletion = {
  deleted_by?: string;
  email?: string;
  purged_at?: number;
  username?: string;
  written_off_microusd?: number;
};

export type AccountSummary = {
  agent_sessions?: number;
  client_breakdown?: { count: number; framework: string }[];
  countries?: { count: number; country: string }[];
  /** Unix seconds the account was deleted; 0 for a live account. */
  deleted_at?: number;
  /** The newest deletion record of a deleted account. */
  deletion?: AccountDeletion | null;
  display_name?: string;
  email?: string;
  id?: number;
  internal_id?: number;
  last_used_at?: number;
  login_providers?: string[];
  media_jobs?: { failed: number; total: number };
  price_plan?: AccountPricePlan | null;
  quota?: number;
  rate_limit?: AccountRateLimit;
  recent_spend_microusd?: number;
  remark?: string;
  request_count_recent?: number;
  /** Unix seconds until which signing in restores it; 0 when not restorable. */
  restore_until?: number;
  role?: number;
  settlement_failures?: { open: number };
  signup?: AccountSignup;
  status?: number;
  storage_bytes_used?: number;
  storage_quota_bytes?: number;
  topup_total_microusd?: number;
  used_quota_microusd?: number;
  username?: string;
};

export type PlatformSummary = {
  active_30d?: number;
  /** Soft-deleted accounts of every role. */
  deleted?: number;
  disabled?: number;
  low_balance_active?: number;
  on_price_plan?: number;
  platform_members?: number;
  topped_up?: number;
  total_customers?: number;
  zero_topped_up?: number;
};

export type AccountKey = {
  allow_ips?: string | null;
  created_time?: number;
  expired_time?: number;
  group?: string;
  id?: string;
  key_prefix?: string;
  key_suffix?: string;
  last_used_at?: number;
  model_limits?: string;
  name?: string;
  remain_quota?: number;
  rpm_limit?: number;
  status?: number;
  tpm_limit?: number;
  unlimited_quota?: boolean;
  used_quota?: number;
};

export type AccountKeyOperationalState =
  | 'account_disabled'
  | 'disabled'
  | 'enabled'
  | 'expired';

export type AdjacentAccountTarget =
  | { account: Account; kind: 'row' }
  | { kind: 'page'; page: number; pick: 'first' | 'last' }
  | null;

/** Platform roles live in helpers/roles; re-exported for the customer modal. */
export { ROLE_ADMIN } from '../../helpers/roles';

/**
 * Resolve adjacent-account navigation without treating a paginated page as
 * the complete customer list. Page targets are fetched before the modal is
 * moved so J/K navigation stays continuous across page boundaries.
 */
export const adjacentAccountTarget = (
  rows: Account[],
  activeID: Account['id'] | null | undefined,
  page: number,
  pageSize: number,
  total: number,
  direction: 'next' | 'previous',
): AdjacentAccountTarget => {
  if (activeID == null) return null;
  const index = rows.findIndex((row) => String(row.id) === String(activeID));
  if (index < 0) return null;
  if (direction === 'previous') {
    if (index > 0) return { account: rows[index - 1], kind: 'row' };
    return page > 1 ? { kind: 'page', page: page - 1, pick: 'last' } : null;
  }
  if (index < rows.length - 1) {
    return { account: rows[index + 1], kind: 'row' };
  }
  return page * pageSize < total
    ? { kind: 'page', page: page + 1, pick: 'first' }
    : null;
};

export const usd = (micro?: number | null) =>
  micro == null ? '—' : `$${(micro / 1_000_000).toFixed(2)}`;

export const int = (value?: number | null) =>
  value == null ? '—' : value.toLocaleString();

export const isFrozen = (account: Pick<Account, 'status'>) =>
  account.status !== 1;

export const isZeroBalance = (account: Account) =>
  !isFrozen(account) && (account.balance_microusd ?? 0) <= 0;

/** Account suspension always wins over the persisted per-key status. */
export const accountKeyOperationalState = (
  account: Pick<Account, 'status'>,
  key: Pick<AccountKey, 'expired_time' | 'status'>,
  nowUnixSeconds = Math.floor(Date.now() / 1000),
): AccountKeyOperationalState => {
  if (isFrozen(account)) return 'account_disabled';
  if (
    key.expired_time &&
    key.expired_time > 0 &&
    key.expired_time <= nowUnixSeconds
  ) {
    return 'expired';
  }
  return key.status === 1 ? 'enabled' : 'disabled';
};

/** Count operational (non-expired, enabled, live-account) API keys. */
export const countOperationalKeys = (
  account: Pick<Account, 'status'>,
  keys: ReadonlyArray<Pick<AccountKey, 'expired_time' | 'status'>>,
  nowUnixSeconds = Math.floor(Date.now() / 1000),
): number =>
  keys.reduce(
    (sum, key) =>
      accountKeyOperationalState(account, key, nowUnixSeconds) === 'enabled'
        ? sum + 1
        : sum,
    0,
  );

export const initials = (account: Pick<Account, 'name' | 'owner_email'>) =>
  (account.name || account.owner_email || '?').trim().slice(0, 2).toUpperCase();

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);

const optionalNumber = (value: unknown) =>
  value === undefined || value === null ? null : Number(value);

/** The list carries gorm's DeletedAt: an RFC 3339 time, or null when live. */
const deletedAtSeconds = (value: unknown): number | undefined => {
  if (typeof value !== 'string' || !value) return undefined;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : undefined;
};

/** Maps one server user record onto the Account view model. */
export const toAccount = (row: Record<string, unknown>): Account => {
  const username = String(row.username || '');
  const displayName = String(row.display_name || '');
  const email = String(row.email || '');
  const clientTop = isRecord(row.client_top)
    ? {
        framework: String(row.client_top.framework || ''),
        country: String(row.client_top.country || ''),
      }
    : null;
  return {
    id: (row.id as number | string) ?? username,
    internal_id: Number(row.internal_id ?? 0) || undefined,
    name: displayName || username || email || String(row.name || ''),
    owner_email: email || String(row.owner_email || ''),
    email,
    username,
    note: String(row.remark || row.note || ''),
    role: Number(row.role ?? 1),
    status: Number(row.status ?? 1),
    balance_microusd: Number(row.quota ?? row.balance_microusd ?? 0),
    used_quota_microusd: Number(row.used_quota ?? 0),
    key_count: optionalNumber(row.token_count),
    recent_spend_microusd: optionalNumber(row.recent_spend_microusd),
    recent_spend_days: Number(row.recent_spend_days ?? 0) || undefined,
    request_count_recent: optionalNumber(row.request_count_recent),
    last_used_at: optionalNumber(row.last_used_at),
    topup_total_microusd: optionalNumber(row.topup_total_microusd),
    has_topped_up: row.has_topped_up === true,
    price_plan_id: Number(row.price_plan_id ?? 0) || 0,
    price_plan_name: String(row.price_plan_name || ''),
    rpm_limit: Number(row.rpm_limit ?? 0) || 0,
    tpm_limit: Number(row.tpm_limit ?? 0) || 0,
    login_providers: Array.isArray(row.login_providers)
      ? row.login_providers.map(String)
      : undefined,
    client_top: clientTop,
    deleted_at: deletedAtSeconds(row.DeletedAt),
    restore_until: Number(row.restore_until ?? 0) || undefined,
  };
};

export const isDeleted = (account: Pick<Account, 'deleted_at'>) =>
  Boolean(account.deleted_at);

/** Days-to-empty estimate: balance ÷ daily burn over the recent window.
 *  Returns null when there is no measurable burn (never billed / no spend). */
export const daysToEmpty = (account: Account): number | null => {
  const balance = account.balance_microusd ?? 0;
  const spend = account.recent_spend_microusd ?? 0;
  const days = account.recent_spend_days || 30;
  if (spend <= 0 || balance <= 0) return null;
  const perDay = spend / days;
  if (perDay <= 0) return null;
  return balance / perDay;
};

export const providerLabel = (provider: string): string => {
  const map: Record<string, string> = {
    google: 'Google',
    github: 'GitHub',
    oidc: 'OIDC',
    email: '邮箱',
    password: '密码',
  };
  return map[provider.toLowerCase()] || provider;
};
