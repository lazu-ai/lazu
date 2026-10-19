import { useState } from "react";
import { withBase } from "./base-path";
import { writeClipboard } from "./clipboard";
import { type HomeLocale, homeCopy } from "./docs-home-data";
import { highlight } from "./endpoint-rail";
import { ENDPOINT_SAMPLES } from "./endpoint-samples";

// Docs home (Docs B, LOCKED 2026-09-23): an editorial left column — search,
// quick tags, three link columns — beside the graphite "Hello, Lazu" column
// with the first request and the agent entry (llms.txt).

const LLMS_URL = "https://lazu.ai/docs/llms.txt";

const TABS = [
  { k: "python", label: "Python" },
  { k: "ts", label: "TypeScript" },
  { k: "curl", label: "cURL" },
] as const;

const Chevron = () => (
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
    <path d="M6 4l4 4-4 4" />
  </svg>
);

const External = () => (
  <svg
    width="11"
    height="11"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M9 3h4v4" />
    <path d="M13 3L7.5 8.5" />
    <path d="M11 9.5V12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h2.5" />
  </svg>
);

const Search = () => (
  <svg
    width="16"
    height="16"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    aria-hidden="true"
  >
    <circle cx="7" cy="7" r="4.5" />
    <path d="M10.5 10.5L14 14" />
  </svg>
);

const Bot = () => (
  <svg
    width="15"
    height="15"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <rect x="3" y="5" width="10" height="8" rx="2.5" />
    <path d="M8 5V2.5M6 9h.01M10 9h.01" />
  </svg>
);

// Hand off to Vocs' own search dialog: the desktop trigger on wide screens,
// the mobile one where the desktop bar is hidden.
function openSearch() {
  const triggers = document.querySelectorAll<HTMLButtonElement>(
    ".vocs_DesktopSearch_search, .vocs_MobileSearch_searchButton",
  );
  const visible = Array.from(triggers).find((el) => el.offsetParent !== null);
  visible?.click();
}

export function DocsHome({ locale = "en" }: { locale?: HomeLocale }) {
  const t = homeCopy(locale);
  const samples = ENDPOINT_SAMPLES.chat.samples;
  const [tab, setTab] = useState<(typeof TABS)[number]["k"]>("python");
  const [copied, setCopied] = useState<string | null>(null);
  const prompt = encodeURIComponent(
    `Read ${LLMS_URL} — the Lazu API docs. Lazu is an OpenAI-compatible AI gateway (base_url https://api.lazu.ai/v1). Help me integrate it; answer only from those docs. My question: `,
  );

  const copy = async (key: string, text: string) => {
    try {
      await writeClipboard(text);
      setCopied(key);
      window.setTimeout(() => setCopied(null), 1300);
    } catch {
      setCopied(null);
    }
  };

  return (
    <div className="lazu-home">
      <div className="lazu-home-eyebrow">{t.eyebrow}</div>
      <h1>{t.title}</h1>
      <p className="lazu-home-lede">{t.lede}</p>
      <button type="button" className="lazu-home-search" onClick={openSearch}>
        <Search />
        <span>{t.search}</span>
        <kbd>⌘K</kbd>
      </button>
      <div className="lazu-home-tags">
        {t.tags.map((tag) => (
          <a key={tag.label} href={withBase(tag.href)}>
            {tag.label}
          </a>
        ))}
      </div>
      <div className="lazu-home-cols">
        {t.columns.map((column) => (
          <nav key={column.title} aria-label={column.title}>
            <h2>{column.title}</h2>
            {column.links.map((item) => (
              <a key={item.href} href={withBase(item.href)}>
                {item.label}
                <Chevron />
              </a>
            ))}
          </nav>
        ))}
      </div>

      <aside className="lazu-home-bench" aria-label={t.benchLabel}>
        <div className="lazu-bench-label">
          <span>{t.benchLabel}</span>
        </div>
        <p className="lazu-home-bench-title" style={{ whiteSpace: "pre-line" }}>
          {t.benchTitle}
        </p>
        <div className="lazu-cb">
          <div className="lazu-cb-head">
            {TABS.map((item) => (
              <button
                key={item.k}
                type="button"
                className="lazu-cb-tab"
                data-on={item.k === tab}
                onClick={() => setTab(item.k)}
              >
                {item.label}
              </button>
            ))}
            <button
              type="button"
              className="lazu-cb-copy"
              onClick={() => copy("code", samples[tab])}
            >
              {copied === "code" ? t.copied : t.copy}
            </button>
          </div>
          <pre className="lazu-cb-pre">
            <code>{highlight(samples[tab])}</code>
          </pre>
        </div>
        <div className="lazu-home-agent">
          <div className="lazu-home-agent-title">
            <Bot />
            {t.agentTitle}
          </div>
          <div className="lazu-home-agent-url">
            <span>{LLMS_URL}</span>
            <button
              type="button"
              className="lazu-cb-copy"
              onClick={() => copy("llms", LLMS_URL)}
            >
              {copied === "llms" ? t.copied : t.copy}
            </button>
          </div>
          <div className="lazu-home-agent-links">
            <a
              className="lazu-cb-copy"
              href={`https://chatgpt.com/?hints=search&q=${prompt}`}
              target="_blank"
              rel="noopener noreferrer"
            >
              ChatGPT
              <External />
            </a>
            <a
              className="lazu-cb-copy"
              href={`https://claude.ai/new?q=${prompt}`}
              target="_blank"
              rel="noopener noreferrer"
            >
              Claude
              <External />
            </a>
            <a
              className="lazu-cb-copy"
              href="https://gemini.google.com/app"
              target="_blank"
              rel="noopener noreferrer"
              onClick={() => copy("gemini", decodeURIComponent(prompt))}
            >
              Gemini
              <External />
            </a>
          </div>
        </div>
      </aside>
    </div>
  );
}
