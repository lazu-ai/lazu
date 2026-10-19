// apps/docs/public/sidebar-extras.js
//
// Injects the docs chrome extras Vocs has no config hooks for:
//   1. A language switcher + theme toggle at the FAR RIGHT of the desktop
//      top nav (per the v2 design), with a fallback instance at the bottom
//      of the sidebar for the mobile drawer (CSS hides it on desktop).
//   2. POST / GET method badges on the sidebar's endpoint nav items.
//   3. The top bar's brand: the Seal lockup goes to the main site home, the
//      DOCS tag beside it to the docs home, and "Models" to the main site's
//      model pages in the active locale.
//
// The docs are served under lazu.ai/docs; every path below is read without
// that base and written back with it (see BASE / withBase).
//
// Plain JS, no bundler — keeps docs' dependency surface tiny. The controls
// speak the same dialect as everywhere else:
//   - Theme: toggles <html class="dark"> + writes localStorage `vocs.theme`
//   - Language: redirects the page to /docs/:lang/<rest>, falling back to en
//     for locales without dedicated docs content.

(function () {
  "use strict";

  var CONSOLE_LABELS = {
    en: "Console",
    zh: "控制台",
    "zh-TW": "主控台",
    ja: "コンソール",
  };

  var LOCALES = [
    { value: "en", label: "English", short: "EN" },
    { value: "zh", label: "简体中文", short: "中" },
    { value: "zh-TW", label: "繁體中文", short: "繁" },
    { value: "ja", label: "日本語", short: "日" },
  ];

  // The docs live under lazu.ai/docs. BASE is that prefix, read from this
  // script's own URL (/docs/sidebar-extras.js). Vocs renders its links with
  // it; the locale logic below works on paths without it, and everything
  // written back to the page (hrefs, redirects) gets it again via withBase.
  var BASE = (function () {
    var src = document.currentScript && document.currentScript.src;
    if (!src) return "/docs";
    return new URL(src, location.href).pathname.replace(
      /\/sidebar-extras\.js$/,
      "",
    );
  })();

  // "/docs/zh/quickstart" -> "/zh/quickstart"; the docs home is "/".
  function stripBase(p) {
    if (!BASE) return p || "/";
    if (p === BASE) return "/";
    if (p.indexOf(BASE + "/") === 0) return p.slice(BASE.length);
    return p;
  }

  // "/zh/quickstart" -> "/docs/zh/quickstart"; the docs home is "/docs"
  // (canonical docs URLs are slashless, locale roots keep their slash).
  function withBase(p) {
    if (!BASE) return p;
    return p === "/" ? BASE : BASE + p;
  }

  // The site home and the model pages live on the main site, outside the
  // docs base path, with the locale as their first segment.
  var SITE_HOME = "/";
  var MODELS_PATH = "/models";
  function sitePathForLocale(path, locale) {
    return locale === "en" ? path : "/" + locale + path;
  }

  function bareDocsPath(p) {
    p = stripBase(p);
    var prefixes = ["/zh-TW", "/zh", "/ja"];
    for (var i = 0; i < prefixes.length; i++) {
      var pre = prefixes[i];
      if (p === pre || p.indexOf(pre + "/") === 0)
        return p.slice(pre.length) || "/";
    }
    return p;
  }

  var ICONS = {
    sun: '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/></svg>',
    moon: '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>',
    globe:
      '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M21.54 15H17a2 2 0 0 0-2 2v4.54"/><path d="M7 3.34V5a3 3 0 0 0 3 3a2 2 0 0 1 2 2c0 1.1.9 2 2 2a2 2 0 0 0 2-2c0-1.1.9-2 2-2h3.17"/><path d="M11 21.95V18a2 2 0 0 0-2-2a2 2 0 0 1-2-2v-1a2 2 0 0 0-2-2H2.05"/></svg>',
    check:
      '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>',
  };

  function activeLocale() {
    var p = stripBase(location.pathname);
    if (p === "/ja" || p.indexOf("/ja/") === 0) return "ja";
    if (p === "/zh-TW" || p.indexOf("/zh-TW/") === 0) return "zh-TW";
    if (p === "/zh" || p.indexOf("/zh/") === 0) return "zh";
    return "en";
  }

  // Pages localized in all docs locales. Anything outside this set falls
  // back to a fuller locale (zh for traditional Chinese, en for Japanese).
  var LOCALIZED_DEEP_PATHS = {
    "/api-reference": true,
    "/authentication": true,
    "/billing": true,
    "/changelog": true,
    "/endpoints/chat": true,
    "/endpoints/embeddings": true,
    "/endpoints/files": true,
    "/endpoints/models": true,
    "/endpoints/responses": true,
    "/endpoints/search": true,
    "/endpoints/usage-requests": true,
    "/errors": true,
    "/examples": true,
    "/limits": true,
    "/models/catalog": true,
    "/models/pricing": true,
    "/quickstart": true,
  };

  function pathForBareAndLocale(bare, target) {
    if (target === "en") return bare;
    if (target === "zh") return bare === "/" ? "/zh/" : "/zh" + bare;
    if (target === "zh-TW") {
      if (bare === "/") return "/zh-TW/";
      if (LOCALIZED_DEEP_PATHS[bare]) return "/zh-TW" + bare;
      return "/zh" + bare;
    }
    if (target === "ja") {
      if (bare === "/") return "/ja/";
      if (LOCALIZED_DEEP_PATHS[bare]) return "/ja" + bare;
      return bare;
    }
    return "/";
  }

  function pathForLocale(target) {
    return withBase(
      pathForBareAndLocale(bareDocsPath(location.pathname), target),
    );
  }

  function pathForActiveLocale(href) {
    if (!href || href.charAt(0) !== "/" || href.indexOf("//") === 0) {
      return href;
    }

    var bare = bareDocsPath(href);
    if (bare !== "/" && !LOCALIZED_DEEP_PATHS[bare]) return href;
    return withBase(pathForBareAndLocale(bare, activeLocale()));
  }

  var NAV_LABELS = {
    en: ["Guides", "API reference", "Changelog", "Models"],
    zh: ["指南", "API 参考", "更新日志", "模型"],
    "zh-TW": ["指南", "API 參考", "更新日誌", "模型"],
    ja: ["ガイド", "API リファレンス", "変更履歴", "モデル"],
  };
  // Index-parallel with NAV_LABELS. Models is not a docs page: vocs.config.ts
  // lists it as https://lazu.ai/models and localizeModelsLink() points it at
  // the main site's model pages for the active locale.
  var NAV_PATHS = ["/quickstart", "/endpoints/chat", "/changelog", "/models"];

  // Vocs portals the compact mobile menu outside the top-nav root.
  var TOP_NAV_LINKS =
    '.vocs_DesktopTopNav a[href^="/"], .vocs_MobileTopNav a[href^="/"], a.vocs_MobileTopNav_navigationItem[href^="/"]';
  var MODELS_LINKS =
    '.vocs_DesktopTopNav a[data-lazu-models], .vocs_DesktopTopNav a[href$="lazu.ai/models"], .vocs_MobileTopNav a[data-lazu-models], .vocs_MobileTopNav a[href$="lazu.ai/models"], a.vocs_MobileTopNav_navigationItem[href$="lazu.ai/models"]';

  function localizeTopNavLinks() {
    document.querySelectorAll(TOP_NAV_LINKS).forEach(function (link) {
      if (
        link.hasAttribute("data-lazu-models") ||
        link.hasAttribute("data-lazu-home")
      )
        return;
      var href = link.getAttribute("href");
      var localized = pathForActiveLocale(href);
      if (localized && localized !== href) link.setAttribute("href", localized);
      var index = NAV_PATHS.indexOf(bareDocsPath(href || ""));
      if (index !== -1) {
        var label = NAV_LABELS[activeLocale()][index];
        if (link.textContent !== label) link.textContent = label;
      }
    });
    localizeModelsLink();
  }

  // Models: a same-tab link to the main site's model pages (/models,
  // /zh/models, ...). Prerendered it is Vocs' external link to
  // https://lazu.ai/models, which still works before this runs.
  function localizeModelsLink() {
    var locale = activeLocale();
    var href = sitePathForLocale(MODELS_PATH, locale);
    var label = NAV_LABELS[locale][NAV_PATHS.indexOf(MODELS_PATH)];
    document.querySelectorAll(MODELS_LINKS).forEach(function (link) {
      link.setAttribute("data-lazu-models", "true");
      if (link.getAttribute("href") !== href) link.setAttribute("href", href);
      link.removeAttribute("target");
      link.removeAttribute("rel");
      if (link.textContent !== label) link.textContent = label;
    });
  }

  // The Seal lockup is the site's, not the docs': it goes to the main site
  // home with a full page load, and the mono DOCS tag beside it becomes its
  // own link to the docs home. Vocs renders the lockup as a router link to
  // the docs home; the capture-phase click handler below overrides where it
  // goes, since changing href alone does not change the router's target.
  var DOCS_TAG_LABEL = "Docs";
  function mountTopBarBrand() {
    var logo = document.querySelector(".vocs_DesktopTopNav_logo a");
    if (!logo) return;
    if (logo.getAttribute("href") !== SITE_HOME)
      logo.setAttribute("href", SITE_HOME);
    logo.setAttribute("data-lazu-home", "true");
    logo.setAttribute("aria-label", "Lazu home");
    var wrap = logo.parentElement;
    if (wrap && !wrap.querySelector(".lazu-docs-tag")) {
      var tag = document.createElement("a");
      tag.className = "lazu-docs-tag";
      tag.href = withBase("/");
      tag.textContent = DOCS_TAG_LABEL;
      tag.setAttribute("aria-label", "Docs home");
      wrap.appendChild(tag);
    }
  }

  function isDark() {
    return document.documentElement.classList.contains("dark");
  }

  function syncFavicons() {
    var href = BASE + (isDark() ? "/icon-paper.svg" : "/icon-graphite.svg");
    document.querySelectorAll('link[rel~="icon"]').forEach(function (favicon) {
      if (favicon.getAttribute("href") !== href) {
        favicon.setAttribute("href", href);
      }
      favicon.setAttribute("type", "image/svg+xml");
    });
  }

  function setDark(next) {
    var html = document.documentElement;
    if (next) {
      html.classList.add("dark");
      html.classList.remove("light");
    } else {
      html.classList.add("light");
      html.classList.remove("dark");
    }
    syncFavicons();
    try {
      localStorage.setItem("vocs.theme", next ? "dark" : "light");
    } catch (_) {
      /* private mode / storage disabled — fine */
    }
  }

  function render(extras) {
    var themeBtn = extras.querySelector(".dub-sidebar-extras__theme");
    if (themeBtn) {
      themeBtn.innerHTML = isDark() ? ICONS.sun : ICONS.moon;
      themeBtn.setAttribute(
        "aria-label",
        isDark() ? "Switch to light mode" : "Switch to dark mode",
      );
    }
    var langTrigger = extras.querySelector(".dub-sidebar-extras__lang");
    if (langTrigger) {
      var active = LOCALES.find(function (l) {
        return l.value === activeLocale();
      });
      langTrigger.querySelector(".dub-sidebar-extras__lang-label").textContent =
        active ? active.short : "EN";
    }
    var menu = extras.querySelector(".dub-sidebar-extras__menu");
    if (menu) {
      var items = menu.querySelectorAll(".dub-sidebar-extras__menu-item");
      items.forEach(function (el) {
        var v = el.getAttribute("data-value");
        var checked = v === activeLocale();
        el.setAttribute("aria-checked", String(checked));
        var checkEl = el.querySelector(".dub-sidebar-extras__menu-check");
        if (checkEl) checkEl.innerHTML = checked ? ICONS.check : "";
      });
    }
  }

  function build(variant) {
    var extras = document.createElement("div");
    extras.className =
      "dub-sidebar-extras" +
      (variant === "topbar" ? " dub-sidebar-extras--topbar" : "");
    extras.innerHTML = [
      // Design order: language dropdown first, then the theme toggle.
      '<div class="dub-sidebar-extras__lang-wrapper">',
      '  <button type="button" class="dub-sidebar-extras__lang" aria-haspopup="menu" aria-expanded="false">',
      "    " + ICONS.globe,
      '    <span class="dub-sidebar-extras__lang-label">EN</span>',
      "  </button>",
      '  <div role="menu" class="dub-sidebar-extras__menu" hidden>',
      LOCALES.map(function (loc) {
        return [
          '    <button type="button" role="menuitemradio"',
          '      class="dub-sidebar-extras__menu-item"',
          '      data-value="' + loc.value + '"',
          '      aria-checked="false">',
          "      <span>" + loc.label + "</span>",
          '      <span class="dub-sidebar-extras__menu-check"></span>',
          "    </button>",
        ].join("");
      }).join(""),
      "  </div>",
      "</div>",
      '<button type="button" class="dub-sidebar-extras__theme" aria-label="Toggle theme">',
      ICONS.moon,
      "</button>",
      // The bar's single ink CTA (C-final top bar): into the console.
      '<a class="dub-console-cta" href="https://lazu.ai/console" target="_blank" rel="noopener noreferrer">',
      CONSOLE_LABELS[activeLocale()] || CONSOLE_LABELS.en,
      '  <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 9.5 9.5 2.5M4.5 2.5h5v5"/></svg>',
      "</a>",
    ].join("");

    // Listeners live on `document`, not on the per-build buttons —
    // wireUpListeners() registers them once. React can tear `extras`
    // out and we re-build it, but delegation on document still routes
    // clicks correctly to whatever .dub-sidebar-extras-* is in the DOM.
    return extras;
  }

  function allExtras() {
    return document.querySelectorAll(".dub-sidebar-extras");
  }

  function closeMenus() {
    document
      .querySelectorAll(".dub-sidebar-extras__menu")
      .forEach(function (m) {
        m.hidden = true;
      });
    document
      .querySelectorAll(".dub-sidebar-extras__lang")
      .forEach(function (t) {
        t.setAttribute("aria-expanded", "false");
      });
  }

  var listenersWired = false;
  function wireUpListeners() {
    if (listenersWired) return;
    listenersWired = true;
    syncFavicons();

    document.addEventListener("click", function (e) {
      var target = e.target;
      if (!(target instanceof Element)) return;

      var themeBtn = target.closest(".dub-sidebar-extras__theme");
      if (themeBtn) {
        setDark(!isDark());
        allExtras().forEach(render);
        return;
      }

      var trigger = target.closest(".dub-sidebar-extras__lang");
      if (trigger) {
        e.stopPropagation();
        var menu = trigger.parentElement.querySelector(
          ".dub-sidebar-extras__menu",
        );
        var wasHidden = menu ? menu.hidden : true;
        closeMenus();
        if (menu && wasHidden) {
          menu.hidden = false;
          trigger.setAttribute("aria-expanded", "true");
        }
        return;
      }

      var item = target.closest(".dub-sidebar-extras__menu-item");
      if (item) {
        var v = item.getAttribute("data-value");
        closeMenus();
        if (!v || v === activeLocale()) return;
        location.href = pathForLocale(v);
        return;
      }

      if (!target.closest(".dub-sidebar-extras")) closeMenus();
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") closeMenus();
    });

    // Keep our icons in sync when Vocs flips the theme from elsewhere.
    new MutationObserver(function () {
      syncFavicons();
      allExtras().forEach(render);
    }).observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    new MutationObserver(syncFavicons).observe(document.head, {
      childList: true,
      subtree: true,
    });
  }

  function tryMount() {
    var mounted = false;
    // Desktop: far right of the top nav (the design's `controls` slot).
    var topnav = document.querySelector(".vocs_DesktopTopNav");
    if (topnav && !topnav.querySelector(".dub-sidebar-extras")) {
      var tExtras = build("topbar");
      topnav.appendChild(tExtras);
      render(tExtras);
    }
    if (topnav) mounted = true;
    mountTopBarBrand();
    localizeTopNavLinks();
    // Mobile: the desktop nav AND the sidebar are both display:none under
    // 1080px, so mounting only into those two left phones with no language
    // switcher, no theme toggle and no way into the console at all.
    var mobilenav = document.querySelector(".vocs_MobileTopNav");
    if (mobilenav && !mobilenav.querySelector(".dub-sidebar-extras")) {
      var mExtras = build("topbar");
      mExtras.classList.add("dub-sidebar-extras--mobile");
      mobilenav.appendChild(mExtras);
      render(mExtras);
    }
    if (mobilenav) mounted = true;
    // Mobile drawer fallback: bottom of the sidebar (hidden on desktop via CSS).
    var host = document.querySelector(".vocs_Sidebar_footerContent");
    if (host && !host.querySelector(".dub-sidebar-extras")) {
      var sExtras = build("sidebar");
      host.appendChild(sExtras);
      render(sExtras);
    }
    if (host) mounted = true;
    return mounted;
  }

  // Vocs' React Router handler retains the original `to` even after href is
  // localized. Intercept normal clicks before React and follow the actual URL.
  document.addEventListener(
    "click",
    function (e) {
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)
        return;
      if (!(e.target instanceof Element)) return;
      var home = e.target.closest("a[data-lazu-home]");
      if (home) {
        e.preventDefault();
        e.stopPropagation();
        location.assign(SITE_HOME);
        return;
      }
      var link = e.target.closest(TOP_NAV_LINKS);
      if (
        !link ||
        link.hasAttribute("download") ||
        link.hasAttribute("data-lazu-models") ||
        link.target === "_blank"
      )
        return;
      var href = link.getAttribute("href");
      var localized = pathForActiveLocale(href);
      if (!localized || activeLocale() === "en") return;
      e.preventDefault();
      e.stopPropagation();
      location.assign(localized);
    },
    true,
  );

  syncFavicons();

  afterHydration(attemptMount);

  // React owns every node under #app once hydration has walked it (it tags
  // each one with a __reactFiber$ key). Touching the tree before the last node
  // is claimed makes React meet a node it did not render, throw #418 and
  // re-render the page from scratch — which a fixed delay after load did on
  // slow connections. So wait for the last node, then a frame.
  function afterHydration(cb) {
    var started = Date.now();
    (function poll() {
      var leaf = document.getElementById("app");
      while (leaf && leaf.lastElementChild) leaf = leaf.lastElementChild;
      var claimed =
        leaf &&
        Object.keys(leaf).some(function (k) {
          return k.indexOf("__reactFiber$") === 0;
        });
      if (claimed || Date.now() - started > 15000) {
        window.requestAnimationFrame(function () {
          window.setTimeout(cb, 0);
        });
        return;
      }
      window.setTimeout(poll, 100);
    })();
  }

  function attemptMount() {
    wireUpListeners();
    tryMount();
    var pending = null;
    var observer = new MutationObserver(function () {
      if (pending) return;
      pending = window.setTimeout(function () {
        pending = null;
        tryMount();
      }, 180);
    });
    observer.observe(document.body, { childList: true, subtree: true });
  }
})();
