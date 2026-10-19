import dayjs from 'dayjs';
import React, { useContext, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import PlatformOverview from './overview/PlatformOverview';
import WorkspaceOverview from './overview/WorkspaceOverview';
import OnboardingStrip from '../../components/onboarding/OnboardingStrip';
import OnboardingTakeover from '../../components/onboarding/OnboardingTakeover';
import useOnboarding from '../../components/onboarding/useOnboarding';
import ConsolePage from '../../components/ui/console-page';
import { useConsoleScope } from '../../context/ConsoleScope';
import { StatusContext } from '../../context/Status';
import { UserContext } from '../../context/User';
import { extensionSidebarEntries, webExtensions } from '../../extensions';
import { LeaveProjectAction } from '../../features/projects/components/LeaveProjectAction';
import {
  AttentionCard,
  MemberOverview,
} from '../../features/projects/components/OverviewCards';
import { MyBudgetPage } from '../../features/projects/pages/ProjectWalletPage';
import { useProject } from '../../features/projects/ProjectContext';
import { useProjectDetail } from '../../features/projects/queries';
import { API, showError } from '../../helpers';
import { getExternalAPIBaseURL } from '../../helpers/apiUrl';
import { useSidebar } from '../../hooks/common/useSidebar';
import { readBlobDownloadError } from '../Log/log-support';
import type { OverviewRange } from './overview/overview-api';
import './dashboard-d.css';
import './dashboard-global.css';

const ns = 'page.console.dashboard';

const PRESET_SECONDS = {
  '24h': 86400,
  '7d': 7 * 86400,
  '30d': 30 * 86400,
  '90d': 90 * 86400,
} as const;

/** The unix window a range covers right now, for the export endpoint. */
function rangeWindow(range: OverviewRange): { start: number; end: number } {
  if (range.preset === 'custom') return { start: range.start, end: range.end };
  // Whole hours through the current one: the server answers them from
  // hourly rows instead of reading every request of the range.
  const end = Math.floor(Date.now() / 1000 / 3600) * 3600 + 3599;
  return { start: end + 1 - PRESET_SECONDS[range.preset], end };
}

const Dashboard = () => {
  const { t } = useTranslation(['translation', 'projects'], {
    nsMode: 'fallback',
  });
  const [userState] = useContext(UserContext);
  const [statusState] = useContext(StatusContext);
  const { availableScopes } = useConsoleScope();
  const project = useProject();
  const projectDetail = useProjectDetail({
    enabled: project.enabled && project.isManager,
  });
  const projectId = project.enabled ? project.current?.id : undefined;
  const { isModuleVisible } = useSidebar();
  const [range, setRange] = useState<OverviewRange>({ preset: '24h' });
  // Team projects: a member's overview in a shared project is R6, not the
  // gateway overview; owner/admin keep the overview and grow 需要处理.
  const projectMemberView =
    project.enabled && Boolean(project.current?.isShared) && !project.isManager;
  // Home follows the site role. A personal filter selected in Usage & Logs
  // must not turn an operator's platform overview into their own usage.
  const platformScope = availableScopes.some(
    (scope) => scope.plane === 'platform',
  );
  const user = userState?.user;

  const { dismiss: dismissOnboarding, state: onboardingState } =
    useOnboarding();
  // The takeover only earns the main area while there is nothing to chart yet;
  // afterwards the remaining steps live in the compact strip.
  const showOnboardingTakeover = Boolean(
    onboardingState?.active && !onboardingState.has_request,
  );
  const showOnboardingStrip = Boolean(
    onboardingState?.active && onboardingState.has_request,
  );
  const onboardingApiBaseURL = useMemo(
    () =>
      `${getExternalAPIBaseURL(window.location.origin, window.location.hostname)}/v1`,
    [],
  );

  const balance: number | undefined = projectId
    ? projectDetail.data
      ? Number(projectDetail.data.balance_microusd) || 0
      : undefined
    : user?.id
      ? Number((user as { quota?: unknown }).quota) || 0
      : undefined;

  const exportUsage = async (format: 'csv' | 'xlsx') => {
    const { start, end } = rangeWindow(range);
    let response;
    try {
      response = await API.get('/api/data/export', {
        params: {
          end_timestamp: end,
          format,
          scope: platformScope ? 'platform' : 'self',
          project_id: platformScope ? undefined : projectId,
          start_timestamp: start,
        },
        responseType: 'blob',
        skipErrorHandler: true,
      });
    } catch (error) {
      showError(await readBlobDownloadError(error), t(`${ns}.error.export`));
      return;
    }
    const blob = new Blob([response.data]);
    const href = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = href;
    link.download = `usage-${dayjs.unix(start).format('YYYYMMDD')}-${dayjs.unix(end).format('YYYYMMDD')}.${format}`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(href);
  };

  if (platformScope) {
    return (
      <div className='gateway-page'>
        <PlatformOverview
          range={range}
          onRange={setRange}
          onExport={exportUsage}
        />
      </div>
    );
  }

  if (project.enabled && project.current?.status === 'suspended')
    return <MyBudgetPage />;

  if (projectMemberView) {
    return (
      <ConsolePage
        appearance='review'
        className='pj-page pj-member-page'
        title={t('page.console.overview.workspaceTitle', {
          defaultValue: '总览',
        })}
        description={t('projects.myBudget.lede', {
          project: project.currentName,
          owner: project.current?.ownerName ?? '',
        })}
      >
        <MemberOverview key={project.current?.id} />
      </ConsolePage>
    );
  }

  return (
    <div className='gateway-page'>
      {webExtensions().map((extension) =>
        extension.DashboardTop ? (
          <extension.DashboardTop
            key={`${extension.name}-${user?.id}`}
            userId={user?.id}
          />
        ) : null,
      )}
      {showOnboardingTakeover && onboardingState ? (
        <OnboardingTakeover
          accountLabel={[user?.display_name || user?.username, user?.email]
            .filter(Boolean)
            .join(' · ')}
          apiBaseURL={onboardingApiBaseURL}
          onDismiss={dismissOnboarding}
          state={onboardingState}
        />
      ) : null}
      {showOnboardingStrip && onboardingState ? (
        <OnboardingStrip
          onDismiss={dismissOnboarding}
          state={onboardingState}
        />
      ) : null}
      <WorkspaceOverview
        range={range}
        onRange={setRange}
        onExport={exportUsage}
        balance={balance}
        appLinks={extensionSidebarEntries()
          .filter(
            (entry) =>
              entry.leavesConsole &&
              isModuleVisible('workspace', entry.moduleKey) &&
              (entry.enabled ? entry.enabled(statusState?.status) : true),
          )
          .map((entry) => ({ href: entry.to, label: entry.label(t) }))}
        canInspectUsage={isModuleVisible('workspace', 'usage')}
        canManageKeys={isModuleVisible('workspace', 'token')}
      />
      {project.enabled ? (
        <div className='pj-overview-extra' key={project.current?.id}>
          <LeaveProjectAction />
          <AttentionCard />
        </div>
      ) : null}
    </div>
  );
};

export default Dashboard;
