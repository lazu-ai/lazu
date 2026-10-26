import clsx from 'clsx';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import ReactMarkdown, {
  type Options as ReactMarkdownOptions,
} from 'react-markdown';
import RemarkBreaks from 'remark-breaks';
import RemarkGfm from 'remark-gfm';
import { useDebouncedCallback } from 'use-debounce';
import { hasMath } from './has-math';
import RehypeHighlightSubset from './rehype-highlight-subset';
import { copy } from '../../../helpers/clipboard';
import { rehypeSplitWordsIntoSpans } from '../../../helpers/rehype-streaming';
import { sanitizeMarkup } from '../../../helpers/sanitize';
import { Button, Tooltip, Toast } from '@/components/ui/console-primitives';
import { IconCopy } from '@/components/ui/icon-aliases';
import 'highlight.js/styles/github.css';
import 'katex/dist/katex.min.css';
import './markdown.css';

type MermaidRuntime = (typeof import('mermaid'))['default'];

/* Formulas are rare, so remark-math and rehype-katex (KaTeX itself, ~77 KiB
   gzip) load only for content that may hold one (hasMath: a `$` or a ```math
   fence); until they arrive it renders as text. The stylesheet stays static: it is small, and a dynamic CSS import
   would pull the entry stylesheet into this chunk's CSS. */
type MathPlugins = {
  remark: NonNullable<ReactMarkdownOptions['remarkPlugins']>[number];
  rehype: NonNullable<ReactMarkdownOptions['rehypePlugins']>[number];
};
let mathPlugins: MathPlugins | null = null;
let mathPluginsPromise: Promise<MathPlugins> | null = null;
function loadMathPlugins(): Promise<MathPlugins> {
  mathPluginsPromise ??= Promise.all([
    import('remark-math'),
    import('rehype-katex'),
  ]).then(([remark, rehype]) => {
    mathPlugins = { remark: remark.default, rehype: rehype.default };
    return mathPlugins;
  });
  return mathPluginsPromise;
}
function useMathPlugins(content: string): MathPlugins | null {
  const wanted = hasMath(content);
  const [loaded, setLoaded] = useState(mathPlugins);
  useEffect(() => {
    if (!wanted || loaded) return;
    let active = true;
    void loadMathPlugins().then((plugins) => {
      if (active) setLoaded(plugins);
    });
    return () => {
      active = false;
    };
  }, [wanted, loaded]);
  return wanted ? loaded : null;
}

const CodeBlockContext = React.createContext(false);

let mermaidModulePromise: Promise<MermaidRuntime> | null = null;

const loadMermaid = async () => {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid')
      .then((module) => {
        const mermaid = module.default;
        mermaid.initialize({
          startOnLoad: false,
          theme: 'default',
          securityLevel: 'loose',
        });
        return mermaid;
      })
      .catch((error: unknown) => {
        mermaidModulePromise = null;
        throw error;
      });
  }
  return mermaidModulePromise;
};

