import { Star } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import './source-star.css';
import BrandLogo from '../../brand/BrandLogo';
import { useActualTheme } from '../../context/Theme';
import {
  SOURCE_STAR_MESSAGE_TYPE,
  isSourceStarred,
  parseSourceStarResult,
} from '../../helpers/sourceStar';

/* Only a path on this site is a place to return to. */
const siteReturnPath = (raw: string | null) =>
  raw && raw.startsWith('/') && !raw.startsWith('//') && !raw.includes('\\')
    ? raw
    : '/';

/**
 * Where the API sends the browser after the star authorization. In a popup
 * it reports the outcome to the page that opened it and closes, so it is
 * seen for a moment at most; as a full page it returns to that page with the
 * outcome in the address. What it says is the outcome, in one line.
 */
export default function SourceStarCallback() {
  const { t } = useTranslation();
  const actualTheme = useActualTheme();
  const [searchParams] = useSearchParams();
  const [stuck, setStuck] = useState(false);
  const result = parseSourceStarResult(searchParams.get('result')) ?? 'failed';
  const popup = searchParams.get('popup') === '1';

  useEffect(() => {
    const returnPath = siteReturnPath(searchParams.get('return'));
    const opener = window.opener as Window | null;
    if (popup) {
      if (opener && !opener.closed) {
        opener.postMessage(
          { type: SOURCE_STAR_MESSAGE_TYPE, result },
          window.location.origin,
        );
      }
      window.close();
      /* A browser that keeps the window open gets told it may close it. */
      const timer = window.setTimeout(() => setStuck(true), 400);
      return () => window.clearTimeout(timer);
    }
    const target = new URL(returnPath, window.location.origin);
    target.searchParams.set('star', result);
    window.location.replace(target.pathname + target.search + target.hash);
    return undefined;
  }, [popup, result, searchParams]);

  const starred = isSourceStarred(result);

  return (
    <div className='source-star-page'>
      <BrandLogo
        alt='Lazu'
        className='source-star-page__mark'
        size='footer'
        tone={actualTheme === 'dark' ? 'paper' : 'graphite'}
        variant='primary'
      />
      <div className='source-star-page__body' role='status'>
        <Star
          aria-hidden='true'
          className='source-star-page__star'
          fill={starred ? 'currentColor' : 'none'}
          size={28}
          strokeWidth={1.5}
        />
        <h1>{t(`page.public.auth.sourceStar.${result}`)}</h1>
        <p>
          {t(
            stuck
              ? 'page.public.auth.sourceStar.close'
              : 'page.public.auth.sourceStar.closing',
          )}
        </p>
      </div>
      {popup ? (
        <button
          className='source-star-page__close'
          data-owns-chrome=''
          onClick={() => window.close()}
          type='button'
        >
          {t('page.public.auth.sourceStar.closeButton')}
        </button>
      ) : (
        <span />
      )}
    </div>
  );
}
