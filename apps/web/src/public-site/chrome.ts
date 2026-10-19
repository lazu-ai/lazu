// Behaviour for the static copy of the public chrome and the API-rendered
// pages around it. The React header/footer own these controls on the SPA;
// here the same markup (data-chrome hooks) is wired without React.

const THEME_KEY = 'lazu.theme';

const applyTheme = (theme: 'light' | 'dark') => {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  document.body.setAttribute('theme-mode', theme);
};

const currentTheme = (): 'light' | 'dark' =>
  document.documentElement.classList.contains('dark') ? 'dark' : 'light';

const closeOnOutside = (
  trigger: HTMLElement,
  panel: HTMLElement,
  close: () => void,
) => {
  document.addEventListener('mousedown', (event) => {
    if (panel.hidden) return;
    const target = event.target as Node;
    if (!panel.contains(target) && !trigger.contains(target)) close();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && !panel.hidden) close();
  });
};

const wireThemeButtons = () => {
  document
    .querySelectorAll<HTMLButtonElement>('[data-chrome="theme"]')
    .forEach((button) => {
      button.addEventListener('click', () => {
        const next = currentTheme() === 'dark' ? 'light' : 'dark';
        try {
          localStorage.setItem(THEME_KEY, next);
        } catch {
          // Storage can be blocked; the theme still applies to this page.
        }
        applyTheme(next);
      });
    });
};

// The page's own hreflang alternates, by locale: the exact translation of
// this page where one exists (a page may have only two).
const alternates = () => {
  const map = new Map<string, string>();
  document
    .querySelectorAll<HTMLLinkElement>('link[rel="alternate"][hreflang]')
    .forEach((link) => {
      const lang = link.hreflang;
      if (!lang || lang === 'x-default') return;
      try {
        // The site's own pages: keep the path, whatever host it was
        // written with (always the canonical https://lazu.ai).
        const url = new URL(link.href, window.location.href);
        map.set(lang, url.pathname + url.search);
      } catch {
        // A malformed alternate is simply not offered.
      }
    });
  return map;
};

const wireLanguageMenu = () => {
  const trigger = document.querySelector<HTMLButtonElement>(
    '[data-chrome="lang"]',
  );
  const menu = trigger?.parentElement?.querySelector<HTMLElement>(
    '.lazu-landing__lang-menu',
  );
  if (!trigger || !menu) return;
  const setOpen = (open: boolean) => {
    menu.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
  };
  trigger.addEventListener('click', () => setOpen(menu.hidden));
  closeOnOutside(trigger, menu, () => setOpen(false));
  // The static header links each language to its section root, which always
  // exists; point each at this page's translation when the page has one.
  const translations = alternates();
  menu.querySelectorAll<HTMLAnchorElement>('a[data-lang]').forEach((item) => {
    const locale = item.dataset.lang || 'en';
    const translation = translations.get(locale);
    if (translation) item.href = translation;
    item.addEventListener('click', () => {
      // Unprefixed pages follow the stored preference, so store the choice
      // before the link is followed or /faq would open in the old language.
      try {
        localStorage.setItem('i18nextLng', locale);
      } catch {
        // The URL prefix carries the language regardless.
      }
    });
  });
};

// Signed-in visitors get the way back to the console instead of sign-in.
const wireAccountActions = () => {
  let signedIn = false;
  try {
    const user = JSON.parse(localStorage.getItem('user') || 'null');
    signedIn = Boolean(user && typeof user === 'object');
  } catch {
    signedIn = false;
  }
  if (!signedIn) return;
  document
    .querySelectorAll<HTMLElement>('[data-chrome-account]')
    .forEach((element) => {
      element.hidden = element.dataset.chromeAccount !== 'member';
    });
};

const wireMenu = () => {
  const trigger = document.querySelector<HTMLButtonElement>(
    '[data-chrome="menu"]',
  );
  const panel = document.getElementById('lazu-public-menu');
  if (!trigger || !panel) return;
  const setOpen = (open: boolean) => {
    panel.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
  };
  trigger.addEventListener('click', () => setOpen(panel.hidden));
  closeOnOutside(trigger, panel, () => setOpen(false));
};

