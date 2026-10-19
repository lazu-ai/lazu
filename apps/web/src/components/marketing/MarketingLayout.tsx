import { Suspense, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Outlet, useLocation } from 'react-router-dom';
import PublicFooter from './PublicFooter';
import PublicHeader from './PublicHeader';
import { localeFromPath } from '../../helpers/marketingLocale';
import Loading from '../layout/AppLoading';

// Shared layout for every public-facing route (`/`, `/faq`,
// `/about`, `/privacy`, `/terms`). Renders the language + theme
// switcher header above the route content via <Suspense fallback={<Loading />}><Outlet /></Suspense>.
//
// PageLayout draws no header for marketing paths (and gives app-owned
// public pages this same PublicHeader), so the header below is the only top
// bar on marketing routes.
const MarketingLayout = () => {
  const location = useLocation();
  const { i18n } = useTranslation();

  // A locale-prefixed URL asserts its language. i18n.ts already honors the
  // prefix on first load; this covers client-side navigations between
  // prefixed pages (and switching prefixes). Unprefixed paths deliberately
  // leave the visitor's own preference alone.
  useEffect(() => {
    const asserted = localeFromPath(location.pathname);
    if (asserted && i18n.language !== asserted) {
      void i18n.changeLanguage(asserted).catch(() => {});
    }
  }, [location.pathname, i18n]);

  return (
    <>
      <PublicHeader />
      <Suspense fallback={<Loading />}>
        <Outlet />
      </Suspense>
      <PublicFooter />
    </>
  );
};

export default MarketingLayout;
