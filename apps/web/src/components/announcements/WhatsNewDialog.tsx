import { RefreshCcw, Send } from 'lucide-react';
import { useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router-dom';
import { formatVersionLabel } from './versionLabel';
import {
  WHATS_NEW_OPEN_EVENT,
  type WhatsNewOpenDetail,
} from './whatsNewChannel';
import { StatusContext } from '../../context/Status';
import { useSession } from '../../context/User';
import {
  ANNOUNCEMENTS_UPDATED_EVENT,
  type Announcement,
} from '../../helpers/announcement';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import AnnouncementTimeline from '../common/announcement/AnnouncementTimeline';
import { Button, Modal } from '../ui';
import './whats-new-dialog.css';

type AnnouncementList = {
  items?: Announcement[];
};

export { openWhatsNewDialog } from './whatsNewChannel';

const dismissAnnouncement = async (id: number | string) => {
  unwrap(await API.post(`/api/announcements/${id}/dismiss`));
};

const WhatsNewDialog = () => {
  const { i18n, t } = useTranslation();
  const location = useLocation();
  const [statusState] = useContext(StatusContext);
  const userSession = useSession();
  const [items, setItems] = useState<Announcement[]>([]);
  const [pinnedAnnouncement, setPinnedAnnouncement] =
    useState<Announcement | null>(null);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [markingRead, setMarkingRead] = useState(false);

  const isConsoleRoute = location.pathname.startsWith('/console');
  const user = userSession.user;
  const announcementLocale = i18n.resolvedLanguage || i18n.language;

  const loadAnnouncements = useCallback(
    async (pinned?: Announcement | null) => {
      if (!user) return [];
      setLoading(true);
      try {
        const list = unwrap<AnnouncementList | undefined>(
          await API.get('/api/announcements?limit=30', {
            params: { locale: announcementLocale },
            disableDuplicate: true,
          }),
        );
        const nextItems = Array.isArray(list?.items) ? list.items : [];
        const includesPinned =
          pinned?.id !== undefined &&
          nextItems.some((item) => item.id === pinned.id);
        const mergedItems =
          pinned && !includesPinned ? [pinned, ...nextItems] : nextItems;
        setItems(mergedItems);
        return mergedItems;
      } finally {
        setLoading(false);
      }
    },
    [announcementLocale, user],
  );

  // The panel is opened only on request now. The daily interrupt popup is
  // retired: its once-per-day throttle moved onto the version plate's pulse
  // (see VersionPlate.tsx), so the console never steals focus on load.
  useEffect(() => {
    const handleOpen = (event: Event) => {
      if (!user) return;
      const detail = (event as CustomEvent<WhatsNewOpenDetail>).detail;
      setPinnedAnnouncement(detail?.pinnedAnnouncement || null);
      setOpen(true);
    };
    window.addEventListener(WHATS_NEW_OPEN_EVENT, handleOpen);
    return () => window.removeEventListener(WHATS_NEW_OPEN_EVENT, handleOpen);
  }, [user]);

  // Keep the copy in lockstep with the console language. In particular, if a
  // user changes language while the panel is open, reload the localized view
  // immediately instead of leaving stale copy until the next manual refresh.
  useEffect(() => {
    if (!open || !user) return;
    loadAnnouncements(pinnedAnnouncement).catch(() => undefined);
  }, [announcementLocale, loadAnnouncements, open, pinnedAnnouncement, user]);

  const unreadItems = useMemo(
    () => items.filter((item) => item.isUnread !== false),
    [items],
  );

  const versionLabel = useMemo(
    () => formatVersionLabel(statusState?.status?.version),
    [statusState?.status?.version],
  );

  // Closing is just closing — glancing at the panel is not reading it. Marking
  // read is the explicit footer action, which is also what clears the plate.
  const handleMarkAllRead = useCallback(async () => {
    setMarkingRead(true);
    try {
      // The reading surface is intentionally capped at 30 items, but "mark all"
      // means every unread announcement in the store. Drain the unseen feed in
      // server-sized batches so older unread items are not left behind.
      const dismissed = new Set<number | string>();
      for (;;) {
        const unseen = unwrap<AnnouncementList | undefined>(
          await API.get('/api/announcements/unseen', {
            params: { limit: 100, locale: announcementLocale },
            disableDuplicate: true,
          }),
        );
        const batch = Array.isArray(unseen?.items) ? unseen.items : [];
        const ids = batch
          .map((item) => item.id)
          .filter(
            (id): id is number | string =>
              id !== undefined && id !== null && !dismissed.has(id),
          );
        if (ids.length === 0) break;
        ids.forEach((id) => dismissed.add(id));
        await Promise.all(ids.map((id) => dismissAnnouncement(id)));
      }
      setItems((current) =>
        current.map((item) => ({ ...item, isUnread: false })),
      );
      window.dispatchEvent(new CustomEvent(ANNOUNCEMENTS_UPDATED_EVENT));
      setOpen(false);
    } catch (error) {
      showError(error);
    } finally {
      setMarkingRead(false);
    }
  }, [announcementLocale]);

  if (!user || !isConsoleRoute) {
    return null;
  }

  const countLabel = t('observability.whatsNew.count', {
    defaultValue: '{{total}} 条',
    total: items.length,
  });
  const headerMeta = [versionLabel, items.length ? countLabel : '']
    .filter(Boolean)
    .join(' · ');
  // While the first fetch is still in flight `items` is empty, which would read
  // as "nothing unread" and briefly label the primary action 关闭 — on the very
  // open that was almost certainly triggered by an unread badge. Treat an
  // in-flight load as "assume unread"; if it resolves to nothing the label
  // settles on 关闭, and either action just closes the panel.
  const hasUnread = unreadItems.length > 0;

  return (
    <Modal
      body={
        <AnnouncementTimeline
          announcements={items}
          animateEntrance
          emptyDescription={t('observability.whatsNew.emptyDescription')}
          emptyTitle={t('observability.whatsNew.emptyTitle')}
          scrollable={false}
          showUnreadState
          t={t}
        />
      }
      className='whats-new-panel'
      busy={markingRead}
      closeLabel={t('legacy.i18n.close.6c14bd7f6f')}
      description={
        <span className='sr-only'>
          {t('observability.whatsNew.description')}
        </span>
      }
      footer={
        <>
          <Button
            loading={loading}
            disabled={markingRead}
            onClick={() => void loadAnnouncements().catch(showError)}
            size='sm'
            variant='secondary'
          >
            <RefreshCcw aria-hidden='true' size={13} />
            {t('observability.common.refresh')}
          </Button>
          <Button
            className='whats-new-panel__mark-read'
            disabled={loading || markingRead}
            loading={markingRead}
            onClick={() =>
              hasUnread ? void handleMarkAllRead() : setOpen(false)
            }
            size='sm'
            variant='primary'
          >
            {hasUnread || loading
              ? t('observability.whatsNew.markAllRead', {
                  defaultValue: '全部已读',
                })
              : t('legacy.i18n.close.6c14bd7f6f')}
          </Button>
        </>
      }
      onOpenChange={setOpen}
      open={open}
      title={
        <>
          <span aria-hidden='true' className='whats-new-panel__grab' />
          <span className='whats-new-panel__heading'>
            <span className='whats-new-panel__title'>
              <Send aria-hidden='true' size={16} />
              {t('observability.whatsNew.title')}
            </span>
            {headerMeta ? (
              <span className='whats-new-panel__meta'>{headerMeta}</span>
            ) : null}
          </span>
        </>
      }
    />
  );
};

export default WhatsNewDialog;
