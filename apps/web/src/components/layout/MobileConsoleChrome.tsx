import {
  BarChart3,
  CreditCard,
  Grid2X2,
  KeyRound,
  Menu,
  Package,
  type LucideIcon,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  consoleRouteLabelKeys,
  isUsageLogsPath,
  resolveConsoleRouteMeta,
} from './routeMeta';
import { preloadConsoleRoute } from './routePreload';
import { useConsoleScope } from '../../context/ConsoleScope';
import { useSession } from '../../context/User';
import { useSidebar } from '../../hooks/common/useSidebar';
import VersionPlate from '../announcements/VersionPlate';
import { StatusCenter } from '../ui/status-center';

/* The five tabs carry the sidebar's own names, in the sidebar's order, so a
   phone and a desktop never call the same page two things. 个人设置 sits behind
   the avatar in the top bar. */
const BOTTOM_TABS = [
  {
    to: '/console',
    labelKey: 'nav.sidebarDashboard',
    fallback: '总览',
    icon: Grid2X2,
    exact: true,
    section: 'workspace',
    module: 'detail',
  },
  {
    to: '/console/models',
    labelKey: 'nav.sidebarModels',
    fallback: '模型',
    icon: Package,
  },
  {
    to: '/console/token',
    labelKey: 'nav.apiKeys',
    fallback: 'API Keys',
    icon: KeyRound,
    section: 'workspace',
    module: 'token',
  },
  {
    to: '/console/usage?tab=logs',
    labelKey: 'nav.sidebarUsage',
    fallback: '用量与日志',
    icon: BarChart3,
    match: '/console/usage',
    queryTab: 'logs',
    section: 'workspace',
    module: 'usage',
  },
  {
    to: '/console/wallet',
    labelKey: 'nav.wallet',
    fallback: '钱包',
    icon: CreditCard,
    section: 'account',
    module: 'wallet',
  },
] satisfies Array<{
  exact?: boolean;
  fallback: string;
  icon: LucideIcon;
  labelKey: string;
  match?: string;
  module?: string;
  queryTab?: string;
  section?: string;
  to: string;
}>;

type MobileConsoleChromeProps = {
  drawerOpen?: boolean;
  onMenuToggle?: () => void;
  pathname: string;
  placement?: 'top' | 'bottom';
  search?: string;
};

const MobileConsoleChrome = ({
  placement,
  pathname,
  search = '',
  onMenuToggle,
  drawerOpen,
}: MobileConsoleChromeProps) => {
  const { t } = useTranslation();
  const userSession = useSession();
  const { isModuleVisible } = useSidebar();
  const { availableScopes } = useConsoleScope();
  const platformUser = availableScopes.some(
    (scope) => scope.plane === 'platform',
  );

  if (placement === 'bottom') {
    return (
      <nav
        className='mobile-console-tabbar'
        aria-label={t('legacy.i18n.mobile_console_navigation.05ece7334d')}
      >
        {BOTTOM_TABS.filter(
          ({ module, section }) =>
            !module || !section || isModuleVisible(section, module),
        ).map(
          ({ to, labelKey, fallback, icon: Icon, exact, match, queryTab }) => {
            const target = match ?? to;
            const active = queryTab
              ? isUsageLogsPath(pathname, search)
              : exact
                ? pathname === target
                : pathname.startsWith(target);
            return (
              <Link
                key={to}
                to={to}
                className={`mobile-console-tabbar__item ${active ? 'is-active' : ''}`}
                onFocus={() => preloadConsoleRoute(to.split('?')[0])}
                onPointerEnter={() => preloadConsoleRoute(to.split('?')[0])}
              >
                <Icon size={20} strokeWidth={1.7} aria-hidden='true' />
                <span>{t(labelKey, { defaultValue: fallback })}</span>
              </Link>
            );
          },
        )}
      </nav>
    );
  }

  const title = resolveConsoleRouteMeta(pathname);
  // The same predicate the sidebar uses, so the top bar never disagrees with
  // the drawer beside it or with the page heading below it.
  const routeName = consoleRouteLabelKeys(title, platformUser);
  // Home is the one route whose name depends on who is reading: an operator
  // lands on the platform overview, everyone else on their own 总览.
  const titleLabel =
    pathname === '/console' && platformUser
      ? t('console.scope.platformLabel', { defaultValue: 'Admin console' })
      : t(routeName.label, { defaultValue: routeName.fallback });
  const user =
    userSession.user && typeof userSession.user === 'object'
      ? (userSession.user as { display_name?: string; username?: string })
      : undefined;
  const initial = (user?.display_name || user?.username || '?')
    .trim()
    .charAt(0)
    .toUpperCase();

  return (
    <header className='mobile-console-topbar'>
      <div className='mobile-console-toolbar'>
        {onMenuToggle ? (
          <button
            type='button'
            className='mobile-console-menu-trigger'
            aria-label={
              drawerOpen
                ? t('legacy.i18n.collapse_menu.1617b91983')
                : t('legacy.i18n.expand_menu.95bdb0ab15')
            }
            aria-expanded={Boolean(drawerOpen)}
            onClick={onMenuToggle}
          >
            <Menu size={18} />
          </button>
        ) : null}
        <div className='mobile-console-title'>
          <span>{titleLabel}</span>
        </div>
        <div className='mobile-console-actions'>
          {/* Only the page name and two status icons live up here: service
              status (it used to float over the content, bottom-right) and the
              product updates plate. Theme and language are in the drawer. */}
          <StatusCenter placement='inline' />
          <VersionPlate variant='mobile' />
          <Link
            to='/console/personal'
            className='mobile-console-avatar'
            aria-label={t('nav.personalSettings', {
              defaultValue: '个人设置',
            })}
          >
            {initial}
          </Link>
        </div>
      </div>
    </header>
  );
};

export default MobileConsoleChrome;
