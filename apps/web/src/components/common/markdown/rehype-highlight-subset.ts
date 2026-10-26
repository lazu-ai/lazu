/* Code highlighting for the console's Markdown with a fixed set of
   languages. rehype-highlight registers lowlight's 37 "common" grammars even
   when given a subset, so this is its transform (MIT, rehypejs/rehype-
   highlight 7.0.2) over createLowlight with only the languages announcements
   and docs use. A fenced block in any other language is left unhighlighted. */
import bash from 'highlight.js/lib/languages/bash';
import http from 'highlight.js/lib/languages/http';
import javascript from 'highlight.js/lib/languages/javascript';
import json from 'highlight.js/lib/languages/json';
import python from 'highlight.js/lib/languages/python';
import typescript from 'highlight.js/lib/languages/typescript';
import yaml from 'highlight.js/lib/languages/yaml';
import { createLowlight } from 'lowlight';
import type { Element, ElementContent, Root, RootContent } from 'hast';

export const HIGHLIGHT_LANGUAGES = {
  bash,
  http,
  javascript,
  json,
  python,
  typescript,
  yaml,
};

const lowlight = createLowlight(HIGHLIGHT_LANGUAGES);
lowlight.registerAlias({
  bash: ['sh', 'shell', 'zsh', 'console'],
  javascript: ['js', 'jsx', 'mjs', 'cjs'],
  json: ['jsonc', 'json5'],
  python: ['py'],
  typescript: ['ts', 'tsx'],
  yaml: ['yml'],
});

function languageOf(node: Element): string | false | undefined {
  const list = node.properties?.className;
  if (!Array.isArray(list)) return undefined;
  let name: string | undefined;
  for (const item of list) {
    const value = String(item);
    if (value === 'no-highlight' || value === 'nohighlight') return false;
    if (!name && value.startsWith('lang-')) name = value.slice(5);
    if (!name && value.startsWith('language-')) name = value.slice(9);
  }
  return name;
}

function textOf(node: RootContent | Root): string {
  if (node.type === 'text') return node.value;
  if ('children' in node) return node.children.map(textOf).join('');
  return '';
}

function highlight(node: Element) {
  for (const child of node.children) {
    if (child.type !== 'element') continue;
    if (child.tagName === 'code' && node.tagName === 'pre') {
      const lang = languageOf(child);
      if (!lang || !lowlight.registered(lang)) continue;
      const classes = Array.isArray(child.properties.className)
        ? child.properties.className
        : [];
      if (!classes.includes('hljs')) classes.unshift('hljs');
      child.properties.className = classes;
      const result = lowlight.highlight(lang, textOf(child));
      if (result.children.length > 0) {
        child.children = result.children as ElementContent[];
      }
      continue;
    }
    highlight(child);
  }
}

export default function rehypeHighlightSubset() {
  return (tree: Root) => {
    for (const child of tree.children) {
      if (child.type === 'element') highlight(child);
    }
  };
}
