import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router-dom';
import './public-footer.css';
import ThemedBrandLogo from './ThemedBrandLogo';
import { webExtensions } from '../../extensions';
import {
  getCommunityURL,
  getDocsHomeURL,
  getDocsPageURL,
  getDocsURL,
  getFooterModels,
  getSupportEmail,
} from '../../helpers/brandUrls';
import {
  isMarketingBarePath,
  localizedMarketingPath,
  splitMarketingPath,
  type MarketingLocale,
} from '../../helpers/marketingLocale';
import { UPSTREAM_NOTICE, UPSTREAM_URL } from '../../helpers/upstream';
import { usePublishedSource } from '../../hooks/common/usePublishedSource';
import type { CSSProperties } from 'react';

type FooterItem = {
  href: string;
  label: string;
  /** Rendered through the SPA router; everything else is a document link. */
  internal?: boolean;
  mono?: boolean;
};

const LOCALES: MarketingLocale[] = ['en', 'zh', 'zh-TW', 'ja'];

// One link per comparison page in the docs (apps/docs/pages/compare), named
// as in the docs sidebar so the anchor text matches the page title people
// search for. Paths keep their leading slash: the brand check exempts these
// routes only in that spelling.
const COMPARISONS: { page: string; name: string }[] = [
  { page: '/compare/litellm', name: 'LiteLLM' },
  { page: '/compare/new-api', name: 'New API' },
  { page: '/compare/one-api', name: 'One API' },
  { page: '/compare/openrouter', name: 'OpenRouter' },
  { page: '/compare/portkey', name: 'Portkey' },
  { page: '/compare/helicone', name: 'Helicone' },
  { page: '/compare/bifrost', name: 'Bifrost' },
  { page: '/compare/kong', name: 'Kong AI Gateway' },
];

// Read on use: the extension list is not ready while modules load.
const extensionFooterItems = () =>
  webExtensions().flatMap((extension) => extension.publicNav?.footer ?? []);

type PublicFooterProps = {
  /** Render for a path other than the router's (chrome partials for the
   *  API-rendered /models pages and extensions' pages). */
  pathnameOverride?: string;
};

// Locked public footer (public-web-system #15), widened for search in
// November 2026: up to six structured columns and a legal line naming the
// operator. "Popular models" appears only when the build names models
// (VITE_BRAND_FOOTER_MODELS); every other column is always there. Language and theme live in the header only. The operator appears here and in the
// Organization schema only — never in titles or headings.
const PublicFooter = ({ pathnameOverride }: PublicFooterProps = {}) => {
  const { t, i18n } = useTranslation('marketing');
  const location = useLocation();
  const split = splitMarketingPath(pathnameOverride ?? location.pathname);
  const { barePath } = split;
  // Marketing pages and the API-rendered sections carry their language in
  // the URL; app pages (payment returns, invites …) have no /zh/ variant, so
  // there the links follow the language the visitor is reading in.
  const hasLanguageURL =
    pathnameOverride !== undefined || isMarketingBarePath(barePath);
  const locale: MarketingLocale = hasLanguageURL
    ? split.locale
    : (LOCALES.find((value) => value === i18n.language) ?? 'en');
  const at = (bare: string) => localizedMarketingPath(bare, locale);
  const supportEmail = getSupportEmail();
  const communityURL = getCommunityURL();
  const sourceURL = usePublishedSource().url;
  const footerModels = getFooterModels();

  const columns: { key: string; title: string; items: FooterItem[] }[] = [
    {
      key: 'product',
      title: t('footer.product'),
      items: [
        { href: at('/models'), label: t('footer.models') },
        { href: at('/faq'), label: t('footer.faq'), internal: true },
        { href: '/register', label: t('footer.start'), internal: true },
      ],
    },
    ...(footerModels.length
      ? [
          {
            key: 'models',
            title: t('footer.popularModels'),
            items: [
              ...footerModels.map((model) => ({
                href: at(model.path),
                label: model.label,
              })),
              { href: at('/models'), label: t('footer.allModels') },
            ],
          },
        ]
      : []),
    {
      key: 'developers',
      title: t('footer.developers'),
      items: [
        { href: getDocsHomeURL(locale), label: t('footer.docs') },
        { href: getDocsURL('api-reference'), label: t('footer.apiReference') },
        { href: getDocsURL('changelog'), label: t('footer.changelog') },
        { href: sourceURL, label: t('landing.nav.github') },
        { href: '/llms.txt', label: 'llms.txt', mono: true },
        { href: '/models.json', label: '/models.json', mono: true },
      ],
    },
    {
      key: 'compare',
      title: t('footer.compare'),
      items: [
        ...COMPARISONS.map((comparison) => ({
          href: getDocsPageURL(comparison.page, locale),
          label: t('footer.compareWith', { name: comparison.name }),
        })),
        {
          href: getDocsPageURL('/compare/overview', locale),
          label: t('footer.allComparisons'),
        },
      ],
    },
    {
      key: 'company',
      title: t('footer.company'),
      items: [
        { href: at('/about'), label: t('footer.about'), internal: true },
        ...(communityURL
          ? [{ href: communityURL, label: t('footer.community') }]
          : []),
        { href: `mailto:${supportEmail}`, label: supportEmail },
      ],
    },
    {
      key: 'legal',
      title: t('footer.legal'),
      items: [
        { href: at('/terms'), label: t('footer.terms'), internal: true },
        { href: at('/privacy'), label: t('footer.privacy'), internal: true },
      ],
    },
  ];
  for (const item of extensionFooterItems()) {
    columns
      .find((column) => column.key === item.column)
      ?.items.splice(item.index, 0, {
        href: at(item.path),
        label: item.label[locale] ?? item.label.en,
      });
  }

  return (
    <footer className='lazu-public-footer' role='contentinfo'>
      <div className='lazu-public-footer__inner'>
        <div
          className='lazu-public-footer__grid'
          style={
            {
              '--lazu-footer-columns': columns.length,
            } as CSSProperties
          }
        >
          <div className='lazu-public-footer__brand'>
            <Link reloadDocument to={at('/')} aria-label={t('brand.name')}>
              <ThemedBrandLogo size='footer' alt={t('brand.name')} />
            </Link>
            <p>{t('footer.blurb')}</p>
          </div>
          {columns.map((column) => (
            <nav
              className='lazu-public-footer__col'
              key={column.key}
              aria-label={column.title}
            >
              <div className='lazu-public-footer__eyebrow'>{column.title}</div>
              {column.items.map((item) =>
                item.internal ? (
                  <Link reloadDocument key={item.href} to={item.href}>
                    {item.label}
                  </Link>
                ) : (
                  <a
                    key={item.href}
                    href={item.href}
                    className={item.mono ? 'is-mono' : undefined}
                  >
                    {item.label}
                  </a>
                ),
              )}
            </nav>
          ))}
        </div>
        <div className='lazu-public-footer__legal'>
          <span className='lazu-public-footer__line'>
            {t('footer.legalLine')}
          </span>
          <a
            className='lazu-public-footer__upstream'
            href={UPSTREAM_URL}
            rel='noopener noreferrer'
            target='_blank'
          >
            {UPSTREAM_NOTICE}
          </a>
        </div>
      </div>
    </footer>
  );
};

export default PublicFooter;
