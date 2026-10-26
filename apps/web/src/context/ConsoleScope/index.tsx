import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import {
  readSessionProjects,
  useProjectsChangedTick,
} from '../../features/projects/bus';
import { readProjectsFlag } from '../../features/projects/flag';
import { isAdminRole } from '../../helpers/roles';
import { useSession } from '../User';
import type {
  ProjectRole,
  ProjectSummary,
} from '../../features/projects/types';

export type ConsoleScopeKind =
  | 'workspace_manager'
  | 'member'
  | 'key_pass'
  | 'platform';

export type ConsoleScopeProject = {
  id: string;
  role: ProjectRole;
  /** Owned by the viewer (their default project). */
  isDefault: boolean;
  /** Has been upgraded by a first invitation. */
  isShared: boolean;
  ownerName: string;
  memberCount: number;
  balanceMicro: number | null;
  status: ProjectSummary['status'];
};

export type ConsoleScope = {
  id: string;
  isWorkspaceFallback?: boolean;
  kind: ConsoleScopeKind;
  label: string;
  meta: string;
  plane: 'customer' | 'platform';
  /** Present when the scope is a team project (projects feature on). */
  project?: ConsoleScopeProject;
};

export type ConsoleScopeCopy = {
  keyMeta?: string;
  platformAdmin?: string;
  platformLabel?: string;
  workspaceFallback?: string;
  workspaceManager?: string;
  workspaceMember?: string;
};

type SessionUser = {
  email?: unknown;
  id?: unknown;
  key_name?: unknown;
  role?: unknown;
  username?: unknown;
  workspace_id?: unknown;
  workspace_name?: unknown;
};

type ConsoleScopeContextValue = {
  activeScope: ConsoleScope;
  availableScopes: ConsoleScope[];
  setActiveScope: (scopeId: string) => void;
};

export const CONSOLE_WORKSPACE_FALLBACK = 'API 工作台';
const WORKSPACE_MANAGER_META = '空间管理员';
const WORKSPACE_MEMBER_META = '空间成员';

const DEFAULT_SCOPE: ConsoleScope = {
  id: 'workspace:default',
  isWorkspaceFallback: true,
  kind: 'member',
  label: CONSOLE_WORKSPACE_FALLBACK,
  meta: WORKSPACE_MEMBER_META,
  plane: 'customer',
};

const ConsoleScopeContext = createContext<ConsoleScopeContextValue>({
  activeScope: DEFAULT_SCOPE,
  availableScopes: [DEFAULT_SCOPE],
  setActiveScope: () => undefined,
});

const text = (value: unknown) =>
  typeof value === 'string' || typeof value === 'number'
    ? String(value).trim()
    : '';

export const getConsoleScopeLabel = (
  scope: ConsoleScope,
  copy: ConsoleScopeCopy = {},
) => {
  if (scope.plane === 'platform') {
    return copy.platformLabel || text(scope.label) || '管理后台';
  }
  const label = text(scope.label);
  if (copy.workspaceFallback && (scope.isWorkspaceFallback || !label)) {
    return copy.workspaceFallback;
  }
  return label || CONSOLE_WORKSPACE_FALLBACK;
};

export const getConsoleScopeMeta = (
  scope: ConsoleScope,
  copy: ConsoleScopeCopy = {},
) => {
  if (scope.kind === 'key_pass') return copy.keyMeta || scope.meta;
  if (scope.kind === 'platform') return copy.platformAdmin || scope.meta;
  // A workspace's meta is the viewer's project role, never their platform role.
  return scope.meta === WORKSPACE_MANAGER_META
    ? copy.workspaceManager || scope.meta
    : copy.workspaceMember || scope.meta;
};

export const projectScopeId = (projectId: string) => `workspace:${projectId}`;

/** One customer scope per project the viewer belongs to. The owner's default
 *  project keeps today's label until a first invitation names it, so a lone
 *  user sees exactly the workspace they see today. */
