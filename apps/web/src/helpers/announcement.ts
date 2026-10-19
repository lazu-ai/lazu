import { getRelativeTime } from './utils';

// Dispatched after announcements are dismissed (from any surface) so other
// surfaces can refresh their per-user unread state from the server.
export const ANNOUNCEMENTS_UPDATED_EVENT = 'lazu:announcements-updated';

export type Announcement = {
  affected_models?: string[];
  content?: string;
  delivery?: 'changelog' | 'notice' | 'critical';
  effective_date?: string | null;
  extra?: unknown;
  id?: number | string;
  isUnread?: boolean;
  key?: string;
  publishDate?: Date | number | string;
  relative?: string;
  time?: string;
  title?: string;
  type?: string;
  [key: string]: unknown;
};

export type NormalizedAnnouncement = Announcement & {
  id: number | string;
  isUnread: boolean;
  key: string;
  relative: string;
  time: string;
  title: string;
  type: string;
};

const getAnnouncementTimestamp = (
  publishDate?: Date | number | string,
): number => {
  if (!publishDate) return 0;
  const date = new Date(publishDate);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
};

export const getAnnouncementKey = (item: Announcement = {}): string =>
  `${item?.publishDate || ''}-${(item?.content || '').slice(0, 30)}`;

export const formatAnnouncementAbsoluteTime = (
  publishDate?: Date | number | string,
): string => {
  const timestamp = getAnnouncementTimestamp(publishDate);
  if (!timestamp) return String(publishDate || '');

  const date = new Date(timestamp);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
};

export const sortAnnouncementsByDateDesc = <T extends Announcement>(
  announcements: T[] = [] as T[],
): T[] =>
  [...announcements].sort(
    (left, right) =>
      getAnnouncementTimestamp(right?.publishDate) -
      getAnnouncementTimestamp(left?.publishDate),
  );

export const normalizeAnnouncement = (
  item: Announcement = {},
  index = 0,
  unreadSet = new Set<string>(),
): NormalizedAnnouncement => {
  const key = item?.key || getAnnouncementKey(item);

  return {
    id: item?.id ?? key ?? `announcement-${index}`,
    key,
    ...item,
    type: item?.type || 'default',
    title: item?.title || '',
    time: item?.time || formatAnnouncementAbsoluteTime(item?.publishDate),
    relative: item?.relative || String(getRelativeTime(item?.publishDate)),
    isUnread:
      typeof item?.isUnread === 'boolean' ? item.isUnread : unreadSet.has(key),
  };
};

export const normalizeAnnouncements = (
  announcements: Announcement[] = [],
  unreadSet = new Set<string>(),
) =>
  sortAnnouncementsByDateDesc(announcements).map((item, index) =>
    normalizeAnnouncement(item, index, unreadSet),
  );

export const getAnnouncementStats = (announcements: Announcement[] = []) => {
  const normalizedList = normalizeAnnouncements(announcements);

  return {
    total: normalizedList.length,
    latest: normalizedList[0] || null,
    unread: normalizedList.filter((item) => item.isUnread).length,
    withExtra: normalizedList.filter((item) => Boolean(item.extra)).length,
  };
};
