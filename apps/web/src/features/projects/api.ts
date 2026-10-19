/* Real client for the team-projects contract (backend brief §4). Every call
 * returns the unwrapped `data` or throws a LazuApiError carrying the catalog
 * `code`, `details` and `status`. The calls skip the global error handler:
 * each page decides how a failure reads (inline, receipt or state). */
import { projectError } from './errors';
import {
  type Attention,
  type AutoReloadSettingsInput,
  type BudgetHistoryEntry,
  type BudgetRequest,
  type InvitePreview,
  type MyBudget,
  type Paged,
  type ProjectDetail,
  type ProjectAutoReload,
  type ProjectInvite,
  type ProjectMember,
  type ProjectMemberDetail,
  type ProjectRole,
  type ProjectSettings,
  type ProjectToken,
  type ProjectTopup,
  type ProjectWallet,
  type RemovalReceipt,
  type RevealReceipt,
  type RotateReceipt,
  type UndoReceipt,
} from './types';
import { API } from '../../helpers/api';
import {
  LazuApiError,
  localApiError,
  readApiError,
  unwrap,
} from '../../lib/api-error';
import type { AxiosRequestConfig } from 'axios';

/* A failed call becomes one LazuApiError. No response at all is a local
 * `network` / `timeout` failure: its outcome is unknown, not a refusal. */
const toProjectError = (error: unknown): never => {
  if (error instanceof LazuApiError) throw error;
  throw new LazuApiError(readApiError(error));
};

const config = (
  extra: AxiosRequestConfig = {},
): AxiosRequestConfig & {
  skipErrorHandler: boolean;
} => ({ skipErrorHandler: true, disableDuplicate: true, ...extra }) as never;

const get = async <T>(url: string, params?: Record<string, unknown>) => {
  try {
    return unwrap<T>(await API.get(url, config({ params })));
  } catch (error) {
    return toProjectError(error);
  }
};
const post = async <T>(
  url: string,
  body?: unknown,
  params?: Record<string, unknown>,
  operationId?: string,
) => {
  try {
    return unwrap<T>(
      await API.post(
        url,
        body ?? {},
        config({
          params,
          headers: operationId ? { 'Idempotency-Key': operationId } : undefined,
        }),
      ),
    );
  } catch (error) {
    return toProjectError(error);
  }
};
const put = async <T>(
  url: string,
  body?: unknown,
  params?: Record<string, unknown>,
) => {
  try {
    return unwrap<T>(await API.put(url, body ?? {}, config({ params })));
  } catch (error) {
    return toProjectError(error);
  }
};
const del = async <T>(url: string, params?: Record<string, unknown>) => {
  try {
    return unwrap<T>(await API.delete(url, config({ params })));
  } catch (error) {
    return toProjectError(error);
  }
};

/** Read every page; fail rather than silently returning an incomplete collection. */
export async function collectPages<T>(
  fetchPage: (page: number) => Promise<Paged<T> | T[]>,
): Promise<Paged<T>> {
  const items: T[] = [];
  for (let page = 1; ; page++) {
    const result = await fetchPage(page);
    if (Array.isArray(result))
      return {
        items: [...items, ...result],
        total: items.length + result.length,
        page: 1,
        page_size: items.length + result.length,
      };
    items.push(...result.items);
    if (items.length >= result.total)
      return { ...result, items, page: 1, page_size: items.length };
    if (!result.items.length) throw localApiError('incomplete_page');
  }
}

