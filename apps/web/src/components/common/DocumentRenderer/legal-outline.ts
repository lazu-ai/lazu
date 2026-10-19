/** Chapter outline for a legal document.
 *
 *  The backend serves these as Markdown (internal/setting/system_setting/
 *  legal_defaults/*.md) with no structured metadata, so the reading layout
 *  derives what it needs — the chapter list and the "last updated" line — from
 *  the text itself. Admin overrides may be HTML instead; the same shapes are
 *  read out of headings there.
 */

import { fromMarkdown } from 'mdast-util-from-markdown';
import { visit } from 'unist-util-visit';

export type LegalChapter = {
  /** Slug used for the anchor id. Stable across renders of the same text. */
  id: string;
  /** The leading enumerator when the heading carries one ("一", "2"). */
  number: string;
  /** Heading text with the enumerator removed. */
  title: string;
  line?: number;
};

/** "一、我们处理的信息" → { number: "一", title: "我们处理的信息" }
 *  "2. Information we collect" → { number: "2", title: "Information we collect" }
 *  Anything else keeps its whole text and gets no enumerator. */
export const splitChapterHeading = (heading: string) => {
  const text = heading.trim();
  const cjk = /^([一二三四五六七八九十百]+)\s*[、.．]\s*(.+)$/.exec(text);
  if (cjk) return { number: cjk[1], title: cjk[2] };
  const arabic = /^(\d+)\s*[、.．)]\s*(.+)$/.exec(text);
  if (arabic) return { number: arabic[1], title: arabic[2] };
  return { number: '', title: text };
};

export const chapterId = (index: number, title: string) => {
  const slug = title
    .toLowerCase()
    .replace(/[^\p{Letter}\p{Number}]+/gu, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 48);
  return `legal-${index + 1}${slug ? `-${slug}` : ''}`;
};

type TextNode = {
  type: string;
  value?: string;
  alt?: string | null;
  children?: TextNode[];
};

const nodeText = (node: TextNode): string => {
  if (node.type === 'html') return '';
  return node.value ?? node.alt ?? node.children?.map(nodeText).join('') ?? '';
};

/** A heading may carry inline marks — "## 1. **Eligibility**", a link, code.
 *  The outline stores the words a reader sees, because the rendered heading
 *  is matched back to this entry by its text. Only paired emphasis is
 *  unwrapped, so an underscore inside a word (`user_id`) survives. */
export const plainHeadingText = (heading: string) =>
  nodeText(
    fromMarkdown(
      `## ${heading.replace(/<[^>]+>/g, '').replace(/\r?\n/g, ' ')}`,
    ),
  ).trim();

export const outlineOf = (content: string): LegalChapter[] => {
  const body = bodyWithoutHeadMatter(content);
  const headings: Array<{ text: string; line?: number }> = [];
  visit(fromMarkdown(body), 'heading', (node) => {
    if (node.depth === 2)
      headings.push({ text: nodeText(node), line: node.position?.start.line });
  });
  if (!headings.length) {
    for (const match of body.matchAll(/<h2[^>]*>([\s\S]*?)<\/h2>/gi))
      headings.push({ text: plainHeadingText(match[1]) });
  }
  return headings.map(({ text, line }, index) => {
    const { number, title } = splitChapterHeading(text);
    return { id: chapterId(index, title), number, title, line };
  });
};

/** The labels each bundled document uses for its published date. Japanese
 *  says 最終更新日 rather than 更新日期, and a locale missing from this list
 *  silently loses its header date, so every shipped translation belongs here.
 *  Longest first: 最近更新日期 must win before 更新日期 can match inside it. */
const UPDATED_LABELS =
  '最近更新日期|最后更新日期|最終更新日期|最終更新日|更新日期|Last updated|Last Updated';

/** The published date lives in a "最近更新日期：2026 年 8 月 27 日" line (or an
 *  English "Last updated: …"). It is prose, not a field, so a document without
 *  one simply shows no date rather than a fabricated one. */
export const updatedDateOf = (content: string) => {
  const match = new RegExp(
    `(?:${UPDATED_LABELS})\\s*[:：]?\\s*([^\\n<]{3,40})`,
  ).exec(content);
  return match ? match[1].trim().replace(/[。.]$/, '') : '';
};

/** The page head already carries the document title and its "last updated"
 *  line, so the body drops both rather than printing them twice. */
export const bodyWithoutHeadMatter = (content: string) =>
  content
    .replace(/^\s*#\s+.+$/m, '')
    .replace(new RegExp(`^\\s*(?:${UPDATED_LABELS})\\s*[:：][^\\n]*$`, 'm'), '')
    .replace(/^<h1[^>]*>[\s\S]*?<\/h1>/i, '')
    .replace(
      new RegExp(
        `<p\\b[^>]*>\\s*(?:${UPDATED_LABELS})\\s*[:：][^<]*<\\/p>`,
        'i',
      ),
      '',
    )
    .trimStart();
