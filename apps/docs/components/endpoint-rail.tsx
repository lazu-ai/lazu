import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { stripBase } from "./base-path";
import { writeClipboard } from "./clipboard";
import { EndpointReceipt } from "./endpoint-billing";
import { ENDPOINT_SAMPLES } from "./endpoint-samples";
import { DEFAULT_CHAT_MODEL } from "./models";

// The graphite workbench column on endpoint pages (Docs B, LOCKED 2026-09-23).
// Top to bottom: send panel (key · model · Send), the request in three
// languages, the response, and the receipt. Before a send, response and
// receipt are labelled examples; after a send both switch to the reader's own
// request, read from the response body and the X-Lazu-* headers.

type Samples = { curl: string; python: string; ts: string };
type RequestTab = keyof Samples;
type ResponseTab = "example" | "stream" | "error" | "body" | "headers";

type EndpointRailProps = {
  /** Endpoint key into ENDPOINT_SAMPLES (chat, search, models, …). */
  endpoint: string;
  /** Extra cards rendered below the receipt. */
  extra?: ReactNode;
};

type LiveResult = {
  status: number;
  ms: number;
  headers: Array<[string, string]>;
  body: string;
  usage: {
    input?: number;
    output?: number;
    searchRequests?: number;
    searchUnits?: number;
  };
  model?: string;
  keyEnding: string;
  blocked?: boolean;
};

type PricingModel = {
  model_name: string;
  supported_endpoints?: Record<string, { path?: string }>;
  lanes?: Array<{ available?: boolean }>;
};

const DEFAULT_ERROR = `{
  "error": {
    "type": "rate_limit_exceeded",
    "message": "Per-key limit reached. Retry after 12s.",
    "request_id": "req_01ABCDEF"
  }
}`;

const REQUEST_TABS: Array<{ k: RequestTab; label: string }> = [
  { k: "curl", label: "cURL" },
  { k: "python", label: "Python" },
  { k: "ts", label: "TypeScript" },
];

type Copy = {
  key: string;
  keyPlaceholder: string;
  keySet: string;
  getKey: string;
  model: string;
  lane: string;
  keyEnding: string;
  unavailable: string;
  send: string;
  sendAgain: string;
  sending: string;
  note: string;
  request: string;
  response: string;
  example: string;
  live: string;
  stream: string;
  error: string;
  body: string;
  headers: string;
  copy: string;
  copied: string;
  blocked: string;
  blockedTag: string;
  receipt: string;
  fromHeaders: string;
  requestId: string;
  providerRegion: string;
  tokens: string;
  searchUsage: string;
  noCompatibleModels: string;
  pendingCost: string;
  openConsole: string;
};

