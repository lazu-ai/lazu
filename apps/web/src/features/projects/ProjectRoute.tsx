/* Route guard for project pages: the projects entry switch must be on, the
 * active scope must be a shared project, and the viewer's project role must
 * be high enough. Anything else lands on the overview rather than a 403 —
 * these entries simply do not exist for that reader. */
import { lazy, Suspense } from 'react';
import { Navigate } from 'react-router-dom';
import { useProject } from './ProjectContext';
import Loading from '../../components/layout/AppLoading';
import type { ReactNode } from 'react';

const TeamEmptyPage = lazy(() => import('./pages/TeamEmptyPage'));

export function ProjectRoute({
  children,
  need,
}: {
  children: ReactNode;
  need: 'manager' | 'owner' | 'member';
}) {
  const project = useProject();
  if (project.loading) return <Loading />;
  if (!project.enabled) return <Navigate to='/console' replace />;
  if (!project.current?.isShared) return <Navigate to='/console' replace />;
  if (need === 'owner' && !project.isOwner)
    return <Navigate to='/console' replace />;
  if (need === 'manager' && !project.isManager)
    return <Navigate to='/console' replace />;
  return <>{children}</>;
}

/* 成员与预算 is the one project entry that exists before a project has
 * anyone in it: a solo owner gets the page that explains what inviting
 * would do, instead of being bounced to the overview for asking. */
export function TeamRoute({ children }: { children: ReactNode }) {
  const project = useProject();
  if (project.loading) return <Loading />;
  if (!project.enabled || !project.current)
    return <Navigate to='/console' replace />;
  if (!project.current.isShared) {
    if (!project.isSoloOwner) return <Navigate to='/console' replace />;
    return (
      <Suspense fallback={<Loading />}>
        <TeamEmptyPage />
      </Suspense>
    );
  }
  if (!project.isManager) return <Navigate to='/console' replace />;
  return <>{children}</>;
}