const flashLabel = (button: HTMLElement) => {
  const label = button.dataset.copiedLabel;
  if (!label) return;
  const target =
    button.querySelector<HTMLElement>('b') || (button as HTMLElement);
  const original = target.textContent;
  target.textContent = label;
  window.setTimeout(() => {
    target.textContent = original;
  }, 1600);
};

// data-copy="text", data-copy-from="element id" (its text), or
// data-copy-url="/path.md" (the fetched body).
const wireCopyButtons = () => {
  document
    .querySelectorAll<HTMLElement>(
      '[data-copy], [data-copy-from], [data-copy-url]',
    )
    .forEach((button) => {
      button.addEventListener('click', async () => {
        let source: string | null | undefined = button.dataset.copy;
        if (button.dataset.copyFrom) {
          source = document.getElementById(
            button.dataset.copyFrom,
          )?.textContent;
        } else if (button.dataset.copyUrl) {
          try {
            const response = await fetch(button.dataset.copyUrl);
            source = response.ok ? await response.text() : null;
          } catch {
            source = null;
          }
        }
        if (!source) return;
        try {
          await navigator.clipboard.writeText(source);
          flashLabel(button);
        } catch {
          // Clipboard access denied: nothing to undo.
        }
      });
    });
};

// Code samples: one tab list per [data-tabs] group, panels by data-panel.
const wireTabs = () => {
  document.querySelectorAll<HTMLElement>('[data-tabs]').forEach((group) => {
    const tabs = group.querySelectorAll<HTMLButtonElement>('[role="tab"]');
    const panels = group.querySelectorAll<HTMLElement>('[role="tabpanel"]');
    tabs.forEach((tab) => {
      tab.addEventListener('click', () => {
        tabs.forEach((other) =>
          other.setAttribute('aria-selected', String(other === tab)),
        );
        panels.forEach((panel) => {
          panel.hidden = panel.id !== tab.getAttribute('aria-controls');
        });
      });
    });
  });
};

// Catalog: filter the prerendered cards by vendor chip and search text.
const wireCatalogFilter = () => {
  const root = document.querySelector<HTMLElement>('[data-catalog]');
  if (!root) return;
  const input = root.querySelector<HTMLInputElement>('[data-catalog-search]');
  const chips = root.querySelectorAll<HTMLAnchorElement>('[data-vendor-chip]');
  const groups = root.querySelectorAll<HTMLElement>('[data-vendor-group]');
  const empty = root.querySelector<HTMLElement>('[data-catalog-empty]');
  let vendor = '';
  const apply = () => {
    const query = (input?.value || '').trim().toLowerCase();
    let shown = 0;
    groups.forEach((group) => {
      const groupVendor = group.dataset.vendorGroup || '';
      let groupShown = 0;
      group
        .querySelectorAll<HTMLElement>('[data-model-card]')
        .forEach((card) => {
          const match =
            (!vendor || vendor === groupVendor) &&
            (!query || (card.dataset.search || '').includes(query));
          card.hidden = !match;
          if (match) groupShown += 1;
        });
      group.hidden = groupShown === 0;
      shown += groupShown;
    });
    if (empty) empty.hidden = shown > 0;
  };
  input?.addEventListener('input', apply);
  chips.forEach((chip) => {
    chip.addEventListener('click', (event) => {
      event.preventDefault();
      vendor = chip.dataset.vendorChip || '';
      chips.forEach((other) =>
        other.setAttribute('aria-current', other === chip ? 'true' : 'false'),
      );
      apply();
    });
  });
};

export const mountPublicChrome = () => {
  const start = () => {
    wireThemeButtons();
    wireLanguageMenu();
    wireAccountActions();
    wireMenu();
    wireCopyButtons();
    wireTabs();
    wireCatalogFilter();
  };
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start, { once: true });
  } else {
    start();
  }
};
