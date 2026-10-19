import type { Plugin } from "vite";

// Vocs 1.4 mounts desktop and mobile search even when closed. Patch the
// integration (with upgrade guards), leaving its dialog and keyboard UI intact.
export function searchPerformance(): Plugin {
  return {
    name: "lazu-search-on-intent",
    enforce: "pre",
    transform(code, id) {
      if (!/[\\/]vocs[\\/]_lib[\\/]app[\\/]/.test(id)) return;
      const replace = (from: string, to: string) => {
        if (!code.includes(from))
          throw new Error(
            `[docs] search performance patch needs updating: ${id}`,
          );
        code = code.replace(from, to);
      };
      if (id.endsWith("/hooks/useSearchIndex.js")) {
        if (!code.includes("MiniSearch.loadJSON"))
          throw new Error("[docs] search hook changed upstream");
        return `
import { getSearchIndex } from 'virtual:searchIndex';
import { useEffect, useState } from 'react';
let cached;
let pending;
export function warmSearchIndex() {
  if (cached) return Promise.resolve(cached);
  if (!pending) pending = Promise.all([getSearchIndex(), import('minisearch')])
    .then(([json, { default: MiniSearch }]) => {
      cached = MiniSearch.loadJSON(json, {
        fields: ['title', 'titles', 'text'],
        searchOptions: { boost: { title: 4, text: 2, titles: 1 }, fuzzy: 0.2, prefix: true },
        storeFields: ['href', 'html', 'isPage', 'text', 'title', 'titles'],
      });
      return cached;
    }).finally(() => { pending = undefined; });
  return pending;
}
export function useSearchIndex(enabled = true) {
  const [index, setIndex] = useState(cached);
  const [error, setError] = useState(false);
  const [visible, setVisible] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setError(false);
    setVisible(false);
    const timer = setTimeout(() => { if (!cancelled) setVisible(true); }, 300);
    warmSearchIndex().then(value => { if (!cancelled) setIndex(value); })
      .catch(() => { if (!cancelled) setError(true); });
    return () => { cancelled = true; clearTimeout(timer); };
  }, [enabled, attempt]);
  return { index, error, visible, retry: () => setAttempt(value => value + 1) };
}`;
      }
      if (id.endsWith("/components/DesktopSearch.js")) {
        replace("import { useSearchIndex }", "import { warmSearchIndex }");
        replace("    useSearchIndex();", "");
        replace(
          "className: styles.search, type:",
          "onPointerEnter: () => { void warmSearchIndex().catch(() => {}); }, onFocus: () => { void warmSearchIndex().catch(() => {}); }, className: styles.search, type:",
        );
        return code;
      }
      if (id.endsWith("/components/SearchDialog.js")) {
        replace(
          "const searchIndex = useSearchIndex();",
          "const { index: searchIndex, error: searchError, visible: searchWaitVisible, retry: retrySearch } = useSearchIndex(props.open);",
        );
        replace(
          "children: [searchTerm && results.length === 0 &&",
          `children: [!searchIndex && searchWaitVisible && _jsx("li", { role: "status", children: searchError ? _jsx("button", { type: "button", onClick: retrySearch, children: "Search could not load. Retry" }) : "Loading search…" }), searchIndex && searchTerm && results.length === 0 &&`,
        );
        return code;
      }
    },
  };
}
