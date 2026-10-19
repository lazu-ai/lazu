/* Every project query key starts with the project id so a switch can cancel
 * and drop one project's cache without touching another's. */
export const projectsQueryRoot = (projectId?: string) =>
  projectId ? (['projects', projectId] as const) : (['projects'] as const);

export const projectKeys = {
  detail: (id: string) => [...projectsQueryRoot(id), 'detail'] as const,
  members: (id: string) => [...projectsQueryRoot(id), 'members'] as const,
  member: (id: string, userId: string) =>
    [...projectsQueryRoot(id), 'member', userId] as const,
  invites: (id: string) => [...projectsQueryRoot(id), 'invites'] as const,
  myTokens: (id: string) => [...projectsQueryRoot(id), 'my-tokens'] as const,
  projectTokens: (id: string) =>
    [...projectsQueryRoot(id), 'project-tokens'] as const,
  models: (id: string) => [...projectsQueryRoot(id), 'models'] as const,
  myBudget: (id: string) => [...projectsQueryRoot(id), 'my-budget'] as const,
  budgetRequests: (id: string) =>
    [...projectsQueryRoot(id), 'budget-requests'] as const,
  attention: (id: string) => [...projectsQueryRoot(id), 'attention'] as const,
  wallet: (id: string) => [...projectsQueryRoot(id), 'wallet'] as const,
  autoReload: (id: string) =>
    [...projectsQueryRoot(id), 'auto-reload'] as const,
  topups: (id: string) => [...projectsQueryRoot(id), 'topups'] as const,
  topup: (id: string, topupId: string) =>
    [...projectsQueryRoot(id), 'topup', topupId] as const,
  invite: (token: string) => ['invite', token] as const,
};
