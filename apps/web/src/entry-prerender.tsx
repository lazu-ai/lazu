import { createInstance } from 'i18next';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { StaticRouter } from 'react-router-dom/server';
import { AppFrame, AppProviders } from './AppRoot';
import PublicFooter from './components/marketing/PublicFooter';
import PublicHeader from './components/marketing/PublicHeader';
import { ThemeProvider } from './context/Theme';
import { webExtensions } from './extensions';
import en from './i18n/locales/en.json';
import ja from './i18n/locales/ja.json';
import zhTW from './i18n/locales/zh-TW.json';
import zh from './i18n/locales/zh.json';
import marketingEn from './marketing/marketing.en.json';
import marketingJa from './marketing/marketing.ja.json';
import marketingZhTW from './marketing/marketing.zh-TW.json';
import marketingZh from './marketing/marketing.zh.json';
import NotFound from './pages/NotFound';

type PublicLocale = 'en' | 'ja' | 'zh' | 'zh-TW';

const prerenderResources = {
  en: { marketing: marketingEn, translation: en.translation },
  ja: { marketing: marketingJa, translation: ja.translation },
  zh: { marketing: marketingZh, translation: zh.translation },
  'zh-TW': { marketing: marketingZhTW, translation: zhTW.translation },
};

const normalizePublicLocale = (language: string): PublicLocale => {
  if (language === 'ja' || language === 'zh' || language === 'zh-TW') {
    return language;
  }
  return 'en';
};

const createPrerenderI18n = async (language: string) => {
  const locale = normalizePublicLocale(language);
  const instance = createInstance();
  await instance.use(initReactI18next).init({
    defaultNS: 'translation',
    fallbackLng: 'en',
    initAsync: false,
    interpolation: { escapeValue: false },
    lng: locale,
    ns: ['translation', 'marketing'],
    react: { useSuspense: false },
    resources: prerenderResources,
  });
  return instance;
};

export const renderMarketingRoute = async (route: string, language = 'en') => {
  const i18n = await createPrerenderI18n(language);
  return renderToString(
    <React.StrictMode>
      <I18nextProvider i18n={i18n}>
        <AppProviders prerenderRoute={route}>
          <StaticRouter location={route}>
            <AppFrame />
          </StaticRouter>
        </AppProviders>
      </I18nextProvider>
    </React.StrictMode>,
  );
};

/**
 * Static copies of the public header and footer for the API-rendered pages
 * (/models and the pages extensions add). `section` is the bare path they stand for, so the header
 * marks the right nav item and the footer's language links land in the same
 * section of each locale. nginx splices these in through SSI.
 */
export const renderPublicChrome = async (
  part: 'header' | 'footer',
  language: string,
  section: string,
) => {
  const i18n = await createPrerenderI18n(language);
  const locale = normalizePublicLocale(language);
  const pathname = locale === 'en' ? section : `/${locale}${section}`;
  // An extension's pages may carry their own header and footer.
  for (const extension of webExtensions()) {
    const own = extension.publicChrome?.(part, pathname, section);
    if (own) return renderToString(own);
  }
  const Part = part === 'header' ? PublicHeader : PublicFooter;
  return renderToString(
    <I18nextProvider i18n={i18n}>
      <AppProviders prerenderRoute={pathname}>
        <StaticRouter location={pathname}>
          <ThemeProvider>
            <Part pathnameOverride={pathname} />
          </ThemeProvider>
        </StaticRouter>
      </AppProviders>
    </I18nextProvider>,
  );
};

/** The 404 page nginx answers unknown addresses with: public chrome around
 *  the locked StatusPage. NotFound is lazy in the SPA, so it is rendered
 *  directly here rather than through the router's Suspense boundary. */
export const renderNotFoundPage = async (language = 'en') => {
  const i18n = await createPrerenderI18n(language);
  const route = '/__not-found';
  return renderToString(
    <I18nextProvider i18n={i18n}>
      <AppProviders prerenderRoute={route}>
        <StaticRouter location={route}>
          <ThemeProvider>
            <PublicHeader />
            <NotFound />
            <PublicFooter />
          </ThemeProvider>
        </StaticRouter>
      </AppProviders>
    </I18nextProvider>,
  );
};
