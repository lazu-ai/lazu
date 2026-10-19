/* Everything that needs the document's text — and with it react-markdown,
 * remark and the micromark parser behind the chapter outline.
 *
 * Kept out of the entry bundle: /terms and /privacy are eager routes because
 * they are prerendered, and importing this at module scope put the whole
 * Markdown pipeline (~230KB of source) into the first download for every
 * visitor to every page. Nothing here runs until the document has come back
 * from the API, which never happens while prerendering — so the lazy boundary
 * around it costs the static output nothing.
 */
import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import ReactMarkdown from 'react-markdown';
import { Link } from 'react-router-dom';
import RemarkGfm from 'remark-gfm';
import {
  bodyWithoutHeadMatter,
  chapterId,
  outlineOf,
  splitChapterHeading,
  updatedDateOf,
  type LegalChapter,
} from './legal-outline';
import { sanitizeMarkup } from '../../../helpers/sanitize';

const isHtmlContent = (content: string): boolean =>
  typeof content === 'string' && /<\/?[a-z][\s\S]*>/i.test(content);

type DocumentBodyProps = {
  chapters: LegalChapter[];
  content: string;
};

type DocumentSibling = {
  description: string;
  external?: boolean;
  title: string;
  to: string;
};

const withHeadingAnchors = (html: string, chapters: LegalChapter[]) => {
  let index = 0;
  return html.replace(/<h2(\s[^>]*)?>/gi, (match, attrs = '') => {
    const chapter = chapters[index];
    index += 1;
    if (!chapter) return match;
    const kept = String(attrs || '').replace(
      /\sid\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi,
      '',
    );
    return `<h2${kept} id="${chapter.id}">`;
  });
};

/** react-markdown hands a heading its rendered children, which are React
 *  elements once the heading carries inline marks. String()-coercing those
 *  yields "[object Object]", so the text used for matching is flattened. */
const flattenText = (node: React.ReactNode): string => {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(flattenText).join('');
  if (React.isValidElement(node))
    return flattenText(
      (node.props as { children?: React.ReactNode })?.children,
    );
  return '';
};

/** Drop the enumerator from the rendered heading while keeping its marks: the
 *  numeral is printed separately beside the title. */
const withoutLeading = (node: React.ReactNode, count: number) => {
  if (count <= 0) return node;
  let remaining = count;
  return React.Children.toArray(node).map((child) => {
    if (remaining <= 0) return child;
    if (typeof child === 'string') {
      const take = Math.min(remaining, child.length);
      remaining -= take;
      return child.slice(take);
    }
    // An element is never part of the enumerator; stop trimming at it.
    remaining = 0;
    return child;
  });
};

const DocumentBody = ({ chapters, content }: DocumentBodyProps) => {
  const body = useMemo(() => bodyWithoutHeadMatter(content), [content]);
  const safeHtml = useMemo(
    () =>
      isHtmlContent(body)
        ? withHeadingAnchors(sanitizeMarkup(body), chapters)
        : '',
    [body, chapters],
  );

  if (safeHtml) {
    return (
      <div
        className='legal-doc'
        dangerouslySetInnerHTML={{ __html: safeHtml }}
      />
    );
  }

  return (
    <div className='legal-doc'>
      <ReactMarkdown
        remarkPlugins={[RemarkGfm]}
        components={{
          // The document's own "一、" enumerators become a mono numeral set
          // beside the heading — the chapter number a legal reader cites.
          h1: () => null,
          h2: ({ children, node }) => {
            const text = flattenText(children);
            const { number, title } = splitChapterHeading(text);
            const match = chapters.find(
              (chapter) => chapter.line === node?.position?.start.line,
            );
            return (
              <h2 id={match?.id}>
                {number ? (
                  <span className='legal-doc__no'>{number}</span>
                ) : null}
                <span>
                  {withoutLeading(children, text.length - title.length)}
                </span>
              </h2>
            );
          },
        }}
      >
        {body}
      </ReactMarkdown>
    </div>
  );
};

/** A legal document exactly as the public page sets it, for an editor's
 *  preview: the leading `#` title and "last updated" line are lifted out of the
 *  body and shown as the page shows them, chapter enumerators become the same
 *  numerals, and the same reading styles apply. Rendering the raw draft instead
 *  showed a heading and a date line the visitor never sees. */