export function Mermaid({ code }: { code: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [hasError, setHasError] = useState(false);

  useEffect(() => {
    let cancelled = false;

    if (code && ref.current) {
      setHasError(false);
      loadMermaid()
        .then((mermaid) => {
          if (cancelled || !ref.current) {
            return;
          }
          return mermaid.run({
            nodes: [ref.current],
            suppressErrors: true,
          });
        })
        .catch((error: unknown) => {
          if (cancelled) {
            return;
          }
          setHasError(true);
          console.error(
            '[Mermaid] ',
            error instanceof Error ? error.message : error,
          );
        });
    }

    return () => {
      cancelled = true;
    };
  }, [code]);

  function viewSvgInNewWindow() {
    const svg = ref.current?.querySelector('svg');
    if (!svg) return;
    const text = new XMLSerializer().serializeToString(svg);
    const blob = new Blob([text], { type: 'image/svg+xml' });
    const url = URL.createObjectURL(blob);
    window.open(url, '_blank');
  }

  if (hasError) {
    return null;
  }

  return (
    <div
      className={clsx('mermaid-container')}
      style={{
        cursor: 'pointer',
        overflow: 'auto',
        padding: '12px',
        border: '1px solid var(--ui-border)',
        borderRadius: '8px',
        backgroundColor: 'var(--ui-surface-muted)',
        margin: '12px 0',
      }}
      ref={ref}
      onClick={() => viewSvgInNewWindow()}
    >
      {code}
    </div>
  );
}

export function PreCode({ children }: { children?: React.ReactNode }) {
  const ref = useRef<HTMLPreElement>(null);
  const [mermaidCode, setMermaidCode] = useState('');
  const [htmlCode, setHtmlCode] = useState('');
  const { t } = useTranslation();

  const renderArtifacts = useDebouncedCallback(() => {
    if (!ref.current) return;
    const mermaidDom = ref.current.querySelector<HTMLElement>(
      'code.language-mermaid',
    );
    if (mermaidDom) {
      setMermaidCode(mermaidDom.innerText);
    }
    const htmlDom =
      ref.current.querySelector<HTMLElement>('code.language-html');
    const refText = ref.current.querySelector<HTMLElement>('code')?.innerText;
    if (htmlDom) {
      setHtmlCode(htmlDom.innerText);
    } else if (
      refText?.startsWith('<!DOCTYPE') ||
      refText?.startsWith('<svg') ||
      refText?.startsWith('<?xml')
    ) {
      setHtmlCode(refText);
    }
  }, 600);

  // 处理代码块的换行
  useEffect(() => {
    if (ref.current) {
      const codeElements = ref.current.querySelectorAll('code');
      const wrapLanguages = [
        '',
        'md',
        'markdown',
        'text',
        'txt',
        'plaintext',
        'tex',
        'latex',
      ];
      codeElements.forEach((codeElement) => {
        const languageClass = codeElement.className.match(/language-(\w+)/);
        const name = languageClass ? languageClass[1] : '';
        if (wrapLanguages.includes(name)) {
          codeElement.style.whiteSpace = 'pre-wrap';
        }
      });
      setTimeout(renderArtifacts, 1);
    }
  }, []);

  return (
    <CodeBlockContext.Provider value>
      <pre
        ref={ref}
        style={{
          position: 'relative',
          backgroundColor: 'var(--ui-surface-muted)',
          border: '1px solid var(--ui-border)',
          borderRadius: '6px',
          padding: '12px',
          margin: '12px 0',
          overflow: 'auto',
          fontSize: '14px',
          lineHeight: '1.4',
        }}
      >
        <div
          className='copy-code-button'
          style={{
            position: 'absolute',
            top: '8px',
            right: '8px',
            display: 'flex',
            gap: '4px',
            zIndex: 10,
            opacity: 0,
            transition: 'opacity 0.2s ease',
          }}
        >
          <Tooltip content={t('legacy.i18n.copy_code.3b1fde4d23')}>
            <Button
              size='small'
              theme='borderless'
              icon={<IconCopy />}
              onClick={(e: React.MouseEvent<HTMLButtonElement>) => {
                e.preventDefault();
                e.stopPropagation();
                if (ref.current) {
                  const codeElement = ref.current.querySelector('code');
                  const code = codeElement?.textContent ?? '';
                  copy(code).then((success) => {
                    if (success) {
                      Toast.success(
                        t('legacy.i18n.code_copied_to_clipboard.f2755c6008'),
                      );
                    } else {
                      Toast.error(
                        t(
                          'legacy.i18n.copy_failed_please_copy_manually.2b1cae855e',
                        ),
                      );
                    }
                  });
                }
              }}
              style={{
                padding: '4px',
                backgroundColor: 'var(--ui-surface-raised)',
                borderRadius: '4px',
                cursor: 'pointer',
                border: '1px solid var(--ui-border)',
                boxShadow: '0 1px 2px rgba(0, 0, 0, 0.1)',
              }}
            />
          </Tooltip>
        </div>
        {children}
      </pre>
      {mermaidCode.length > 0 && (
        <Mermaid code={mermaidCode} key={mermaidCode} />
      )}
      {htmlCode.length > 0 && (
        <div
          style={{
            border: '1px solid var(--ui-border)',
            borderRadius: '8px',
            padding: '16px',
            margin: '12px 0',
            backgroundColor: 'var(--ui-surface-muted)',
          }}
        >
          <div
            style={{
              marginBottom: '8px',
              fontSize: '12px',
              color: 'var(--ui-text-faint)',
            }}
          >
            {t('component.markdown.htmlPreview')}
          </div>
          <div dangerouslySetInnerHTML={{ __html: sanitizeMarkup(htmlCode) }} />
        </div>
      )}
    </CodeBlockContext.Provider>
  );
}

function CustomCode({
  children,
  className,
}: React.ComponentPropsWithoutRef<'code'>) {
  const ref = useRef<HTMLElement>(null);
  const isInsidePre = React.useContext(CodeBlockContext);
  /* react-markdown gives fenced blocks a `language-*` class and leaves inline
     spans bare. Everything below dresses a block: display:block inside its own
     div, a 400px collapse and a copy button. Applied to a single-backtick span
     it tore the sentence in half, so inline code returns early. */
  const isBlock = isInsidePre || /\blanguage-/.test(className || '');
  const [collapsed, setCollapsed] = useState(true);
  const [showToggle, setShowToggle] = useState(false);
  const { t } = useTranslation();

  useEffect(() => {
    if (ref.current) {
      const codeHeight = ref.current.scrollHeight;
      setShowToggle(codeHeight > 400);
      ref.current.scrollTop = ref.current.scrollHeight;
    }
  }, [children]);

  const toggleCollapsed = () => {
    setCollapsed((value) => !value);
  };

  if (!isBlock) {
    return (
      <code
        className={clsx(className)}
        ref={ref}
        style={{
          background: 'var(--ui-surface-muted)',
          border: '1px solid var(--ui-border)',
          borderRadius: '4px',
          fontFamily: 'var(--ui-font-mono)',
          fontSize: '0.88em',
          padding: '1px 5px',
        }}
      >
        {children}
      </code>
    );
  }

  const renderShowMoreButton = () => {
    if (showToggle && collapsed) {
      return (
        <div
          style={{
            position: 'absolute',
            bottom: '8px',
            right: '8px',
            left: '8px',
            display: 'flex',
            justifyContent: 'center',
          }}
        >
          <Button size='small' onClick={toggleCollapsed} theme='solid'>
            {t('legacy.i18n.show_more.a2205b3878')}
          </Button>
        </div>
      );
    }
    return null;
  };

  return (
    <div style={{ position: 'relative' }}>
      <code
        className={clsx(className)}
        ref={ref}
        style={{
          maxHeight: collapsed ? '400px' : 'none',
          overflowY: 'hidden',
          display: 'block',
          padding: '8px 12px',
          backgroundColor: 'var(--ui-surface-muted)',
          borderRadius: '4px',
          fontSize: '13px',
          lineHeight: '1.4',
        }}
      >
        {children}
      </code>
      {renderShowMoreButton()}
    </div>
  );
}

function escapeBrackets(text: string) {
  const pattern =
    /(```[\s\S]*?```|`.*?`)|\\\[([\s\S]*?[^\\])\\\]|\\\((.*?)\\\)/g;
  return text.replace(
    pattern,
    (match, codeBlock, squareBracket, roundBracket) => {
      if (codeBlock) {
        return codeBlock;
      } else if (squareBracket) {
        return `$$${squareBracket}$$`;
      } else if (roundBracket) {
        return `$${roundBracket}$`;
      }
      return match;
    },
  );
}

function tryWrapHtmlCode(text: string) {
  // 尝试包装HTML代码
  if (text.includes('```')) {
    return text;
  }
  return text
    .replace(
      /([`]*?)(\w*?)([\n\r]*?)(<!DOCTYPE html>)/g,
      (match, quoteStart, _lang, _newLine, doctype) => {
        return !quoteStart ? '\n```html\n' + doctype : match;
      },
    )
    .replace(
      /(<\/body>)([\r\n\s]*?)(<\/html>)([\n\r]*)([`]*)([\n\r]*?)/g,
      (match, bodyEnd, space, htmlEnd, _newLine, quoteEnd) => {
        return !quoteEnd ? bodyEnd + space + htmlEnd + '\n```\n' : match;
      },
    );
}

interface MarkdownContentProps {
  animated?: boolean;
  className?: string;
  content: string;
  previousContentLength?: number;
}

function _MarkdownContent(props: MarkdownContentProps) {
  const {
    content,
    className,
    animated = false,
    previousContentLength = 0,
  } = props;

  const escapedContent = useMemo(() => {
    return tryWrapHtmlCode(escapeBrackets(content));
  }, [content]);

  // 判断是否为用户消息
  const isUserMessage = className && className.includes('user-message');

  const math = useMathPlugins(escapedContent);

  const rehypePluginsBase = useMemo(() => {
    const base: NonNullable<ReactMarkdownOptions['rehypePlugins']> = [
      ...(math ? [math.rehype] : []),
      RehypeHighlightSubset,
    ];
    if (animated) {
      base.push([rehypeSplitWordsIntoSpans, { previousContentLength }]);
    }
    return base;
  }, [animated, math, previousContentLength]);

  return (
    <ReactMarkdown
      remarkPlugins={[...(math ? [math.remark] : []), RemarkGfm, RemarkBreaks]}
      rehypePlugins={rehypePluginsBase}
      components={{
        pre: PreCode,
        code: CustomCode,
        p: (pProps: React.ComponentPropsWithoutRef<'p'>) => (
          <p
            {...pProps}
            dir='auto'
            style={{
              lineHeight: '1.6',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
        a: (aProps: React.ComponentPropsWithoutRef<'a'>) => {
          const href = aProps.href || '';
          if (/\.(aac|mp3|opus|wav)$/.test(href)) {
            return (
              <figure style={{ margin: '12px 0' }}>
                <audio controls src={href} style={{ width: '100%' }}></audio>
              </figure>
            );
          }
          if (/\.(3gp|3g2|webm|ogv|mpeg|mp4|avi)$/.test(href)) {
            return (
              <video
                controls
                style={{ width: '100%', maxWidth: '100%', margin: '12px 0' }}
              >
                <source src={href} />
              </video>
            );
          }
          const isInternal = /^\/#/i.test(href);
          const target = isInternal ? '_self' : (aProps.target ?? '_blank');
          return (
            <a
              {...aProps}
              target={target}
              style={{
                color: isUserMessage ? '#87CEEB' : 'var(--ui-accent)',
                textDecoration: 'none',
              }}
              onMouseEnter={(event) => {
                event.currentTarget.style.textDecoration = 'underline';
              }}
              onMouseLeave={(event) => {
                event.currentTarget.style.textDecoration = 'none';
              }}
            />
          );
        },
        h1: (props: React.ComponentPropsWithoutRef<'h1'>) => (
          <h1
            {...props}
            style={{
              fontSize: '24px',
              fontWeight: 'bold',
              margin: '20px 0 12px 0',
              color: isUserMessage ? 'white' : 'var(--ui-text)',
            }}
          />
        ),
        h2: (props: React.ComponentPropsWithoutRef<'h2'>) => (
          <h2
            {...props}
            style={{
              fontSize: '20px',
              fontWeight: 'bold',
              margin: '18px 0 10px 0',
              color: isUserMessage ? 'white' : 'var(--ui-text)',
            }}
          />
        ),
        h3: (props: React.ComponentPropsWithoutRef<'h3'>) => (
          <h3
            {...props}
            style={{
              fontSize: '18px',
              fontWeight: 'bold',
              margin: '16px 0 8px 0',
              color: isUserMessage ? 'white' : 'var(--ui-text)',
            }}
          />
        ),
        h4: (props: React.ComponentPropsWithoutRef<'h4'>) => (
          <h4
            {...props}
            style={{
              fontSize: '16px',
              fontWeight: 'bold',
              margin: '14px 0 6px 0',
              color: isUserMessage ? 'white' : 'var(--ui-text)',
            }}
          />
        ),
        h5: (props: React.ComponentPropsWithoutRef<'h5'>) => (
          <h5
            {...props}
            style={{
              fontSize: '14px',
              fontWeight: 'bold',
              margin: '12px 0 4px 0',
              color: isUserMessage ? 'white' : 'var(--ui-text)',
            }}
          />
        ),
        h6: (props: React.ComponentPropsWithoutRef<'h6'>) => (
          <h6
            {...props}
            style={{
              fontSize: '13px',
              fontWeight: 'bold',
              margin: '10px 0 4px 0',
              color: isUserMessage ? 'white' : 'var(--ui-text)',
            }}
          />
        ),
        blockquote: (props: React.ComponentPropsWithoutRef<'blockquote'>) => (
          <blockquote
            {...props}
            style={{
              borderLeft: isUserMessage
                ? '4px solid rgba(255, 255, 255, 0.5)'
                : '4px solid var(--ui-accent)',
              paddingLeft: '16px',
              margin: '12px 0',
              backgroundColor: isUserMessage
                ? 'rgba(255, 255, 255, 0.1)'
                : 'var(--ui-surface-muted)',
              padding: '8px 16px',
              borderRadius: '0 4px 4px 0',
              fontStyle: 'italic',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
        ul: (props: React.ComponentPropsWithoutRef<'ul'>) => (
          <ul
            {...props}
            style={{
              // Tailwind's preflight zeroes list-style globally, which silently
              // turned every markdown bullet list into flat indented lines.
              listStyle: 'disc outside',
              margin: '8px 0',
              paddingLeft: '20px',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
        ol: (props: React.ComponentPropsWithoutRef<'ol'>) => (
          <ol
            {...props}
            style={{
              listStyle: 'decimal outside',
              margin: '8px 0',
              paddingLeft: '20px',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
        li: (props: React.ComponentPropsWithoutRef<'li'>) => (
          <li
            {...props}
            style={{
              margin: '4px 0',
              lineHeight: '1.6',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
        table: (props: React.ComponentPropsWithoutRef<'table'>) => (
          <div style={{ overflow: 'auto', margin: '12px 0' }}>
            <table
              {...props}
              style={{
                width: '100%',
                borderCollapse: 'collapse',
                border: isUserMessage
                  ? '1px solid rgba(255, 255, 255, 0.3)'
                  : '1px solid var(--ui-border)',
                borderRadius: '6px',
                overflow: 'hidden',
              }}
            />
          </div>
        ),
        th: (props: React.ComponentPropsWithoutRef<'th'>) => (
          <th
            {...props}
            style={{
              padding: '8px 12px',
              backgroundColor: isUserMessage
                ? 'rgba(255, 255, 255, 0.2)'
                : 'var(--ui-surface-raised)',
              border: isUserMessage
                ? '1px solid rgba(255, 255, 255, 0.3)'
                : '1px solid var(--ui-border)',
              fontWeight: 'bold',
              textAlign: 'left',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
        td: (props: React.ComponentPropsWithoutRef<'td'>) => (
          <td
            {...props}
            style={{
              padding: '8px 12px',
              border: isUserMessage
                ? '1px solid rgba(255, 255, 255, 0.3)'
                : '1px solid var(--ui-border)',
              color: isUserMessage ? 'white' : 'inherit',
            }}
          />
        ),
      }}
    >
      {escapedContent}
    </ReactMarkdown>
  );
}

export const MarkdownContent = React.memo(_MarkdownContent);

export interface MarkdownRendererProps extends Omit<
  React.HTMLAttributes<HTMLDivElement>,
  'content'
> {
  animated?: boolean;
  content: string;
  fontFamily?: string;
  fontSize?: number;
  loading?: boolean;
  previousContentLength?: number;
}

export function MarkdownRenderer(props: MarkdownRendererProps) {
  const { t } = useTranslation();
  const {
    content,
    loading,
    fontSize = 14,
    fontFamily = 'inherit',
    className,
    style,
    animated = false,
    previousContentLength = 0,
    ...otherProps
  } = props;

  return (
    <div
      className={clsx('markdown-body', className)}
      style={{
        fontSize: `${fontSize}px`,
        fontFamily: fontFamily,
        lineHeight: '1.6',
        color: 'var(--ui-text)',
        ...style,
      }}
      dir='auto'
      {...otherProps}
    >
      {loading ? (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '8px',
            padding: '16px',
            color: 'var(--ui-text-faint)',
          }}
        >
          <div
            style={{
              width: '16px',
              height: '16px',
              border: '2px solid var(--ui-border)',
              borderTop: '2px solid var(--ui-accent)',
              borderRadius: '50%',
              animation: 'spin 1s linear infinite',
            }}
          />
          {t('component.markdown.rendering')}
        </div>
      ) : (
        <MarkdownContent
          content={content}
          className={className}
          animated={animated}
          previousContentLength={previousContentLength}
        />
      )}
    </div>
  );
}

export default MarkdownRenderer;
