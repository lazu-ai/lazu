/* Route-level gates that swap a legacy page for its project-state page.
 * Switch off → the legacy page renders untouched. */
import { useQuery } from '@tanstack/react-query';
import { lazy, Suspense, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { accountPaymentQueryOptions } from './paymentRecord';
import { useProject } from './ProjectContext';
import Loading from '../../components/layout/AppLoading';
import { Button } from '../../components/ui/button';
import { InlineNotice } from '../../components/ui/inline-notice';
import type { ReactNode } from 'react';

const ProjectWalletPage = lazy(() => import('./pages/ProjectWalletPage'));
const ProjectKeysPage = lazy(() => import('./pages/KeysPage'));

/** Wallet: with the switch on, every project (default or shared) gets the
 *  R7/R8 wallet; members of a shared project get 我的预算. A `top_up_id` in
 *  the URL (Stripe return, payment e-mails) is the wallet's own concern: the
 *  page pins that record; the gate only moves the reader to the right
 *  project first. A payer who no longer sees the project still lands in
 *  their own wallet, where the record endpoint admits historical payers. */
export function WalletGate({ legacy }: { legacy: ReactNode }) {
  const project = useProject();
  const [params] = useSearchParams();
  const target = params.get('project_id');
  const topupId = params.get('top_up_id')?.trim();
  useQuery({
    ...accountPaymentQueryOptions(topupId || ''),
    enabled: Boolean(topupId),
  });
  const navigate = useNavigate();
  const { t } = useTranslation('projects');
  const targetProject = project.projects.find((item) => item.id === target);
  useEffect(() => {
    if (!target || project.loading || !project.enabled || !targetProject)
      return;
    if (project.switchProject(target) === 'switched') {
      const next = new URLSearchParams();
      if (topupId) next.set('top_up_id', topupId);
      const document = params.get('document_error');
      if (document) next.set('document_error', document);
      navigate(`/console/wallet${next.size ? `?${next}` : ''}`, {
        replace: true,
      });
    }
  }, [topupId, target, targetProject, project, navigate, params]);
  if (project.loading) return <Loading />;
  if (!project.enabled) return <>{legacy}</>;
  if (target && !topupId) {
    if (targetProject && !project.dirtyEntries.length) return <Loading />;
    return (
      <InlineNotice
        tone='warning'
        title={t('projects.wallet.openFailed')}
        description={t('projects.wallet.openFailedBody')}
        actions={
          <Button
            onClick={() => navigate('/console/wallet', { replace: true })}
          >
            {t('projects.topup.backToWallet')}
          </Button>
        }
      />
    );
  }
  if (target && targetProject && !project.dirtyEntries.length)
    return <Loading />;
  if (!project.current) return <>{legacy}</>;
  return (
    <Suspense fallback={<Loading />}>
      <ProjectWalletPage key={project.current.id} />
    </Suspense>
  );
}

/** API Keys: only a shared project changes the page (R5); an unshared
 *  default project is exactly today's Keys page. */
export function TokenGate({ legacy }: { legacy: ReactNode }) {
  const project = useProject();
  if (project.loading) return <Loading />;
  if (!project.enabled || !project.current?.isShared) return <>{legacy}</>;
  if (project.current.status !== 'active')
    return <Navigate to='/console/usage' replace />;
  return (
    <Suspense fallback={<Loading />}>
      <ProjectKeysPage key={project.current.id} />
    </Suspense>
  );
}
