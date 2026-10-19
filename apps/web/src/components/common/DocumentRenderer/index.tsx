import { ExternalLink } from 'lucide-react';
import { Suspense, lazy, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router-dom';
import { API } from '../../../helpers/api';
import {
  localizedMarketingPath,
  splitMarketingPath,
} from '../../../helpers/marketingLocale';
import { showError } from '../../../helpers/utils';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import { unwrap } from '../../../lib/api-error';
import {
  Button,
  DataSurface,
  EmptyState,
  PageHeader,
  Skeleton,
} from '../../ui';
import './legal-document.css';

/* The document itself loads with its Markdown pipeline, once there is a
 * document to render. Prerendering never reaches this branch — the fetch does
 * not resolve on the server — so the static output is unchanged. */
const LegalDocument = lazy(() => import('./LegalDocument'));

/* The document, before it arrives.
 *
 * What used to stand here was a small centred "Loading document" box, and the
 * page was as tall as that box — so the footer sat at y=243, a third of the
 * way up an empty viewport, and the moment the text landed (5,596px of it) the
 * footer was thrown to y=5,650. That one move was a layout shift of 0.83, more
 * than three times Google's "poor" threshold and the largest on the site.
 *
 * The outline is the reading column itself: same grid, same measure, a page's
 * worth of height. The footer starts below the fold, where the real document
 * puts it, and stays there. */
function DocumentSkeleton() {
  const revealed = useDelayedReveal();
  return (
    <div className='legal-page legal-page--pending'>
      {revealed ? (
        <div className='legal-layout' aria-busy='true' role='status'>
          <div className='legal-skeleton'>
            <Skeleton height={13} width={120} />
            <Skeleton
              className='legal-skeleton__title'
              height={34}
              width='58%'
            />
            <Skeleton height={13} width={190} />
            <div className='legal-skeleton__toc'>
              <Skeleton height={12} width={64} />
              <Skeleton height={12} width='72%' />
              <Skeleton height={12} width='64%' />
              <Skeleton height={12} width='68%' />
            </div>
            <div className='legal-skeleton__body'>
              {[92, 88, 96, 71, 90, 84, 94, 62].map((width, index) => (
                <Skeleton height={14} key={index} width={`${width}%`} />
              ))}
            </div>
          </div>
          <div className='legal-skeleton__rail'>
            <Skeleton height={12} width='70%' />
            <Skeleton height={12} width='84%' />
            <Skeleton height={12} width='60%' />
          </div>
        </div>
      ) : null}
    </div>
  );
}

type DocumentSibling = {
  description: string;
  external?: boolean;
  title: string;
  to: string;
};

type DocumentRendererProps = {
  apiEndpoint: string;
  title: string;
  cacheKey: string;
  emptyMessage: string;
  /** Related legal documents, shown as cards under the last chapter. */
  siblings?: DocumentSibling[];
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const CONTENT_FIELDS = [
  'content',
  'html',
  'markdown',
  'text',
  'body',
  'value',
] as const;

const normalizeContent = (value: unknown): string => {
  if (typeof value === 'string') return value;
  if (value == null) return '';
  // Never String()-coerce an object (that yields "[object Object]"); pull a
  // known string field if present, otherwise treat as empty so the caller
  // falls back to the empty/error state.
  if (isRecord(value)) {
    for (const field of CONTENT_FIELDS) {
      const candidate = value[field];
      if (typeof candidate === 'string' && candidate.trim()) return candidate;
    }
  }
  return '';
};

const isUrl = (content: string): boolean => {
  try {
    new URL(content.trim());
    return true;
  } catch {
    return false;
  }
};

/** Give every h2 in an admin-supplied HTML override the same anchor id the
 *  Markdown path derives, so one rail implementation serves both.
 *
 *  An id the author already wrote is replaced rather than kept: the contents
 *  list and the scroll-spy both address chapters by the derived id, so a
 *  heading that keeps its own id becomes a link to nowhere. */
const DocumentRenderer = ({
  apiEndpoint,
  title,
  emptyMessage,
  siblings = [],
}: DocumentRendererProps) => {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const lang = i18n.language;
  /* /zh/privacy asserts its language through the URL, and MarketingLayout
     reads it back from there. Links that drop the prefix therefore switch the
     reader to English mid-document, so every in-app destination is rebuilt
     with the locale the current path carries. */
  const locale = splitMarketingPath(location.pathname).locale;
  const localized = (path: string) => localizedMarketingPath(path, locale);
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;

    setLoading(true);
    setContent('');

    const loadContent = async () => {
      try {
        // Legal overrides must be fetched fresh; never present a cached version
        // as current when the authoritative request fails.
        const res = await API.get(apiEndpoint, {
          params: { lang },
          skipErrorHandler: true,
        });
        const nextContent = normalizeContent(unwrap<unknown>(res));
        if (nextContent) {
          if (!cancelled) {
            setContent(nextContent);
          }
          return;
        }

        if (!cancelled) {
          showError(emptyMessage);
          setContent('');
        }
      } catch (error) {
        if (!cancelled) {
          showError(error, emptyMessage);
          setContent('');
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    };

    void loadContent();

    return () => {
      cancelled = true;
    };
  }, [apiEndpoint, emptyMessage, lang]);

  if (loading) return <DocumentSkeleton />;

  if (!content || content.trim() === '') {
    return (
      <div className='console-route-shell'>
        <EmptyState
          title={t('page.public.document.emptyTitle', { title })}
          description={emptyMessage}
        />
      </div>
    );
  }

  if (isUrl(content)) {
    const externalUrl = content.trim();

    return (
      <div className='console-route-shell'>
        <PageHeader
          eyebrow={t('page.public.document.eyebrow')}
          title={title}
          description={t('page.public.document.externalDescription')}
        />
        <DataSurface>
          <Button
            render={
              <a
                href={externalUrl}
                target='_blank'
                rel='noopener noreferrer'
                title={externalUrl}
              />
            }
            variant='primary'
          >
            <ExternalLink size={15} />
            {t('page.public.document.visit', { title })}
          </Button>
        </DataSurface>
      </div>
    );
  }

  return (
    <div className='legal-page'>
      <Suspense fallback={<DocumentSkeleton />}>
        <LegalDocument
          content={content}
          localized={localized}
          siblings={siblings}
          title={title}
        />
      </Suspense>
    </div>
  );
};

export default DocumentRenderer;
