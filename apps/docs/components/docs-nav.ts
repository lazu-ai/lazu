/* The docs navigation, per locale. Shared by vocs.config.ts (the sidebar)
 * and the 404 page (its "Did you mean …?" suggestion), so both always name
 * the same pages the same way. Plain data only: this module is bundled into
 * the client. */

export type DocsLocale = "en" | "zh" | "zh-TW" | "ja";

type SidebarCopy = {
  groups: [string, string, string, string, string, string];
  pages: [
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
  ];
};

export const sidebarCopy: Record<DocsLocale, SidebarCopy> = {
  en: {
    groups: [
      "Get started",
      "Models",
      "Endpoints",
      "Limits & quotas",
      "Billing",
      "Reference",
    ],
    pages: [
      "Introduction",
      "Quickstart",
      "Authentication",
      "Catalog",
      "Pricing & lanes",
      "Chat completions",
      "Responses",
      "Embeddings",
      "Search",
      "Files",
      "Models list",
      "Request details",
      "Rate limits",
      "How pricing works",
      "Errors",
      "Code examples",
      "API Explorer",
      "Changelog",
      "Images, video and audio",
      "Agent session costs",
      "Seedance 2.5",
    ],
  },
  zh: {
    groups: ["开始", "模型", "接口", "限额", "计费", "参考"],
    pages: [
      "介绍",
      "快速开始",
      "鉴权",
      "模型目录",
      "定价与线路",
      "Chat completions",
      "Responses",
      "Embeddings",
      "Search",
      "Files",
      "模型列表",
      "请求详情",
      "请求频率限制",
      "计费规则",
      "错误码",
      "代码示例",
      "API Explorer",
      "更新日志",
      "图片、视频与音频",
      "Agent 会话费用",
      "Seedance 2.5",
    ],
  },
  "zh-TW": {
    groups: ["開始", "模型", "端點", "限額", "計費", "參考"],
    pages: [
      "介紹",
      "快速開始",
      "驗證",
      "模型目錄",
      "定價與線路",
      "Chat completions",
      "Responses",
      "Embeddings",
      "Search",
      "Files",
      "模型列表",
      "請求詳情",
      "請求頻率限制",
      "計費規則",
      "錯誤碼",
      "程式碼範例",
      "API Explorer",
      "更新日誌",
      "圖片、影片與音訊",
      "Agent 工作階段費用",
      "Seedance 2.5",
    ],
  },
  ja: {
    groups: [
      "はじめに",
      "モデル",
      "エンドポイント",
      "上限",
      "請求",
      "リファレンス",
    ],
    pages: [
      "概要",
      "クイックスタート",
      "認証",
      "モデルカタログ",
      "料金と経路",
      "Chat completions",
      "Responses",
      "Embeddings",
      "Search",
      "Files",
      "モデル一覧",
      "リクエスト詳細",
      "レート制限",
      "料金体系",
      "エラーコード",
      "コードサンプル",
      "API Explorer",
      "更新履歴",
      "画像・動画・音声",
      "Agent セッション費用",
      "Seedance 2.5",
    ],
  },
};

export function createSidebar(prefix: string, copy: SidebarCopy) {
  const link = (route: string) => `${prefix}${route}` || "/";
  const [start, models, endpoints, , billing, reference] = copy.groups;
  const p = copy.pages;
  return [
    {
      text: start,
      collapsed: false,
      items: [
        { link: link("/"), text: p[0] },
        { link: link("/quickstart"), text: p[1] },
        { link: link("/authentication"), text: p[2] },
      ],
    },
    {
      text: models,
      collapsed: false,
      items: [
        { link: link("/models/catalog"), text: p[3] },
        { link: link("/models/pricing"), text: p[4] },
      ],
    },
    {
      text: endpoints,
      collapsed: false,
      items: [
        { link: link("/endpoints/chat"), text: p[5] },
        { link: link("/endpoints/responses"), text: p[6] },
        { link: link("/endpoints/decisions"), text: "Decisions" },
        { link: link("/endpoints/embeddings"), text: p[7] },
        { link: link("/endpoints/search"), text: p[8] },
        { link: link("/endpoints/files"), text: p[9] },
        { link: link("/endpoints/models"), text: p[10] },
        { link: link("/endpoints/usage-requests"), text: p[11] },
        { link: link("/endpoints/media"), text: p[18] },
        { link: link("/endpoints/seedance"), text: p[20] },
      ],
    },
    {
      text: billing,
      collapsed: false,
      items: [
        { link: link("/billing"), text: p[13] },
        { link: link("/agent-session-costs"), text: p[19] },
      ],
    },
    {
      text: reference,
      collapsed: false,
      items: [
        { link: link("/errors"), text: p[14] },
        { link: link("/examples"), text: p[15] },
        { link: link("/api-reference"), text: p[16] },
        { link: link("/changelog"), text: p[17] },
      ],
    },
  ];
}

export function createSidebars() {
  return {
    "/": createSidebar("", sidebarCopy.en),
    "/zh/": createSidebar("/zh", sidebarCopy.zh),
    "/zh-TW/": createSidebar("/zh-TW", sidebarCopy["zh-TW"]),
    "/ja/": createSidebar("/ja", sidebarCopy.ja),
  };
}
