import { Activity, CreditCard, Network, Palette, UserPlus } from 'lucide-react';
import { lazy, Suspense, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, Navigate, useSearchParams } from 'react-router-dom';
import {
  SettingsHeaderActionsProvider,
  useSettingsNavigationBlocked,
  useSettingsSaveAll,
} from './headerActions';
import {
  Button,
  DataCard,
  LoadingState,
  Modal,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '../../components/ui';
import ConsolePage from '../../components/ui/console-page';
import { isRoot } from '../../helpers';

const RateLimits = lazy(() => import('./Access/RateLimits'));

const AccessSetting = lazy(() => import('./Access'));
const BillingSetting = lazy(() => import('./Billing'));
const BrandingSetting = lazy(() => import('./Branding'));
const ObservabilitySetting = lazy(() => import('./Observability'));
const UpstreamSetting = lazy(() => import('./Upstream'));

/* One tab per thing you administer.
 *
 * The previous split was by nature — platform / security / ops — which gave
 * "ops" nothing in common except being left over: quota thresholds, channel
 * auto-disable, log switches, rate limits and Gemini's thinking budget all
 * shared one scroll. Grouping by object means each tab can be described in a
 * sentence, and it puts the deployment toggles (sidebar preset, product switches) with
 * accounts rather than under security, where nobody looked for them.
 * Locked spec: brand/foundations/yunmu-console.md, "Locked settings surfaces".
 */
const tabItems = [
  {
    value: 'accounts',
    labelKey: 'page.console.settingV2.tabs.accounts',
    titleKey: 'page.console.settingV2.access.title',
    icon: UserPlus,
    sections: [{ Component: AccessSetting, key: 'access' }],
  },
  {
    value: 'billing',
    labelKey: 'page.console.settingV2.tabs.billing',
    titleKey: 'page.console.settingV2.billing.title',
    icon: CreditCard,
    sections: [{ Component: BillingSetting, key: 'billing' }],
  },
  /* Site identity, legal copy and announcement mail were bundled into the
     billing tab, which meant the tab named after money also owned the logo and
     the privacy policy. Billing is money policy only now, so the site settings
     get the tab they always needed. */
  {
    value: 'branding',
    labelKey: 'page.console.settingV2.tabs.branding',
    titleKey: 'page.console.settingV2.branding.title',
    icon: Palette,
    sections: [{ Component: BrandingSetting, key: 'branding' }],
  },
  {
    value: 'upstream',
    labelKey: 'page.console.settingV2.tabs.upstream',
    titleKey: 'page.console.settingV2.upstream.title',
    icon: Network,
    sections: [{ Component: UpstreamSetting, key: 'upstream' }],
  },
  {
    value: 'observability',
    labelKey: 'page.console.settingV2.tabs.observability',
    titleKey: 'page.console.settingV2.obs.title',
    icon: Activity,
    sections: [{ Component: ObservabilitySetting, key: 'observability' }],
  },
];

const validTabs = new Set(tabItems.map((item) => item.value));

/* Old tab values keep working: bookmarks, deep links from docs and the
   in-product help all point at the previous names. */
const TAB_ALIASES: Record<string, string> = {
  access: 'accounts',
  ops: 'observability',
  // `platform` used to land on the tab that carried the site settings, which
  // is now its own tab rather than a lodger in billing.
  platform: 'branding',
  security: 'accounts',
  // 速率与等级 became 速率限制 when spend tiers were removed.
  tiers: 'rate-limits',
};

export default function Setting() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const requestedTab = params.get('tab') || 'accounts';
  const aliasedTab = TAB_ALIASES[requestedTab];
  const activeTab = aliasedTab || requestedTab;

  if (aliasedTab) {
    return <Navigate to={`/console/setting?tab=${aliasedTab}`} replace />;
  }

  if (!validTabs.has(activeTab) && activeTab !== 'rate-limits') {
    return <Navigate to='/console/setting?tab=accounts' replace />;
  }

  if (!isRoot()) {
    return (
      <ConsolePage
        appearance='review'
        title={t('page.console.settingV2.pageTitle', {
          defaultValue: '平台设置',
        })}
        description={t('page.console.settingV2.notRootDescription')}
        badge='SETTINGS'
      >
        <DataCard>
          <div className='text-sm text-[var(--ui-text-secondary)]'>
            {t('page.console.settingV2.notRoot')}
          </div>
        </DataCard>
      </ConsolePage>
    );
  }

  if (activeTab === 'rate-limits') {
    return (
      <ConsolePage
        appearance='review'
        title={t('page.console.settingV2.access.rateLimits.title', {
          defaultValue: '速率限制',
        })}
        actions={
          <Link to='/console/setting'>
            {t('common.back', { defaultValue: '返回设置' })}
          </Link>
        }
      >
        <Suspense
          fallback={<LoadingState title={t('page.system.loading.title')} />}
        >
          <RateLimits />
        </Suspense>
      </ConsolePage>
    );
  }

  return (
    <SettingsHeaderActionsProvider>
      {(headerActions) => (
        <ConsolePage
          appearance='review'
          title={t('page.console.settingV2.pageTitle', {
            defaultValue: '平台设置',
          })}
          className='settings-page'
        >
          <SettingsPanels
            activeTab={activeTab}
            headerActions={headerActions}
            onTabChange={(next) =>
              setParams(next === 'accounts' ? {} : { tab: next }, {
                replace: true,
              })
            }
          />
        </ConsolePage>
      )}
    </SettingsHeaderActionsProvider>
  );
}