export function getConsoleScopes(
  user: SessionUser = {},
  projects: ProjectSummary[] = [],
): ConsoleScope[] {
  const platformOperator = isAdminRole(user.role);
  const workspaceId = text(user.workspace_id) || 'default';
  const workspaceName = text(user.workspace_name);
  const workspaceKind: ConsoleScopeKind = platformOperator
    ? 'workspace_manager'
    : 'member';
  const scopes: ConsoleScope[] = projects.length
    ? projects.map((project) => {
        const isShared = project.upgraded_at !== null;
        const isDefault = project.is_default;
        return {
          id: projectScopeId(project.id),
          isWorkspaceFallback: !isShared && !workspaceName,
          kind: workspaceKind,
          label: isShared ? project.name : workspaceName || project.name,
          meta:
            project.role === 'member'
              ? WORKSPACE_MEMBER_META
              : WORKSPACE_MANAGER_META,
          plane: 'customer',
          project: {
            id: project.id,
            role: project.role,
            isDefault,
            isShared,
            ownerName: project.owner?.username || '',
            memberCount: project.member_count,
            balanceMicro: project.balance_microusd,
            status: project.status,
          },
        };
      })
    : [
        {
          id: `workspace:${workspaceId}`,
          isWorkspaceFallback: !workspaceName,
          kind: workspaceKind,
          label: workspaceName || CONSOLE_WORKSPACE_FALLBACK,
          meta: WORKSPACE_MEMBER_META,
          plane: 'customer',
        },
      ];

  const keyName = text(user.key_name);
  if (keyName) {
    scopes.push({
      id: `key:${keyName}`,
      kind: 'key_pass',
      label: keyName,
      meta: 'API Key',
      plane: 'customer',
    });
  }

  if (platformOperator) {
    scopes.push({
      id: 'platform:operations',
      kind: 'platform',
      label: '管理后台',
      meta: '管理员',
      plane: 'platform',
    });
  }

  return scopes;
}

export const canAccessConsoleScope = (
  activeScope: ConsoleScopeKind,
  allowedScopes: ConsoleScopeKind[],
) => allowedScopes.includes(activeScope);

/** Keep project navigation independent of the role-based home overview. */
export const getDefaultConsoleScopeId = (scopes: ConsoleScope[]) =>
  scopes.find((scope) => scope.plane === 'customer')?.id ||
  scopes.find((scope) => scope.plane === 'platform')?.id ||
  scopes[0]?.id ||
  DEFAULT_SCOPE.id;

const sessionIdentity = (user: SessionUser) =>
  text(user.id) || text(user.username) || text(user.email) || 'anonymous';

const readStoredUser = (): SessionUser => {
  try {
    const value = JSON.parse(localStorage.getItem('user') || '{}');
    return value && typeof value === 'object' ? (value as SessionUser) : {};
  } catch {
    return {};
  }
};

const readStoredScope = (storageKey: string, scopes: ConsoleScope[]) => {
  try {
    const stored = localStorage.getItem(storageKey);
    return stored && scopes.some((scope) => scope.id === stored) ? stored : '';
  } catch {
    return '';
  }
};

export function ConsoleScopeProvider({ children }: { children: ReactNode }) {
  const userSession = useSession();
  const storedUser = useMemo(() => readStoredUser(), []);
  const user = (userSession.user || storedUser) as SessionUser;
  const projectsTick = useProjectsChangedTick();
  const projects = useMemo(
    () => (readProjectsFlag(user) ? readSessionProjects(user) : []),
    // The tick is the mock store's change signal; real mode changes `user`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [user, projectsTick],
  );
  const availableScopes = useMemo(
    () => getConsoleScopes(user, projects),
    [user, projects],
  );
  const storageKey = `lazu:console-scope:${sessionIdentity(user)}`;
  const [selectedId, setSelectedId] = useState(() =>
    readStoredScope(storageKey, availableScopes),
  );

  useEffect(() => {
    const stored = localStorage.getItem(storageKey);
    // Usage & Logs and project navigation own explicit scope selections.
    // Preserve them across mounts; the home overview follows the site role
    // independently, since the old ConsoleScopeBar is hidden by the shell.
    const storedScope = availableScopes.find((scope) => scope.id === stored);
    setSelectedId(
      storedScope ? storedScope.id : getDefaultConsoleScopeId(availableScopes),
    );
  }, [availableScopes, storageKey]);

  const defaultScopeId = getDefaultConsoleScopeId(availableScopes);
  const activeScope =
    availableScopes.find((scope) => scope.id === selectedId) ||
    availableScopes.find((scope) => scope.id === defaultScopeId) ||
    availableScopes[0] ||
    DEFAULT_SCOPE;

  const setActiveScope = useCallback(
    (scopeId: string) => {
      if (!availableScopes.some((scope) => scope.id === scopeId)) return;
      localStorage.setItem(storageKey, scopeId);
      setSelectedId(scopeId);
    },
    [availableScopes, storageKey],
  );

  const value = useMemo(
    () => ({ activeScope, availableScopes, setActiveScope }),
    [activeScope, availableScopes, setActiveScope],
  );

  return (
    <ConsoleScopeContext.Provider value={value}>
      {children}
    </ConsoleScopeContext.Provider>
  );
}

export const useConsoleScope = () => useContext(ConsoleScopeContext);
