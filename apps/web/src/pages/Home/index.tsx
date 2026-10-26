import { Suspense, lazy, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import LazuLanding from './components/LazuLanding';
import { usePrerender } from '../../context/Prerender';
import { useStatus } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import { API } from '../../helpers/api';
import { splitMarketingPath } from '../../helpers/marketingLocale';
import { sanitizeMarkup } from '../../helpers/sanitize';
import { unwrap } from '../../lib/api-error';
import './home-modern.css';

const HOME_PAGE_CONTENT_CACHE_KEY = 'home_page_content_v3';
const PREVIOUS_HOME_PAGE_CONTENT_CACHE_KEY = 'home_page_content_v2';
const LEGACY_HOME_PAGE_CONTENT_CACHE_KEY = 'home_page_content';

type HomeContentProps = {
  content: string;
};

type ApiGetConfig = NonNullable<Parameters<typeof API.get>[1]> & {
  skipErrorHandler?: boolean;
};

const isExternalHomePageContent = (content = '') =>
  content.startsWith('https://');

const isHtmlContent = (content = '') => /<\/?[a-z][\s\S]*>/i.test(content);

const getTargetOriginFromUrl = (url: string): string | null => {
  try {
    return new URL(url).origin;
  } catch (error) {
    return null;
  }
};

const normalizeOrigin = (value = ''): string | null => {
  const candidate = String(value).trim();
  if (!candidate) return null;
  if (candidate.startsWith('http://') || candidate.startsWith('https://')) {
    return getTargetOriginFromUrl(candidate);
  }
  try {
    return new URL(`https://${candidate}`).origin;
  } catch (error) {
    return null;
  }
};

const getCachedHomeContent = () =>
  localStorage.getItem(HOME_PAGE_CONTENT_CACHE_KEY) ||
  localStorage.getItem(PREVIOUS_HOME_PAGE_CONTENT_CACHE_KEY) ||
  localStorage.getItem(LEGACY_HOME_PAGE_CONTENT_CACHE_KEY) ||
  '';

/* A deployment can replace this page with its own Markdown, and almost none
 * do — but importing the renderer here put the whole remark/micromark pipeline
 * (~340KB of source) into the entry bundle for every visitor to every page.
 * It loads if and when there is custom Markdown to render. */
const HomeMarkdown = lazy(() => import('./HomeMarkdown'));

const HomeContent = ({ content }: HomeContentProps) => {
  const safeHtml = useMemo(
    () => (isHtmlContent(content) ? sanitizeMarkup(content) : ''),
    [content],
  );

  if (safeHtml) {
    return (
      <div
        className='home-markdown-shell'
        dangerouslySetInnerHTML={{ __html: safeHtml }}
      />
    );
  }

  return (
    <div className='home-markdown-shell'>
      <Suspense fallback={null}>
        <HomeMarkdown content={content} />
      </Suspense>
    </div>
  );
};

const Home = () => {
  const { t, i18n } = useTranslation();
  const { route: prerenderRoute } = usePrerender();
  const statusState = useStatus();
  const actualTheme = useActualTheme();
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const [homePageContentLoaded, setHomePageContentLoaded] = useState(
    prerenderRoute !== null &&
      splitMarketingPath(prerenderRoute).barePath === '/',
  );
  const [homePageContent, setHomePageContent] = useState('');
  const browserOrigin =
    typeof window === 'undefined' ? '' : window.location.origin;
  const serverAddress = statusState?.status?.server_address || browserOrigin;
  const trustedIframeOrigins = useMemo(() => {
    const origins = new Set<string>();
    const configuredOrigins = String(
      import.meta.env.VITE_ALLOWED_IFRAME_ORIGINS || '',
    );
    const addOrigin = (value: string) => {
      const normalized = normalizeOrigin(value);
      if (normalized) origins.add(normalized);
    };

    addOrigin(browserOrigin);
    addOrigin(serverAddress);
    configuredOrigins
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
      .forEach(addOrigin);

    return origins;
  }, [browserOrigin, serverAddress]);
  const homePageContentOrigin = useMemo(
    () => getTargetOriginFromUrl(homePageContent),
    [homePageContent],
  );
  const isTrustedHomePageContentOrigin = useMemo(() => {
    if (!isExternalHomePageContent(homePageContent)) return false;
    return (
      typeof homePageContentOrigin === 'string' &&
      trustedIframeOrigins.has(homePageContentOrigin)
    );
  }, [homePageContent, homePageContentOrigin, trustedIframeOrigins]);

  useEffect(() => {
    let cancelled = false;

    const displayHomePageContent = async () => {
      const cachedRawContent = getCachedHomeContent();
      if (typeof cachedRawContent === 'string' && cachedRawContent) {
        setHomePageContent(cachedRawContent);
      }

      try {
        const config: ApiGetConfig = {
          skipErrorHandler: true,
        };
        const data = unwrap<unknown>(
          await API.get('/api/home_page_content', config),
        );
        const contentValue = typeof data === 'string' ? data : '';
        if (cancelled) return;

        setHomePageContent(contentValue);
        localStorage.setItem(HOME_PAGE_CONTENT_CACHE_KEY, contentValue);
        localStorage.removeItem(LEGACY_HOME_PAGE_CONTENT_CACHE_KEY);
      } catch (error) {
        console.error('Failed to fetch homepage content:', error);
        if (!cancelled && !cachedRawContent) setHomePageContent('');
      } finally {
        if (!cancelled) {
          setHomePageContentLoaded(true);
        }
      }
    };

    displayHomePageContent();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!isExternalHomePageContent(homePageContent)) return undefined;

    const iframe = iframeRef.current;
    const targetOrigin = getTargetOriginFromUrl(homePageContent);
    if (!targetOrigin) {
      console.warn(
        '[Home] Skip postMessage: failed to parse iframe target origin.',
        homePageContent,
      );
      return undefined;
    }
    if (!trustedIframeOrigins.has(targetOrigin)) {
      console.warn(
        '[Home] Skip postMessage: iframe origin is not trusted.',
        targetOrigin,
      );
      return undefined;
    }
    if (!iframe) {
      console.warn('[Home] Skip postMessage: iframe element is not available.');
      return undefined;
    }

    const postPreferences = () => {
      iframe.contentWindow?.postMessage(
        { themeMode: actualTheme },
        targetOrigin,
      );
      iframe.contentWindow?.postMessage({ lang: i18n.language }, targetOrigin);
    };

    postPreferences();
    iframe.addEventListener('load', postPreferences);
    return () => iframe.removeEventListener('load', postPreferences);
  }, [actualTheme, homePageContent, i18n.language, trustedIframeOrigins]);

  return (
    <div className='w-full overflow-x-hidden'>
      {!homePageContentLoaded ? (
        <div className='home-loading-shell'>
          <div className='home-loading-panel'>
            {t('page.public.home.loadingHomepage')}
          </div>
        </div>
      ) : homePageContent === '' ? (
        <LazuLanding /> /* When admins inject custom home content, render it inside
           the regular app-shell chrome — only the built-in
           LazuLanding hides the shared header/footer (it brings
           its own nav). */
      ) : (
        <div className='overflow-x-hidden w-full'>
          {isExternalHomePageContent(homePageContent) ? (
            <div className='home-embed-shell'>
              <iframe
                ref={iframeRef}
                src={homePageContent}
                className='home-embed-frame'
                title={t('page.public.home.customContentTitle')}
                sandbox={isTrustedHomePageContentOrigin ? 'allow-scripts' : ''}
                referrerPolicy='no-referrer'
              />
            </div>
          ) : (
            <HomeContent content={homePageContent} />
          )}
        </div>
      )}
    </div>
  );
};

export default Home;
