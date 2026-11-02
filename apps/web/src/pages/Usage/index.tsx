import { lazy, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, useSearchParams } from 'react-router-dom';
import { UsageHeadSlotContext } from './headSlot';
import { UsageRangeSeg } from './UsageRangeSeg';
import ConsolePage from '../../components/ui/console-page';
import { LoadingState } from '../../components/ui/loading-state';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { useSidebar } from '../../hooks/common/useSidebar';
import './usage-page.css';

const Log = lazy(() => import('../Log'));
const Tasks = lazy(() => import('../Tasks'));

/* The page's peer views, all on the head's underline strip: the request log,
   its Agent sessions and media tasks (videos and images, which bill when they
   finish). Uploaded files are listed under 生成任务's 上传的文件, so the old
   ?tab=files lands there. The time range sits in the page head and the three
   views share it; on phones each view's filter sheet carries it instead. */
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
    fallback: '生成任务',
  },
] as const;

const components = {
  logs: Log,
  tasks: Tasks,
};

export default function UsagePage() {
  const { t } = useTranslation();
  const { isModuleVisible } = useSidebar();
  const narrow = useIsMobile();
  const [params, setParams] = useSearchParams();
  const [headSlot, setHeadSlot] = useState<HTMLElement | null>(null);
  const activeTab = params.get('tab') || 'logs';

  if (activeTab === 'files') {
    const next = new URLSearchParams(params);
    next.set('tab', 'tasks');
    if (isModuleVisible('workspace', 'files')) next.set('kind', 'uploads');
    return <Navigate to={`/console/usage?${next}`} replace />;
  }
  if (!views.some((item) => item.tab === activeTab)) {
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
    const view = views.find((item) => item.value === value);
    if (!view) return;
    const next = new URLSearchParams(params);
    next.set('tab', view.tab);
    if (view.value === 'sessions') {
      next.set('view', 'sessions');
    } else {
      next.delete('view');
    }
    /* The time range (range / from / to) rides along to the next tab; each
       tab's own status, search and open row stay behind. */
    if (view.value !== activeView) {
      for (const key of [
        'request',
        'task',
        'status',
        'days',
        'q',
        'kind',
        'model',
      ]) {
        next.delete(key);
      }
    }
    setParams(next);
  };

  return (
    <UsageHeadSlotContext.Provider value={headSlot}>
      <ConsolePage
        appearance='review'
        title={t('page.console.usage.title')}
        actions={
          <div className='usage-head-tools'>
            {narrow ? null : (
              <UsageRangeSeg className='usage-seg usage-range--head' />
            )}
            <div className='usage-head-tools__slot' ref={setHeadSlot} />
          </div>
        }
        tabs={
          <Tabs value={activeView} onValueChange={pickView}>
            <TabsList
              aria-label={t('page.console.usage.tabs.label', {
                defaultValue: '用量视图',
              })}
            >
              {views.map((item) => (
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