const COPY: Record<string, Copy> = {
  en: {
    key: "Key",
    keyPlaceholder: "Paste an API key to send",
    keySet: "Key set",
    getKey: "Get a key",
    model: "Model",
    lane: "Lane",
    keyEnding: "Key ending",
    unavailable: "Not reported",
    send: "Send request",
    sendAgain: "Send again",
    sending: "Sending…",
    note: "Sent from your browser to api.lazu.ai · key stays in this tab",
    request: "Request",
    response: "Response",
    example: "Example",
    live: "Your request · live",
    stream: "Stream",
    error: "Error",
    body: "Body",
    headers: "Headers",
    copy: "Copy",
    copied: "Copied",
    blocked:
      "The browser stopped this request before it reached the API. Copy the cURL command and run it in a terminal instead.",
    blockedTag: "Blocked by the browser",
    receipt: "Your receipt",
    fromHeaders: "request and response",
    requestId: "Request ID",
    providerRegion: "Gateway · region",
    tokens: "Tokens in / out",
    searchUsage: "Search requests / billable units",
    noCompatibleModels: "No compatible models",
    pendingCost: "Cost not reported",
    openConsole: "Open this request in the console",
  },
  zh: {
    key: "Key",
    keyPlaceholder: "粘贴 API Key 即可发送",
    keySet: "Key 已填",
    getKey: "获取 Key",
    model: "模型",
    lane: "线路",
    keyEnding: "Key 尾号",
    unavailable: "未返回",
    send: "发送请求",
    sendAgain: "再发一次",
    sending: "发送中…",
    note: "从你的浏览器直接发往 api.lazu.ai · Key 只留在这个标签页",
    request: "请求",
    response: "响应",
    example: "示例",
    live: "你的请求 · 实时",
    stream: "流式",
    error: "错误",
    body: "Body",
    headers: "Headers",
    copy: "复制",
    copied: "已复制",
    blocked:
      "请求在到达 API 之前被浏览器拦下了。请复制 cURL 命令在终端里运行。",
    blockedTag: "被浏览器拦截",
    receipt: "你的回执",
    fromHeaders: "请求与响应",
    requestId: "请求 ID",
    providerRegion: "网关节点 · 区域",
    tokens: "Token 输入 / 输出",
    searchUsage: "搜索请求 / 计费单位",
    noCompatibleModels: "没有兼容模型",
    pendingCost: "未返回费用",
    openConsole: "在控制台打开这条请求",
  },
  "zh-TW": {
    key: "Key",
    keyPlaceholder: "貼上 API Key 即可送出",
    keySet: "Key 已填",
    getKey: "取得 Key",
    model: "模型",
    lane: "通道",
    keyEnding: "Key 尾碼",
    unavailable: "未回傳",
    send: "送出請求",
    sendAgain: "再送一次",
    sending: "送出中…",
    note: "從你的瀏覽器直接送往 api.lazu.ai · Key 只留在這個分頁",
    request: "請求",
    response: "回應",
    example: "範例",
    live: "你的請求 · 即時",
    stream: "串流",
    error: "錯誤",
    body: "Body",
    headers: "Headers",
    copy: "複製",
    copied: "已複製",
    blocked: "請求在抵達 API 前被瀏覽器擋下了。請複製 cURL 指令在終端機執行。",
    blockedTag: "被瀏覽器攔截",
    receipt: "你的回執",
    fromHeaders: "請求與回應",
    requestId: "請求 ID",
    providerRegion: "閘道節點 · 區域",
    tokens: "Token 輸入 / 輸出",
    searchUsage: "搜尋請求 / 計費單位",
    noCompatibleModels: "沒有相容模型",
    pendingCost: "未回傳費用",
    openConsole: "在主控台開啟這筆請求",
  },
  ja: {
    key: "キー",
    keyPlaceholder: "API キーを貼り付けて送信",
    keySet: "キー設定済み",
    getKey: "キーを取得",
    model: "モデル",
    lane: "レーン",
    keyEnding: "キー末尾",
    unavailable: "未返却",
    send: "リクエスト送信",
    sendAgain: "もう一度送信",
    sending: "送信中…",
    note: "ブラウザから api.lazu.ai へ直接送信 · キーはこのタブにだけ残ります",
    request: "リクエスト",
    response: "レスポンス",
    example: "例",
    live: "あなたのリクエスト · ライブ",
    stream: "ストリーム",
    error: "エラー",
    body: "Body",
    headers: "Headers",
    copy: "コピー",
    copied: "コピー済み",
    blocked:
      "リクエストは API に届く前にブラウザで止められました。cURL コマンドをコピーしてターミナルで実行してください。",
    blockedTag: "ブラウザがブロック",
    receipt: "あなたの明細",
    fromHeaders: "リクエストとレスポンス",
    requestId: "リクエスト ID",
    providerRegion: "ゲートウェイ · リージョン",
    tokens: "トークン 入力 / 出力",
    searchUsage: "検索リクエスト / 課金単位",
    noCompatibleModels: "対応モデルなし",
    pendingCost: "料金未返却",
    openConsole: "このリクエストをコンソールで開く",
  },
};

// Locale is read from the URL, which only exists on the client. Reading it
// during render made the first client render disagree with the prerendered
// HTML, so resolve it after mount instead.
function useCopy(): Copy {
  const [copy, setCopy] = useState<Copy>(COPY.en);
  useEffect(() => {
    const seg = stripBase(window.location.pathname).split("/")[1];
    setCopy(COPY[seg] ?? COPY.en);
  }, []);
  return copy;
}

const API_ORIGIN = String(
  import.meta.env.VITE_LAZU_API_ORIGIN || "https://api.lazu.ai",
).replace(/\/+$/, "");
const WEB_ORIGIN = String(
  import.meta.env.VITE_LAZU_WEB_ORIGIN || "https://lazu.ai",
).replace(/\/+$/, "");
const KEY_STORAGE = "lazu.docs.api-key";

