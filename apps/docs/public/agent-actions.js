// apps/docs/public/agent-actions.js
//
// Page actions for people who hand docs to an AI (Docs B, LOCKED 2026-09-23):
//
//   [ Copy page | ▾ ]   one click copies the page as Markdown; the menu adds
//     Copy ─ Copy page as Markdown · Copy for agent
//     Open in ─ ChatGPT · Claude · Gemini
//
// "Copy for agent" is links + a prompt for local coding agents (Claude Code,
// Codex, Cursor) and ends at "My task:" so the reader types straight after it.
// "Open in" prefills a shorter Q&A prompt; Gemini has no supported prefill
// parameter, so it copies the prompt first and says so.
//
// Also here: heading anchors copy their link, the ink toast that confirms
// every copy, and the overflow cue on code blocks. Every route ships a raw
// Markdown sibling (`/endpoints/chat` -> `/endpoints/chat.md`).
//
// Mount discipline: wait out hydration, delegate clicks on document, re-mount
// through a debounced MutationObserver on SPA navigations.

(function () {
  "use strict";

  // The docs live under lazu.ai/docs. BASE is that path prefix, read from
  // this script's own URL (/docs/agent-actions.js) so a preview served
  // elsewhere keeps working; SITE is the public docs root the prompts cite.
  var BASE = (function () {
    var src = document.currentScript && document.currentScript.src;
    if (!src) return "/docs";
    return new URL(src, location.href).pathname.replace(
      /\/agent-actions\.js$/,
      "",
    );
  })();
  var ORIGIN = "https://lazu.ai";
  var SITE = ORIGIN + BASE;
  var API = "https://api.lazu.ai";

  // Metadata is generated from each page's initial HTML by postbuild-geo.
  // Read that document on route changes so canonical, hreflang and JSON-LD
  // remain in sync with the visible content. Hash links keep native behavior.
  document.addEventListener(
    "click",
    function (event) {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      if (!(event.target instanceof Element)) return;
      var link = event.target.closest("a[href]");
      if (
        !link ||
        link.hasAttribute("download") ||
        (link.target && link.target !== "_self")
      )
        return;
      var destination;
      try {
        destination = new URL(link.href, location.href);
      } catch {
        return;
      }
      if (destination.origin !== location.origin) return;
      if (
        destination.pathname !== BASE &&
        destination.pathname.indexOf(BASE + "/") !== 0
      )
        return;
      if (
        destination.pathname === location.pathname &&
        destination.search === location.search
      )
        return;
      event.preventDefault();
      event.stopPropagation();
      location.assign(destination.href);
    },
    true,
  );

  // "/docs/zh/quickstart" -> "/zh/quickstart"; the docs home is "/".
  function docsPath() {
    var p = location.pathname;
    if (!BASE) return p || "/";
    if (p === BASE) return "/";
    if (p.indexOf(BASE + "/") === 0) return p.slice(BASE.length);
    return p;
  }

  var COPY = {
    en: {
      copyPage: "Copy page",
      more: "More ways to use this page",
      groupCopy: "Copy",
      groupOpen: "Open in",
      markdown: "Copy page as Markdown",
      markdownHint: "Paste into any chat as context",
      agent: "Copy for agent",
      agentHint: "Links + prompt for Claude Code, Codex, Cursor",
      geminiHint: "copies prompt first",
      copiedPage: "Page copied as Markdown",
      copiedAgent:
        "Prompt copied — paste into Claude Code, Codex or Cursor and add your task",
      copiedGemini: "Prompt copied — paste it into Gemini",
      copiedLink: "Link copied",
      failed: "Copy failed",
      anchor: "Copy link to this section",
      read: function (n) {
        return n + " min";
      },
      agentPrompt: function (md) {
        return [
          "Use the Lazu API docs to complete a task in this project.",
          "",
          "Docs for this task: " + md,
          "All docs (index): " + SITE + "/llms.txt",
          "",
          "Lazu is an OpenAI-compatible AI gateway: one API key reaches every model in its catalog.",
          "- Base URL: " +
            API +
            "/v1. With an OpenAI SDK, only base_url and api_key change.",
          "- Auth: Authorization: Bearer $LAZU_API_KEY. Read the key from the environment; never hard-code, log or commit it.",
          "- Models: choose an ID from GET " +
            API +
            "/api/models/catalog whose supported_endpoints contains the requested endpoint path. Use that entry's id; do not guess model names.",
          "",
          "Rules:",
          "- Where these docs and your memory of the OpenAI API disagree, follow the docs.",
          "- If the docs do not cover something, say so. Do not invent parameters, endpoints or prices.",
          "- After a test call, show me the X-Lazu-Request-Id response header so I can check the receipt.",
          "",
          "My task: ",
        ].join("\n");
      },
      chatPrompt: function (md) {
        return (
          "Read " +
          md +
          ", a page of the Lazu API docs (index: " +
          SITE +
          "/llms.txt). Lazu is an OpenAI-compatible AI gateway: base_url " +
          API +
          "/v1, auth Bearer $LAZU_API_KEY. Answer my questions from these docs only and say when they don't cover something. Write examples in Python (OpenAI SDK) or curl against the Lazu base_url. Answer first, then detail. My question: "
        );
      },
    },
    zh: {
      copyPage: "复制页面",
      more: "更多使用方式",
      groupCopy: "复制",
      groupOpen: "打开",
      markdown: "复制为 Markdown",
      markdownHint: "粘贴到任何对话里作为资料",
      agent: "复制给 Agent",
      agentHint: "链接 + 提示词，给 Claude Code、Codex、Cursor",
      geminiHint: "先复制提示词",
      copiedPage: "已复制整页 Markdown",
      copiedAgent:
        "提示词已复制 — 粘贴到 Claude Code、Codex 或 Cursor，接着写你的任务",
      copiedGemini: "提示词已复制 — 粘贴到 Gemini 即可",
      copiedLink: "链接已复制",
      failed: "复制失败",
      anchor: "复制本节链接",
      read: function (n) {
        return n + " 分钟";
      },
      agentPrompt: function (md) {
        return [
          "请参考 Lazu API 文档，在这个项目里完成一项任务。",
          "",
          "本任务相关文档：" + md,
          "全部文档索引：" + SITE + "/llms.txt",
          "",
          "Lazu 是 OpenAI 兼容的 AI 网关：一个 API Key 可以调用目录里的所有模型。",
          "- Base URL：" +
            API +
            "/v1。使用 OpenAI SDK 时只需改 base_url 和 api_key。",
          "- 鉴权：Authorization: Bearer $LAZU_API_KEY。从环境变量读取 Key，不要写死、打印或提交到代码库。",
          "- 模型：从 GET " +
            API +
            "/api/models/catalog 中选择 supported_endpoints 包含目标接口路径的条目，使用它的 id，不要猜模型名。",
          "",
          "规则：",
          "- 文档与你记忆中的 OpenAI API 不一致时，以文档为准。",
          "- 文档没有写到的内容请直接说明，不要编造参数、接口或价格。",
          "- 测试调用后，把响应头 X-Lazu-Request-Id 告诉我，方便我核对回执。",
          "",
          "我的任务：",
        ].join("\n");
      },
      chatPrompt: function (md) {
        return (
          "请阅读 " +
          md +
          "，这是 Lazu API 文档中的一页（索引：" +
          SITE +
          "/llms.txt）。Lazu 是 OpenAI 兼容的 AI 网关：base_url 为 " +
          API +
          "/v1，鉴权用 Bearer $LAZU_API_KEY。只根据这些文档回答，文档没写的请直接说明。示例代码用 Python（OpenAI SDK）或 curl，base_url 用 Lazu 的。先给结论，再给细节。我的问题："
        );
      },
    },
    "zh-TW": {
      copyPage: "複製頁面",
      more: "更多使用方式",
      groupCopy: "複製",
      groupOpen: "開啟",
      markdown: "複製為 Markdown",
      markdownHint: "貼到任何對話中作為資料",
      agent: "複製給 Agent",
      agentHint: "連結 + 提示詞，給 Claude Code、Codex、Cursor",
      geminiHint: "先複製提示詞",
      copiedPage: "已複製整頁 Markdown",
      copiedAgent:
        "提示詞已複製 — 貼到 Claude Code、Codex 或 Cursor，接著寫你的任務",
      copiedGemini: "提示詞已複製 — 貼到 Gemini 即可",
      copiedLink: "連結已複製",
      failed: "複製失敗",
      anchor: "複製本節連結",
      read: function (n) {
        return n + " 分鐘";
      },
      agentPrompt: function (md) {
        return [
          "請參考 Lazu API 文件，在這個專案裡完成一項任務。",
          "",
          "本任務相關文件：" + md,
          "全部文件索引：" + SITE + "/llms.txt",
          "",
          "Lazu 是 OpenAI 相容的 AI 閘道：一把 API Key 可以呼叫目錄裡的所有模型。",
          "- Base URL：" +
            API +
            "/v1。使用 OpenAI SDK 時只需改 base_url 和 api_key。",
          "- 驗證：Authorization: Bearer $LAZU_API_KEY。從環境變數讀取 Key，不要寫死、印出或提交到程式庫。",
          "- 模型：從 GET " +
            API +
            "/api/models/catalog 中選擇 supported_endpoints 包含目標介面路徑的項目，使用其 id，不要猜模型名稱。",
          "",
          "規則：",
          "- 文件與你記憶中的 OpenAI API 不一致時，以文件為準。",
          "- 文件沒有寫到的內容請直接說明，不要編造參數、介面或價格。",
          "- 測試呼叫後，把回應標頭 X-Lazu-Request-Id 告訴我，方便我核對回執。",
          "",
          "我的任務：",
        ].join("\n");
      },
      chatPrompt: function (md) {
        return (
          "請閱讀 " +
          md +
          "，這是 Lazu API 文件中的一頁（索引：" +
          SITE +
          "/llms.txt）。Lazu 是 OpenAI 相容的 AI 閘道：base_url 為 " +
          API +
          "/v1，驗證用 Bearer $LAZU_API_KEY。只根據這些文件回答，文件沒寫的請直接說明。範例程式碼用 Python（OpenAI SDK）或 curl，base_url 用 Lazu 的。先給結論，再給細節。我的問題："
        );
      },
    },
    ja: {
      copyPage: "ページをコピー",
      more: "このページの使い方",
      groupCopy: "コピー",
      groupOpen: "開く",
      markdown: "Markdown としてコピー",
      markdownHint: "チャットに貼り付けて資料として使う",
      agent: "エージェント用にコピー",
      agentHint: "Claude Code・Codex・Cursor 向けのリンクとプロンプト",
      geminiHint: "先にプロンプトをコピー",
      copiedPage: "ページを Markdown でコピーしました",
      copiedAgent:
        "プロンプトをコピーしました — Claude Code・Codex・Cursor に貼り付けてタスクを書いてください",
      copiedGemini: "プロンプトをコピーしました — Gemini に貼り付けてください",
      copiedLink: "リンクをコピーしました",
      failed: "コピー失敗",
      anchor: "このセクションのリンクをコピー",
      read: function (n) {
        return n + " 分";
      },
      agentPrompt: function (md) {
        return [
          "Lazu API ドキュメントを参照して、このプロジェクトでタスクを完了してください。",
          "",
          "このタスクのドキュメント: " + md,
          "全ドキュメントの索引: " + SITE + "/llms.txt",
          "",
          "Lazu は OpenAI 互換の AI ゲートウェイです。1 つの API キーでカタログ内のすべてのモデルを呼び出せます。",
          "- Base URL: " +
            API +
            "/v1。OpenAI SDK では base_url と api_key だけを変更します。",
          "- 認証: Authorization: Bearer $LAZU_API_KEY。キーは環境変数から読み取り、ハードコード・ログ出力・コミットはしないでください。",
          "- モデル: GET " +
            API +
            "/api/models/catalog から、supported_endpoints に対象エンドポイントのパスがある項目の id を使ってください。モデル名を推測しないでください。",
          "",
          "ルール:",
          "- このドキュメントとあなたの OpenAI API の知識が食い違う場合は、ドキュメントに従ってください。",
          "- ドキュメントに書かれていないことはその旨を伝え、パラメーター・エンドポイント・料金を作らないでください。",
          "- テスト呼び出しの後、レスポンスヘッダー X-Lazu-Request-Id を教えてください。明細を確認します。",
          "",
          "タスク: ",
        ].join("\n");
      },
      chatPrompt: function (md) {
        return (
          md +
          " を読んでください。Lazu API ドキュメントの 1 ページです（索引: " +
          SITE +
          "/llms.txt）。Lazu は OpenAI 互換の AI ゲートウェイで、base_url は " +
          API +
          "/v1、認証は Bearer $LAZU_API_KEY です。回答はこれらのドキュメントのみに基づき、書かれていないことはその旨を伝えてください。コード例は Python（OpenAI SDK）か curl で、Lazu の base_url を使ってください。結論から先に。質問: "
        );
      },
    },
  };

  var LANG_ATTR = { en: "en", zh: "zh", "zh-TW": "zh-TW", ja: "ja" };

  function activeLocale() {
    var p = docsPath();
    if (p === "/ja" || p.indexOf("/ja/") === 0) return "ja";
    if (p === "/zh-TW" || p.indexOf("/zh-TW/") === 0) return "zh-TW";
    if (p === "/zh" || p.indexOf("/zh/") === 0) return "zh";
    return "en";
  }

  // Vocs hard-codes <html lang="en"> at render time; the prerendered HTML is
  // corrected by scripts/postbuild-geo.ts, and this keeps the attribute right
  // across client-side navigations.
  function syncLang() {
    var lang = LANG_ATTR[activeLocale()];
    if (document.documentElement.lang !== lang)
      document.documentElement.lang = lang;
  }

  // The route's Markdown sibling, with the base path: /docs/zh/quickstart.md.
  function mdPath() {
    var p = docsPath();
    if (p === "" || p === "/") return BASE + "/index.md";
    if (/^\/(zh|zh-TW|ja)\/?$/.test(p))
      return BASE + p.replace(/\/$/, "") + "/index.md";
    return BASE + p.replace(/\/$/, "") + ".md";
  }

  // Prompts always point at the public site, even from a preview host, so a
  // pasted prompt never sends an agent to localhost.
  function mdUrl() {
    var local = /^(localhost|127\.|0\.0\.0\.0)/.test(location.hostname);
    return (local ? ORIGIN : location.origin) + mdPath();
  }

  var ICON = {
    copy: '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="2"/><path d="M10.5 5.5V4a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 4v5A1.5 1.5 0 0 0 4 10.5h1.5"/></svg>',
    chev: '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 6l4 4 4-4"/></svg>',
    md: '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="1.5" y="3.5" width="13" height="9" rx="1.5"/><path d="M4 10V6l2 2 2-2v4M11 6v4M9.5 8.5L11 10l1.5-1.5"/></svg>',
    bot: '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="5" width="10" height="8" rx="2.5"/><path d="M8 5V2.5M6 9h.01M10 9h.01"/></svg>',
    ext: '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 3h4v4"/><path d="M13 3L7.5 8.5"/><path d="M11 9.5V12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h2.5"/></svg>',
    check:
      '<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7"/></svg>',
  };

  // ---------- toast (墨条) ----------
  var toastTimer = null;
  function toast(message, detail) {
    var el = document.querySelector(".lazu-toast");
    if (!el) {
      el = document.createElement("div");
      el.className = "lazu-toast";
      el.setAttribute("role", "status");
      el.setAttribute("aria-live", "polite");
      document.body.appendChild(el);
    }
    el.innerHTML =
      ICON.check +
      "<span></span>" +
      (detail ? '<span class="lazu-toast-detail"></span>' : "");
    el.children[1].textContent = message;
    if (detail) el.children[2].textContent = detail;
    el.setAttribute("data-open", "true");
    if (toastTimer) window.clearTimeout(toastTimer);
    toastTimer = window.setTimeout(function () {
      el.setAttribute("data-open", "false");
    }, 2600);
  }

  function writeClipboard(text) {
    var legacy = function () {
      var scratch = document.createElement("textarea");
      scratch.value = text;
      scratch.setAttribute("readonly", "");
      scratch.style.position = "fixed";
      scratch.style.opacity = "0";
      document.body.appendChild(scratch);
      scratch.select();
      var ok = document.execCommand("copy");
      scratch.remove();
      if (!ok) throw new Error("copy rejected");
    };
    // navigator.clipboard is missing on plain-HTTP self-hosted docs; touching
    // .writeText there throws synchronously, so guard the API itself.
    if (!navigator.clipboard || !navigator.clipboard.writeText) {
      return Promise.resolve().then(legacy);
    }
    return navigator.clipboard.writeText(text).catch(legacy);
  }

  function copyMarkdown() {
    var t = COPY[activeLocale()];
    return fetch(mdPath())
      .then(function (response) {
        var type = response.headers.get("content-type") || "";
        // nginx soft-404s unknown paths to the SPA shell; treat HTML as a miss.
        if (!response.ok || type.indexOf("text/html") !== -1)
          throw new Error("no markdown for this route");
        return response.text();
      })
      .then(writeClipboard)
      .then(function () {
        toast(t.copiedPage);
      })
      .catch(function () {
        toast(t.failed);
      });
  }

  // ---------- page actions ----------
  function buildActions() {
    var t = COPY[activeLocale()];
    var chat = encodeURIComponent(t.chatPrompt(mdUrl()));
    var wrap = document.createElement("div");
    wrap.className = "lazu-page-actions";
    wrap.setAttribute("data-path", location.pathname);
    wrap.innerHTML =
      '<div class="lazu-split">' +
      '<button type="button" class="lazu-split-main" data-action="markdown">' +
      ICON.copy +
      "<span></span></button>" +
      '<button type="button" class="lazu-split-more" aria-haspopup="menu" aria-expanded="false">' +
      ICON.chev +
      "</button></div>" +
      '<div class="lazu-menu" role="menu" hidden>' +
      '<div class="lazu-menu-group" aria-hidden="true"></div>' +
      '<button type="button" role="menuitem" class="lazu-menu-item" data-action="markdown">' +
      ICON.md +
      "<span><b></b><small></small></span></button>" +
      '<button type="button" role="menuitem" class="lazu-menu-item" data-action="agent">' +
      ICON.bot +
      "<span><b></b><small></small></span></button>" +
      '<div class="lazu-menu-rule"></div>' +
      '<div class="lazu-menu-group" aria-hidden="true"></div>' +
      '<a role="menuitem" class="lazu-menu-item" target="_blank" rel="noopener noreferrer" href="https://chatgpt.com/?hints=search&amp;q=' +
      chat +
      '">' +
      ICON.ext +
      "<span><b>ChatGPT</b></span></a>" +
      '<a role="menuitem" class="lazu-menu-item" target="_blank" rel="noopener noreferrer" href="https://claude.ai/new?q=' +
      chat +
      '">' +
      ICON.ext +
      "<span><b>Claude</b></span></a>" +
      '<a role="menuitem" class="lazu-menu-item" target="_blank" rel="noopener noreferrer" data-action="gemini" href="https://gemini.google.com/app">' +
      ICON.ext +
      "<span><b>Gemini</b></span><em></em></a>" +
      "</div>";
    wrap.querySelector(".lazu-split-main span").textContent = t.copyPage;
    wrap.querySelector(".lazu-split-more").setAttribute("aria-label", t.more);
    var groups = wrap.querySelectorAll(".lazu-menu-group");
    groups[0].textContent = t.groupCopy;
    groups[1].textContent = t.groupOpen;
    var items = wrap.querySelectorAll(".lazu-menu-item");
    items[0].querySelector("b").textContent = t.markdown;
    items[0].querySelector("small").textContent = t.markdownHint;
    items[1].querySelector("b").textContent = t.agent;
    items[1].querySelector("small").textContent = t.agentHint;
    items[4].querySelector("em").textContent = t.geminiHint;
    return wrap;
  }

  function sidebarContext() {
    var active = document.querySelector(
      ".vocs_Sidebar a[data-active='true'], .vocs_Sidebar .vocs_Sidebar_item_active",
    );
    if (!active) return "";
    var section = active.closest(".vocs_Sidebar_section");
    var title = section && section.querySelector(".vocs_Sidebar_sectionTitle");
    return title ? title.textContent.trim() : "";
  }

  function readMinutes(article) {
    var text = article.innerText || "";
    var cjk = (text.match(/[぀-ヿ㐀-鿿]/g) || []).length;
    var words = (text.replace(/[぀-ヿ㐀-鿿]/g, " ").match(/\S+/g) || []).length;
    return Math.max(1, Math.ceil(words / 220 + cjk / 500));
  }

  function mountActions() {
    var article = document.querySelector(".vocs_Content");
    if (!article || article.querySelector(".lazu-home")) return;
    var head = article.querySelector(".lazu-page-head");
    if (
      head &&
      head.classList.contains("lazu-page-head--prose") &&
      head.getAttribute("data-path") !== location.pathname
    ) {
      head.remove();
      head = null;
    }
    var current = article.querySelector(".lazu-page-actions");
    if (current && current.getAttribute("data-path") === location.pathname)
      return;
    if (current) current.remove();

    if (!head) {
      // Prose pages: build the same row the endpoint pages render — eyebrow
      // (sidebar section · reading time) on the left, actions on the right.
      var header = article.querySelector(".vocs_Header");
      if (!header) return;
      head = document.createElement("div");
      head.className = "lazu-page-head lazu-page-head--prose";
      head.setAttribute("data-path", location.pathname);
      var eyebrow = document.createElement("div");
      eyebrow.className = "lazu-endpoint-eyebrow";
      var section = sidebarContext();
      var t = COPY[activeLocale()];
      eyebrow.textContent =
        (section ? section + " · " : "") + t.read(readMinutes(article));
      head.appendChild(eyebrow);
      header.insertAdjacentElement("beforebegin", head);
    }
    head.appendChild(buildActions());
  }

  function closeMenus(except) {
    document.querySelectorAll(".lazu-menu").forEach(function (menu) {
      if (menu === except) return;
      menu.hidden = true;
      var trigger = menu.parentElement.querySelector(".lazu-split-more");
      if (trigger) trigger.setAttribute("aria-expanded", "false");
    });
  }

  function keepMenuInViewport(menu) {
    menu.style.transform = "";
    var bounds = menu.getBoundingClientRect();
    var inset = 20;
    var shift = 0;
    if (bounds.left < inset) shift = inset - bounds.left;
    if (bounds.right + shift > window.innerWidth - inset)
      shift = window.innerWidth - inset - bounds.right;
    if (shift) menu.style.transform = "translateX(" + shift + "px)";
  }

  // ---------- heading anchors ----------
  function mountAnchors() {
    var t = COPY[activeLocale()];
    document
      .querySelectorAll(".vocs_Content .vocs_Autolink")
      .forEach(function (link) {
        if (link.getAttribute("data-lazu-anchor") === "true") return;
        link.setAttribute("data-lazu-anchor", "true");
        link.removeAttribute("aria-hidden");
        link.setAttribute("tabindex", "0");
        link.setAttribute("aria-label", t.anchor);
        link.setAttribute("title", t.anchor);
      });
  }

  function copyAnchor(link) {
    var t = COPY[activeLocale()];
    var url = new URL(link.getAttribute("href"), location.href);
    var hash = url.hash;
    history.replaceState(history.state, "", hash);
    var target = document.getElementById(decodeURIComponent(hash.slice(1)));
    if (target) target.scrollIntoView({ block: "start" });
    writeClipboard(url.href).then(
      function () {
        toast(t.copiedLink, hash);
      },
      function () {
        toast(t.failed);
      },
    );
  }

  // ---------- listeners ----------
  var wired = false;
  function wire() {
    if (wired) return;
    wired = true;
    document.addEventListener("click", function (event) {
      var target = event.target;
      if (!(target instanceof Element)) return;

      var more = target.closest(".lazu-split-more");
      if (more) {
        var menu = more
          .closest(".lazu-page-actions")
          .querySelector(".lazu-menu");
        var open = menu.hidden;
        closeMenus(menu);
        menu.hidden = !open;
        more.setAttribute("aria-expanded", String(open));
        if (open) {
          keepMenuInViewport(menu);
          var first = menu.querySelector(".lazu-menu-item");
          if (first) first.focus();
        }
        return;
      }

      var action = target.closest("[data-action]");
      if (action && action.closest(".lazu-page-actions")) {
        var t = COPY[activeLocale()];
        var kind = action.getAttribute("data-action");
        if (kind === "markdown") {
          copyMarkdown();
        } else if (kind === "agent") {
          writeClipboard(t.agentPrompt(mdUrl())).then(
            function () {
              toast(t.copiedAgent);
            },
            function () {
              toast(t.failed);
            },
          );
        } else if (kind === "gemini") {
          writeClipboard(t.chatPrompt(mdUrl())).then(
            function () {
              toast(t.copiedGemini);
            },
            function () {
              toast(t.failed);
            },
          );
        }
        closeMenus();
        return;
      }
      if (target.closest(".lazu-menu a")) {
        closeMenus();
        return;
      }

      var anchor = target.closest(".vocs_Content .vocs_Autolink");
      if (anchor) {
        event.preventDefault();
        copyAnchor(anchor);
        return;
      }

      if (!target.closest(".lazu-page-actions")) closeMenus();
    });

    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape") {
        var open = document.querySelector(".lazu-menu:not([hidden])");
        if (open) {
          closeMenus();
          var trigger = open.parentElement.querySelector(".lazu-split-more");
          if (trigger) trigger.focus();
        }
        return;
      }
      var target = event.target;
      if (!(target instanceof Element)) return;
      if (
        (event.key === "Enter" || event.key === " ") &&
        target.matches(".vocs_Content .vocs_Autolink")
      ) {
        event.preventDefault();
        copyAnchor(target);
        return;
      }
      if (
        (event.key === "ArrowDown" || event.key === "ArrowUp") &&
        target.closest(".lazu-menu")
      ) {
        event.preventDefault();
        var items = Array.prototype.slice.call(
          target.closest(".lazu-menu").querySelectorAll(".lazu-menu-item"),
        );
        var index = items.indexOf(target.closest(".lazu-menu-item"));
        var next =
          items[
            (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) %
              items.length
          ];
        if (next) next.focus();
      }
    });
  }

  function mount() {
    syncLang();
    mountActions();
    mountAnchors();
  }

  function start() {
    wire();
    mount();
    var pending = null;
    new MutationObserver(function () {
      if (pending) return;
      pending = window.setTimeout(function () {
        pending = null;
        mount();
      }, 150);
    }).observe(document.body, { childList: true, subtree: true });
  }

  afterHydration(start);

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

