/* The docs 404, in the same family as the web app's status pages: a large
 * serif 404, one title, one sentence, the address that missed, two ways out
 * and — only when a real page is one or two keystrokes away — a "Did you
 * mean …?" line.
 *
 * Vocs renders its own NotFound for the catch-all route; the
 * lazu-docs-not-found Vite plugin (scripts/not-found-plugin.ts) swaps this
 * component in, so it sits inside the normal docs chrome. */
import { Link, useLocation } from "react-router";
import { withBase } from "./base-path";
import { createSidebars, type DocsLocale } from "./docs-nav";

const COPY: Record<
  DocsLocale,
  {
    description: string;
    home: string;
    reference: string;
    suggestion: [string, string];
    title: string;
  }
> = {
  en: {
    title: "Page not found",
    description:
      "This page doesn't exist in the docs. The link may be mistyped, or the page has moved.",
    home: "Docs home",
    reference: "API reference",
    suggestion: ["Did you mean ", "?"],
  },
  zh: {
    title: "文档页不存在",
    description: "文档里没有这个页面，可能是链接写错了，或者页面已经移走。",
    home: "文档首页",
    reference: "API 参考",
    suggestion: ["要找的是不是「", "」？"],
  },
  "zh-TW": {
    title: "文件頁不存在",
    description: "文件裡沒有這個頁面，可能是連結寫錯了，或者頁面已經移走。",
    home: "文件首頁",
    reference: "API 參考",
    suggestion: ["要找的是不是「", "」？"],
  },
  ja: {
    title: "ページが見つかりません",
    description:
      "ドキュメントにこのページはありません。リンクの誤りか、ページが移動した可能性があります。",
    home: "ドキュメントのホーム",
    reference: "API リファレンス",
    suggestion: ["お探しのページは「", "」ですか？"],
  },
};

const PREFIX: Record<DocsLocale, string> = {
  en: "",
  zh: "/zh",
  "zh-TW": "/zh-TW",
  ja: "/ja",
};

function localeOf(pathname: string): DocsLocale {
  if (/^\/zh-TW(\/|$)/i.test(pathname)) return "zh-TW";
  if (/^\/zh(\/|$)/i.test(pathname)) return "zh";
  if (/^\/ja(\/|$)/i.test(pathname)) return "ja";
  return "en";
}

const MAX_DISTANCE = 2;

function distance(a: string, b: string): number {
  if (Math.abs(a.length - b.length) > MAX_DISTANCE) return MAX_DISTANCE + 1;
  let previous = Array.from({ length: b.length + 1 }, (_, index) => index);
  for (let i = 1; i <= a.length; i += 1) {
    const current = [i];
    for (let j = 1; j <= b.length; j += 1) {
      current[j] = Math.min(
        previous[j] + 1,
        current[j - 1] + 1,
        previous[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1),
      );
    }
    previous = current;
  }
  return previous[b.length];
}

type Page = { link: string; text: string };

export function suggestDocsPage(pathname: string): Page | null {
  const locale = localeOf(pathname);
  const key = locale === "en" ? "/" : `${PREFIX[locale]}/`;
  const sidebars = createSidebars() as Record<string, Array<{ items: Page[] }>>;
  const pages = sidebars[key].flatMap((group) => group.items);
  const path = pathname.toLowerCase().replace(/\/+$/, "") || "/";
  let best: Page | null = null;
  let bestDistance = MAX_DISTANCE + 1;
  for (const page of pages) {
    const d = distance(path, page.link.toLowerCase());
    if (d < bestDistance) {
      best = page;
      bestDistance = d;
    }
  }
  if (best) return best;
  // An address that runs past a real page into a sub-path that does not
  // exist points at the page it ran past. Locale roots say nothing.
  return (
    pages
      .filter(
        (page) =>
          page.link !== key.replace(/\/$/, "") &&
          page.link !== "/" &&
          path.startsWith(`${page.link.toLowerCase()}/`),
      )
      .sort((a, b) => b.link.length - a.link.length)[0] ?? null
  );
}

export function NotFound() {
  const { pathname, search } = useLocation();
  const locale = localeOf(pathname);
  const copy = COPY[locale];
  const suggestion = suggestDocsPage(pathname);
  return (
    <div className="docs-404">
      <section className="docs-404__body">
        <div aria-hidden="true" className="docs-404__mark">
          404
        </div>
        <h1 className="docs-404__title">{copy.title}</h1>
        <p className="docs-404__description">{copy.description}</p>
        <div className="docs-404__pill">
          {`GET ${withBase(pathname)}${search}`}
        </div>
        <div className="docs-404__actions">
          <Link
            className="docs-404__btn docs-404__btn--primary"
            to={PREFIX[locale] || "/"}
          >
            {copy.home}
          </Link>
          <Link
            className="docs-404__btn"
            to={`${PREFIX[locale]}/api-reference`}
          >
            {copy.reference}
          </Link>
        </div>
        {suggestion ? (
          <div className="docs-404__hint">
            {copy.suggestion[0]}
            <Link to={suggestion.link}>{suggestion.text}</Link>
            {copy.suggestion[1]}
          </div>
        ) : null}
      </section>
    </div>
  );
}