export const LegalDocumentPreview = ({
  content,
  title,
}: {
  content: string;
  title: string;
}) => {
  const { t } = useTranslation();
  const chapters = useMemo(() => outlineOf(content), [content]);
  const updatedAt = useMemo(() => updatedDateOf(content), [content]);
  return (
    <article className='legal-preview'>
      <header className='legal-head'>
        <h1>{title}</h1>
        {updatedAt ? (
          <div className='legal-head__meta'>
            <span>
              {t('page.public.document.updatedAt', {
                defaultValue: '最近更新',
              })}{' '}
              <b>{updatedAt}</b>
            </span>
          </div>
        ) : null}
      </header>
      <DocumentBody chapters={chapters} content={content} />
    </article>
  );
};

/** Highlights the chapter the reader is actually in, not the one they last
 *  clicked: the rail follows the scroll position. */
const useActiveChapter = (chapters: LegalChapter[]) => {
  const [active, setActive] = useState('');

  useEffect(() => {
    if (!chapters.length) return undefined;
    const read = () => {
      let current = chapters[0]?.id || '';
      for (const chapter of chapters) {
        const node = document.getElementById(chapter.id);
        if (node && node.getBoundingClientRect().top < 140)
          current = chapter.id;
      }
      setActive(current);
    };
    read();
    window.addEventListener('scroll', read, { passive: true });
    window.addEventListener('resize', read);
    return () => {
      window.removeEventListener('scroll', read);
      window.removeEventListener('resize', read);
    };
  }, [chapters]);

  return active;
};

type LegalDocumentProps = {
  content: string;
  localized: (path: string) => string;
  siblings: DocumentSibling[];
  title: string;
};

export default function LegalDocument({
  content,
  localized,
  siblings,
  title,
}: LegalDocumentProps) {
  const { t } = useTranslation();
  const chapters = useMemo(() => outlineOf(content), [content]);
  const updatedAt = useMemo(() => updatedDateOf(content), [content]);
  const activeChapter = useActiveChapter(chapters);

  return (
    <div className='legal-layout'>
      <article>
        <header className='legal-head'>
          <div className='legal-head__crumb'>
            <Link reloadDocument to={localized('/')}>
              {t('legacy.i18n.home.ff93ad0e4e')}
            </Link>
            <span aria-hidden='true'>/</span>
            <span>
              {t('page.public.document.legal', { defaultValue: '法律' })}
            </span>
          </div>
          <h1>{title}</h1>
          <div className='legal-head__meta'>
            {updatedAt ? (
              <span>
                {t('page.public.document.updatedAt', {
                  defaultValue: '最近更新',
                })}{' '}
                <b>{updatedAt}</b>
              </span>
            ) : null}
          </div>
        </header>

        {chapters.length > 1 ? (
          <nav
            className='legal-toc'
            aria-label={t('page.public.document.contents', {
              defaultValue: '目录',
            })}
          >
            <div className='legal-toc__label'>
              {t('page.public.document.contents', { defaultValue: '目录' })}
            </div>
            <ol>
              {chapters.map((chapter, index) => (
                <li key={chapter.id}>
                  <a href={`#${chapter.id}`}>
                    <span className='legal-toc__no'>
                      {chapter.number || index + 1}
                    </span>
                    <span>{chapter.title}</span>
                  </a>
                </li>
              ))}
            </ol>
          </nav>
        ) : null}

        <DocumentBody chapters={chapters} content={content} />

        {siblings.length ? (
          <div className='legal-siblings'>
            {siblings.map((sibling) =>
              sibling.external ? (
                <a
                  href={sibling.to}
                  key={sibling.to}
                  rel='noopener noreferrer'
                  target='_blank'
                >
                  <b>{sibling.title}</b>
                  <span>{sibling.description}</span>
                </a>
              ) : (
                <Link
                  reloadDocument
                  key={sibling.to}
                  to={localized(sibling.to)}
                >
                  <b>{sibling.title}</b>
                  <span>{sibling.description}</span>
                </Link>
              ),
            )}
          </div>
        ) : null}

        <div className='legal-foot'>
          <a href='mailto:support@lazu.ai'>support@lazu.ai</a>
        </div>
      </article>

      {chapters.length > 1 ? (
        <nav
          className='legal-anchors'
          aria-label={t('page.public.document.contents', {
            defaultValue: '目录',
          })}
        >
          <div className='legal-anchors__label'>
            {t('page.public.document.onThisPage', {
              defaultValue: '本页章节',
            })}
          </div>
          {chapters.map((chapter, index) => (
            <a
              className={chapter.id === activeChapter ? 'is-active' : undefined}
              href={`#${chapter.id}`}
              key={chapter.id}
            >
              <span className='legal-anchors__no'>
                {chapter.number || index + 1}
              </span>
              <span>{chapter.title}</span>
            </a>
          ))}
        </nav>
      ) : null}
    </div>
  );
}
