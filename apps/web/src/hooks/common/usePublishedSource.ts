import { useQuery } from '@tanstack/react-query';
import { useLocation } from 'react-router-dom';
import { isAppOwnedPath } from '../../components/layout/route-shell';
import { usePrerender } from '../../context/Prerender';
import { useStatus } from '../../context/Status';
import { API } from '../../helpers/api';
import { getSourceURL } from '../../helpers/brandUrls';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type PublishedSource = { enabled: boolean; url: string };

const fetchPublishedSource = async (): Promise<PublishedSource> => {
  const data = unwrap<{ enabled?: unknown; source_url?: unknown } | null>(
    await API.get('/api/source/star', {
      skipErrorHandler: true,
    } as Parameters<typeof API.get>[1]),
  );
  return {
    enabled: data?.enabled === true,
    url: typeof data?.source_url === 'string' ? data.source_url : '',
  };
};

/**
 * The repository behind the GitHub entry, and whether the site can star it
 * for the visitor. App pages learn both from /api/status; the public pages
 * never load that, so there the one public question is asked instead, once
 * per visit and never while prerendering, when the pages carry the
 * build-time link. The two answers come from the same deployment.
 */
export const usePublishedSource = (): { canStar: boolean; url: string } => {
  const { pathname } = useLocation();
  const { route: prerenderRoute } = usePrerender();
  const { status } = useStatus();
  const probe = useQuery({
    queryKey: queryKeys.publishedSource,
    queryFn: fetchPublishedSource,
    enabled:
      !prerenderRoute &&
      !isAppOwnedPath(pathname) &&
      status?.source_star === undefined,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
  return {
    canStar: status?.source_star ?? probe.data?.enabled ?? false,
    url: getSourceURL(status?.source_url || probe.data?.url),
  };
};
