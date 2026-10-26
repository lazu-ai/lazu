import { describe, expect, it } from 'vitest';
import rehypeHighlightSubset from './rehype-highlight-subset';
import type { Element, Root } from 'hast';

const block = (className: string[] | undefined, text: string): Root => ({
  type: 'root',
  children: [
    {
      type: 'element',
      tagName: 'pre',
      properties: {},
      children: [
        {
          type: 'element',
          tagName: 'code',
          properties: className ? { className } : {},
          children: [{ type: 'text', value: text }],
        },
      ],
    },
  ],
});

const code = (tree: Root) =>
  (tree.children[0] as Element).children[0] as Element;

const run = (tree: Root) => {
  rehypeHighlightSubset()(tree);
  return code(tree);
};

describe('rehypeHighlightSubset', () => {
  it('highlights a language in the subset', () => {
    const node = run(block(['language-json'], '{"a": 1}'));
    expect(node.properties.className).toContain('hljs');
    expect(JSON.stringify(node.children)).toContain('hljs-attr');
  });

  it('resolves aliases', () => {
    for (const lang of ['ts', 'sh', 'yml', 'js', 'py']) {
      const node = run(block([`language-${lang}`], 'x = 1'));
      expect(node.properties.className).toContain('hljs');
    }
  });

  it('leaves other languages, plain blocks and no-highlight alone', () => {
    for (const className of [
      ['language-rust'],
      undefined,
      ['language-json', 'no-highlight'],
    ]) {
      const tree = block(className, 'fn main() {}');
      const node = run(tree);
      expect(node.properties.className ?? []).not.toContain('hljs');
      expect(node.children).toEqual([{ type: 'text', value: 'fn main() {}' }]);
    }
  });

  it('ignores inline code outside a pre', () => {
    const tree: Root = {
      type: 'root',
      children: [
        {
          type: 'element',
          tagName: 'p',
          properties: {},
          children: [
            {
              type: 'element',
              tagName: 'code',
              properties: { className: ['language-json'] },
              children: [{ type: 'text', value: '{}' }],
            },
          ],
        },
      ],
    };
    rehypeHighlightSubset()(tree);
    const inline = (tree.children[0] as Element).children[0] as Element;
    expect(inline.properties.className).toEqual(['language-json']);
  });
});