export type ProjectsClient = {
  // A · project
  getProject(id: string): Promise<ProjectDetail>;
  updateProject(
    id: string,
    body: {
      name?: string;
      settings?: Partial<ProjectSettings>;
      expected_version: number;
    },
  ): Promise<ProjectDetail>;
  dissolveProject(id: string, confirmName: string): Promise<void>;
  // B · members
  listMembers(id: string): Promise<ProjectMember[]>;
  getMember(id: string, userId: string): Promise<ProjectMemberDetail>;
  listBudgetHistory(
    id: string,
    userId: string,
    page: number,
  ): Promise<Paged<BudgetHistoryEntry>>;
  setMemberRole(
    id: string,
    userId: string,
    role: Exclude<ProjectRole, 'owner'>,
  ): Promise<ProjectMember>;
  setMemberBudget(
    id: string,
    userId: string,
    body: {
      budget_microusd: number | null;
      warning_pct: number;
      expected_version: number;
      allowed_models?: string[];
      reasoning_effort_cap?: string;
    },
  ): Promise<{
    member: ProjectMember;
    allocation: ProjectDetail['allocation'];
  }>;
  setMemberStatus(
    id: string,
    userId: string,
    status: 'active' | 'suspended',
  ): Promise<ProjectMember>;
  removeMember(id: string, userId: string): Promise<RemovalReceipt>;
  undoRemove(
    id: string,
    userId: string,
    removalId: string,
  ): Promise<UndoReceipt>;
  leaveProject(id: string): Promise<void>;
  // C · invites
  listInvites(id: string): Promise<ProjectInvite[]>;
  createInvite(
    id: string,
    body: {
      email: string;
      role: Exclude<ProjectRole, 'owner'>;
      budget_microusd?: number | null;
      project_name?: string;
    },
  ): Promise<ProjectInvite>;
  resendInvite(id: string, inviteId: string): Promise<ProjectInvite>;
  revokeInvite(id: string, inviteId: string): Promise<void>;
  previewInvite(token: string): Promise<InvitePreview>;
  acceptInvite(
    token: string,
  ): Promise<{ project_id: string; role: ProjectRole }>;
  // D · keys
  listMyTokens(id: string): Promise<ProjectToken[]>;
  listProjectTokens(id: string): Promise<ProjectToken[]>;
  createToken(
    id: string,
    body: Record<string, unknown> & {
      name: string;
      model_limits: string[];
      cap_microusd: number | null;
    },
  ): Promise<{ token: ProjectToken; key: string }>;
  getToken(
    tokenId: string,
    projectId: string,
  ): Promise<Record<string, unknown>>;
  updateTokenFull(
    tokenId: string,
    projectId: string,
    body: Record<string, unknown>,
  ): Promise<ProjectToken>;
  updateToken(
    tokenId: string,
    projectId: string,
    body: {
      name: string;
      model_limits: string[];
      cap_microusd: number | null;
      expected_version: number;
    },
  ): Promise<ProjectToken>;
  updateTokenConnect(
    tokenId: string,
    projectId: string,
    fields: {
      expected_version?: number;
      product?: string;
      vendor_groups?: Record<string, 'direct' | 'cheap'>;
    },
  ): Promise<ProjectToken>;
  revealToken(tokenId: string, projectId: string): Promise<RevealReceipt>;
  setTokenEnabled(
    tokenId: string,
    projectId: string,
    enabled: boolean,
    expectedVersion: number,
  ): Promise<ProjectToken>;
  deleteToken(tokenId: string, projectId: string): Promise<void>;
  rotateToken(
    tokenId: string,
    projectId: string,
    oldExpires: 'now' | '24h',
    operationId: string,
  ): Promise<RotateReceipt>;
  listProjectModels(id: string): Promise<string[]>;
  // E · budgets
  getMyBudget(id: string): Promise<MyBudget>;
  listBudgetRequests(id: string, page: number): Promise<Paged<BudgetRequest>>;
  createBudgetRequest(
    id: string,
    body: { requested_microusd: number; reason: string },
  ): Promise<BudgetRequest>;
  withdrawBudgetRequest(id: string, requestId: string): Promise<BudgetRequest>;
  approveBudgetRequest(
    id: string,
    requestId: string,
    budgetMicro: number | null,
  ): Promise<BudgetRequest>;
  rejectBudgetRequest(
    id: string,
    requestId: string,
    note: string,
  ): Promise<BudgetRequest>;
  // F · attention
  getAttention(id: string, page: number): Promise<Attention>;
  // G · wallet
  getWallet(
    id: string,
    range?: { start_timestamp: number; end_timestamp: number },
  ): Promise<ProjectWallet>;
  listTopups(id: string, page: number): Promise<Paged<ProjectTopup>>;
  getTopup(id: string, topupId: string): Promise<ProjectTopup>;
  getTopupDocument(
    id: string,
    topupId: string,
    document: 'invoice' | 'invoice_page' | 'receipt',
  ): Promise<{ url: string }>;
  startTopup(
    id: string,
    amountUSD: number,
    method: string,
    operationId: string,
  ): Promise<{ pay_link: string; top_up_id: string; payment_status?: string }>;
  getPortalUrl(id: string): Promise<{ url: string }>;
  /* Withdraws a pending order: Stripe's checkout page is expired with it. */
  cancelTopup(id: string, topupId: string): Promise<ProjectTopup>;
  // G2 · auto-reload (owner only)
  getAutoReload(id: string): Promise<ProjectAutoReload>;
  updateAutoReload(
    id: string,
    body: AutoReloadSettingsInput & { enabled?: boolean },
  ): Promise<ProjectAutoReload>;
  /** Opens the Stripe page that saves a card: turn on, or change the card.
   *  Nothing is charged there. Empty settings keep the saved numbers. */
  startAutoReloadSetup(
    id: string,
    body: AutoReloadSettingsInput | Record<string, never>,
  ): Promise<{ setup_link: string; session_id: string }>;
  confirmAutoReloadSetup(
    id: string,
    sessionId: string,
  ): Promise<ProjectAutoReload>;
  disableAutoReload(id: string): Promise<ProjectAutoReload>;
};

