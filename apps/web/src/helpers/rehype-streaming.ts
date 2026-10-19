import { visit } from 'unist-util-visit';
import type { Element, ElementContent, Root, RootContent, Text } from 'hast';

export interface StreamingWordSplitOptions {
  previousContentLength?: number;
}

const ANIMATED_TAGS = new Set([
  'p',
  'h1',
  'h2',
  'h3',
  'h4',
  'h5',
  'h6',
  'li',
  'strong',
]);

const isTextNode = (node: RootContent): node is Text => node.type === 'text';

/**
 * Rehype plugin used by the streaming Markdown view. Existing text is kept
 * stable while newly appended words receive the existing fade-in class.
 */
export function rehypeSplitWordsIntoSpans(
  options: StreamingWordSplitOptions = {},
) {
  const { previousContentLength = 0 } = options;

  return (tree: Root): void => {
    let currentCharCount = 0;

    visit(tree, 'element', (node: Element) => {
      if (!ANIMATED_TAGS.has(node.tagName) || !node.children) return;

      const newChildren: ElementContent[] = [];
      node.children.forEach((child) => {
        if (!isTextNode(child)) {
          newChildren.push(child);
          return;
        }

        try {
          const segmenter = new Intl.Segmenter('zh', { granularity: 'word' });
          for (const segment of segmenter.segment(child.value)) {
            const word = segment.segment;
            if (!word) continue;

            const isNewContent = currentCharCount >= previousContentLength;
            newChildren.push({
              type: 'element',
              tagName: 'span',
              properties: {
                className: isNewContent ? ['animate-fade-in'] : [],
              },
              children: [{ type: 'text', value: word }],
            });
            currentCharCount += word.length;
          }
        } catch {
          const isNewContent = currentCharCount >= previousContentLength;
          if (isNewContent) {
            newChildren.push({
              type: 'element',
              tagName: 'span',
              properties: { className: ['animate-fade-in'] },
              children: [{ type: 'text', value: child.value }],
            });
          } else {
            newChildren.push(child);
          }
          currentCharCount += child.value.length;
        }
      });
      node.children = newChildren;
    });
  };
}
