// MDX → plain-Markdown serializer for the docs pages.
//
// Two consumers, one output contract:
//   1. sync-openapi.ts emits a per-route `.md` next to every page (the
//      "复制为 Markdown" button and agent deep-links fetch these), and
//   2. the same pass feeds llms-full.txt — which previously interpolated
//      the raw MDX source, so every endpoint page shipped as unrendered
//      <EndpointPage>/<Field> component soup to exactly the readers the
//      file exists for.
//
// The serializer parses real MDX (remark-mdx) and lowers each docs
// component to the Markdown it visually stands for; unknown components
// unwrap to their children rather than leaking tags. Component prop
// expressions (fields={[...]}, values={[...]}, sample template literals)
// are evaluated — they are build-time literals authored in this repo,
// not user input.
import remarkGfm from "remark-gfm";
import remarkMdx from "remark-mdx";
import remarkParse from "remark-parse";
import remarkStringify from "remark-stringify";
import { unified } from "unified";

import { type HomeLocale, homeCopy } from "../components/docs-home-data";
import { ENDPOINT_SAMPLES } from "../components/endpoint-samples";

type Locale = "en" | "zh" | "zh-TW" | "ja";

type Node = {
  attributes?: JsxAttribute[];
  children?: Node[];
  lang?: string | null;
  name?: string | null;
  type: string;
  value?: string;
};

type JsxAttribute = {
  name?: string;
  type: string;
  value?: string | { type: string; value: string } | null;
};

type FieldChild = { description?: unknown; name?: string; type?: string };

const L10N: Record<
  Locale,
  {
    exampleRequest: string;
    exampleResponse: string;
    optional: string;
    required: string;
    values: string;
  }
> = {
  en: {
    exampleRequest: "Example request",
    exampleResponse: "Example response",
    optional: "optional",
    required: "required",
    values: "One of",
  },
  zh: {
    exampleRequest: "请求示例",
    exampleResponse: "响应示例",
    optional: "可选",
    required: "必填",
    values: "取值",
  },
  "zh-TW": {
    exampleRequest: "請求範例",
    exampleResponse: "回應範例",
    optional: "可選",
    required: "必填",
    values: "取值",
  },
  ja: {
    exampleRequest: "リクエスト例",
    exampleResponse: "レスポンス例",
    optional: "任意",
    required: "必須",
    values: "指定可能な値",
  },
};

const parser = unified().use(remarkParse).use(remarkMdx).use(remarkGfm);
const printer = unified()
  .use(remarkStringify, {
    bullet: "-",
    fences: true,
    rule: "-",
  })
  .use(remarkGfm);

// --- small mdast constructors ------------------------------------------------

const text = (value: string): Node => ({ type: "text", value });
const inlineCode = (value: string): Node => ({ type: "inlineCode", value });
const strong = (children: Node[]): Node => ({ type: "strong", children });
const paragraph = (children: Node[]): Node => ({ type: "paragraph", children });
const heading = (depth: number, children: Node[]): Node =>
  ({ type: "heading", depth, children }) as unknown as Node;
const code = (value: string, lang?: string): Node => ({
  type: "code",
  lang: lang || null,
  value,
});
const listItem = (children: Node[]): Node => ({
  type: "listItem",
  children,
});
const list = (children: Node[]): Node =>
  ({
    type: "list",
    ordered: false,
    spread: false,
    children,
  }) as unknown as Node;

// --- attribute helpers -------------------------------------------------------

function attr(node: Node, name: string): string | undefined {
  const found = node.attributes?.find(
    (a) => a.type === "mdxJsxAttribute" && a.name === name,
  );
  if (!found) return undefined;
  if (found.value === null || found.value === undefined) return "true";
  if (typeof found.value === "string") return found.value;
  return found.value.value;
}

function hasAttr(node: Node, name: string): boolean {
  return Boolean(
    node.attributes?.some(
      (a) => a.type === "mdxJsxAttribute" && a.name === name,
    ),
  );
}

// Evaluate a build-time literal prop expression (arrays / template strings
// authored in the MDX files of this repo).
function evalLiteral(source: string | undefined): unknown {
  if (!source) return undefined;
  try {
    return new Function(`return (${source})`)();
  } catch {
    return undefined;
  }
}

// --- transform ---------------------------------------------------------------

function inlineChildren(nodes: Node[] | undefined): Node[] {
  const out: Node[] = [];
  for (const child of nodes || []) {
    if (child.type === "mdxJsxTextElement") {
      if (child.name === "code") {
        out.push(inlineCode(plainText(child.children)));
      } else {
        out.push(...inlineChildren(child.children));
      }
      continue;
    }
    if (child.type === "mdxTextExpression") {
      const value = evalLiteral(child.value);
      out.push(text(typeof value === "string" ? value : child.value || ""));
      continue;
    }
    if (child.children) {
      out.push({ ...child, children: inlineChildren(child.children) });
      continue;
    }
    out.push(child);
  }
  return out;
}

