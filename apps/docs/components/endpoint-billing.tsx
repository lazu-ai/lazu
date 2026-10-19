import { useLocation } from "react-router";
import { withBase } from "./base-path";
import { DEFAULT_CHAT_MODEL } from "./models";

const COPY = {
  en: {
    billed: "Billing",
    pricing: "Pricing & lanes",
    usagePricing:
      "Usage tokens at the selected model and lane price; other billable dimensions may apply.",
    search: "Search charges depend on the selected backend and billable units.",
    other:
      "Check this endpoint's billable dimensions and the request receipt; do not infer a charge from the response alone.",
    read: "Read-only reference endpoint; this is not a model generation request.",
    receipt: "Request receipt",
    demo: "Illustrative example",
    disclaimer:
      "Example only — not a quote and not the result of your request. Send the request above to see your own receipt.",
    model: "Model",
    lane: "Lane",
    usage: "Tokens · input / output",
    inputUsage: "Tokens · input",
    searchUsage: "Search billable units",
    backend: "Backend",
    key: "Key",
    details: "Read a real request receipt",
    reference:
      "After a billable request, use its gateway request ID to inspect usage dimensions, billing line items and timing.",
  },
  zh: {
    billed: "计费",
    pricing: "价格与线路",
    usagePricing:
      "按所选模型与线路的价格计算 token 用量；可能包含其他计费维度。",
    search: "搜索费用取决于所选后端与计费单位。",
    other: "查看该接口的计费维度和请求回执，不要仅根据响应推算费用。",
    read: "只读查询接口，不是模型生成请求。",
    receipt: "请求回执",
    demo: "示例回执",
    disclaimer:
      "仅为示例，不是报价，也不是你这次请求的结果。发送上方请求即可看到你自己的回执。",
    model: "模型",
    lane: "线路",
    usage: "Token · 输入 / 输出",
    inputUsage: "Token · 输入",
    searchUsage: "搜索计费单位",
    backend: "搜索后端",
    key: "Key",
    details: "查看真实请求回执",
    reference: "计费请求完成后，使用网关请求 ID 查询用量维度、计费明细与耗时。",
  },
  "zh-TW": {
    billed: "計費",
    pricing: "價格與線路",
    usagePricing:
      "依所選模型與線路的價格計算 token 用量；可能包含其他計費維度。",
    search: "搜尋費用取決於所選後端與計費單位。",
    other: "查看此介面的計費維度與請求回執，不要只依回應推算費用。",
    read: "唯讀查詢介面，不是模型生成請求。",
    receipt: "請求回執",
    demo: "範例回執",
    disclaimer:
      "僅為範例，不是報價，也不是你這次請求的結果。送出上方請求即可看到你自己的回執。",
    model: "模型",
    lane: "線路",
    usage: "Token · 輸入 / 輸出",
    inputUsage: "Token · 輸入",
    searchUsage: "搜尋計費單位",
    backend: "搜尋後端",
    key: "Key",
    details: "查看真實請求回執",
    reference: "計費請求完成後，使用網關請求 ID 查詢用量維度、計費明細與耗時。",
  },
  ja: {
    billed: "課金",
    pricing: "料金とレーン",
    usagePricing:
      "選択したモデルとレーンの料金でトークン使用量を計算します。他の課金項目が適用される場合もあります。",
    search: "検索料金はバックエンドと課金単位によって異なります。",
    other:
      "課金項目とリクエストの明細を確認してください。レスポンスだけから料金を推測しないでください。",
    read: "読み取り専用 API。モデル生成リクエストではありません。",
    receipt: "リクエスト明細",
    demo: "明細の例",
    disclaimer:
      "例示のみで、見積もりでもあなたのリクエストの結果でもありません。上のリクエストを送信すると自分の明細が表示されます。",
    model: "モデル",
    lane: "レーン",
    usage: "トークン · 入力 / 出力",
    inputUsage: "トークン · 入力",
    searchUsage: "検索課金単位",
    backend: "検索バックエンド",
    key: "キー",
    details: "実際のリクエスト明細を見る",
    reference:
      "課金リクエストの完了後、ゲートウェイのリクエスト ID で使用量、課金項目、所要時間を確認できます。",
  },
};

function useBillingCopy() {
  const { pathname } = useLocation();
  const segment = pathname.split("/")[1];
  const locale =
    segment === "zh" || segment === "zh-TW" || segment === "ja"
      ? segment
      : "en";
  return { t: COPY[locale], prefix: locale === "en" ? "" : `/${locale}` };
}

export function EndpointBilling({
  method,
  path,
}: {
  method: string;
  path: string;
}) {
  const { t, prefix } = useBillingCopy();
  const note =
    method === "GET"
      ? t.read
      : ["/v1/chat/completions", "/v1/responses", "/v1/embeddings"].includes(
            path,
          )
        ? t.usagePricing
        : path === "/v1/search"
          ? t.search
          : t.other;
  return (
    <p className="lazu-endpoint-billing">
      <strong>{t.billed}</strong>
      <span>
        {note} <a href={withBase(`${prefix}/models/pricing`)}>{t.pricing} ↗</a>
      </span>
    </p>
  );
}

export function EndpointReceipt({ endpoint }: { endpoint: string }) {
  const { t, prefix } = useBillingCopy();
  // This standalone teaching example is deliberately distinct from SDK samples
  // and live responses. Never present a guessed price as a measured charge.
  const generation = endpoint === "chat" || endpoint === "responses";
  const search = endpoint === "search";
  const embeddings = endpoint === "embeddings";
  const illustrative = generation || search || embeddings;
  return (
    <section className="lazu-receipt" aria-label={t.receipt}>
      <div className="lazu-receipt-head">
        <span>{t.receipt}</span>
        {illustrative ? <span>{t.demo}</span> : null}
      </div>
      {illustrative ? (
        <>
          <div className="lazu-receipt-total">
            <strong>
              {search ? "$0.0050" : embeddings ? "$0.0001" : "$0.00028"}
            </strong>
            <span data-ok="true">200 · 842 ms</span>
          </div>
          <dl>
            <div>
              <dt>Request ID</dt>
              <dd>req_demo_01</dd>
            </div>
            <div>
              <dt>{search ? t.backend : `${t.model} · ${t.lane}`}</dt>
              <dd>
                {search
                  ? "example-search-backend"
                  : `${embeddings ? "text-embedding-3-small" : DEFAULT_CHAT_MODEL} · stable`}
              </dd>
            </div>
            <div>
              <dt>
                {search ? t.searchUsage : embeddings ? t.inputUsage : t.usage}
              </dt>
              <dd>{search ? "1" : embeddings ? "1,200" : "1,200 / 320"}</dd>
            </div>
            <div>
              <dt>{t.key}</dt>
              <dd>demo-key</dd>
            </div>
          </dl>
          <p className="lazu-receipt-note">{t.disclaimer}</p>
        </>
      ) : (
        <p className="lazu-receipt-note">{t.reference}</p>
      )}
      <a
        className="lazu-receipt-link"
        href={withBase(`${prefix}/endpoints/usage-requests`)}
      >
        {t.details} →
      </a>
    </section>
  );
}