function streamify(raw: string | undefined): string | undefined {
  if (!raw) return raw;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return JSON.stringify(
        { ...parsed, stream: true, stream_options: { include_usage: true } },
        null,
        2,
      );
    }
  } catch {
    /* Keep a non-JSON example unchanged. */
  }
  return raw;
}

function usageFromPayload(payload: unknown): LiveResult["usage"] {
  if (!payload || typeof payload !== "object") return {};
  const body = payload as { usage?: unknown; response?: { usage?: unknown } };
  const raw = body.usage ?? body.response?.usage;
  if (!raw || typeof raw !== "object") return {};
  const usage = raw as Record<string, unknown>;
  const number = (value: unknown) =>
    typeof value === "number" && Number.isFinite(value) ? value : undefined;
  return {
    input: number(usage.prompt_tokens ?? usage.input_tokens),
    output: number(usage.completion_tokens ?? usage.output_tokens),
    searchRequests: number(usage.web_search_requests),
    searchUnits: number(usage.web_search_billable_units),
  };
}

function usageFromStream(text: string): LiveResult["usage"] {
  let latest: LiveResult["usage"] = {};
  for (const frame of text.split(/\r?\n\r?\n/)) {
    const data = frame
      .split(/\r?\n/)
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trimStart())
      .join("\n");
    if (!data || data === "[DONE]") continue;
    try {
      const found = usageFromPayload(JSON.parse(data) as unknown);
      if (Object.values(found).some((value) => value !== undefined))
        latest = found;
    } catch {
      /* Ignore non-JSON SSE events; keep the last reported usage. */
    }
  }
  return latest;
}

function modelFits(model: PricingModel, path?: string) {
  if (!model.lanes?.some((lane) => lane.available)) return false;
  if (!path) return false;
  return Object.values(model.supported_endpoints ?? {}).some(
    (target) => target?.path === path,
  );
}

