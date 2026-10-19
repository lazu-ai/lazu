// Copy for the docs home (Docs B, LOCKED 2026-09-23). One module feeds both
// the rendered page (docs-home.tsx) and its Markdown sibling (mdx-markdown.ts),
// so the .md an agent reads says exactly what the page says.

export type HomeLocale = "en" | "zh" | "zh-TW" | "ja";

type HomeLink = { href: string; label: string };

export type HomeCopy = {
  eyebrow: string;
  title: string;
  lede: string;
  search: string;
  tags: HomeLink[];
  columns: Array<{ title: string; links: HomeLink[] }>;
  benchLabel: string;
  benchTitle: string;
  agentTitle: string;
  copy: string;
  copied: string;
};

const routes = {
  quickstart: "/quickstart",
  authentication: "/authentication",
  catalog: "/models/catalog",
  pricing: "/models/pricing",
  chat: "/endpoints/chat",
  responses: "/endpoints/responses",
  embeddings: "/endpoints/embeddings",
  search: "/endpoints/search",
  files: "/endpoints/files",
  media: "/endpoints/media",
  errors: "/errors",
  limits: "/limits",
  billing: "/billing",
  explorer: "/api-reference",
  changelog: "/changelog",
};

type Labels = Record<keyof typeof routes, string>;

const LABELS: Record<HomeLocale, Labels> = {
  en: {
    quickstart: "Quickstart",
    authentication: "Authentication",
    catalog: "Model catalog",
    pricing: "Pricing & lanes",
    chat: "Chat completions",
    responses: "Responses",
    embeddings: "Embeddings",
    search: "Search",
    files: "Files",
    media: "Images, video and audio",
    errors: "Errors",
    limits: "Rate limits",
    billing: "How pricing works",
    explorer: "API Explorer",
    changelog: "Changelog",
  },
  zh: {
    quickstart: "快速开始",
    authentication: "鉴权",
    catalog: "模型目录",
    pricing: "定价与渠道",
    chat: "Chat completions",
    responses: "Responses",
    embeddings: "Embeddings",
    search: "Search",
    files: "Files",
    media: "图片、视频与音频",
    errors: "错误码",
    limits: "请求频率限制",
    billing: "计费规则",
    explorer: "API Explorer",
    changelog: "更新日志",
  },
  "zh-TW": {
    quickstart: "快速開始",
    authentication: "驗證",
    catalog: "模型目錄",
    pricing: "定價與通道",
    chat: "Chat completions",
    responses: "Responses",
    embeddings: "Embeddings",
    search: "Search",
    files: "Files",
    media: "圖片、影片與音訊",
    errors: "錯誤碼",
    limits: "請求頻率限制",
    billing: "計費規則",
    explorer: "API Explorer",
    changelog: "更新日誌",
  },
  ja: {
    quickstart: "クイックスタート",
    authentication: "認証",
    catalog: "モデルカタログ",
    pricing: "料金と経路",
    chat: "Chat completions",
    responses: "Responses",
    embeddings: "Embeddings",
    search: "Search",
    files: "Files",
    media: "画像・動画・音声",
    errors: "エラーコード",
    limits: "レート制限",
    billing: "料金体系",
    explorer: "API Explorer",
    changelog: "更新履歴",
  },
};

const TEXT: Record<
  HomeLocale,
  Omit<HomeCopy, "tags" | "columns"> & { groups: [string, string, string] }
> = {
  en: {
    eyebrow: "Lazu API documentation",
    title: "Build on one key.",
    lede: "An OpenAI-compatible gateway to the models your key can reach. Discover a model, call its endpoint, read the receipt.",
    search: "Search docs or paste an error code",
    groups: ["Start", "Endpoints", "Reference"],
    benchLabel: "Hello, Lazu",
    benchTitle: "Change the base URL.\nKeep your SDK.",
    agentTitle: "Give these docs to your agent",
    copy: "Copy",
    copied: "Copied",
  },
  zh: {
    eyebrow: "Lazu API 文档",
    title: "一个 Key，接入所有模型。",
    lede: "OpenAI 兼容的网关，接入你的 Key 可用的所有模型。先查模型，再调接口，最后看回执。",
    search: "搜索文档，或粘贴错误码",
    groups: ["开始", "接口", "参考"],
    benchLabel: "Hello, Lazu",
    benchTitle: "只改 base URL，\nSDK 照用。",
    agentTitle: "把文档交给你的 Agent",
    copy: "复制",
    copied: "已复制",
  },
  "zh-TW": {
    eyebrow: "Lazu API 文件",
    title: "一把 Key，接入所有模型。",
    lede: "OpenAI 相容的閘道，接入你的 Key 可用的所有模型。先查模型，再呼叫介面，最後看回執。",
    search: "搜尋文件，或貼上錯誤碼",
    groups: ["開始", "端點", "參考"],
    benchLabel: "Hello, Lazu",
    benchTitle: "只改 base URL，\nSDK 照用。",
    agentTitle: "把文件交給你的 Agent",
    copy: "複製",
    copied: "已複製",
  },
  ja: {
    eyebrow: "Lazu API ドキュメント",
    title: "1 つのキーで、すべてのモデルへ。",
    lede: "キーで使えるモデルにつながる OpenAI 互換ゲートウェイ。モデルを探し、エンドポイントを呼び、明細を読む。",
    search: "ドキュメント検索、またはエラーコードを貼り付け",
    groups: ["はじめに", "エンドポイント", "リファレンス"],
    benchLabel: "Hello, Lazu",
    benchTitle: "base URL を変えるだけ。\nSDK はそのまま。",
    agentTitle: "ドキュメントをエージェントに渡す",
    copy: "コピー",
    copied: "コピー済み",
  },
};

export function homeCopy(locale: HomeLocale): HomeCopy {
  const prefix = locale === "en" ? "" : `/${locale}`;
  const labels = LABELS[locale];
  const link = (key: keyof typeof routes): HomeLink => ({
    href: prefix + routes[key],
    label: labels[key],
  });
  const { groups, ...text } = TEXT[locale];
  return {
    ...text,
    tags: [
      { href: prefix + routes.errors, label: "insufficient_quota" },
      { href: prefix + routes.chat, label: "stream_options" },
      { href: prefix + routes.quickstart, label: "base_url" },
      { href: prefix + routes.limits, label: "429" },
      { href: prefix + routes.chat, label: "tool_choice" },
    ],
    columns: [
      {
        title: groups[0],
        links: [
          link("quickstart"),
          link("authentication"),
          link("catalog"),
          link("pricing"),
        ],
      },
      {
        title: groups[1],
        links: [
          link("chat"),
          link("responses"),
          link("embeddings"),
          link("search"),
          link("files"),
          link("media"),
        ],
      },
      {
        title: groups[2],
        links: [
          link("errors"),
          link("limits"),
          link("billing"),
          link("explorer"),
          link("changelog"),
        ],
      },
    ],
  };
}