/** The five groups as real panels: only the open one mounts.
 *
 *  They used to be anchors into one 6,900px document — every group rendered,
 *  and a "tab" click was a scrollIntoView. That made the tab bar a table of
 *  contents pretending to be a control, and it put four groups the reader is
 *  not editing between them and the save bar. One panel at a time also scopes
 *  the save bar to the group in front of the reader.
 */
function SettingsPanels({
  activeTab,
  headerActions,
  onTabChange,
}: {
  activeTab: string;
  headerActions: ReactNode;
  onTabChange: (next: string) => void;
}) {
  const { t } = useTranslation();
  const dirty = useSettingsNavigationBlocked();
  const saveAll = useSettingsSaveAll();
  const [pendingTab, setPendingTab] = useState<string | null>(null);
  const [savingBeforeSwitch, setSavingBeforeSwitch] = useState(false);

  const requestTab = (next: string) => {
    if (next === activeTab) return;
    // Switching away would unmount the drafts. Ask before that happens
    // rather than discarding edits the reader cannot see any more.
    if (dirty) {
      setPendingTab(next);
      return;
    }
    onTabChange(next);
  };

  return (
    <>
      <Tabs
        onValueChange={(value) => requestTab(String(value))}
        value={activeTab}
      >
        <TabsList variant='line'>
          {tabItems.map((item) => (
            <TabsTrigger key={item.value} value={item.value}>
              {t(item.labelKey)}
            </TabsTrigger>
          ))}
        </TabsList>
        {tabItems.map((item) => (
          <TabsContent key={item.value} value={item.value}>
            <section
              id={`settings-group-${item.value}`}
              className='settings-continuous-group'
            >
              <Suspense
                fallback={
                  <LoadingState title={t('page.system.loading.title')} />
                }
              >
                {item.sections.map(({ Component, key }) => (
                  <Component key={key} />
                ))}
              </Suspense>
            </section>
          </TabsContent>
        ))}
      </Tabs>
      {headerActions}

      <Modal
        /* While the save is in flight the guard cannot be dismissed: its
           handler still switches tabs on success, so closing it early would
           move the reader after they chose to stay. */
        busy={savingBeforeSwitch}
        open={pendingTab !== null}
        onOpenChange={(open) => {
          if (!open) setPendingTab(null);
        }}
        title={t('page.console.settingV2.leaveGuard.title', {
          defaultValue: '这一组还有未保存的改动',
        })}
        description={t('page.console.settingV2.leaveGuard.description', {
          defaultValue: '切到别的分组会丢弃它们。要先保存吗？',
        })}
        footer={
          <>
            <Button
              disabled={savingBeforeSwitch}
              onClick={() => setPendingTab(null)}
              variant='secondary'
            >
              {t('page.console.settingV2.leaveGuard.stay', {
                defaultValue: '留在这里',
              })}
            </Button>
            <Button
              disabled={savingBeforeSwitch}
              onClick={() => {
                const next = pendingTab;
                setPendingTab(null);
                if (next) onTabChange(next);
              }}
              variant='ghost-danger'
            >
              {t('page.console.settingV2.leaveGuard.discard', {
                defaultValue: '放弃并切换',
              })}
            </Button>
            <Button
              loading={savingBeforeSwitch}
              onClick={async () => {
                const next = pendingTab;
                if (!next) return;
                // A failed save keeps the drafts and the reader where they
                // are; the field's own message explains why.
                setSavingBeforeSwitch(true);
                const saved = await saveAll?.();
                setSavingBeforeSwitch(false);
                if (!saved) return;
                setPendingTab(null);
                onTabChange(next);
              }}
            >
              {t('page.console.settingV2.leaveGuard.save', {
                defaultValue: '保存并切换',
              })}
            </Button>
          </>
        }
      />
    </>
  );
}