function plainText(nodes: Node[] | undefined): string {
  return (nodes || [])
    .map((child) => {
      if (child.type === "text" || child.type === "inlineCode") {
        return child.value || "";
      }
      if (child.type === "mdxTextExpression") {
        const value = evalLiteral(child.value);
        return typeof value === "string" ? value : "";
      }
      return plainText(child.children);
    })
    .join("");
}

// Flatten a component's flow children into inline content for one paragraph
// (used by tiles/cards whose body is a single sentence).
function inlineFlow(nodes: Node[] | undefined): Node[] {
  const out: Node[] = [];
  for (const child of nodes || []) {
    if (child.type === "paragraph") {
      if (out.length) out.push(text(" "));
      out.push(...inlineChildren(child.children));
    } else {
      out.push(...inlineChildren([child]));
    }
  }
  return out;
}

function fieldItem(node: Node, locale: Locale): Node {
  const t = L10N[locale];
  const head: Node[] = [inlineCode(attr(node, "name") || "")];
  const type = attr(node, "type");
  if (type) head.push(text(" "), inlineCode(type));
  if (hasAttr(node, "required")) head.push(text(` (${t.required})`));
  else if (hasAttr(node, "nullable")) head.push(text(` (${t.optional})`));

  const description = inlineFlow(node.children);
  if (description.length) head.push(text(" — "), ...description);

  const values = evalLiteral(attr(node, "values"));
  if (Array.isArray(values) && values.length) {
    head.push(text(` ${t.values}: `));
    values.forEach((value, i) => {
      if (i) head.push(text(", "));
      head.push(inlineCode(String(value)));
    });
  }

  const children: Node[] = [paragraph(head)];
  const sub = evalLiteral(attr(node, "fields"));
  if (Array.isArray(sub) && sub.length) {
    children.push(
      list(
        (sub as FieldChild[]).map((field) =>
          listItem([
            paragraph([
              inlineCode(field.name || ""),
              ...(field.type ? [text(" "), inlineCode(field.type)] : []),
              ...(typeof field.description === "string"
                ? [text(" — "), text(field.description)]
                : []),
            ]),
          ]),
        ),
      ),
    );
  }
  return listItem(children);
}

function endpointSamples(asideSource: string, locale: Locale): Node[] {
  const key = asideSource.match(/endpoint="([^"]+)"/)?.[1];
  const rail = key ? ENDPOINT_SAMPLES[key] : undefined;
  if (!rail) return [];
  const t = L10N[locale];
  const out: Node[] = [
    heading(2, [text(t.exampleRequest)]),
    code(rail.samples.curl, "bash"),
  ];
  if (rail.response) {
    out.push(
      heading(2, [text(t.exampleResponse)]),
      code(rail.response, "json"),
    );
  }
  return out;
}