/* Mark code blocks with their language (the dark header reads it) and whether
 * they have more to show sideways, so the CSS can fade the trailing edge.
 * Silent scroll removes the scrollbar; without a cue, overflowing code on a
 * phone reads as if it simply ended. */
(function markCodeBlocks() {
  function scan() {
    document.querySelectorAll(".vocs_CodeBlock").forEach(function (block) {
      var pre = block.querySelector("pre");
      if (!pre) return;
      var code = pre.querySelector("code");
      var language =
        pre.getAttribute("data-language") ||
        (code &&
          (code.getAttribute("data-language") ||
            (code.className.match(/language-([^ ]+)/) || [])[1])) ||
        (pre.className.match(/language-([^ ]+)/) || [])[1] ||
        "text";
      if (block.getAttribute("data-language") !== language)
        block.setAttribute("data-language", language);
      var more = pre.scrollWidth - pre.clientWidth > 4;
      block.setAttribute("data-overflow", more ? "true" : "false");
    });
  }
  function schedule() {
    window.requestAnimationFrame(scan);
  }
  if (document.readyState === "complete") schedule();
  else window.addEventListener("load", schedule, { once: true });
  window.addEventListener("resize", schedule);
  new MutationObserver(schedule).observe(document.body, {
    childList: true,
    subtree: true,
  });
})();
