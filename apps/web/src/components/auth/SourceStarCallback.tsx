import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { AuthPanel, AuthShell } from './AuthLayout';
import {
  SOURCE_STAR_MESSAGE_TYPE,
  parseSourceStarResult,
} from '../../helpers/sourceStar';

/* Only a path on this site is a place to return to. */
const siteReturnPath = (raw: string | null) =>
  raw && raw.startsWith('/') && !raw.startsWith('//') && !raw.includes('\\')
    ? raw
    : '/';

/**
 * Where the API sends the browser after the star authorization. In a popup
 * it reports the outcome to the page that opened it and closes; as a full
 * page it returns to that page with the outcome in the address.
 */
export default function SourceStarCallback() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const [stuck, setStuck] = useState(false);

  useEffect(() => {
    const result =
      parseSourceStarResult(searchParams.get('result')) ?? 'failed';
    const returnPath = siteReturnPath(searchParams.get('return'));
    const opener = window.opener as Window | null;
    if (searchParams.get('popup') === '1') {
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
  }, [searchParams]);

  return (
    <AuthShell>
      <AuthPanel
        title={t('page.public.auth.sourceStar.title')}
        description={t(
          stuck
            ? 'page.public.auth.sourceStar.close'
            : 'page.public.auth.sourceStar.description',
        )}
      >
        <span />
      </AuthPanel>
    </AuthShell>
  );
}
