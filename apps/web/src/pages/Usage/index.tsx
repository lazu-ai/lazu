import { lazy, Suspense, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, useSearchParams } from 'react-router-dom';
import { UsageHeadSlotContext } from './headSlot';
import { LoadingState, Tabs, TabsList, TabsTrigger } from '../../components/ui';
import ConsolePage from '../../components/ui/console-page';
import { useSidebar } from '../../hooks/common/useSidebar';

const Log = lazy(() => import('../Log'));
const MonthlyReport = lazy(() => import('../MonthlyReport'));
const Files = lazy(() => import('../Files'));
const Tasks = lazy(() => import('../Tasks'));

/* The page's peer views, all on the head's underline strip: the request log,
   its Agent sessions, media tasks (videos and images, which bill when they
   finish), the monthly report and files. Files is an API-side
   resource rather than its own workflow, so it rides here instead of a peer
   sidebar entry — see brand/foundations/yunmu-console.md. Nothing is behind a
   「更多」 menu any more. */
const views = [
  {
    value: 'requests',
    tab: 'logs',
    labelKey: 'page.console.log.session.tabs.requests',
    fallback: 'API 调用',
  },
  {
    value: 'sessions',
    tab: 'logs',
    labelKey: 'page.console.log.session.tabs.sessions',
    fallback: 'Agent 会话',
  },
  {
    value: 'tasks',
    tab: 'tasks',
    labelKey: 'page.console.usage.tabs.tasks',
    fallback: '任务',
  },
  {
    value: 'monthly',
    tab: 'monthly',
    labelKey: 'page.console.usage.tabs.monthly',
    fallback: '月度报告',
  },
  {
    value: 'files',
    tab: 'files',
    labelKey: 'page.console.usage.tabs.files',
    fallback: '文件',
    module: 'files',
  },
] as const;

const components = {
  logs: Log,
  tasks: Tasks,
  monthly: MonthlyReport,
  files: Files,
};

export default function UsagePage() {
  const { t } = useTranslation();
  const { isModuleVisible } = useSidebar();
  const [params, setParams] = useSearchParams();
  const [headSlot, setHeadSlot] = useState<HTMLElement | null>(null);
  const activeTab = params.get('tab') || 'logs';
  const visibleViews = useMemo(
    () =>
      views.filter(
        (item) =>
          !('module' in item) || isModuleVisible('workspace', item.module),
      ),
    [isModuleVisible],
  );

  if (!visibleViews.some((item) => item.tab === activeTab)) {
    return <Navigate to='/console/usage?tab=logs' replace />;
  }

  const activeView =
    activeTab === 'logs'
      ? params.get('view') === 'sessions'
        ? 'sessions'
        : 'requests'
      : activeTab;
  const ActiveComponent = components[activeTab as keyof typeof components];

  const pickView = (value: string) => {
    const view = visibleViews.find((item) => item.value === value);
    if (!view) return;
    const next = new URLSearchParams(params);
    next.set('tab', view.tab);
    if (view.value === 'sessions') {
      next.set('view', 'sessions');
    } else if (view.tab === 'logs') {
      next.delete('view');
    } else {
      next.delete('view');
      next.delete('request');
      if (view.tab !== 'tasks') {
        for (const key of ['task', 'status', 'days', 'q']) next.delete(key);
      }
    }
    setParams(next);
  };

  return (
    <UsageHeadSlotContext.Provider value={headSlot}>
      <ConsolePage
        appearance='review'
        title={t('page.console.usage.title')}
        actions={<div className='usage-head-tools' ref={setHeadSlot} />}
        tabs={
          <Tabs value={activeView} onValueChange={pickView}>
            <TabsList
              aria-label={t('page.console.usage.tabs.label', {
                defaultValue: '用量视图',
              })}
            >
              {visibleViews.map((item) => (
                <TabsTrigger key={item.value} value={item.value}>
                  {t(item.labelKey, { defaultValue: item.fallback })}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        }
        className='usage-page--log-first'
      >
        <Suspense
          fallback={
            <LoadingState
              aria-live='polite'
              className='lazu-loading--inline'
              role='status'
              title={t('page.system.loading.title')}
            />
          }
        >
          <ActiveComponent />
        </Suspense>
      </ConsolePage>
    </UsageHeadSlotContext.Provider>
  );
}
