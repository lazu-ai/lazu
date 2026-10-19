import React from 'react';
import { flushSync } from 'react-dom';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { AppFrame, AppProviders } from './AppRoot';
import { prefetchAuthStatus } from './components/auth/authStatus';
import '@fontsource-variable/geist/wght.css';
import '@fontsource/ibm-plex-mono/400.css';
import '@fontsource/ibm-plex-mono/500.css';
import '@fontsource/ibm-plex-mono/600.css';
import { webExtensions } from './extensions';
import { i18nReady } from './i18n/i18n';
import { preloadNotFound } from './lib/not-found-preload';
import { startPrerender } from './lib/prerender-startup';
import './index.css';
import './styles/brand.generated.css';
import './styles/tokens.css';
import './styles/ui.css';
import './styles/shell.css';
import './styles/atmosphere.css';
import './styles/yunmu.css';
import './styles/console-review.css';
// initialization

/* A page that opens on the door needs to know which ways in are open, and the
 * door itself cannot ask until its chunk has arrived. Asking here puts the two
 * waits side by side instead of one after the other. */
if (window.location.pathname === '/login') {
  prefetchAuthStatus();
}

for (const extension of webExtensions()) extension.onAppStart?.();

const rootElement = document.getElementById('root');

if (!rootElement) {
  throw new Error('Root element not found');
}

const prerenderRoute =
  rootElement.dataset.hydrate === 'true'
    ? rootElement.dataset.prerenderRoute || null
    : null;

const app = (
  <React.StrictMode>
    <AppProviders prerenderRoute={prerenderRoute}>
      <BrowserRouter
        future={{
          v7_startTransition: true,
          v7_relativeSplatPath: true,
        }}
      >
        <AppFrame />
      </BrowserRouter>
    </AppProviders>
  </React.StrictMode>
);

if (prerenderRoute) {
  // Keep the prerendered tree intact while its initial translations load.
  // Suspending the shell during hydration can misalign its sibling nodes.
  void startPrerender(i18nReady, rootElement, () => {
    ReactDOM.hydrateRoot(rootElement, app);
  });
} else if (rootElement.dataset.prerenderRoute === '/__not-found') {
  // The prerendered 404 stays on screen (the guard in the page keeps it)
  // until the page that replaces it is complete: its chunk and its
  // translations (see lib/not-found-preload). React clears the root when it
  // commits, so the swap is one frame.
  void Promise.allSettled([i18nReady, preloadNotFound()]).then(() => {
    const root = ReactDOM.createRoot(rootElement);
    flushSync(() => root.render(app));
    rootElement.style.removeProperty('visibility');
  });
} else if (rootElement.dataset.prerenderRoute) {
  // Marketing routes are eagerly imported. Hold the existing geometry until
  // the correct language is ready instead of collapsing the entire document.
  void startPrerender(i18nReady, rootElement, () => {
    const root = ReactDOM.createRoot(rootElement);
    flushSync(() => root.render(app));
    rootElement.style.removeProperty('visibility');
  });
} else {
  ReactDOM.createRoot(rootElement).render(app);
}
