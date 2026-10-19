import {
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigationType } from 'react-router-dom';
import ConsoleScopeBar from './ConsoleScopeBar';
import ConsoleTopBar from './ConsoleTopBar';
import ChromeControls from './headerbar/ChromeControls';
import MobileConsoleChrome from './MobileConsoleChrome';
import { isAppOwnedPath, normalizeShellPath } from './route-shell';
import {
  consoleRouteLabelKeys,
  getRouteTransitionKind,
  resolveConsoleRouteMeta,
} from './routeMeta';
import SiderBar from './SiderBar';
import App from '../../App';
import { StatusContext } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import { UserContext } from '../../context/User';
import { surfaceForPath } from '../../extensions';
import { surfaceLanguageKey } from '../../extensions/i18n';
import { ProjectRemovedNotice } from '../../features/projects/components/ProjectRemovedNotice';
import { useProject } from '../../features/projects/ProjectContext';
import { API } from '../../helpers/api';
import { setStatusData } from '../../helpers/data';
import {
  getLogo,
  getSystemName,
  isRoot,
  safeJsonParse,
} from '../../helpers/utils';
import { MOBILE_BREAKPOINT, useIsMobile } from '../../hooks/common/useIsMobile';
import { unwrap } from '../../lib/api-error';
import { useRoutePrefetch } from '../../lib/route-prefetch';
import WhatsNewDialog from '../announcements/WhatsNewDialog';
import ErrorBoundary from '../common/ErrorBoundary';
import PublicFooter from '../marketing/PublicFooter';
import PublicHeader from '../marketing/PublicHeader';
import { StatusCenter, recordStatusIssue } from '../ui';
import type { UserPayload } from '../../context/User/reducer';
import type { CSSProperties } from 'react';

const TABLET_BREAKPOINT = 1024;
const useIsomorphicLayoutEffect =
  typeof window === 'undefined' ? useEffect : useLayoutEffect;

type ResizeHandler = (width: number) => void;

type ApiGetConfig = NonNullable<Parameters<typeof API.get>[1]> & {
  skipErrorHandler?: boolean;
};

const debounce = (fn: ResizeHandler, delay: number): ResizeHandler => {
  let timeoutId: ReturnType<typeof setTimeout> | undefined;
  return (width: number) => {
    clearTimeout(timeoutId);
    timeoutId = setTimeout(() => fn(width), delay);
  };
};

const CARD_PRO_PAGES = [
  '/console/channel',
  '/console/token',
  '/console/models',
];

const AUTH_SHELL_ROUTES = [
  '/login',
  '/login/password',
  '/register',
  '/reset',
  '/user/reset',
  '/auth/oidc/callback',
];

const AUTH_SHELL_OWNED_CONTROLS_ROUTES = [
  '/login',
  '/login/password',
  '/register',
  '/reset',
  '/user/reset',
  '/auth/oidc/callback',
];

const PUBLIC_NO_FOOTER_ROUTES = ['/', '/quickstart'];

