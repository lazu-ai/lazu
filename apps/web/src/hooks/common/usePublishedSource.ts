import { useQuery, type QueryClient } from '@tanstack/react-query';
import { useLocation } from 'react-router-dom';
import { isAppOwnedPath } from '../../components/layout/route-shell';
import { usePrerender } from '../../context/Prerender';
import { useStatus } from '../../context/Status';
import { API } from '../../helpers/api';
import { buildAPIURL } from '../../helpers/apiUrl';
import { getSourceURL } from '../../helpers/brandUrls';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

const PUBLISHED_SOURCE_PATH = '/api/source/star';

type PublishedSource = { enabled: boolean; url: string; stars: number | null };

type PublishedSourceBody = {
  enabled?: unknown;
  source_url?: unknown;
  stars?: unknown;
} | null;

const readPublishedSource = (data: PublishedSourceBody): PublishedSource => ({
  enabled: data?.enabled === true,
  url: typeof data?.source_url === 'string' ? data.source_url : '',
  stars:
    typeof data?.stars === 'number' && Number.isFinite(data.stars)
      ? data.stars
      : null,
});

/* Set when the visitor has just starred: the next question skips the
   browser's copy of the answer (it is cacheable for five minutes) and
   replaces it, so a reload shows the count that includes the new star. */
let skipCachedAnswer = false;

const fetchPublishedSource = async (): Promise<PublishedSource> => {
  if (skipCachedAnswer) {
    skipCachedAnswer = false;
    const response = await fetch(buildAPIURL(PUBLISHED_SOURCE_PATH), {
      cache: 'reload',
      credentials: 'same-origin',
    });
    if (!response.ok) throw new Error(`published source ${response.status}`);
    const body = (await response.json()) as { data?: PublishedSourceBody };
    return readPublishedSource(body?.data ?? null);
  }
  return readPublishedSource(
    unwrap<PublishedSourceBody>(
      await API.get(PUBLISHED_SOURCE_PATH, {
        skipErrorHandler: true,
      } as Parameters<typeof API.get>[1]),
    ),
  );
};

/**
 * After a new star: the count goes up by one at once, then the deployment is
 * asked again past the browser's cache. The server counts the star it just
 * made, so its answer carries the same number and later visits keep it.
 */
export const countNewSourceStar = async (queryClient: QueryClient) => {
  const queryKey = queryKeys.publishedSource;
  skipCachedAnswer = true;
  /* A question still in flight (a page just back from the full-page trip)
     would be answered from the browser's cache, and a refetch only joins it
     while it has no answer yet, so it is dropped first. */
  await queryClient.cancelQueries({ queryKey });
  queryClient.setQueryData<PublishedSource>(queryKey, (current) =>
    current && current.stars !== null
      ? { ...current, stars: current.stars + 1 }
      : current,
  );
  await queryClient.invalidateQueries({ queryKey });
};

/**
 * The repository behind the GitHub entry, whether the site can star it for
 * the visitor, and its star count when the deployment knows it. App pages
 * learn the first two from /api/status; the public pages never load that,
 * and only the public question carries the count, so the public pages ask it
 * once per visit and never while prerendering, when the pages carry the
 * build-time link. The answers come from the same deployment.
 */
export const usePublishedSource = (): {
  canStar: boolean;
  url: string;
  stars: number | null;
} => {
  const { pathname } = useLocation();
  const { route: prerenderRoute } = usePrerender();
  const { status } = useStatus();
  const probe = useQuery({
    queryKey: queryKeys.publishedSource,
    queryFn: fetchPublishedSource,
    enabled: !prerenderRoute && !isAppOwnedPath(pathname),
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
  return {
    canStar: probe.data?.enabled ?? status?.source_star ?? false,
    url: getSourceURL(probe.data?.url || status?.source_url),
    stars: probe.data?.stars ?? null,
  };
};
