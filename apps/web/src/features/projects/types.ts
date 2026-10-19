/* Contract types for the team-projects milestone.
 * Source of truth: the project handlers in internal/controller and the
 * project models in internal/model.
 * Money is microUSD (integer); null budget = unlimited; times are UTC unix
 * seconds; ids are opaque public ids. */

export type ProjectRole = 'owner' | 'admin' | 'member';
export type MembershipStatus = 'active' | 'suspended' | 'removed';

export type UserRef = { id: string; username: string };

export type ProjectSettings = {
  who_can_topup: 'owner' | 'owner_admin';
  default_member_budget_microusd: number | null;
  warning_pct: number;
  project_cap_microusd: number | null;
  /** Reasoning-effort cap new members start with; "" = no cap. */
  default_reasoning_effort_cap?: string;
};

/** `/api/user/self` → `projects[]` */
export type ProjectSummary = {
  id: string;
  name: string;
  role: ProjectRole;
  status: MembershipStatus;
  is_default: boolean;
  upgraded_at: number | null;
  owner: UserRef;
  member_count: number;
  balance_microusd: number | null;
};

export type ProjectAllocation = {
  member_caps_microusd: number;
  unlimited_members: number;
};

export type ProjectDetail = {
  id: string;
  name: string;
  owner: UserRef;
  upgraded_at: number | null;
  role: ProjectRole;
  status: MembershipStatus;
  member_count: number;
  pending_invites: number;
  balance_microusd: number | null;
  month_spend_microusd: number;
  project_cap_microusd: number | null;
  version: number;
  allocation: ProjectAllocation;
  settings: ProjectSettings;
};

export type ProjectMember = {
  user_id: string;
  username: string;
  email: string;
  role: ProjectRole;
  status: MembershipStatus;
  joined_at: number;
  invited_by: UserRef | null;
  budget_microusd: number | null;
  warning_pct: number;
  version: number;
  month_spend_microusd: number;
  rejected_count_today: number;
  key_count: number;
  last_active_at: number | null;
  /** Models this member's Keys may call; empty = every project model. */
  allowed_models?: string[];
  /** Highest reasoning effort this member's Keys may use; "" = no cap. */
  reasoning_effort_cap?: string;
  /** Requests this month whose effort the gateway lowered to a cap. */
  effort_downed_month?: number;
};

export type BudgetHistoryEntry = {
  id: string;
  created_at: number;
  changed_by: UserRef | null;
  old_budget_microusd: number | null;
  new_budget_microusd: number | null;
  reason: string;
};

export type UsageDay = {
  date: string;
  spent_microusd: number;
  request_count: number;
  rejected_count: number;
};

export type ProjectMemberDetail = ProjectMember & {
  keys: ProjectToken[];
  budget_history: BudgetHistoryEntry[];
  usage_7d: UsageDay[];
};

export type InviteStatus = 'pending' | 'accepted' | 'revoked' | 'expired';

export type ProjectInvite = {
  id: string;
  email: string;
  role: Exclude<ProjectRole, 'owner'>;
  budget_microusd: number | null;
  status: InviteStatus;
  expires_at: number;
  invited_by: UserRef | null;
  resent_at: number | null;
};

export type InvitePreview = {
  project_name: string;
  inviter: UserRef;
  /** The address the invitation was sent to; only it can accept. */
  email: string;
  role: Exclude<ProjectRole, 'owner'>;
  budget_microusd: number | null;
  expires_at: number;
  status: InviteStatus;
};

export type EffectiveLimitKind = 'key' | 'member' | 'project' | 'balance';

export type RejectionReason =
  | 'member_budget_exceeded'
  | 'key_budget_exceeded'
  | 'project_budget_exceeded'
  | 'model_not_allowed'
  | 'insufficient_quota'
  | 'project_access_suspended';

export type RejectionCount = { reason: string; count: number };

export type TokenCapabilities = {
  can_reveal: boolean;
  can_update: boolean;
  can_disable: boolean;
  can_delete: boolean;
  can_rotate: boolean;
};

export type ProjectToken = {
  product?: string;
  vendor_groups?: Record<string, 'direct' | 'cheap'>;
  vendor_limits_enabled?: boolean;
  vendor_limits?: string[];
  model_limits_enabled?: boolean;
  id: string;
  name: string;
  masked_key: string;
  status: 'enabled' | 'disabled' | 'expired' | 'exhausted';
  secret_available: boolean;
  owner: (UserRef & { role: ProjectRole }) | null;
  /** True once the owner was removed from (or left) the project: the Key
   *  stopped with them and can only be deleted, never re-enabled. */
  stopped_by_removal?: boolean;
  version: number;
  capabilities: TokenCapabilities;
  month_spend_microusd: number;
  cap_microusd: number | null;
  model_limits: string[];
  effective_limit: {
    kind: EffectiveLimitKind;
    remaining_microusd: number | null;
  };
  rejected_today: RejectionCount[];
  /** The Key's own reasoning-effort cap; "" follows the member's. */
  reasoning_effort_cap?: string;
  /** The owning member's cap, which the Key's can only lower. */
  member_effort_cap?: string;
  /** Requests this month whose effort was lowered to a cap. */
  effort_downed_month?: number;
  created_at: number;
  last_used_at: number | null;
  /** Set only for the reader's own Keys when rotation left an overlap. */
  rotated_old_expires_at?: number | null;
};

