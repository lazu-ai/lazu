/* react-query hooks over the contract client. All keys carry the project id
 * (see queryKeys.ts); nothing here uses keepPreviousData across projects. */
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { collectPages, type ProjectsClient } from './api';
import { useProject } from './ProjectContext';
import { projectKeys, projectsQueryRoot } from './queryKeys';

type Enabled = { enabled?: boolean };

const useClient = () => {
  const { client, current } = useProject();
  return { client, projectId: current?.id ?? '' };
};

const useProjectQuery = <T>(
  key: readonly unknown[],
  fetcher: (client: ProjectsClient, projectId: string) => Promise<T>,
  options: Enabled = {},
) => {
  const { client, projectId } = useClient();
  return useQuery({
    queryKey: key,
    queryFn: () => fetcher(client as ProjectsClient, projectId),
    enabled: Boolean(client && projectId) && options.enabled !== false,
    retry: false,
    staleTime: 15_000,
  });
};

export const useProjectDetail = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.detail(projectId),
    (c, id) => c.getProject(id),
    options,
  );
};

export const useProjectMembers = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.members(projectId),
    (c, id) => c.listMembers(id),
    options,
  );
};

export const useProjectMember = (userId: string, options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.member(projectId, userId),
    (c, id) => c.getMember(id, userId),
    { enabled: Boolean(userId) && options?.enabled !== false },
  );
};

export const useProjectInvites = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.invites(projectId),
    (c, id) => c.listInvites(id),
    options,
  );
};

export const useMyProjectTokens = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.myTokens(projectId),
    (c, id) => c.listMyTokens(id),
    options,
  );
};

export const useProjectTokens = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.projectTokens(projectId),
    (c, id) => c.listProjectTokens(id),
    options,
  );
};

export const useProjectModels = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.models(projectId),
    (c, id) => c.listProjectModels(id),
    options,
  );
};

export const useMyBudget = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.myBudget(projectId),
    (c, id) => c.getMyBudget(id),
    options,
  );
};

export const useBudgetRequests = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.budgetRequests(projectId),
    (c, id) => collectPages((page) => c.listBudgetRequests(id, page)),
    options,
  );
};

export const useAttention = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.attention(projectId),
    (c, id) => c.getAttention(id, 1),
    options,
  );
};

export const useProjectWallet = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.wallet(projectId),
    (c, id) => c.getWallet(id),
    options,
  );
};

/** The owner's auto-reload. Members never ask: the server would refuse. */
export const useProjectAutoReload = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.autoReload(projectId),
    (c, id) => c.getAutoReload(id),
    options,
  );
};

export const useProjectTopups = (options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.topups(projectId),
    (c, id) => collectPages((page) => c.listTopups(id, page)),
    options,
  );
};

export const useProjectTopup = (topupId: string, options?: Enabled) => {
  const { projectId } = useClient();
  return useProjectQuery(
    projectKeys.topup(projectId, topupId),
    (c, id) => c.getTopup(id, topupId),
    { enabled: Boolean(topupId) && options?.enabled !== false },
  );
};

/** Invalidate everything for the active project (after any mutation). */
export const useInvalidateProject = () => {
  const queryClient = useQueryClient();
  const { projectId } = useClient();
  return useCallback(
    (
      scope?:
        | 'all'
        | 'detail'
        | 'members'
        | 'invites'
        | 'tokens'
        | 'budget'
        | 'wallet',
    ) => {
      if (!projectId) return Promise.resolve();
      const keys: Array<readonly unknown[]> = [];
      if (scope === 'members' || scope === 'budget' || scope === 'tokens')
        keys.push([...projectsQueryRoot(projectId), 'member']);
      switch (scope) {
        case 'members':
          keys.push(
            projectKeys.members(projectId),
            projectKeys.detail(projectId),
            projectKeys.attention(projectId),
          );
          break;
        case 'invites':
          keys.push(
            projectKeys.invites(projectId),
            projectKeys.detail(projectId),
            projectKeys.attention(projectId),
          );
          break;
        case 'tokens':
          keys.push(
            projectKeys.projectTokens(projectId),
            projectKeys.myTokens(projectId),
            projectKeys.attention(projectId),
          );
          break;
        case 'budget':
          keys.push(
            projectKeys.myBudget(projectId),
            projectKeys.budgetRequests(projectId),
            projectKeys.members(projectId),
            projectKeys.detail(projectId),
            projectKeys.attention(projectId),
          );
          break;
        case 'wallet':
          keys.push(
            projectKeys.wallet(projectId),
            projectKeys.topups(projectId),
            projectKeys.autoReload(projectId),
            projectKeys.detail(projectId),
          );
          break;
        case 'detail':
          keys.push(projectKeys.detail(projectId));
          break;
        default:
          keys.push(projectsQueryRoot(projectId));
      }
      return Promise.all(
        keys.map((queryKey) => queryClient.invalidateQueries({ queryKey })),
      ).then(() => undefined);
    },
    [projectId, queryClient],
  );
};

export const useProjectsClient = () => {
  const { client } = useProject();
  if (!client) throw new Error('projects client not ready');
  return client;
};
