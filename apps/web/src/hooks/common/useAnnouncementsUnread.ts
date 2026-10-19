import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useContext, useEffect, useMemo } from 'react';
import { UserContext } from '../../context/User';
import {
  ANNOUNCEMENTS_UPDATED_EVENT,
  type Announcement,
} from '../../helpers/announcement';
import { API } from '../../helpers/api';
import { queryKeys } from '../../lib/query-keys';

type UnseenResponse = {
  data?: {
    critical_item?: Announcement;
    has_critical?: boolean;
    items?: Announcement[];
  };
  success?: boolean;
};

type SilentGetConfig = NonNullable<Parameters<typeof API.get>[1]> & {
  skipErrorHandler?: boolean;
};

// The unseen endpoint is capped anyway; we only ever render "has unread" or a
// small count, so a short page is enough and keeps the poll cheap.
const UNSEEN_LIMIT = 20;

/**
 * Shared unread-announcement state for every surface that shows the version
 * plate (sidebar brand row, mobile topbar).
 *
 * Both plates read one cached answer per account, so mounting them on every
 * page switch asks the server nothing new. The answer is refreshed when the
 * tab returns after a few minutes and on ANNOUNCEMENTS_UPDATED_EVENT, which
 * the What's New panel dispatches after dismissing items — that event is the
 * only contract between the reading surface and the entry points.
 */
export const useAnnouncementsUnread = (enabled = true) => {
  const [userState] = useContext(UserContext);
  const userId = (userState.user as { id?: unknown } | undefined)?.id;
  const active = enabled && userId !== undefined && userId !== null;
  const queryClient = useQueryClient();
  const queryKey = useMemo(
    () => [...queryKeys.user, 'announcements-unseen', String(userId ?? '')],
    [userId],
  );

  const unseen = useQuery({
    queryKey,
    enabled: active,
    // The unread dot is decorative: a failed read keeps the count already
    // shown and never raises a toast.
    queryFn: async () => {
      const silent: SilentGetConfig = { skipErrorHandler: true };
      const res = await API.get<UnseenResponse>(
        `/api/announcements/unseen?limit=${UNSEEN_LIMIT}`,
        silent,
      );
      return res.data?.data ?? null;
    },
    staleTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });

  useEffect(() => {
    if (!active) return undefined;
    const handleUpdated = () => {
      void queryClient.invalidateQueries({ queryKey });
    };
    window.addEventListener(ANNOUNCEMENTS_UPDATED_EVENT, handleUpdated);
    return () => {
      window.removeEventListener(ANNOUNCEMENTS_UPDATED_EVENT, handleUpdated);
    };
  }, [active, queryClient, queryKey]);

  const refresh = useCallback(async () => {
    if (active) await queryClient.invalidateQueries({ queryKey });
  }, [active, queryClient, queryKey]);

  const data = active ? unseen.data : null;
  const items = Array.isArray(data?.items) ? data.items : [];
  // Older API nodes do not provide the aggregate flag yet, so retain the
  // item-level check as a rolling-deploy-compatible fallback.
  const hasCritical =
    data?.has_critical === true ||
    items.some((item) => item.delivery === 'critical');

  return {
    criticalAnnouncement:
      (data?.critical_item as Announcement | undefined) ?? null,
    hasCritical,
    hasUnread: items.length > 0,
    refresh,
    unreadCount: items.length,
  };
};

export default useAnnouncementsUnread;