export type BudgetRequestStatus =
  | 'pending'
  | 'approved'
  | 'rejected'
  | 'withdrawn';

export type BudgetRequest = {
  id: string;
  user: UserRef;
  requested_microusd: number;
  reason: string;
  status: BudgetRequestStatus;
  created_at: number;
  handled_by: UserRef | null;
  handled_note: string | null;
  handled_at: number | null;
  /** Filled by approve: the budget that resulted. */
  approved_budget_microusd?: number | null;
};

export type MyBudget = {
  budget_microusd: number | null;
  spent_microusd: number;
  remaining_microusd: number | null;
  daily_avg_microusd: number;
  resets_at: number;
  warning_pct: number;
  version: number;
  project_cap: { cap_microusd: number | null; spent_microusd: number } | null;
  balance_days_left: number | null;
  history: BudgetHistoryEntry[];
  /** The viewer's own reasoning-effort cap in this project; "" = no cap. */
  reasoning_effort_cap?: string;
};

export type AttentionRejection = {
  actor: UserRef | null;
  token: { id: string; name: string };
  reason: string;
  count: number;
  last_at: number;
};

/** A member past their budget's warning line this month. */
export type AttentionNearBudget = {
  actor: UserRef;
  spent_microusd: number;
  budget_microusd: number;
  warning_pct: number;
  exhausted: boolean;
};

export type Attention = {
  as_of: number;
  window_start: number;
  window_end: number;
  rejected_count_today: number;
  pending_invites_count: number;
  pending_budget_requests_count: number;
  balance_days_left: number | null;
  rejections: Paged<AttentionRejection>;
  near_budget: { items: AttentionNearBudget[]; total: number };
};

export type BillTo = {
  name: string | null;
  address: {
    line1: string | null;
    line2: string | null;
    city: string | null;
    state: string | null;
    postal_code: string | null;
    country: string | null;
  } | null;
  tax_ids: Array<{ type: string; value: string }>;
};

export type ProjectWallet = {
  period_spend_microusd?: number;
  period_topup_microusd?: number;
  balance_microusd: number | null;
  month_spend_microusd?: number;
  month_topup_microusd?: number;
  daily_avg_microusd?: number;
  days_left: number | null;
  project_cap?: { cap_microusd: number | null; spent_microusd: number };
  billing_details?: BillTo | null;
  capabilities?: { can_topup: boolean; can_manage_billing: boolean };
};

/* G2 · auto-reload: one setting per project, the owner's. `configured` is
 * false until the owner has touched it; `card` is null until a setup session
 * saved one. Amounts are whole USD because a reload is a card top-up. */
export type AutoReloadPause = '' | 'card_declined' | 'cap_reached';

export type ProjectAutoReload = {
  configured: boolean;
  enabled: boolean;
  threshold_usd: number;
  amount_usd: number;
  monthly_cap_usd: number;
  card: { brand: string; last4: string; label: string } | null;
  setup_pending: boolean;
  paused_reason: AutoReloadPause;
  paused_at: number;
  last_failure: string;
  last_failed_at: number;
  last_run_at: number;
  last_charge_at: number;
  last_reload: { id: string; amount_usd: number; completed_at: number } | null;
  month_spent_usd: number;
  min_amount_usd: number;
  max_amount_usd: number;
};

export type AutoReloadSettingsInput = {
  threshold_usd: number;
  amount_usd: number;
  monthly_cap_usd: number;
};

export type PaymentStatus =
  | 'pending'
  | 'paid'
  | 'failed'
  | 'expired'
  | 'canceled';
export type InvoiceStatus = 'pending' | 'ready' | 'failed' | 'legacy';

export type ProjectTopup = {
  id: string;
  created_at: number;
  paid_amount_microusd: number;
  credited_microusd: number;
  paid_by: UserRef | null;
  method: string;
  payment_status: PaymentStatus;
  invoice_status: InvoiceStatus;
  invoice: {
    number: string | null;
    hosted_url: string | null;
    pdf_url: string | null;
    bill_to: BillTo | null;
  } | null;
  receipt_url: string | null;
  /* A pending order keeps the checkout page Stripe opened for it, payable
     until pay_by (Unix seconds); absent once it is paid or closed. */
  pay_link?: string | null;
  pay_by?: number | null;
};

export type Paged<T> = {
  items: T[];
  total: number;
  page: number;
  page_size: number;
};

export type RemovalReceipt = {
  removal_id: string;
  undo_until: number;
};

export type UndoReceipt = {
  status: MembershipStatus;
  restored_key_ids: string[];
  skipped_key_ids: string[];
};

export type RevealReceipt = {
  key: string;
  notification_status: 'queued' | 'not_required';
};

export type RotateReceipt = {
  key: string;
  old_expires_at: number | null;
};

export type ProjectsFeature = {
  enabled: boolean;
  projects: ProjectSummary[];
  defaultProjectId: string | null;
};