export const realProjectsClient: ProjectsClient = {
  getProject: (id) => get(`/api/project/${id}`),
  updateProject: (id, body) => put(`/api/project/${id}`, body),
  dissolveProject: (id, confirmName) =>
    post(`/api/project/${id}/dissolve`, { confirm_name: confirmName }),

  listMembers: async (id) =>
    (
      await collectPages<ProjectMember>((page) =>
        get(`/api/project/${id}/members`, { page, page_size: 100 }),
      )
    ).items,
  getMember: (id, userId) => get(`/api/project/${id}/members/${userId}`),
  listBudgetHistory: (id, userId, page) =>
    get(`/api/project/${id}/members/${userId}/budget-history`, {
      page,
      page_size: 20,
    }),
  setMemberRole: (id, userId, role) =>
    put(`/api/project/${id}/members/${userId}/role`, { role }),
  setMemberBudget: (id, userId, body) =>
    put(`/api/project/${id}/members/${userId}/budget`, body),
  setMemberStatus: (id, userId, status) =>
    put(`/api/project/${id}/members/${userId}/status`, { status }),
  removeMember: (id, userId) => del(`/api/project/${id}/members/${userId}`),
  undoRemove: (id, userId, removalId) =>
    post(`/api/project/${id}/members/${userId}/undo-remove`, {
      removal_id: removalId,
    }),
  leaveProject: (id) => post(`/api/project/${id}/leave`),

  listInvites: async (id) =>
    (
      await collectPages<ProjectInvite>((page) =>
        get(`/api/project/${id}/invites`, { page, page_size: 100 }),
      )
    ).items,
  createInvite: (id, body) => post(`/api/project/${id}/invites`, body),
  resendInvite: (id, inviteId) =>
    post(`/api/project/${id}/invites/${inviteId}/resend`),
  revokeInvite: (id, inviteId) =>
    post(`/api/project/${id}/invites/${inviteId}/revoke`),
  previewInvite: (token) => get(`/api/invite/${token}`),
  acceptInvite: (token) => post(`/api/invite/${token}/accept`),

  listMyTokens: async (id) =>
    (
      await collectPages<ProjectToken>((page) =>
        get('/api/token/', { project_id: id, page, p: page, page_size: 100 }),
      )
    ).items,
  listProjectTokens: async (id) =>
    (
      await collectPages<ProjectToken>((page) =>
        get(`/api/project/${id}/tokens`, { page, p: page, page_size: 100 }),
      )
    ).items,
  createToken: (id, body) => post('/api/token/', { ...body, project_id: id }),
  getToken: (tokenId, projectId) =>
    get(`/api/token/${tokenId}`, { project_id: projectId }),
  updateTokenFull: (tokenId, projectId, body) =>
    put('/api/token/', { ...body, id: tokenId, project_id: projectId }),
  updateToken: (tokenId, projectId, body) =>
    put(
      '/api/token/',
      { ...body, id: tokenId, project_id: projectId },
      { policy_only: true },
    ),
  updateTokenConnect: (tokenId, projectId, fields) =>
    put(
      '/api/token/',
      { ...fields, id: tokenId, project_id: projectId },
      { connect_only: true },
    ),
  revealToken: (tokenId, projectId) =>
    get(`/api/token/${tokenId}/secret`, { project_id: projectId }),
  setTokenEnabled: (tokenId, projectId, enabled, expectedVersion) =>
    put(
      '/api/token/',
      {
        id: tokenId,
        project_id: projectId,
        status: enabled ? 1 : 2,
        expected_version: expectedVersion,
      },
      { status_only: true },
    ),
  deleteToken: (tokenId, projectId) =>
    del(`/api/token/${tokenId}`, { project_id: projectId }),
  rotateToken: (tokenId, projectId, oldExpires, operationId) =>
    post(
      `/api/token/${tokenId}/rotate`,
      {
        old_expires: oldExpires,
      },
      { project_id: projectId },
      operationId,
    ),
  listProjectModels: async (id) => {
    const data = await get<string[] | { models?: string[] }>(
      '/api/user/models',
      {
        project_id: id,
      },
    );
    return Array.isArray(data) ? data : (data.models ?? []);
  },

  getMyBudget: (id) => get(`/api/project/${id}/my-budget`),
  listBudgetRequests: (id, page) =>
    get(`/api/project/${id}/budget-requests`, { page, page_size: 20 }),
  createBudgetRequest: (id, body) =>
    post(`/api/project/${id}/budget-requests`, body),
  withdrawBudgetRequest: (id, requestId) =>
    post(`/api/project/${id}/budget-requests/${requestId}/withdraw`),
  approveBudgetRequest: (id, requestId, budgetMicro) =>
    post(`/api/project/${id}/budget-requests/${requestId}/approve`, {
      budget_microusd: budgetMicro,
    }),
  rejectBudgetRequest: (id, requestId, note) =>
    post(`/api/project/${id}/budget-requests/${requestId}/reject`, { note }),

  getAttention: (id, page) =>
    get(`/api/project/${id}/attention`, { page, page_size: 20 }),

  getWallet: (id, range) => get(`/api/project/${id}/wallet`, range),
  listTopups: (id, page) =>
    get(`/api/project/${id}/topups`, { page, page_size: 20 }),
  getTopup: (id, topupId) => get(`/api/project/${id}/topups/${topupId}`),
  getTopupDocument: (id, topupId, document) =>
    get(`/api/project/${id}/topups/${topupId}/document`, { document }),
  cancelTopup: (id, topupId) =>
    post(`/api/project/${id}/topups/${topupId}/cancel`, {}),
  startTopup: async (id, amountUSD, method, operationId) => {
    if (!Number.isSafeInteger(amountUSD) || amountUSD <= 0)
      throw projectError('invalid_amount');
    const data = await post<{
      pay_link?: string;
      top_up_id?: string;
      payment_status?: string;
    } | null>(
      '/api/user/stripe/pay',
      { amount: amountUSD, payment_method: method, project_id: id },
      undefined,
      operationId,
    );
    // An idempotent replay of a paid order carries no checkout link; any
    // other answer without one means checkout did not open.
    if (data?.payment_status !== 'paid' && !data?.pay_link)
      throw projectError('payment_provider_unavailable', {}, 503);
    return {
      pay_link: data?.pay_link || '',
      payment_status: data?.payment_status,
      top_up_id: data?.top_up_id || '',
    };
  },
  getPortalUrl: (id) => get('/api/user/self/stripe/portal', { project_id: id }),

  getAutoReload: (id) => get(`/api/project/${id}/auto-reload`),
  updateAutoReload: (id, body) => put(`/api/project/${id}/auto-reload`, body),
  startAutoReloadSetup: async (id, body) => {
    const data = await post<{
      setup_link?: string;
      session_id?: string;
    } | null>(`/api/project/${id}/auto-reload/setup`, body);
    if (!data?.setup_link)
      throw projectError('payment_provider_unavailable', {}, 503);
    return { setup_link: data.setup_link, session_id: data.session_id || '' };
  },
  confirmAutoReloadSetup: (id, sessionId) =>
    post(`/api/project/${id}/auto-reload/confirm`, { session_id: sessionId }),
  disableAutoReload: (id) => post(`/api/project/${id}/auto-reload/disable`),
};