// Tiny highlighter for the graphite column, using the same code tokens as the
// article's Shiki theme. One <span class="ln"> per line carries the number.
export function highlight(code: string): ReactNode[] {
  const kw =
    /^(curl|import|from|const|await|new|return|def|print|for|in|export|async|function)$/;
  return String(code)
    .split("\n")
    .map((ln, i) => {
      const out: ReactNode[] = [];
      const re =
        /("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|#[^\n]*|\/\/[^\n]*|\b\d+(?:\.\d+)?\b|--?[A-Za-z][\w-]*|[A-Za-z_$][\w$.]*|\s+|.)/g;
      let m: RegExpExecArray | null;
      let key = 0;
      while ((m = re.exec(ln))) {
        const tok = m[0];
        let cls = "";
        if (/^["']/.test(tok)) cls = "s";
        else if (/^#|^\/\//.test(tok)) cls = "c";
        else if (/^\d/.test(tok)) cls = "f";
        else if (/^--?[A-Za-z]/.test(tok)) cls = "f";
        else if (kw.test(tok)) cls = "k";
        out.push(
          cls ? (
            <span key={key++} className={"hl-" + cls}>
              {tok}
            </span>
          ) : (
            tok
          ),
        );
      }
      return (
        <span className="lazu-line" key={i}>
          <span className="ln" aria-hidden="true">
            {i + 1}
          </span>
          {out.length ? out : " "}
          {"\n"}
        </span>
      );
    });
}

const CopyIcon = () => (
  <svg
    width="13"
    height="13"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <rect x="5.5" y="5.5" width="8" height="8" rx="2" />
    <path d="M10.5 5.5V4a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 4v5A1.5 1.5 0 0 0 4 10.5h1.5" />
  </svg>
);

const PlayIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
    <path d="M5 3.5v9l7-4.5z" fill="currentColor" />
  </svg>
);

export function EndpointRail({ endpoint, extra }: EndpointRailProps) {
  const data = ENDPOINT_SAMPLES[endpoint];
  const t = useCopy();
  const [tab, setTab] = useState<RequestTab>("curl");
  const [respTab, setRespTab] = useState<ResponseTab>("example");
  const [copied, setCopied] = useState<string | null>(null);
  const [apiKey, setApiKey] = useState("");
  const [models, setModels] = useState<string[] | null>(null);
  const [model, setModel] = useState(DEFAULT_CHAT_MODEL);
  const [streamRequested, setStreamRequested] = useState(false);
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<LiveResult | null>(null);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const sampleModel = useMemo(() => {
    try {
      const body = JSON.parse(data.tryBody ?? "{}") as { model?: unknown };
      return typeof body.model === "string" ? body.model : null;
    } catch {
      return null;
    }
  }, [data.tryBody]);

  useEffect(() => {
    if (sampleModel) setModel(sampleModel);
  }, [sampleModel]);

  // The picker reads the public catalog; until it answers, the only option is
  // the sample's own model, which is a known value rather than a guess.
  useEffect(() => {
    if (!sampleModel) return;
    let live = true;
    fetch(`${API_ORIGIN}/api/pricing`)
      .then((res) => (res.ok ? res.json() : Promise.reject(res.status)))
      .then((json: { data?: PricingModel[] }) => {
        if (!live) return;
        const names = (json.data ?? [])
          .filter((m) => modelFits(m, data.path))
          .map((m) => m.model_name)
          .sort((a, b) => a.localeCompare(b));
        setModels(names);
        setModel((current) =>
          names.includes(current) ? current : (names[0] ?? ""),
        );
      })
      .catch(() => {
        if (!live) return;
        setModels([sampleModel]);
        setModel(sampleModel);
      });
    return () => {
      live = false;
    };
  }, [data.path, sampleModel]);

  useEffect(() => {
    try {
      const saved = window.sessionStorage.getItem(KEY_STORAGE);
      if (saved) setApiKey(saved);
    } catch {
      /* storage blocked */
    }
  }, []);

  const swapModel = (text: string) =>
    sampleModel && model !== sampleModel
      ? text.split(sampleModel).join(model)
      : text;

  const streamable = data.streamable ?? false;
  const sourceSamples =
    streamRequested && streamable
      ? (data.streamSamples ?? data.samples)
      : data.samples;
  const samples: Samples = {
    curl: swapModel(sourceSamples.curl),
    python: swapModel(sourceSamples.python),
    ts: swapModel(sourceSamples.ts),
  };
  const reqTxt = samples[tab];

  const exampleText =
    respTab === "stream"
      ? (data.responseStream ?? data.response)
      : respTab === "error"
        ? (data.error ?? DEFAULT_ERROR)
        : data.response;
  const liveText =
    result && respTab === "headers"
      ? result.headers.map(([k, v]) => `${k}: ${v}`).join("\n") || "—"
      : (result?.body ?? "");
  const respTxt = result ? liveText : exampleText;

  const flash = async (k: string, txt: string) => {
    try {
      await writeClipboard(txt);
      if (copyTimer.current) clearTimeout(copyTimer.current);
      setCopied(k);
      copyTimer.current = setTimeout(() => setCopied(null), 1300);
    } catch {
      setCopied(null);
    }
  };

  const onKey = (value: string) => {
    setApiKey(value);
    try {
      if (value) window.sessionStorage.setItem(KEY_STORAGE, value);
      else window.sessionStorage.removeItem(KEY_STORAGE);
    } catch {
      /* storage blocked */
    }
  };

  const send = async () => {
    if (
      !data.path ||
      !apiKey ||
      (sampleModel && models !== null && !models.includes(model))
    )
      return;
    setSending(true);
    setResult(null);
    setRespTab(streamRequested ? "stream" : "example");
    const sentModel = sampleModel ? model : undefined;
    const keyEnding = apiKey.slice(-4);
    const started = performance.now();
    try {
      const body =
        data.method === "GET"
          ? undefined
          : swapModel(
              (streamRequested && streamable
                ? streamify(data.tryBody)
                : data.tryBody) ?? "{}",
            );
      const res = await fetch(API_ORIGIN + data.path, {
        method: data.method ?? "POST",
        headers: {
          Authorization: `Bearer ${apiKey}`,
          "Content-Type": "application/json",
        },
        body,
      });
      const text = await res.text();
      let pretty = text;
      let usage: LiveResult["usage"] = {};
      if (res.headers.get("content-type")?.includes("text/event-stream")) {
        usage = usageFromStream(text);
      } else {
        try {
          const parsed = JSON.parse(text) as unknown;
          pretty = JSON.stringify(parsed, null, 2);
          usage = usageFromPayload(parsed);
        } catch {
          /* Keep non-JSON responses raw. */
        }
      }
      const headers: Array<[string, string]> = [];
      res.headers.forEach((value, name) => {
        if (name.startsWith("x-lazu-") || name === "retry-after")
          headers.push([name, value]);
      });
      setResult({
        status: res.status,
        ms: Math.round(performance.now() - started),
        headers,
        body: pretty.slice(0, 6000),
        usage,
        model: sentModel,
        keyEnding,
      });
      setRespTab("body");
    } catch {
      setResult({
        status: 0,
        ms: Math.round(performance.now() - started),
        headers: [],
        body: t.blocked,
        usage: {},
        model: sentModel,
        keyEnding,
        blocked: true,
      });
      setRespTab("body");
    }
    setSending(false);
  };

  const header = (name: string) =>
    result?.headers.find(([k]) => k === name)?.[1];
  const requestId = header("x-lazu-request-id");
  const cost = header("x-lazu-cost-usd");
  const laneFallback = header("x-lazu-lane-fallback");
  const lane =
    header("x-lazu-lane") || laneFallback?.split("->").at(-1) || t.unavailable;
  const responseTabs: Array<{ k: ResponseTab; label: string }> = result
    ? [
        { k: "body", label: t.body },
        { k: "headers", label: t.headers },
      ]
    : [
        { k: "example", label: "200" },
        ...(streamable ? [{ k: "stream" as const, label: t.stream }] : []),
        { k: "error", label: t.error },
      ];

  return (
    <div className="lazu-bench">
      {data.path ? (
        <div className="lazu-send">
          <div className="lazu-send-row">
            <label className="lazu-send-label" htmlFor={`key-${endpoint}`}>
              {t.key}
            </label>
            <div className="lazu-send-field">
              <input
                id={`key-${endpoint}`}
                type="password"
                value={apiKey}
                placeholder={t.keyPlaceholder}
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => onKey(e.target.value)}
              />
              {apiKey ? (
                <span className="lazu-send-ok">{t.keySet}</span>
              ) : (
                <a
                  href={`${WEB_ORIGIN}/console/token`}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t.getKey}
                </a>
              )}
            </div>
            <button
              type="button"
              className="lazu-send-button"
              data-again={Boolean(result)}
              disabled={
                sending ||
                !apiKey ||
                Boolean(
                  sampleModel && models !== null && !models.includes(model),
                )
              }
              onClick={send}
            >
              <PlayIcon />
              {sending ? t.sending : result ? t.sendAgain : t.send}
            </button>
          </div>
          <div className="lazu-send-row">
            {sampleModel ? (
              <>
                <label
                  className="lazu-send-label"
                  htmlFor={`model-${endpoint}`}
                >
                  {t.model}
                </label>
                <select
                  id={`model-${endpoint}`}
                  className="lazu-send-select"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                >
                  {(models ?? [sampleModel]).map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                  {models?.length === 0 ? (
                    <option value="">{t.noCompatibleModels}</option>
                  ) : null}
                </select>
              </>
            ) : null}
            <span className="lazu-send-note">{t.note}</span>
            {streamable ? (
              <label className="lazu-send-stream">
                <input
                  type="checkbox"
                  checked={streamRequested}
                  onChange={(event) => setStreamRequested(event.target.checked)}
                />
                {t.stream}
              </label>
            ) : null}
          </div>
        </div>
      ) : null}

      <div className="lazu-bench-label">
        <span>{t.request}</span>
        <span className="lazu-bench-ep">
          {(data.displayMethod ?? data.method ?? "POST") +
            " " +
            (data.displayPath ?? data.path ?? "")}
        </span>
      </div>
      <div className="lazu-cb">
        <div className="lazu-cb-head">
          {REQUEST_TABS.map((tb) => (
            <button
              key={tb.k}
              type="button"
              className="lazu-cb-tab"
              data-on={tb.k === tab}
              onClick={() => setTab(tb.k)}
            >
              {tb.label}
            </button>
          ))}
          <button
            type="button"
            className="lazu-cb-copy"
            onClick={() => flash("req", reqTxt)}
          >
            <CopyIcon />
            {copied === "req" ? t.copied : t.copy}
          </button>
        </div>
        <pre className="lazu-cb-pre" data-numbered="true">
          <code>{highlight(reqTxt)}</code>
        </pre>
      </div>

      <div className="lazu-bench-label">
        <span>{t.response}</span>
        <span className="lazu-bench-tag" data-live={Boolean(result)}>
          {result
            ? result.blocked
              ? t.blockedTag
              : `${t.live} · ${result.status} · ${result.ms} ms`
            : sending
              ? t.sending
              : t.example}
        </span>
      </div>
      <div className="lazu-cb">
        <div className="lazu-cb-head">
          {responseTabs.map((tb) => (
            <button
              key={tb.k}
              type="button"
              className="lazu-cb-tab"
              data-on={tb.k === respTab}
              onClick={() => {
                setRespTab(tb.k);
                if (tb.k === "stream") setStreamRequested(true);
              }}
            >
              {tb.label}
            </button>
          ))}
          <button
            type="button"
            className="lazu-cb-copy"
            onClick={() => flash("resp", respTxt)}
            disabled={sending}
          >
            <CopyIcon />
            {copied === "resp" ? t.copied : t.copy}
          </button>
        </div>
        <pre className="lazu-cb-pre">
          <code>
            {sending
              ? t.sending
              : result?.blocked
                ? result.body
                : highlight(respTxt)}
          </code>
        </pre>
      </div>

      {/* A receipt only exists for a request the gateway accepted; errors
          keep the labelled example below the real response. */}
      {sending ? (
        <section
          className="lazu-receipt"
          aria-label={t.receipt}
          aria-busy="true"
        >
          <div className="lazu-receipt-head">{t.receipt}</div>
          <div className="lazu-receipt-total">
            <strong aria-hidden="true">—</strong>
            <span>{t.sending}</span>
          </div>
          <dl>
            {[
              t.requestId,
              t.model,
              t.lane,
              t.providerRegion,
              endpoint === "search" ? t.searchUsage : t.tokens,
              t.keyEnding,
            ].map((label) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>…</dd>
              </div>
            ))}
          </dl>
        </section>
      ) : result && !result.blocked && result.status < 400 ? (
        <section
          className="lazu-receipt"
          data-live="true"
          aria-label={t.receipt}
        >
          <div className="lazu-receipt-head">
            <span>{t.receipt}</span>
            <span>{t.fromHeaders}</span>
          </div>
          <div className="lazu-receipt-total">
            <strong>{cost ? `$${cost}` : t.pendingCost}</strong>
            <span data-ok={result.status < 400}>
              {result.status} · {result.ms} ms
            </span>
          </div>
          <dl>
            <div>
              <dt>{t.requestId}</dt>
              <dd>{requestId ?? "—"}</dd>
            </div>
            <div>
              <dt>{t.model}</dt>
              <dd>{result.model ?? t.unavailable}</dd>
            </div>
            <div>
              <dt>{t.lane}</dt>
              <dd>{lane}</dd>
            </div>
            <div>
              <dt>{t.providerRegion}</dt>
              <dd>
                {[header("x-lazu-provider"), header("x-lazu-region")]
                  .filter(Boolean)
                  .join(" · ") || "—"}
              </dd>
            </div>
            <div>
              <dt>{endpoint === "search" ? t.searchUsage : t.tokens}</dt>
              <dd>
                {endpoint === "search"
                  ? `${result.usage.searchRequests ?? "—"} / ${result.usage.searchUnits ?? "—"}`
                  : `${result.usage.input ?? "—"} / ${result.usage.output ?? "—"}`}
              </dd>
            </div>
            <div>
              <dt>{t.keyEnding}</dt>
              <dd>••••{result.keyEnding}</dd>
            </div>
          </dl>
          {requestId ? (
            <a
              className="lazu-receipt-link"
              href={`${WEB_ORIGIN}/console/usage?tab=logs&request=${encodeURIComponent(requestId)}`}
              target="_blank"
              rel="noreferrer"
            >
              {t.openConsole} →
            </a>
          ) : null}
        </section>
      ) : (
        <EndpointReceipt endpoint={endpoint} />
      )}

      {extra ? <div className="lazu-bench-extra">{extra}</div> : null}
    </div>
  );
}
