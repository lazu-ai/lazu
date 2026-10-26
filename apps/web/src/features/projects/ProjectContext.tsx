/* Session-level project state: which project the console is looking at,
 * the viewer's role in it, the contract client, and the unsaved-edit guard
 * that the switcher and the master-detail page consult before moving. */
import { useQueryClient } from '@tanstack/react-query';
import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { isSoloOwner } from './access';
import {
  announceProjectsChanged,
  readSessionProjects,
  registerMockProjectsReader,
} from './bus';
import { loadProjectsClient } from './client';
import { isProjectsMockEnabled, readProjectsFlag } from './flag';
import { projectsQueryRoot } from './queryKeys';
import {
  projectScopeId,
  useConsoleScope,
  type ConsoleScopeProject,
} from '../../context/ConsoleScope';
import { useSession } from '../../context/User';
import {
  refreshSelf,
  readSelf,
  useSelfQuery,
  type SelfRecord,
} from '../../hooks/common/useSelfQuery';
import type { ProjectsClient } from './api';
import type { ProjectRole, ProjectSummary } from './types';

export type DirtyEntry = {
  key: string;
  label: string;
  save?: () => Promise<void>;
};

export type RemovedNotice = {
  projectName: string;
  keyCount: number;
};

type ProjectContextValue = {
  enabled: boolean;
  /** Switch is on but the client / project list is still loading. */
  loading: boolean;
  client: ProjectsClient | null;
  projects: ProjectSummary[];
  /** The project behind the active customer scope, if any. */
  current: ConsoleScopeProject | null;
  currentName: string;
  role: ProjectRole;
  isShared: boolean;
  isOwner: boolean;
  isManager: boolean;
  /* One switch behind every "start a team" entry — the sidebar group, the
     wallet card and the Keys fork. A project that already has people in it
     stops offering to start one, in all three places at once. */
  isSoloOwner: boolean;
  viewerId: string;
  viewerName: string;
  refreshProjects: () => Promise<void>;
  switchProject: (
    projectId: string,
    discardDirty?: boolean,
  ) => 'switched' | 'blocked' | 'unknown';
  dirtyEntries: DirtyEntry[];
  registerDirty: (entry: DirtyEntry | null, key: string) => void;
  removedNotice: RemovedNotice | null;
  dismissRemoved: () => void;
};

const noop = () => undefined;

const ProjectContext = createContext<ProjectContextValue>({
  enabled: false,
  loading: false,
  client: null,
  projects: [],
  current: null,
  currentName: '',
  role: 'owner',
  isShared: false,
  isOwner: true,
  isManager: true,
  isSoloOwner: false,
  viewerId: '',
  viewerName: '',
  refreshProjects: async () => undefined,
  switchProject: () => 'unknown',
  dirtyEntries: [],
  registerDirty: noop,
  removedNotice: null,
  dismissRemoved: noop,
});

const text = (value: unknown) =>
  typeof value === 'string' || typeof value === 'number' ? String(value) : '';