function transform(node: Node, locale: Locale): Node[] {
  if (node.type === "mdxjsEsm") return [];
  if (node.type === "mdxFlowExpression") return [];

  if (node.type === "mdxJsxFlowElement" || node.type === "mdxJsxTextElement") {
    switch (node.name) {
      case "EndpointPage": {
        const out: Node[] = [heading(1, [text(attr(node, "title") || "")])];
        const method = attr(node, "method");
        const path = attr(node, "path");
        if (method && path) {
          out.push(
            paragraph([strong([text(method)]), text(" "), inlineCode(path)]),
          );
        }
        const description = attr(node, "description");
        if (description) out.push(paragraph([text(description)]));
        const asideAttr = node.attributes?.find(
          (a) => a.type === "mdxJsxAttribute" && a.name === "aside",
        );
        const asideSource =
          asideAttr && typeof asideAttr.value === "object" && asideAttr.value
            ? asideAttr.value.value
            : "";
        out.push(...endpointSamples(asideSource || "", locale));
        out.push(...transformAll(node.children, locale));
        return out;
      }
      case "FieldList":
        return [
          list(
            (node.children || [])
              .filter((child) => child.name === "Field")
              .map((child) => fieldItem(child, locale)),
          ),
        ];
      case "Field":
        return [list([fieldItem(node, locale)])];
      case "CodeSample": {
        const source = (node.children || []).find(
          (child) => child.type === "mdxFlowExpression",
        )?.value;
        const value = evalLiteral(source);
        const title = attr(node, "title");
        const out: Node[] = [];
        if (title) out.push(paragraph([strong([text(title)])]));
        if (typeof value === "string") {
          out.push(code(value, attr(node, "language") || "text"));
        }
        return out;
      }
      case "EndpointGrid":
      case "Cards":
        return [
          list(
            (node.children || [])
              .filter((child) => child.type === "mdxJsxFlowElement")
              .flatMap((child) => {
                const title = attr(child, "title") || "";
                const href = attr(child, "href");
                const description = attr(child, "description");
                const body = inlineFlow(child.children);
                const head: Node[] = href
                  ? [
                      {
                        type: "link",
                        url: href,
                        children: [strong([text(title)])],
                      } as unknown as Node,
                    ]
                  : [strong([text(title)])];
                const tail: Node[] = description
                  ? [text(" — "), text(description)]
                  : body.length
                    ? [text(" — "), ...body]
                    : [];
                return [listItem([paragraph([...head, ...tail])])];
              }),
          ),
        ];
      case "MiniCard": {
        const title = attr(node, "title");
        const out: Node[] = [];
        if (title) out.push(paragraph([strong([text(title)])]));
        out.push(...transformAll(node.children, locale));
        return out;
      }
      case "Callout":
        return [
          {
            type: "blockquote",
            children: transformAll(node.children, locale),
          },
        ];
      case "ApiExplorer":
        return [
          paragraph([
            text("OpenAPI: "),
            inlineCode("/openapi.json"),
            text(" · "),
            inlineCode("/openapi.yaml"),
          ]),
        ];
      case "DocsHome": {
        const home = homeCopy((attr(node, "locale") as HomeLocale) || "en");
        const link = (href: string, label: string): Node =>
          ({
            type: "link",
            url: href,
            children: [text(label)],
          }) as unknown as Node;
        return [
          heading(1, [text(home.title)]),
          paragraph([text(home.lede)]),
          paragraph([
            strong([text("Base URL")]),
            text(": "),
            inlineCode("https://api.lazu.ai/v1"),
            text(" · "),
            strong([text("Auth")]),
            text(": "),
            inlineCode("Authorization: Bearer $LAZU_API_KEY"),
            text(" · "),
            strong([text("Index")]),
            text(": "),
            inlineCode("https://lazu.ai/docs/llms.txt"),
          ]),
          ...home.columns.flatMap((column) => [
            heading(2, [text(column.title)]),
            list(
              column.links.map((item) =>
                listItem([paragraph([link(item.href, item.label)])]),
              ),
            ),
          ]),
          heading(2, [text(home.benchLabel)]),
          paragraph([text(home.benchTitle.replace("\n", " "))]),
          code(ENDPOINT_SAMPLES.chat.samples.python, "python"),
          heading(2, [text(home.agentTitle)]),
          paragraph([link("https://lazu.ai/docs/llms.txt", "llms.txt")]),
        ];
      }
      case "EndpointRail":
      case "EndpointAside":
        return []; // interactive-only chrome; samples come via EndpointPage
      default:
        // Unknown component: unwrap so nothing leaks as a raw tag.
        return transformAll(node.children, locale);
    }
  }

  if (node.children) {
    if (node.type === "paragraph" || node.type === "tableCell") {
      return [{ ...node, children: inlineChildren(node.children) }];
    }
    return [{ ...node, children: transformAll(node.children, locale) }];
  }
  return [node];
}

function transformAll(nodes: Node[] | undefined, locale: Locale): Node[] {
  return (nodes || []).flatMap((node) => transform(node, locale));
}

// Docs pages link each other root-relative ("/errors"). In the site that
// resolves under the /docs base; in a Markdown copy read by an agent it would
// resolve against lazu.ai's root and land on the web app. Make them absolute.
function absolutizeLinks(nodes: Node[] | undefined, siteOrigin: string) {
  for (const node of nodes || []) {
    const linked = node as Node & { url?: unknown };
    if (
      (node.type === "link" || node.type === "definition") &&
      typeof linked.url === "string" &&
      linked.url.startsWith("/") &&
      !linked.url.startsWith("//")
    ) {
      const [path, hash] = linked.url.split("#", 2);
      linked.url = `${path === "/" ? siteOrigin : `${siteOrigin}${path}`}${
        hash === undefined ? "" : `#${hash}`
      }`;
    }
    absolutizeLinks(node.children, siteOrigin);
  }
}

/** Render one MDX page body (frontmatter already stripped) to Markdown. */
export function mdxToMarkdown(
  body: string,
  locale: Locale = "en",
  siteOrigin = "https://lazu.ai/docs",
): string {
  const tree = parser.parse(body) as unknown as Node;
  const transformed = {
    ...tree,
    children: transformAll(tree.children, locale),
  };
  absolutizeLinks(transformed.children, siteOrigin.replace(/\/+$/, ""));
  return String(printer.stringify(transformed as never))
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}