const PageLayout = () => {
  // A hovered or focused link starts its destination's chunk, so the click
  // lands on a module that is already in memory instead of waiting for one.
  useRoutePrefetch();
  const [, userDispatch] = useContext(UserContext);
  const [, statusDispatch] = useContext(StatusContext);
  const { removedNotice } = useProject();
  const location = useLocation();
  const pathname = normalizeShellPath(location.pathname);
  const isMarketingRoute = !isAppOwnedPath(pathname);
  // Marketing layout is CSS-responsive and has no mobile console chrome.
  // Avoid a synchronous media-store update interrupting its hydration.
  const isMobile = useIsMobile(!isMarketingRoute);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const { i18n, t } = useTranslation();
  const actualTheme = useActualTheme();
  const navigationType = useNavigationType();
  const [windowWidth, setWindowWidth] = useState(
    typeof window !== 'undefined' ? window.innerWidth : 1200,
  );
  const debouncedResizeRef = useRef<ResizeHandler | null>(null);
  const contentRef = useRef<HTMLElement | null>(null);
  const previousLocationKeyRef = useRef(location.key);
  const previousPathnameRef = useRef<string | null>(null);
  const scrollPositionsRef = useRef(
    new Map<string, { content: number; window: number }>(),
  );
  const restoreMobileMenuFocusRef = useRef(false);
  // The live region owns no React children. Updating its text must not
  // re-render App after the destination has already painted.
  const routeAnnouncementRef = useRef<HTMLParagraphElement | null>(null);
  const closeMobileDrawer = useCallback((restoreFocus = true) => {
    restoreMobileMenuFocusRef.current = restoreFocus;
    setDrawerOpen(false);
  }, []);

  useEffect(() => {
    if (drawerOpen || !restoreMobileMenuFocusRef.current) return;
    restoreMobileMenuFocusRef.current = false;
    window.setTimeout(() => {
      document
        .querySelector<HTMLButtonElement>('.mobile-console-menu-trigger')
        ?.focus();
    }, 0);
  }, [drawerOpen]);

  useEffect(() => {
    debouncedResizeRef.current = debounce((width) => {
      setWindowWidth(width);
    }, 150);

    const handleResize = () => {
      debouncedResizeRef.current?.(window.innerWidth);
    };

    window.addEventListener('resize', handleResize);
    return () => {
      window.removeEventListener('resize', handleResize);
    };
  }, []);

  const isTablet = useMemo(
    () => windowWidth >= MOBILE_BREAKPOINT && windowWidth < TABLET_BREAKPOINT,
    [windowWidth],
  );

  const contentPadding = useMemo(() => {
    if (isMobile) return '12px';
    if (isTablet) return '16px';
    return '24px';
  }, [isMobile, isTablet]);

  const shouldHideFooter = CARD_PRO_PAGES.includes(pathname);
  const shouldInnerPadding =
    pathname === '/console' || pathname.startsWith('/console/');
  const isConsoleRoute =
    pathname === '/console' || pathname.startsWith('/console/');
  const isAuthRoute = AUTH_SHELL_ROUTES.includes(pathname);
  // An extension's full-screen app wears neither console nor public chrome.
  const surface = surfaceForPath(pathname);
  const isSurfaceRoute = Boolean(surface);
  const surfaceLanguage = surfaceLanguageKey(pathname);

  const showMobileConsoleChrome = isConsoleRoute && isMobile;
  // App-owned public pages (payment returns, invites, 403, /quickstart …)
  // wear the same header as the marketing site.
  const showPublicHeader =
    !isAuthRoute && !isConsoleRoute && !isMarketingRoute && !isSurfaceRoute;
  const renderSider = isConsoleRoute;
  const showShellChromeControls =
    isAuthRoute && !AUTH_SHELL_OWNED_CONTROLS_ROUTES.includes(pathname);
  const showFooter =
    !isAuthRoute &&
    !isConsoleRoute &&
    !isMarketingRoute &&
    !isSurfaceRoute &&
    !shouldHideFooter &&
    !PUBLIC_NO_FOOTER_ROUTES.includes(pathname);
  const routeTransitionKind = useMemo(
    () =>
      getRouteTransitionKind(
        previousPathnameRef.current,
        location.pathname,
        navigationType,
      ),
    [location.key, location.pathname, navigationType],
  );

  useIsomorphicLayoutEffect(() => {
    const entryKey = location.key;
    const content = contentRef.current;
    let contentScrollTop = content?.scrollTop ?? 0;
    let windowScrollTop = window.scrollY;
    const captureContentScroll = () => {
      contentScrollTop = content?.scrollTop ?? 0;
    };
    const captureWindowScroll = () => {
      windowScrollTop = window.scrollY;
    };
    content?.addEventListener('scroll', captureContentScroll, {
      passive: true,
    });
    window.addEventListener('scroll', captureWindowScroll, { passive: true });
    return () => {
      scrollPositionsRef.current.set(entryKey, {
        content: contentScrollTop,
        window: windowScrollTop,
      });
      content?.removeEventListener('scroll', captureContentScroll);
      window.removeEventListener('scroll', captureWindowScroll);
    };
  }, [location.key]);

  useEffect(() => {
    document.body.classList.remove('sidebar-collapsed');
  }, []);

  useEffect(() => {
    const handleOffline = () =>
      recordStatusIssue({
        kind: 'warning',
        message: 'This device is offline. Existing data remains visible.',
        source: 'Network',
      });
    window.addEventListener('offline', handleOffline);
    return () => window.removeEventListener('offline', handleOffline);
  }, []);

  useEffect(() => {
    restoreMobileMenuFocusRef.current = false;
    setDrawerOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    const previousKey = previousLocationKeyRef.current;
    const previousPathname = previousPathnameRef.current;
    const isInitialRoute = previousPathname === null;

    if (isInitialRoute) {
      previousLocationKeyRef.current = location.key;
      previousPathnameRef.current = location.pathname;
      return;
    }

    if (
      previousKey === location.key &&
      previousPathname === location.pathname
    ) {
      return;
    }

    if (previousPathname === location.pathname) {
      previousLocationKeyRef.current = location.key;
      return;
    }

    const nextScrollPosition =
      navigationType === 'POP'
        ? (scrollPositionsRef.current.get(location.key) ?? {
            content: 0,
            window: 0,
          })
        : { content: 0, window: 0 };

    previousLocationKeyRef.current = location.key;
    previousPathnameRef.current = location.pathname;

    const frame = window.requestAnimationFrame(() => {
      window.scrollTo({ top: nextScrollPosition.window });
      if (contentRef.current) {
        contentRef.current.scrollTop = nextScrollPosition.content;
      }

      const heading = contentRef.current?.querySelector('h1');
      const consoleMeta = isConsoleRoute
        ? resolveConsoleRouteMeta(location.pathname)
        : null;
      const consoleName = consoleMeta
        ? consoleRouteLabelKeys(consoleMeta, isRoot())
        : null;
      const label =
        heading?.textContent?.trim() ||
        (consoleName
          ? t(consoleName.label, { defaultValue: consoleName.fallback })
          : document.title);
      const announcement = label ? `${label} — page loaded` : 'Page loaded';
      contentRef.current?.setAttribute('aria-label', announcement);
      if (routeAnnouncementRef.current) {
        routeAnnouncementRef.current.textContent = announcement;
      }
      contentRef.current?.focus({ preventScroll: true });
    });

    return () => window.cancelAnimationFrame(frame);
  }, [
    isConsoleRoute,
    isMobile,
    location.key,
    location.pathname,
    location.search,
    navigationType,
    t,
  ]);

  const loadUser = useCallback(() => {
    const raw = localStorage.getItem('user');
    const data = safeJsonParse<UserPayload>(raw);
    if (data) {
      userDispatch({ type: 'login', payload: data });
    } else if (raw) {
      localStorage.removeItem('user');
    }
  }, [userDispatch]);

  const loadStatus = useCallback(async () => {
    try {
      const config: ApiGetConfig = { skipErrorHandler: true };
      const data = unwrap<unknown>(await API.get('/api/status', config));
      const statusData =
        data && typeof data === 'object'
          ? (data as Record<string, unknown> & { system_name?: string })
          : {};
      statusDispatch({ type: 'set', payload: statusData });
      setStatusData(statusData);
      document.title = statusData.system_name || getSystemName();
    } catch (error) {
      console.warn('[PageLayout] Failed to load status', error);
      recordStatusIssue({
        kind: 'unavailable',
        message: 'Backend status could not be loaded.',
        source: 'Runtime',
      });
    }
  }, [statusDispatch]);

  useEffect(() => {
    if (isMarketingRoute) return;

    const configuredLogo = getLogo();
    const favicon =
      configuredLogo !== '/logo.png'
        ? configuredLogo
        : actualTheme === 'dark'
          ? '/favicon-paper.svg'
          : '/favicon-graphite.svg';
    const linkElement = document.querySelector<HTMLLinkElement>('#app-favicon');
    if (linkElement) {
      linkElement.href = favicon;
    }
  }, [actualTheme, isMarketingRoute]);

  useEffect(() => {
    if (isMarketingRoute) return;

    loadUser();
    loadStatus().catch(console.error);

    const systemName = getSystemName();
    if (systemName) {
      document.title = systemName;
    }

    // A surface follows the language of its public page the visitor came
    // from (i18n.ts); everything else keeps the console's stored choice.
    const savedLang =
      (surfaceLanguage && localStorage.getItem(surfaceLanguage)) ||
      localStorage.getItem('i18nextLng');
    if (savedLang) {
      i18n.changeLanguage(savedLang);
    }
  }, [i18n, isMarketingRoute, loadStatus, loadUser, surfaceLanguage]);

  return (
    <div
      className={[
        'app-shell-v2',
        isConsoleRoute ? 'app-shell-v2--console' : '',
        surface?.shellClassName ?? '',
        isAuthRoute ? 'app-shell-v2--auth' : '',
        'app-shell-v2--bare',
      ]
        .filter(Boolean)
        .join(' ')}
      style={
        {
          '--shell-content-padding': shouldInnerPadding
            ? contentPadding
            : '0px',
          '--shell-main-offset':
            !isMobile && renderSider ? 'var(--sidebar-current-width)' : '0px',
          '--shell-top-offset': showMobileConsoleChrome
            ? 'var(--mobile-console-top-height)'
            : '0px',
        } as CSSProperties
      }
    >
      {showShellChromeControls ? (
        <ChromeControls className='app-shell-global-controls' />
      ) : null}

      {showMobileConsoleChrome ? (
        <MobileConsoleChrome
          placement='top'
          pathname={location.pathname}
          search={location.search}
          drawerOpen={drawerOpen}
          onMenuToggle={() => setDrawerOpen((prev) => !prev)}
        />
      ) : null}

      <div className='app-shell-body-v2'>
        {isMobile && isConsoleRoute ? (
          <button
            type='button'
            aria-label={t('legacy.i18n.close_sidebar.7b6118813a')}
            aria-hidden={!drawerOpen}
            className='app-shell-sidebar__scrim'
            data-state={drawerOpen ? 'open' : 'closed'}
            onClick={() => closeMobileDrawer(true)}
            tabIndex={drawerOpen ? 0 : -1}
          />
        ) : null}

        {renderSider ? (
          <aside
            aria-hidden={isMobile && !drawerOpen}
            className='app-shell-sidebar app-shell-sidebar--split'
            data-mobile={isMobile ? 'true' : undefined}
            data-state={!isMobile || drawerOpen ? 'open' : 'closed'}
          >
            <SiderBar
              onNavigate={() => {
                if (isMobile) closeMobileDrawer(false);
              }}
            />
          </aside>
        ) : null}

        <main
          className='app-shell-main'
          data-mobile-drawer-open={isMobile && drawerOpen ? 'true' : undefined}
        >
          {showPublicHeader ? <PublicHeader /> : null}
          <section
            ref={contentRef}
            className='app-shell-content'
            data-refresh-state='ready'
            tabIndex={-1}
          >
            <ErrorBoundary
              ground={!isConsoleRoute}
              resetKey={location.pathname}
            >
              <div
                className='app-route-stage'
                data-transition={routeTransitionKind}
                key={location.pathname}
              >
                {isConsoleRoute ? <ConsoleScopeBar /> : null}
                {/* Where you are, plus theme, language, updates and the
                    account — the controls the console had nowhere to put. */}
                {isConsoleRoute && !isMobile ? <ConsoleTopBar /> : null}
                {isConsoleRoute && removedNotice ? (
                  <ProjectRemovedNotice />
                ) : (
                  <App />
                )}
              </div>
            </ErrorBoundary>
          </section>
          {showFooter ? <PublicFooter /> : null}
        </main>
      </div>

      {showMobileConsoleChrome ? (
        <MobileConsoleChrome
          placement='bottom'
          pathname={location.pathname}
          search={location.search}
        />
      ) : null}
      <p
        ref={routeAnnouncementRef}
        className='sr-only'
        aria-atomic='true'
        aria-live='polite'
      />
      {/* On a phone the status button lives in the top bar instead. */}
      {isConsoleRoute && !showMobileConsoleChrome ? (
        <StatusCenter className='app-shell-status-center' />
      ) : null}
      <WhatsNewDialog />
    </div>
  );
};

export default PageLayout;
