import { useEffect, useRef, useState } from "react";
import { DEFAULT_CHAT_MODEL } from "./models";

// Native "try it live" API explorer — the v2 design's bespoke explorer UI,
// wired to REAL requests against the configured Lazu API. The API reflects the
// request Origin in Access-Control-Allow-Origin, so browser fetches work
// directly (no proxy). The key is held only in this tab (optionally
// localStorage) and sent straight to the API.

const BASE = String(
  import.meta.env.VITE_LAZU_API_ORIGIN || "https://api.lazu.ai",
).replace(/\/+$/, "");
const KEY_STORAGE = "lazu.docs.api-key";

type Op = {
  id: string;
  method: "GET" | "POST";
  label: string;
  path: string;
  body?: unknown;
};

const OPS: Op[] = [
  {
    id: "chat",
    method: "POST",
    label: "Create chat completion",
    path: "/v1/chat/completions",
    body: {
      model: DEFAULT_CHAT_MODEL,
      messages: [{ role: "user", content: "Say hello from the explorer" }],
    },
  },
  {
    id: "search",
    method: "POST",
    label: "Run search",
    path: "/v1/search",
    body: { query: "latest OpenAI news", type: "web", max_results: 3 },
  },
  { id: "models", method: "GET", label: "List models", path: "/v1/models" },
  {
    id: "embeddings",
    method: "POST",
    label: "Create embeddings",
    path: "/v1/embeddings",
    body: { model: "text-embedding-3-small", input: ["hello world"] },
  },
  {
    id: "responses",
    method: "POST",
    label: "Create response",
    path: "/v1/responses",
    body: { model: DEFAULT_CHAT_MODEL, input: "Say hi from the explorer" },
  },
];

const bodyFor = (op: Op) => (op.body ? JSON.stringify(op.body, null, 2) : "");

type Result = { status: number; ok: boolean; body: string; ms: number } | null;

export function ApiExplorer() {
  const [opIdx, setOpIdx] = useState(0);
  const [apiKey, setApiKey] = useState("");
  const [remember, setRemember] = useState(false);
  const [body, setBody] = useState(bodyFor(OPS[0]));
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<Result>(null);
  const dirty = useRef(false);

  const op = OPS[opIdx];

  // Restore a remembered key after mount (SSR-safe).
  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(KEY_STORAGE);
      if (saved) {
        setApiKey(saved);
        setRemember(true);
      }
    } catch {
      /* storage blocked */
    }
  }, []);

  const selectOp = (i: number) => {
    setOpIdx(i);
    setResult(null);
    if (!dirty.current) setBody(bodyFor(OPS[i]));
    else setBody(bodyFor(OPS[i]));
    dirty.current = false;
  };

  const onKey = (v: string) => {
    setApiKey(v);
    try {
      if (remember) window.localStorage.setItem(KEY_STORAGE, v);
    } catch {
      /* ignore */
    }
  };

  const toggleRemember = (on: boolean) => {
    setRemember(on);
    try {
      if (on) window.localStorage.setItem(KEY_STORAGE, apiKey);
      else window.localStorage.removeItem(KEY_STORAGE);
    } catch {
      /* ignore */
    }
  };

  const send = async () => {
    setSending(true);
    setResult(null);
    const started = typeof performance !== "undefined" ? performance.now() : 0;
    try {
      const res = await fetch(BASE + op.path, {
        method: op.method,
        headers: {
          Authorization: `Bearer ${apiKey || "YOUR_LAZU_KEY"}`,
          "Content-Type": "application/json",
        },
        body: op.method === "POST" ? body : undefined,
      });
      const text = await res.text();
      let pretty = text;
      try {
        pretty = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        /* not JSON — show raw */
      }
      setResult({
        status: res.status,
        ok: res.ok,
        body: pretty,
        ms: Math.round((performance?.now?.() ?? 0) - started),
      });
    } catch (e) {
      setResult({
        status: 0,
        ok: false,
        body:
          "Request failed: " +
          (e instanceof Error ? e.message : String(e)) +
          "\n\nCheck your network and that the key is valid.",
        ms: Math.round((performance?.now?.() ?? 0) - started),
      });
    }
    setSending(false);
  };

  return (
    <div className="lazu-expl">
      <div className="lazu-expl-list">
        <div className="lazu-expl-list-label">Operations</div>
        {OPS.map((o, i) => (
          <button
            key={o.id}
            type="button"
            className="lazu-expl-op"
            data-on={i === opIdx}
            onClick={() => selectOp(i)}
          >
            <span className="lazu-expl-op-method" data-method={o.method}>
              {o.method}
            </span>
            <span className="lazu-expl-op-label">{o.label}</span>
          </button>
        ))}
      </div>

      <div className="lazu-expl-panel">
        <div className="lazu-expl-reqrow">
          <span className="lazu-method-badge" data-method={op.method}>
            {op.method}
          </span>
          <code className="lazu-expl-path">{op.path}</code>
          <button
            type="button"
            className="lazu-expl-send"
            onClick={send}
            disabled={sending}
          >
            {sending ? "Sending…" : result ? "Resend" : "Send"}
          </button>
        </div>

        <div className="lazu-expl-field">
          <label className="lazu-expl-flabel" htmlFor="lazu-expl-key">
            Authorization
          </label>
          <input
            id="lazu-expl-key"
            type="password"
            className="lazu-expl-input"
            placeholder="Bearer token — paste your Lazu API key"
            value={apiKey}
            autoComplete="off"
            onChange={(e) => onKey(e.target.value)}
          />
          <label className="lazu-expl-remember">
            <input
              type="checkbox"
              checked={remember}
              onChange={(e) => toggleRemember(e.target.checked)}
            />
            Remember in this browser
          </label>
        </div>

        {op.method === "POST" ? (
          <div className="lazu-expl-field">
            <label className="lazu-expl-flabel" htmlFor="lazu-expl-body">
              Request body
            </label>
            <textarea
              id="lazu-expl-body"
              className="lazu-expl-body"
              spellCheck={false}
              value={body}
              onChange={(e) => {
                dirty.current = true;
                setBody(e.target.value);
              }}
            />
          </div>
        ) : null}

        <div className="lazu-expl-resphead">
          <span className="lazu-expl-flabel">Response</span>
          {result ? (
            <span
              className="lazu-expl-status"
              data-ok={result.ok}
              data-err={result.status === 0}
            >
              {result.status === 0
                ? "Network error"
                : `${result.status} · ${result.ms}ms`}
            </span>
          ) : (
            <span className="lazu-expl-status" data-idle="true">
              —
            </span>
          )}
        </div>
        <pre className="lazu-expl-resp">
          {result ? result.body : "// Press Send to run a live request"}
        </pre>
      </div>
    </div>
  );
}
