/* The public marketing and legal pages are their own chunks, so the console
 * entry does not carry them. A prerendered page loads its chunk before it
 * hydrates (index.tsx, entry-prerender.tsx): rendering a lazy component for
 * the first time suspends, and a suspended hydration would throw away the
 * prerendered markup. */
import { lazy, useState } from 'react';
import type { ComponentType } from 'react';

type PageModule = { default: ComponentType };

function preloadablePage(load: () => Promise<PageModule>) {
  let loaded: ComponentType | null = null;
  const Lazy = lazy(load);
  const preload = () =>
    load().then((module) => {
      loaded = module.default;
    });
  function Page() {
    // Fixed per mount: switching from the lazy wrapper to the loaded
    // component mid-life would remount the page and drop its state.
    const [Component] = useState<ComponentType>(() => loaded ?? Lazy);
    return <Component />;
  }
  return Object.assign(Page, { preload });
}

export const Home = preloadablePage(() => import('../pages/Home'));
export const FAQ = preloadablePage(() => import('../pages/FAQ'));
export const About = preloadablePage(() => import('../pages/About'));
export const PrivacyPolicy = preloadablePage(
  () => import('../pages/PrivacyPolicy'),
);
export const UserAgreement = preloadablePage(
  () => import('../pages/UserAgreement'),
);

const pages: Record<string, { preload: () => Promise<void> }> = {
  '/': Home,
  '/faq': FAQ,
  '/about': About,
  '/privacy': PrivacyPolicy,
  '/terms': UserAgreement,
};

/** The marketing page a public path renders, without its locale prefix. */
export function marketingPageFor(path: string) {
  const bare = path.replace(/^\/(zh-TW|zh|ja)(?=\/|$)/, '') || '/';
  return pages[bare.length > 1 ? bare.replace(/\/$/, '') : bare];
}

/** Load the chunk of the marketing page at `path`, if it is one. */
export function preloadMarketingPage(path: string): Promise<void> {
  return marketingPageFor(path)?.preload() ?? Promise.resolve();
}
