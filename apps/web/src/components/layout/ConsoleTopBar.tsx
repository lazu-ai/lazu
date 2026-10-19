/* The console's top bar.
 *
 * Everything a reader needs that is not navigation: where they are standing,
 * and the handful of controls that used to be scattered or missing. Before
 * this bar, theme and language only existed on the login screen, the
 * announcement unread state rode on a version plate wedged into the sidebar's
 * brand row, and the account had no entry at all beyond a logout icon.
 *
 * It sits inside the main column rather than across the whole window, so the
 * sidebar keeps its own top edge, and it paints no surface of its own: the
 * page head's tab rule below is the only line, and a bar with a background
 * would read as a second one.
 */
import {
  Bell,
  CircleHelp,
  Globe2,
  LogOut,
  Moon,
  Settings,
  Sun,
  Wallet,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import './console-topbar.css';
import { resolveConsoleSection } from './routeMeta';
import { preloadConsoleRoute } from './routePreload';
import { useConsoleScope } from '../../context/ConsoleScope';
import { StatusContext } from '../../context/Status';
import { useActualTheme, useSetTheme } from '../../context/Theme';
import { UserContext } from '../../context/User';
import { ProjectSwitcher } from '../../features/projects/components/ProjectSwitcher';
import { useProject } from '../../features/projects/ProjectContext';
import { API } from '../../helpers/api';
import { getDocsBaseURL } from '../../helpers/brandUrls';
import { githubNewIssueURL } from '../../helpers/sourceLinks';
import { UPSTREAM_NOTICE, UPSTREAM_URL } from '../../helpers/upstream';
import { showSuccess } from '../../helpers/utils';
import { useAnnouncementsUnread } from '../../hooks/common/useAnnouncementsUnread';
import { useSidebar } from '../../hooks/common/useSidebar';
import { formatVersionLabel } from '../announcements/versionLabel';
import { openWhatsNewDialog } from '../announcements/whatsNewChannel';
import {
  CompactConfirmation,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../ui';

const LANGUAGES = [
  { label: 'English', shortLabel: 'EN', value: 'en' },
  { label: '简体中文', shortLabel: 'ZH', value: 'zh' },
  { label: '繁體中文', shortLabel: '繁', value: 'zh-TW' },
  { label: '日本語', shortLabel: 'JA', value: 'ja' },
];

const normalizeLanguage = (language?: string | null) => {
  if (!language) return 'en';
  const lower = String(language).replace(/_/g, '-');
  if (/^zh[-]?(tw|hk|hant|mo)/i.test(lower)) return 'zh-TW';
  return lower.split('-')[0].toLowerCase();
};

const initial = (name: string) => (name.trim()[0] || '?').toUpperCase();

const ConsoleTopBar = () => {
  const { i18n, t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const location = useLocation();
  const navigate = useNavigate();
  const project = useProject();
  const [userState, userDispatch] = useContext(UserContext);
  const [statusState] = useContext(StatusContext);
  const { activeScope } = useConsoleScope();
  const actualTheme = useActualTheme();
  const rawSetTheme = useSetTheme();
  const setTheme =
    typeof rawSetTheme === 'function' ? rawSetTheme : () => undefined;
  const user = userState.user;
  const { hasUnread } = useAnnouncementsUnread(Boolean(user));
  const { isModuleVisible } = useSidebar();
  const [loggingOut, setLoggingOut] = useState(false);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const cancelLogoutRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (confirmLogout) cancelLogoutRef.current?.focus();
  }, [confirmLogout]);

  const isDark = actualTheme === 'dark';
  const language = normalizeLanguage(i18n.language);
  const activeLanguage =
    LANGUAGES.find((option) => option.value === language) ?? LANGUAGES[0];
  const versionLabel = formatVersionLabel(statusState?.status?.version);
  /* Same order as the sidebar and the public header: a deployment that points
     documentation elsewhere at build time wins over the server's default, and
     the brand default is the floor. There is no in-app /docs route to fall
     back to. */
  const docsHref =
    import.meta.env.VITE_BRAND_DOCS_URL ||
    statusState?.status?.docs_link ||
    getDocsBaseURL();
  /* AGPL-3.0 asks a network service to offer its source to its users. The
     link appears once the deployment says where that source is published. */
  const sourceHref = statusState?.status?.source_url;
  /* Issues have a known address only on a GitHub repository; a source link on
     any other host keeps its own menu entry and offers no issue shortcut. */
  const issueHref = githubNewIssueURL(sourceHref);
  /* An API-key pass has no wallet of its own, and a deployment can switch the
     page off entirely; /console/wallet refuses both. The sidebar and the
     mobile tab bar hide the entry on the same two tests, so this menu does. */
  const showWallet =
    activeScope.kind !== 'key_pass' && isModuleVisible('account', 'wallet');
  /* A deployment can switch model status off, and its route then redirects to
     the catalogue — so the shortcut goes wherever the sidebar's entry goes,
     including away. */
  const showModelStatus = isModuleVisible('workspace', 'channel_health');

  const section = resolveConsoleSection(location.pathname);
  const sectionLabel = {
    account: t('nav.sidebarAccount', { defaultValue: '账户' }),
    admin: t('nav.platformAdmin', { defaultValue: '平台管理' }),
    project: t('projects.nav.groupSolo', { defaultValue: '团队' }),
    workspace: t('nav.sidebarWorkspace', { defaultValue: '工作台' }),
  }[section];
  const sectionHref = {
    account: '/console/personal',
    admin: '/console/organizations',
    project: '/console/project/members',
    workspace: '/console',
  }[section];

  /* A menu item is not an anchor, so the document-level prefetcher never sees
     it: without this the route's chunk only starts downloading on the click,
     and its data cannot start until the chunk has evaluated. */
  const go = (to: string) => {
    preloadConsoleRoute(to);
    navigate(to);
  };

  const changeLanguage = (next: string) => {
    localStorage.setItem('i18nextLng', next);
    i18n.changeLanguage(next);
  };

  const handleLogout = useCallback(async () => {
    setLoggingOut(true);
    try {
      await API.get('/api/user/logout');
    } catch {
      // The session may already be gone server-side; the client-side cleanup
      // below is what actually signs this browser out.
    }
    showSuccess(t('legacy.i18n.logout_successful.ea3f16cb50'));
    userDispatch({ type: 'logout' });
    localStorage.removeItem('user');
    navigate('/login');
  }, [navigate, t, userDispatch]);

  const viewerName = project.viewerName || user?.username || '';

  return (
    <div className='console-topbar'>
      <nav aria-label={t('console.topbar.where', { defaultValue: '位置' })}>
        <div className='console-topbar__where'>
          <ProjectSwitcher variant='topbar' />
          <span aria-hidden='true' className='console-topbar__sep'>
            ›
          </span>
          <Link className='console-topbar__section' to={sectionHref}>
            {sectionLabel}
          </Link>
        </div>
      </nav>

      <div className='console-topbar__tools'>
        <button
          aria-label={t('console.topbar.updates', { defaultValue: '最新动态' })}
          className='console-topbar__tool console-topbar__tool--labelled'
          data-owns-chrome=''
          onClick={() => openWhatsNewDialog()}
          type='button'
        >
          <Bell aria-hidden='true' size={15} />
          <span>{t('console.topbar.updates')}</span>
          {hasUnread ? (
            <span aria-hidden='true' className='console-topbar__unread' />
          ) : null}
        </button>

        <button
          aria-checked={isDark}
          aria-label={
            isDark
              ? t('legacy.i18n.switch_to_light_mode.dcb754383b')
              : t('legacy.i18n.switch_to_dark_mode.3688be13be')
          }
          className='console-topbar__tool console-topbar__tool--labelled'
          data-owns-chrome=''
          onClick={() => setTheme(isDark ? 'light' : 'dark')}
          role='switch'
          type='button'
        >
          {isDark ? (
            <Moon aria-hidden='true' size={15} />
          ) : (
            <Sun aria-hidden='true' size={15} />
          )}
          <span>
            {t(
              isDark
                ? 'page.console.personal.theme.dark'
                : 'page.console.personal.theme.light',
            )}
          </span>
        </button>

        <DropdownMenu>
          <DropdownMenuTrigger>
            <button
              aria-label={t('common.changeLanguage')}
              className='console-topbar__tool console-topbar__tool--labelled'
              data-owns-chrome=''
              type='button'
            >
              <Globe2 aria-hidden='true' size={15} />
              <span>{activeLanguage.shortLabel}</span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end' className='console-topbar__menu'>
            {LANGUAGES.map((option) => (
              <DropdownMenuItem
                className='console-topbar__item'
                key={option.value}
                onSelect={() => changeLanguage(option.value)}
              >
                <span>{option.label}</span>
                {option.value === activeLanguage.value ? (
                  <span className='console-topbar__item-meta'>
                    {option.shortLabel}
                  </span>
                ) : null}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        {/* Help is also where the build number lives. A gateway's version
            matters when something is wrong, which is exactly when someone
            opens this menu — it does not need a permanent plate of its own. */}
        <DropdownMenu>
          <DropdownMenuTrigger>
            <button
              aria-label={t('console.topbar.help', { defaultValue: '帮助' })}
              className='console-topbar__tool console-topbar__tool--labelled'
              data-owns-chrome=''
              type='button'
            >
              <CircleHelp aria-hidden='true' size={15} />
              <span>{t('console.topbar.help')}</span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end' className='console-topbar__menu'>
            <DropdownMenuItem
              className='console-topbar__item'
              onSelect={() => window.open(docsHref, '_blank', 'noopener')}
            >
              <span>{t('nav.docs', { defaultValue: '文档' })}</span>
            </DropdownMenuItem>
            {showModelStatus ? (
              <DropdownMenuItem
                className='console-topbar__item'
                onMouseEnter={() => preloadConsoleRoute('/console/health')}
                onFocus={() => preloadConsoleRoute('/console/health')}
                onSelect={() => go('/console/health')}
              >
                <span>
                  {t('nav.modelStatus', { defaultValue: '模型状态' })}
                </span>
              </DropdownMenuItem>
            ) : null}
            {versionLabel ? (
              <>
                <div className='console-topbar__rule' />
                <DropdownMenuItem
                  className='console-topbar__item console-topbar__item--quiet'
                  onSelect={() => openWhatsNewDialog()}
                >
                  <span>
                    {t('console.topbar.changelog', {
                      defaultValue: '更新日志',
                    })}
                  </span>
                  <span className='console-topbar__version'>
                    {versionLabel}
                  </span>
                </DropdownMenuItem>
              </>
            ) : null}
            <div className='console-topbar__rule' />
            {sourceHref ? (
              <DropdownMenuItem
                className='console-topbar__item console-topbar__item--quiet'
                onSelect={() => window.open(sourceHref, '_blank', 'noopener')}
              >
                <span>
                  {t('console.topbar.source', { defaultValue: '源代码' })}
                </span>
              </DropdownMenuItem>
            ) : null}
            {issueHref ? (
              <DropdownMenuItem
                className='console-topbar__item console-topbar__item--quiet'
                onSelect={() => window.open(issueHref, '_blank', 'noopener')}
              >
                <span>
                  {t('console.topbar.reportIssue', {
                    defaultValue: '反馈问题',
                  })}
                </span>
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuItem
              className='console-topbar__item console-topbar__item--quiet'
              onSelect={() => window.open(UPSTREAM_URL, '_blank', 'noopener')}
            >
              <span>
                {t('console.topbar.upstream', {
                  defaultValue: '基于 New API',
                })}
              </span>
            </DropdownMenuItem>
            <p className='console-topbar__attribution'>{UPSTREAM_NOTICE}</p>
          </DropdownMenuContent>
        </DropdownMenu>

        <span aria-hidden='true' className='console-topbar__divider' />

        <DropdownMenu onOpenChange={() => setConfirmLogout(false)}>
          <DropdownMenuTrigger>
            <button
              aria-label={t('console.topbar.account', {
                defaultValue: '账户',
              })}
              className='console-topbar__avatar'
              data-owns-chrome=''
              type='button'
            >
              {initial(viewerName || 'U')}
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align='end'
            className={
              confirmLogout
                ? 'lazu-compact-confirmation-surface'
                : 'console-topbar__menu'
            }
          >
            {confirmLogout ? (
              <CompactConfirmation
                title={t('session.logoutConfirm', {
                  defaultValue: '退出当前账户？',
                })}
                cancelLabel={t('common.cancel', { defaultValue: '取消' })}
                confirmLabel={t('session.confirmLogout', {
                  defaultValue: '退出',
                })}
                cancelRef={cancelLogoutRef}
                busy={loggingOut}
                onCancel={() => setConfirmLogout(false)}
                onConfirm={() => void handleLogout()}
              />
            ) : (
              <>
                {viewerName ? (
                  <div className='console-topbar__who'>
                    <b>{viewerName}</b>
                    {typeof user?.email === 'string' && user.email ? (
                      <small>{user.email}</small>
                    ) : null}
                  </div>
                ) : null}
                <DropdownMenuItem
                  className='console-topbar__item'
                  onMouseEnter={() => preloadConsoleRoute('/console/personal')}
                  onFocus={() => preloadConsoleRoute('/console/personal')}
                  onSelect={() => go('/console/personal')}
                >
                  <Settings aria-hidden='true' size={14} />
                  <span>
                    {t('nav.personalSettings', { defaultValue: '个人设置' })}
                  </span>
                </DropdownMenuItem>
                {showWallet ? (
                  <DropdownMenuItem
                    className='console-topbar__item'
                    onMouseEnter={() => preloadConsoleRoute('/console/wallet')}
                    onFocus={() => preloadConsoleRoute('/console/wallet')}
                    onSelect={() => go('/console/wallet')}
                  >
                    <Wallet aria-hidden='true' size={14} />
                    <span>{t('nav.wallet', { defaultValue: '钱包' })}</span>
                  </DropdownMenuItem>
                ) : null}
                <div className='console-topbar__rule' />
                <DropdownMenuItem
                  className='console-topbar__item console-topbar__item--danger'
                  closeOnClick={false}
                  disabled={loggingOut}
                  onSelect={(event) => {
                    event.preventDefault();
                    setConfirmLogout(true);
                  }}
                >
                  <LogOut aria-hidden='true' size={14} />
                  <span>
                    {t('session.confirmLogout', { defaultValue: '退出' })}
                  </span>
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
};

export default ConsoleTopBar;
