import { ArrowUpRight, BookOpen, LogOut } from 'lucide-react';
import { useCallback, useContext, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import ChromeControls from './headerbar/ChromeControls';
import { isNavigationPathActive } from './routeMeta';
import { preloadConsoleRoute } from './routePreload';
import { shouldRenderSidebarSection } from './sidebarVisibility';
import { BrandLogo } from '../../brand';
import { useConsoleScope } from '../../context/ConsoleScope';
import { StatusContext } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import { UserContext } from '../../context/User';
import { extensionSidebarEntries } from '../../extensions';
import { ProjectSwitcher } from '../../features/projects/components/ProjectSwitcher';
import { useProject } from '../../features/projects/ProjectContext';
import { API } from '../../helpers/api';
import { getDocsBaseURL } from '../../helpers/brandUrls';
import { resolveGatewayHref } from '../../helpers/gatewayNavigation';
import { getLucideIcon } from '../../helpers/lucide';
import { showSuccess } from '../../helpers/utils';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { useMinimumLoadingTime } from '../../hooks/common/useMinimumLoadingTime';
import { useSidebar } from '../../hooks/common/useSidebar';
import {
  CompactConfirmation,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Skeleton,
} from '../ui';
/* The sidebar renders the `.pj-sidefoot` role row on every console page, but
   the project stylesheet used to arrive only with a project page's own imports
   — so on Usage, Keys and Models it rendered unstyled. The sidebar shows it,
   so the sidebar imports its styles. */
import '../../features/projects/projects.css';

const ROUTER_MAP = {
  token: '/console/token',
  wallet: '/console/wallet',
  user: '/console/organizations',
  usage: '/console/usage',
  key_inspector: '/console/key',
  search_backend: '/console/search-backend',
  setting: '/console/setting',
  announcements: '/console/ops',
  detail: '/console',
  model_ops: '/console/model-ops',
  model_market: '/console/models',
  channel_health: '/console/health',
  personal: '/console/personal',
  project_members: '/console/project/members',
  project_settings: '/console/project/settings',
};

type RouterKey = keyof typeof ROUTER_MAP;

type SidebarItem = {
  allowed?: boolean;
  /* Set for entries that leave the console (docs). They render as an anchor
     with an outbound marker instead of a router Link. */
  href?: string;
  itemKey: RouterKey | 'docs' | (string & {});
  /* Set for entries extensions add; core entries resolve through ROUTER_MAP. */
  to?: string;
  iconKey?: string;
  /* Set for routes that open outside the console shell (a full-screen app
     of its own), so they carry the same outbound marker. */
  leavesConsole?: boolean;
  text: string;
};

type SidebarSection = {
  items: SidebarItem[];
  key: string;
  label: string;
};

type SiderBarProps = {
  onNavigate?: () => void;
};

type IsModuleVisible = (
  sectionKey: string,
  moduleKey: string | null,
) => boolean;

const filterVisibleItems = (
  items: SidebarItem[],
  sectionKey: string,
  isModuleVisible: IsModuleVisible,
) =>
  items.filter(
    (item) =>
      item.allowed !== false && isModuleVisible(sectionKey, item.itemKey),
  );

/* Two independent axes decide visibility (BRAND.md § 02 permissions): the site
 * role owns the platform-admin section, the console scope owns the tenant
 * sections. The admin group therefore never requires the viewer to switch scope
 * first.
 *
 * The workspace group survives the platform plane. It used to be hidden there,
 * which was defensible while platform was an opt-in ops mode — but platform is
 * now the DEFAULT plane for admins, and hiding it stranded every admin on a
 * sidebar with no 总览, no API Keys and no 用量与日志, including whichever of those
 * pages they were standing on. An admin has a real workspace of their own (own
 * keys, own usage, own wallet), and the route guards already let them reach it,
 * so the nav has to agree.
 *
 * The account group is the viewer's own wallet and profile, so it survives every
 * scope; only an API-key pass loses the wallet, and such a pass is still limited
 * to the handful of pages a bare key can actually use. */
const scopeAllowsItem = (
  scopeKind: 'workspace_manager' | 'member' | 'key_pass' | 'platform',
  sectionKey: string,
  itemKey: RouterKey | 'docs' | (string & {}),
) => {
  if (itemKey === 'docs') return true;
  if (sectionKey === 'admin') return true;
  if (sectionKey === 'project') return scopeKind !== 'key_pass';
  if (sectionKey === 'account') {
    return itemKey === 'personal' || scopeKind !== 'key_pass';
  }
  if (scopeKind !== 'key_pass') return true;
  return [
    'model_market',
    'channel_health',
    'usage',
    'key_inspector',
    'personal',
  ].includes(itemKey);
};

const SiderBar = ({ onNavigate = () => undefined }: SiderBarProps) => {
  const { t } = useTranslation();
  const location = useLocation();
  const actualTheme = useActualTheme();
  const [userState, userDispatch] = useContext(UserContext);
  const [statusState] = useContext(StatusContext);
  // Operators can point documentation elsewhere at runtime
  // (general_setting.docs_link); the build-time default is the fallback,
  // same order the public header uses.
  const docsHref =
    import.meta.env.VITE_BRAND_DOCS_URL ||
    statusState?.status?.docs_link ||
    getDocsBaseURL();
  const {
    hasSectionVisibleModules,
    isModuleVisible,
    loading: sidebarLoading,
  } = useSidebar();
  const showSkeleton = useMinimumLoadingTime(sidebarLoading, 200);
  /* The console top bar carries the project switcher, and it is desktop-only:
     on a phone this drawer is the only chrome there is, so the switcher comes
     back here rather than leaving a multi-project reader stranded. */
  const isMobile = useIsMobile();
  const navigate = useNavigate();
  const [logoutOpen, setLogoutOpen] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const handleLogout = useCallback(async () => {
    setLoggingOut(true);
    try {
      await API.get('/api/user/logout');
    } catch {
      // The session may already be gone server-side; the cleanup below is what
      // actually signs this browser out.
    }
    showSuccess(t('legacy.i18n.logout_successful.ea3f16cb50'));
    userDispatch({ type: 'logout' });
    localStorage.removeItem('user');
    navigate('/login');
  }, [navigate, t, userDispatch]);
  const logoTone = actualTheme === 'dark' ? 'paper' : 'graphite';
  const { activeScope, availableScopes } = useConsoleScope();
  const platformUser = availableScopes.some(
    (scope) => scope.plane === 'platform',
  );
  const project = useProject();
  const projectShared = project.enabled && Boolean(project.current?.isShared);
  const projectManager = projectShared && project.isManager;
  const projectOwner = projectShared && project.isOwner;
  const projectMember = projectShared && !project.isManager;
  /* The team group exists before the team does: a solo owner sees one entry
     that opens the page explaining what inviting someone would do. */
  const projectSolo = project.isSoloOwner;
  const status = statusState?.status;
  const gatewayHref = useMemo(
    () =>
      resolveGatewayHref({
        canAdmin: platformUser,
        isModuleVisible,
        scopeKind: activeScope.kind,
      }),
    [activeScope.kind, isModuleVisible, platformUser],
  );

  // IA per the locked brand/foundations/yunmu-console.md refinement:
  // five primary user tasks. Same-question pages are merged
  // behind in-page tabs (files under usage, permissions under personal) and
  // billing + top-up share one wallet, so the sidebar only carries nouns.
  const sections = useMemo<SidebarSection[]>(
    () => [
      {
        key: 'workspace',
        label: t('nav.sidebarWorkspace', { defaultValue: '工作台' }),
        items: filterVisibleItems(
          [
            {
              text: t('nav.sidebarDashboard', { defaultValue: '总览' }),
              itemKey: 'detail',
            },
            {
              text: t('nav.sidebarModels', { defaultValue: '模型' }),
              itemKey: 'model_market',
            },
            {
              // Sits next to 模型 on purpose: picking a model and judging
              // whether its lanes are holding up are the same decision. The
              // page answers "is the model I call healthy" for operators too,
              // so everyone reads 模型状态 — 「通道」 is not a sidebar word.
              text: t('nav.modelStatus', { defaultValue: '模型状态' }),
              itemKey: 'channel_health',
            },
            {
              text: t('nav.apiKeys', { defaultValue: 'API Keys' }),
              itemKey: 'token',
            },
            {
              text: t('nav.sidebarUsage', { defaultValue: '用量与日志' }),
              itemKey: 'usage',
            },
            ...extensionSidebarEntries().map((entry) => ({
              text: entry.label(t),
              itemKey: entry.moduleKey,
              to: entry.to,
              iconKey: entry.iconKey,
              allowed: entry.enabled ? entry.enabled(status) : true,
              leavesConsole: entry.leavesConsole,
            })),
          ],
          'workspace',
          isModuleVisible,
        ).concat({
          text: t('nav.docs', { defaultValue: '文档' }),
          itemKey: 'docs',
          href: docsHref,
        }),
      },
      /* Managers of a shared project get the full group; a solo owner gets
         the one entry that starts a team. Members never see it; their money
         lives under 账户 as 「我的预算」 in the wallet's slot. */
      {
        key: 'project',
        label: projectShared
          ? t('projects.nav.group', {
              name: project.currentName,
              defaultValue: '{{name}} · 项目',
            })
          : t('projects.nav.groupSolo', { defaultValue: '团队' }),
        items: projectManager
          ? [
              {
                text: t('projects.nav.members', { defaultValue: '成员与预算' }),
                itemKey: 'project_members' as const,
              },
              {
                text: t('projects.nav.settings', { defaultValue: '项目设置' }),
                itemKey: 'project_settings' as const,
                allowed: projectOwner,
              },
            ].filter((item) => item.allowed !== false)
          : projectSolo
            ? [
                {
                  text: t('projects.nav.members', {
                    defaultValue: '成员与预算',
                  }),
                  itemKey: 'project_members' as const,
                },
              ]
            : [],
      },
      {
        key: 'account',
        label: t('nav.sidebarAccount', { defaultValue: '账户' }),
        items: filterVisibleItems(
          [
            {
              text: projectMember
                ? t('projects.nav.myBudget', { defaultValue: '我的预算' })
                : t('nav.wallet', { defaultValue: '钱包' }),
              itemKey: 'wallet',
            },
            {
              text: t('nav.personalSettings', { defaultValue: '个人设置' }),
              itemKey: 'personal',
            },
          ],
          'account',
          isModuleVisible,
        ),
      },
      {
        key: 'admin',
        label: t('nav.platformAdmin', { defaultValue: '平台管理' }),
        items: filterVisibleItems(
          [
            {
              text: t('nav.sidebarOrganizations', { defaultValue: '组织' }),
              itemKey: 'user',
              allowed: platformUser,
            },
            {
              // 模型管理 + 定价运营 + 渠道 are one decision, so they are one page.
              text: t('nav.modelOps', { defaultValue: '模型运营' }),
              itemKey: 'model_ops',
              allowed: platformUser,
            },
            {
              text: t('nav.searchBackend', { defaultValue: '搜索后端' }),
              itemKey: 'search_backend',
              allowed: platformUser,
            },
            {
              text: t('nav.ops', { defaultValue: '运营' }),
              itemKey: 'announcements',
              allowed: platformUser,
            },
            {
              text: t('nav.sidebarSettings', { defaultValue: '设置' }),
              itemKey: 'setting',
              allowed: platformUser,
            },
          ],
          'admin',
          isModuleVisible,
        ),
      },
    ],
    [
      docsHref,
      isModuleVisible,
      platformUser,
      project.currentName,
      projectManager,
      projectMember,
      projectOwner,
      projectShared,
      projectSolo,
      status,
      t,
      userState?.user?.role,
    ],
  );

  const renderItem = (item: SidebarItem) => {
    if (item.href) {
      return (
        <a
          key={item.itemKey}
          className='sidebar-v2__item'
          href={item.href}
          rel='noopener noreferrer'
          target='_blank'
        >
          <span className='sidebar-v2__item-icon'>
            <BookOpen aria-hidden='true' size={16} />
          </span>
          <span className='sidebar-v2__item-label'>{item.text}</span>
          <ArrowUpRight
            aria-hidden='true'
            className='sidebar-v2__item-external'
            size={13}
          />
        </a>
      );
    }

    const to = item.to ?? ROUTER_MAP[item.itemKey as RouterKey];
    const isActive = isNavigationPathActive(location.pathname, to);
    const icon = getLucideIcon(item.iconKey ?? item.itemKey, isActive);
    const content = (
      <Link
        key={item.itemKey}
        to={to}
        className={`sidebar-v2__item ${isActive ? 'is-active' : ''}`}
        onFocus={() => preloadConsoleRoute(to)}
        onMouseEnter={() => preloadConsoleRoute(to)}
        onClick={onNavigate}
      >
        <span className='sidebar-v2__item-icon'>{icon}</span>
        <span className='sidebar-v2__item-label'>{item.text}</span>
        {item.leavesConsole ? (
          <ArrowUpRight
            aria-hidden='true'
            className='sidebar-v2__item-external'
            size={13}
          />
        ) : null}
      </Link>
    );

    return content;
  };

  if (showSkeleton) {
    return (
      <div className='sidebar-v2'>
        <div className='sidebar-v2__brand-row'>
          <span className='sidebar-v2__brand'>
            <BrandLogo
              variant='primary'
              tone={logoTone}
              size='sidebar'
              alt='Lazu'
            />
          </span>
        </div>
        <div className='sidebar-v2__content'>
          <span className='sidebar-v2__label'>&nbsp;</span>
        </div>
      </div>
    );
  }

  return (
    <nav className='sidebar-v2'>
      <div className='sidebar-v2__brand-row'>
        <Link
          to={gatewayHref}
          className='sidebar-v2__brand'
          onClick={onNavigate}
        >
          <BrandLogo
            variant='primary'
            tone={logoTone}
            size='sidebar'
            alt='Lazu'
          />
        </Link>
      </div>

      {isMobile ? <ProjectSwitcher onNavigate={onNavigate} /> : null}

      <div className='sidebar-v2__content'>
        {sections.map((section) => {
          const scopedItems = section.items.filter(
            (item) =>
              scopeAllowsItem(activeScope.kind, section.key, item.itemKey) &&
              !(
                item.itemKey === 'token' &&
                projectShared &&
                project.current?.status !== 'active'
              ),
          );
          if (
            !shouldRenderSidebarSection(
              scopedItems,
              section.key === 'project'
                ? scopedItems.length > 0
                : hasSectionVisibleModules(section.key),
            )
          ) {
            return null;
          }

          return (
            <section key={section.key} className='sidebar-v2__section'>
              <div className='sidebar-v2__label'>{section.label}</div>
              {scopedItems.map(renderItem)}
            </section>
          );
        })}
      </div>

      {projectShared || isMobile ? (
        <div className='sidebar-v2__footer'>
          {projectShared ? (
            <div className='pj-sidefoot'>
              <span className='pj-sidefoot__who'>
                <span className='pj-av'>
                  {(project.viewerName.trim()[0] || '?').toUpperCase()}
                </span>
                {project.viewerName}
              </span>
              <span
                className='pj-badge'
                data-tone={project.isManager ? 'evi' : undefined}
              >
                {t(`projects.role.${project.role}`)}
              </span>
            </div>
          ) : null}
          {/* The console top bar owns the account menu, and it is desktop-only.
              On a phone this drawer is the only chrome, so the way out lives
              here — not behind 个人设置, which a deployment can switch off. */}
          {isMobile ? (
            <div className='sidebar-v2__utility'>
              {/* The phone top bar keeps only the page name and its two
                  status icons, so theme and language live in the drawer. */}
              <ChromeControls compact className='sidebar-v2__chrome-controls' />
              <Popover open={logoutOpen} onOpenChange={setLogoutOpen}>
                <PopoverTrigger>
                  <button
                    aria-label={t('legacy.i18n.logout.6a1a5c708c')}
                    className='sidebar-v2__logout'
                    title={t('legacy.i18n.logout.6a1a5c708c')}
                    type='button'
                  >
                    <LogOut aria-hidden='true' size={15} />
                  </button>
                </PopoverTrigger>
                <PopoverContent
                  side='top'
                  align='end'
                  className='lazu-compact-confirmation-surface'
                >
                  <CompactConfirmation
                    title={t('session.logoutConfirm', {
                      defaultValue: '退出当前账户？',
                    })}
                    cancelLabel={t('common.cancel', { defaultValue: '取消' })}
                    confirmLabel={t('session.confirmLogout', {
                      defaultValue: '退出',
                    })}
                    busy={loggingOut}
                    onCancel={() => setLogoutOpen(false)}
                    onConfirm={() => void handleLogout()}
                  />
                </PopoverContent>
              </Popover>
            </div>
          ) : null}
        </div>
      ) : null}
    </nav>
  );
};

export default SiderBar;
