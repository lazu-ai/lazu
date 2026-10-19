// apps/docs/public/site-footer.js
//
// Mounts the locked public footer (public-web-system #15) under every docs
// page: four link columns and the legal line naming the operator, the same
// block lazu.ai renders from apps/web/src/components/marketing/PublicFooter.
// Vocs has no footer slot, so it goes in at runtime after the prev/next
// footer inside the content column, and is rebuilt when the route changes.
//
// The copy mirrors the `footer.*` keys in apps/web/src/marketing/
// marketing.<locale>.json — the docs image is built without apps/web, so
// change both together.

(function () {
  "use strict";

  var BASE = (function () {
    var src = document.currentScript && document.currentScript.src;
    if (!src) return "/docs";
    return new URL(src, location.href).pathname.replace(
      /\/site-footer\.js$/,
      "",
    );
  })();

  var COPY = {
    en: {
      blurb:
        "An OpenAI-compatible AI gateway. One key for every model, a quota per person, and a bill you can read.",
      product: "Product",
      developers: "Developers",
      company: "Company",
      legal: "Legal",
      models: "Model catalog",
      faq: "FAQ",
      start: "Start free",
      docs: "Docs",
      apiReference: "API reference",
      changelog: "Changelog",
      about: "About",
      blog: "Blog",
      terms: "Terms of Service",
      privacy: "Privacy Policy",
      legalLine:
        "© 2025–2026 DUBRIO LLC · Delaware, USA · Lazu is operated by DUBRIO LLC",
    },
    zh: {
      blurb:
        "OpenAI 兼容的 AI 网关。一个 Key 调用所有模型，按人发额度，账单看得懂。",
      product: "产品",
      developers: "开发者",
      company: "公司",
      legal: "法律",
      models: "模型目录",
      faq: "常见问题",
      start: "免费开始",
      docs: "文档",
      apiReference: "API 参考",
      changelog: "更新日志",
      about: "关于",
      blog: "博客",
      terms: "服务条款",
      privacy: "隐私政策",
      legalLine:
        "© 2025–2026 DUBRIO LLC · 美国特拉华州 · Lazu 由 DUBRIO LLC 运营",
    },
    "zh-TW": {
      blurb:
        "相容 OpenAI 的 AI 閘道。一把 Key 呼叫所有模型，按人發額度，帳單看得懂。",
      product: "產品",
      developers: "開發者",
      company: "公司",
      legal: "法律",
      models: "模型目錄",
      faq: "常見問題",
      start: "免費開始",
      docs: "文件",
      apiReference: "API 參考",
      changelog: "更新日誌",
      about: "關於",
      blog: "部落格",
      terms: "服務條款",
      privacy: "隱私政策",
      legalLine:
        "© 2025–2026 DUBRIO LLC · 美國德拉瓦州 · Lazu 由 DUBRIO LLC 營運",
    },
    ja: {
      blurb:
        "OpenAI 互換の AI ゲートウェイ。1 つの Key ですべてのモデル、メンバーごとの上限、読める請求書。",
      product: "プロダクト",
      developers: "開発者",
      company: "会社",
      legal: "法務",
      models: "モデル一覧",
      faq: "よくある質問",
      start: "無料で始める",
      docs: "ドキュメント",
      apiReference: "API リファレンス",
      changelog: "変更履歴",
      about: "Lazu について",
      blog: "ブログ",
      terms: "利用規約",
      privacy: "プライバシーポリシー",
      legalLine:
        "© 2025–2026 DUBRIO LLC · 米国デラウェア州 · Lazu は DUBRIO LLC が運営しています",
    },
  };

  // "/docs/zh/quickstart" -> "/zh/quickstart"; the docs home is "/".
  function stripBase(p) {
    if (!BASE) return p || "/";
    if (p === BASE) return "/";
    if (p.indexOf(BASE + "/") === 0) return p.slice(BASE.length);
    return p;
  }

  function withBase(p) {
    if (!BASE) return p;
    return p === "/" ? BASE : BASE + p;
  }

  function splitDocsPath() {
    var p = stripBase(location.pathname);
    var locales = ["zh-TW", "zh", "ja"];
    for (var i = 0; i < locales.length; i++) {
      var pre = "/" + locales[i];
      if (p === pre || p.indexOf(pre + "/") === 0) {
        return { locale: locales[i], bare: p.slice(pre.length) || "/" };
      }
    }
    return { locale: "en", bare: p };
  }

  // Locale homes keep their slash (/docs/zh/), the canonical spelling.
  function docsPath(bare, locale) {
    if (locale === "en") return withBase(bare);
    return withBase(bare === "/" ? "/" + locale + "/" : "/" + locale + bare);
  }

  // The main site keeps the locale as its first segment; its home is "/zh/".
  function sitePath(bare, locale) {
    if (locale === "en") return bare;
    return bare === "/" ? "/" + locale + "/" : "/" + locale + bare;
  }

  function esc(value) {
    return String(value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function build(locale) {
    var t = COPY[locale] || COPY.en;
    var columns = [
      [
        t.product,
        [
          [sitePath("/models", locale), t.models],
          [sitePath("/faq", locale), t.faq],
          ["/register", t.start],
        ],
      ],
      [
        t.developers,
        [
          [docsPath("/", locale), t.docs],
          [withBase("/api-reference"), t.apiReference],
          [withBase("/changelog"), t.changelog],
          ["/llms.txt", "llms.txt", true],
          ["/api/pricing", "/api/pricing", true],
        ],
      ],
      [
        t.company,
        [
          [sitePath("/about", locale), t.about],
          [sitePath("/blog", locale), t.blog],
          ["mailto:support@lazu.ai", "support@lazu.ai"],
        ],
      ],
      [
        t.legal,
        [
          [sitePath("/terms", locale), t.terms],
          [sitePath("/privacy", locale), t.privacy],
        ],
      ],
    ];
    var html =
      '<div class="lazu-public-footer__inner"><div class="lazu-public-footer__grid">' +
      '<div class="lazu-public-footer__brand"><a href="' +
      esc(sitePath("/", locale)) +
      '" aria-label="Lazu">' +
      '<img class="lazu-public-footer__logo lazu-public-footer__logo--light" src="' +
      esc(withBase("/logo.svg")) +
      '" alt="Lazu" width="85" height="30">' +
      '<img class="lazu-public-footer__logo lazu-public-footer__logo--dark" src="' +
      esc(withBase("/logo-dark.svg")) +
      '" alt="" width="85" height="30">' +
      "</a><p>" +
      esc(t.blurb) +
      "</p></div>";
    columns.forEach(function (column) {
      html +=
        '<nav class="lazu-public-footer__col" aria-label="' +
        esc(column[0]) +
        '"><div class="lazu-public-footer__eyebrow">' +
        esc(column[0]) +
        "</div>";
      column[1].forEach(function (item) {
        html +=
          '<a href="' +
          esc(item[0]) +
          '"' +
          (item[2] ? ' class="is-mono"' : "") +
          ">" +
          esc(item[1]) +
          "</a>";
      });
      html += "</nav>";
    });
    html +=
      '</div><div class="lazu-public-footer__legal">' +
      '<span class="lazu-public-footer__line">' +
      esc(t.legalLine) +
      "</span></div></div>";

    var footer = document.createElement("footer");
    footer.className = "lazu-public-footer lazu-public-footer--docs";
    footer.setAttribute("role", "contentinfo");
    footer.setAttribute("data-path", location.pathname);
    footer.innerHTML = html;
    return footer;
  }

  function tryMount() {
    var content = document.querySelector(".vocs_DocsLayout_content");
    if (!content) return;
    var existing = document.querySelector(".lazu-public-footer--docs");
    var last = content.lastElementChild;
    if (
      existing &&
      existing.parentElement === content &&
      existing === last &&
      existing.getAttribute("data-path") === location.pathname
    ) {
      return;
    }
    if (existing) existing.remove();
    var where = splitDocsPath();
    content.appendChild(build(where.locale));
  }

  // Then follow client-side route changes.
  function attemptMount() {
    tryMount();
    var pending = null;
    new MutationObserver(function () {
      if (pending) return;
      pending = window.setTimeout(function () {
        pending = null;
        tryMount();
      }, 180);
    }).observe(document.body, { childList: true, subtree: true });
  }

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
})();