export function ProjectProvider({ children }: { children: ReactNode }) {
  const userSession = useSession();
  const { activeScope, availableScopes, setActiveScope } = useConsoleScope();
  const queryClient = useQueryClient();
  const [refreshedUserId, setRefreshedUserId] = useState('');
  const [client, setClient] = useState<ProjectsClient | null>(null);
  const [dirtyEntries, setDirtyEntries] = useState<DirtyEntry[]>([]);
  const [removedNotice, setRemovedNotice] = useState<RemovedNotice | null>(
    null,
  );
  const [mockViewer, setMockViewer] = useState<{
    id: string;
    username: string;
  } | null>(null);
  const lastProjectRef = useRef<ConsoleScopeProject | null>(null);
  const lastProjectNameRef = useRef('');

  const [storedSessionPresent] = useState(() => {
    try {
      return Boolean(JSON.parse(localStorage.getItem('user') || 'null')?.id);
    } catch {
      return false;
    }
  });
  const user = userSession.user;
  const enabled = readProjectsFlag(user);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    void loadProjectsClient().then((loaded) => {
      if (cancelled) return;
      setClient(loaded);
    });
    if (isProjectsMockEnabled()) {
      void import('./mock').then((module) => {
        if (cancelled) return;
        registerMockProjectsReader(module.mockSelfProjects);
        setMockViewer(module.mockViewer());
      });
    }
    return () => {
      cancelled = true;
    };
  }, [enabled]);

  const projects = useMemo(
    () => (enabled ? readSessionProjects(user) : []),
    // availableScopes already tracks the change tick; re-read alongside it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [enabled, user, availableScopes],
  );

  const current =
    activeScope.plane === 'customer' ? (activeScope.project ?? null) : null;

  // Detect "the project I was looking at is gone" (removed, dissolved).
  useEffect(() => {
    if (!enabled) return;
    const previous = lastProjectRef.current;
    if (previous && !projects.some((project) => project.id === previous.id)) {
      if (previous.isShared && previous.role !== 'owner') {
        setRemovedNotice({
          projectName: lastProjectNameRef.current || previous.id,
          keyCount: 0,
        });
      }
      // Cancel and drop every query of the vanished project.
      void queryClient.cancelQueries({
        queryKey: projectsQueryRoot(previous.id),
      });
      queryClient.removeQueries({ queryKey: projectsQueryRoot(previous.id) });
    }
    lastProjectRef.current = current;
    lastProjectNameRef.current = activeScope.label;
  }, [activeScope.label, current, enabled, projects, queryClient]);

  // The account comes from the one shared copy; a changed answer becomes the
  // session user. An unchanged one is not dispatched, so a refresh that finds
  // nothing new does not re-render everything that reads the user.
  const userRef = useRef(user);
  userRef.current = user;
  const applySelf = useCallback(
    (data: SelfRecord | null) => {
      if (!data) return;
      if (JSON.stringify(data) === JSON.stringify(userRef.current)) return;
      userSession.signIn(data as never);
    },
    [userSession.signIn],
  );

  const refreshProjects = useCallback(async () => {
    if (isProjectsMockEnabled()) {
      announceProjectsChanged();
      return;
    }
    try {
      applySelf(await refreshSelf(queryClient));
    } catch {
      /* the next page load re-reads self anyway */
    }
  }, [applySelf, queryClient]);

  // Keep membership current while this tab stays open: the shared query
  // re-reads when the tab comes back into view after a minute or more. A tab
  // left in the background asks nothing.
  const self = useSelfQuery(
    enabled && Boolean(user?.id) && !isProjectsMockEnabled(),
  );
  useEffect(() => {
    if (self.data) applySelf(self.data);
  }, [self.data, applySelf]);

  // Refresh even when a cached pre-rollout user has no projects flag.
  const bootstrapUserId = text(user?.id);
  useEffect(() => {
    if (!bootstrapUserId || isProjectsMockEnabled()) return;
    let cancelled = false;
    void readSelf(queryClient)
      .then((data) => {
        if (!cancelled) applySelf(data);
      })
      .catch(() => undefined)
      .finally(() => {
        if (!cancelled) setRefreshedUserId(bootstrapUserId);
      });
    return () => {
      cancelled = true;
    };
  }, [bootstrapUserId, applySelf, queryClient]);

  const switchProject = useCallback(
    (
      projectId: string,
      discardDirty = false,
    ): 'switched' | 'blocked' | 'unknown' => {
      const scopeId = projectScopeId(projectId);
      if (!availableScopes.some((scope) => scope.id === scopeId))
        return 'unknown';
      if (dirtyEntries.length && !discardDirty) return 'blocked';
      if (discardDirty) setDirtyEntries([]);
      if (current) {
        void queryClient.cancelQueries({
          queryKey: projectsQueryRoot(current.id),
        });
      }
      if (current)
        queryClient.removeQueries({ queryKey: projectsQueryRoot(current.id) });
      setActiveScope(scopeId);
      return 'switched';
    },
    [
      availableScopes,
      current,
      dirtyEntries.length,
      queryClient,
      setActiveScope,
    ],
  );

  const registerDirty = useCallback((entry: DirtyEntry | null, key: string) => {
    setDirtyEntries((entries) => {
      const rest = entries.filter((item) => item.key !== key);
      return entry ? [...rest, entry] : rest;
    });
  }, []);

  // Leaving the tab with unsaved edits asks first (browser-native prompt).
  useEffect(() => {
    if (!dirtyEntries.length) return;
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirtyEntries.length]);

  // Logout / user change clears everything project-scoped.
  const userId = text(user?.id);
  useEffect(() => {
    if (!userId) {
      queryClient.removeQueries({ queryKey: projectsQueryRoot() });
      setDirtyEntries([]);
      setRemovedNotice(null);
    }
  }, [queryClient, userId]);

  const role: ProjectRole = current?.role ?? 'owner';
  const viewerId = mockViewer?.id ?? userId;
  const viewerName =
    mockViewer?.username ?? text(user?.display_name) ?? text(user?.username);

  const value = useMemo<ProjectContextValue>(
    () => ({
      enabled: enabled && client !== null,
      loading:
        (!user && storedSessionPresent) ||
        (Boolean(userId) &&
          !isProjectsMockEnabled() &&
          refreshedUserId !== userId) ||
        (enabled && (client === null || (isProjectsMockEnabled() && !current))),
      client,
      projects,
      current,
      currentName: current ? activeScope.label : '',
      role,
      isShared: Boolean(current?.isShared),
      isOwner: current?.status === 'active' && role === 'owner',
      isManager: current?.status === 'active' && role !== 'member',
      isSoloOwner:
        client !== null &&
        current !== null &&
        isSoloOwner({
          enabled,
          isShared: Boolean(current.isShared),
          role,
          status: current.status,
        }),
      viewerId,
      viewerName: viewerName || text(user?.username),
      refreshProjects,
      switchProject,
      dirtyEntries,
      registerDirty,
      removedNotice,
      dismissRemoved: () => setRemovedNotice(null),
    }),
    [
      activeScope.label,
      refreshedUserId,
      storedSessionPresent,
      user,
      userId,
      client,
      current,
      dirtyEntries,
      enabled,
      projects,
      refreshProjects,
      registerDirty,
      removedNotice,
      role,
      switchProject,
      user?.username,
      viewerId,
      viewerName,
    ],
  );

  return (
    <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
  );
}

export const useProject = () => useContext(ProjectContext);

/** Register a dirty form for the lifetime of `dirty`. */
export const useDirtyGuard = (
  key: string,
  dirty: boolean,
  label: string,
  save?: () => Promise<void>,
) => {
  const { registerDirty } = useProject();
  const saveRef = useRef(save);
  saveRef.current = save;
  const canSave = Boolean(save);
  useEffect(() => {
    registerDirty(
      dirty
        ? { key, label, save: canSave ? () => saveRef.current!() : undefined }
        : null,
      key,
    );
    return () => registerDirty(null, key);
  }, [dirty, key, label, registerDirty, canSave]);
};
