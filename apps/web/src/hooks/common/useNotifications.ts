import { useCallback, useEffect, useState } from 'react';
import {
  ANNOUNCEMENTS_UPDATED_EVENT,
  type Announcement,
} from '../../helpers/announcement';
import { API } from '../../helpers/api';
import type { StatusState } from '../../context/Status/reducer';

type AnnouncementStatus = StatusState & {
  status?: StatusState['status'] & {
    announcements_enabled?: boolean;
  };
};

// Background fetch config: dedupe and stay silent so a best-effort unread
// check never surfaces a global error toast.
type SilentGetConfig = NonNullable<Parameters<typeof API.get>[1]> & {
  disableDuplicate?: boolean;
  skipErrorHandler?: boolean;
};

const isUnreadAnnouncement = (announcement: Announcement): boolean =>
  announcement.isUnread !== false;

export const useNotifications = (
  statusState?: AnnouncementStatus,
  // Announcements are per-user: a signed-out visitor (a payment return, the
  // 403 page) would only get a 401 and a console error for asking.
  signedIn = true,
) => {
  const [noticeVisible, setNoticeVisible] = useState(false);
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);

  const announcementsEnabled =
    statusState?.status?.announcements_enabled ?? true;

  // Source the bell from the same server-side, per-user unread state as the
  // What's New dialog, so dismissing in one surface is reflected in the other
  // instead of relying on a separate localStorage read-state that drifts.
  const refresh = useCallback(async () => {
    if (!announcementsEnabled || !signedIn) {
      setAnnouncements([]);
      return;
    }
    try {
      const config: SilentGetConfig = {
        disableDuplicate: true,
        skipErrorHandler: true,
      };
      const res = await API.get('/api/announcements?limit=20', config);
      const items = (res.data as { data?: { items?: Announcement[] } })?.data
        ?.items;
      setAnnouncements(Array.isArray(items) ? items : []);
    } catch {
      // Best-effort; leave the previous list in place on failure.
    }
  }, [announcementsEnabled, signedIn]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Refresh when any surface (this bell, the sidebar dot, the What's New
  // dialog) dismisses announcements, so the unread count stays consistent.
  useEffect(() => {
    const handleUpdated = () => void refresh();
    window.addEventListener(ANNOUNCEMENTS_UPDATED_EVENT, handleUpdated);
    return () =>
      window.removeEventListener(ANNOUNCEMENTS_UPDATED_EVENT, handleUpdated);
  }, [refresh]);

  const unreadCount = announcements.filter(isUnreadAnnouncement).length;

  const handleNoticeOpen = () => {
    if (!announcementsEnabled) return;
    setNoticeVisible(true);
  };

  const handleNoticeClose = useCallback(async () => {
    setNoticeVisible(false);
    const unreadIds = announcements
      .filter(isUnreadAnnouncement)
      .map((announcement) => announcement.id)
      .filter((id): id is number | string => id !== undefined && id !== null);
    if (unreadIds.length === 0) return;
    setAnnouncements((current) =>
      current.map((announcement) => ({ ...announcement, isUnread: false })),
    );
    await Promise.allSettled(
      unreadIds.map((id) => API.post(`/api/announcements/${id}/dismiss`)),
    );
    // Keep the sidebar dot and What's New dialog in sync.
    window.dispatchEvent(new CustomEvent(ANNOUNCEMENTS_UPDATED_EVENT));
  }, [announcements]);

  return {
    announcementsEnabled,
    noticeVisible,
    unreadCount,
    announcements,
    handleNoticeOpen,
    handleNoticeClose,
  };
};
