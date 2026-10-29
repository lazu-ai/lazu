import { useQuery } from '@tanstack/react-query';
import { useLocation } from 'react-router-dom';
import { isAppOwnedPath } from '../../components/layout/route-shell';
import { usePrerender } from '../../context/Prerender';
import { useStatus } from '../../context/Status';
import { API } from '../../helpers/api';
import { getSourceURL } from '../../helpers/brandUrls';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type PublishedSource = { enabled: boolean; url: string; stars: number | null };

const fetchPublishedSource = async (): Promise<PublishedSource> => {
  const data = unwrap<{
    enabled?: unknown;
    source_url?: unknown;
    stars?: unknown;
  } | null>(
    await API.get('/api/source/star', {
      skipErrorHandler: true,
    } as Parameters<typeof API.get>[1]),
  );
  return {
    enabled: data?.enabled === true,
    url: typeof data?.source_url === 'string' ? data.source_url : '',
    stars:
      typeof data?.stars === 'number' && Number.isFinite(data.stars)
        ? data.stars
        : null,
  };
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
