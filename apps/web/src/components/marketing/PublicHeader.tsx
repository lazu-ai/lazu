import { Check, Globe2, Menu, Moon, Sun, X } from 'lucide-react';
import { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router-dom';
import Reveal from './Reveal';
import ThemedBrandLogo from './ThemedBrandLogo';
import { StatusContext } from '../../context/Status';
import { useActualTheme, useSetTheme } from '../../context/Theme';
import { useSession } from '../../context/User';
import { webExtensions } from '../../extensions';
import { getDocsHomeURL } from '../../helpers/brandUrls';
import {
  isMarketingBarePath,
  localizedMarketingPath,
  splitMarketingPath,
  type MarketingLocale,
} from '../../helpers/marketingLocale';
// Reuse the existing landing nav styles so visual parity is preserved
// across every public route without forking a
// copy of the CSS. The `.lazu-public-header` override below this
// import defangs the landing wrapper's fullscreen behaviour when it
// is used purely as a token scope for the shared header.
import '../../pages/Home/lazu-landing.css';

// Keep in sync with the locales bundled by apps/web/src/i18n/i18n.ts.
// The console header (ChromeControls) exposes the same four locales;
// we mirror them here so every public surface offers the same set.
const LANGUAGE_OPTIONS = [
  { value: 'en', label: 'English', shortLabel: 'EN' },
  { value: 'zh', label: '简体中文', shortLabel: '中' },
  { value: 'zh-TW', label: '繁體中文', shortLabel: '繁' },
  { value: 'ja', label: '日本語', shortLabel: '日' },
] as const;

const normalizeLandingLanguage = (language?: string | null) => {
  if (!language) return 'en';
  const lower = String(language).replace(/_/g, '-');
  if (/^zh[-]?(tw|hk|hant|mo)/i.test(lower)) return 'zh-TW';
  return lower.split('-')[0].toLowerCase();
};

// Read on use: the extension list is not ready while modules load.
const extensionHeaderItems = () =>
  webExtensions().flatMap((extension) => extension.publicNav?.header ?? []);

const sectionOf = (barePath: string) => {
  if (/^\/models(\/|$)/.test(barePath)) return 'models';
  if (/^\/faq\/?$/.test(barePath)) return 'faq';
  const extension = extensionHeaderItems().find(
    (item) => barePath === item.path || barePath.startsWith(`${item.path}/`),
  );
  return extension?.key ?? '';
};

type PublicHeaderProps = {
  /** Render for a path other than the router's (chrome partials for the
   *  API-rendered /models pages and extensions' pages). */
  pathnameOverride?: string;
};

const PublicHeader = ({ pathnameOverride }: PublicHeaderProps = {}) => {
  const { t, i18n } = useTranslation('marketing');
  const location = useLocation();
  const { barePath, locale: pathLocale } = splitMarketingPath(
    pathnameOverride ?? location.pathname,
  );
  // Keep header links inside the current locale tree so a /zh/ visitor —
  // and a crawler walking /zh/ — stays on (and discovers) the /zh/ pages.
  const inLocale = (bare: string) =>
    isMarketingBarePath(bare) ? localizedMarketingPath(bare, pathLocale) : bare;
  const actualTheme = useActualTheme();
  const setTheme = useSetTheme();
  const [statusState] = useContext(StatusContext);
  const isDark = actualTheme === 'dark';
  const brandName = t('brand.name');

  const docsLink = useMemo(
    () =>
      import.meta.env.VITE_BRAND_DOCS_URL ||
      statusState?.status?.docs_link ||
      getDocsHomeURL(pathLocale),
    [statusState, pathLocale],
  );
  // /models (and pages extensions add) are rendered by the API, not by this
  // SPA, so they are plain document links. The section they own is marked
  // for the neutral active fill.
  const activeSection = sectionOf(barePath);
  const navItems: {
    key: string;
    href: string;
    label: string;
    internal?: boolean;
    order: number;
  }[] = [
    {
      key: 'models',
      href: localizedMarketingPath('/models', pathLocale),
      label: t('landing.nav.models'),
      order: 10,
    },
    { key: 'docs', href: docsLink, label: t('landing.nav.docs'), order: 30 },
    {
      key: 'faq',
      href: inLocale('/faq'),
      label: t('landing.nav.faq'),
      internal: true,
      order: 50,
    },
    ...extensionHeaderItems().map((item) => ({
      key: item.key,
      href: localizedMarketingPath(item.path, pathLocale),
      label: item.label[pathLocale] ?? item.label.en,
      order: item.order,
    })),
  ].sort((a, b) => a.order - b.order);
  const [menuOpen, setMenuOpen] = useState(false);

  // Crawlers and the prerendered HTML always see the signed-out actions; a
  // signed-in visitor gets the way back to the console once the page is
  // live (after mount, so hydration matches the prerender).
  const userSession = useSession();
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const signedIn = mounted && Boolean(userSession.user);

  // Each language is a real link to this page in that language, so the
  // alternates are crawlable from the page body as well as from hreflang.
  // Pages without a language in their URL (payment returns, invites …)
  // switch in place. The static copy on API-rendered pages is repointed at
  // the page's own hreflang alternates by public-site/chrome.ts.
  const hasLanguageURL =
    pathnameOverride !== undefined || isMarketingBarePath(barePath);
  const languageHref = (language: MarketingLocale) =>
    hasLanguageURL
      ? localizedMarketingPath(barePath, language)
      : location.pathname + location.search;

  const handleThemeToggle = () => {
    if (typeof setTheme === 'function') {
      setTheme(isDark ? 'light' : 'dark');
    }
  };

  const [langMenuOpen, setLangMenuOpen] = useState(false);
  const langWrapperRef = useRef<HTMLDivElement | null>(null);
  const currentLanguage = normalizeLandingLanguage(i18n.language);
  const activeLanguage = useMemo(
    () =>
      LANGUAGE_OPTIONS.find((option) => option.value === currentLanguage) ||
      LANGUAGE_OPTIONS[0],
    [currentLanguage],
  );

  useEffect(() => {
    if (!langMenuOpen) return;
    const onPointerDown = (event: MouseEvent) => {
      if (!langWrapperRef.current) return;
      if (!langWrapperRef.current.contains(event.target as Node)) {
        setLangMenuOpen(false);
      }
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setLangMenuOpen(false);
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [langMenuOpen]);

  // i18next is configured with useSuspense: true, so an unawaited
  // changeLanguage() in the middle of the render cycle can suspend the
  // tree mid-click and leave the menu in an inconsistent state — users
  // see the dropdown close without the language actually switching.
  // Persisting the choice synchronously, then closing the menu and
  // kicking off the async change in a microtask, gives React a clean
  // commit before Suspense kicks in. Errors are swallowed because the
  // user already saw the menu close — surfacing a stack trace would be
  // noisier than the (rare) silent fallback to the stored preference
  // on the next reload.
  const handleLanguageChange = (language: string) => {
    if (i18n.language === language) {
      setLangMenuOpen(false);
      return;
    }
    try {
      localStorage.setItem('i18nextLng', language);
    } catch {
      // Storage may be blocked (Safari ITP / locked-down enterprise);
      // i18next-browser-languagedetector also writes a cookie, so the
      // preference still survives the reload.
    }
    setLangMenuOpen(false);
    // On a marketing page the language has a URL: navigate to it so the
    // address bar, the prerendered variant and the rendered language agree
    // (MarketingLayout syncs i18n from the prefix). Elsewhere (console),
    // languages have no URL — switch i18n directly as before.
    if (isMarketingBarePath(barePath)) {
      const target = localizedMarketingPath(
        barePath,
        language as MarketingLocale,
      );
      if (target !== location.pathname) window.location.assign(target);
      if (language === 'en') {
        queueMicrotask(() => {
          void i18n.changeLanguage('en').catch(() => {});
        });
      }
      return;
    }
    queueMicrotask(() => {
      void i18n.changeLanguage(language).catch(() => {});
    });
  };

  return (
    <div className='lazu-public-header lazu-landing'>
      <Reveal as='nav' className='lazu-landing__nav'>
        <Link reloadDocument to={inLocale('/')} className='lazu-landing__brand'>
          <ThemedBrandLogo size='header' alt={brandName} />
        </Link>
        <div className='lazu-landing__nav-links'>
          {navItems.map((item) =>
            item.internal ? (
              <Link
                reloadDocument
                key={item.key}
                to={item.href}
                data-nav={item.key}
                aria-current={activeSection === item.key ? 'page' : undefined}
              >
                {item.label}
              </Link>
            ) : (
              <a
                key={item.key}
                href={item.href}
                data-nav={item.key}
                aria-current={activeSection === item.key ? 'page' : undefined}
              >
                {item.label}
              </a>
            ),
          )}
        </div>
        <div className='lazu-landing__nav-spacer' />
        <div className='lazu-landing__nav-actions'>
          <div className='lazu-landing__lang-wrapper' ref={langWrapperRef}>
            <button
              type='button'
              className='lazu-landing__icon-btn lazu-landing__lang-trigger'
              data-chrome='lang'
              aria-label={t('landing.nav.changeLanguage')}
              aria-haspopup='menu'
              aria-expanded={langMenuOpen}
              onClick={() => setLangMenuOpen((open) => !open)}
            >
              <Globe2 size={13} />
              <span>{activeLanguage.shortLabel}</span>
            </button>
            {/* Always in the markup (hidden when closed) so the static copy
                of this header on API-rendered pages can open it too. */}
            <div
              role='menu'
              className='lazu-landing__lang-menu'
              aria-label={t('landing.nav.changeLanguage')}
              hidden={!langMenuOpen}
            >
              {LANGUAGE_OPTIONS.map((option) => (
                <a
                  role='menuitemradio'
                  aria-checked={option.value === activeLanguage.value}
                  key={option.value}
                  className='lazu-landing__lang-item'
                  data-lang={option.value}
                  href={languageHref(option.value)}
                  hrefLang={option.value}
                  onClick={(event) => {
                    event.preventDefault();
                    handleLanguageChange(option.value);
                  }}
                >
                  <span>{option.label}</span>
                  <Check size={13} className='lazu-landing__lang-check' />
                </a>
              ))}
            </div>
          </div>
          <button
            type='button'
            className='lazu-landing__icon-btn lazu-theme-toggle'
            aria-label={t('landing.nav.toggleTheme')}
            data-chrome='theme'
            onClick={handleThemeToggle}
          >
            <Sun size={14} className='lazu-theme-toggle__sun' />
            <Moon size={14} className='lazu-theme-toggle__moon' />
          </button>
          <button
            type='button'
            className='lazu-landing__icon-btn lazu-landing__menu-trigger'
            aria-label={t('landing.nav.menu')}
            aria-expanded={menuOpen}
            aria-controls='lazu-public-menu'
            data-chrome='menu'
            onClick={() => setMenuOpen((open) => !open)}
          >
            {menuOpen ? <X size={15} /> : <Menu size={15} />}
          </button>
          <Link
            reloadDocument
            to='/login'
            className='lazu-landing__signin'
            data-chrome-account='guest'
            hidden={signedIn}
          >
            {t('landing.nav.signIn')}
          </Link>
          <Link
            reloadDocument
            to='/login'
            className='lazu-landing__btn lazu-landing__btn--primary lazu-landing__btn--sm'
            data-chrome-account='guest'
            hidden={signedIn}
          >
            {t('landing.nav.getKey')}
          </Link>
          <Link
            reloadDocument
            to='/console'
            className='lazu-landing__btn lazu-landing__btn--primary lazu-landing__btn--sm'
            data-chrome-account='member'
            hidden={!signedIn}
          >
            {t('landing.nav.console')}
          </Link>
        </div>
        <nav
          id='lazu-public-menu'
          className='lazu-landing__menu'
          aria-label={t('landing.nav.menu')}
          hidden={!menuOpen}
        >
          {navItems.map((item) =>
            item.internal ? (
              <Link
                reloadDocument
                key={item.key}
                to={item.href}
                aria-current={activeSection === item.key ? 'page' : undefined}
                onClick={() => setMenuOpen(false)}
              >
                {item.label}
              </Link>
            ) : (
              <a
                key={item.key}
                href={item.href}
                aria-current={activeSection === item.key ? 'page' : undefined}
              >
                {item.label}
              </a>
            ),
          )}
        </nav>
      </Reveal>
    </div>
  );
};

export default PublicHeader;
