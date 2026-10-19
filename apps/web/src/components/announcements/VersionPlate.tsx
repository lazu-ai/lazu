import { Bell } from 'lucide-react';
import { useContext, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatVersionLabel } from './versionLabel';
import {
  markAutoOpenedToday,
  readLastAutoOpenDay,
  shouldAutoOpenToday,
  todayDayKey,
} from './whatsNewAutoOpen';
import { openWhatsNewDialog } from './whatsNewChannel';
import { StatusContext } from '../../context/Status';
import { UserContext } from '../../context/User';
import { useAnnouncementsUnread } from '../../hooks/common/useAnnouncementsUnread';
import './version-plate.css';

/* Which user has already spent today's pulse in this page session. Module
   scope, not a ref: on mobile both plates (sidebar drawer + topbar) mount in
   the same tick, and two refs would each read localStorage before either wrote
   it, so both would pulse. */
let attentionClaimedFor: string | null = null;

type VersionPlateProps = {
  className?: string;
  /* 'sidebar' sits in the brand row next to the wordmark; 'mobile' sits in the
     mobile topbar action row as a bell. The version number is not drawn on a
     phone — next to the page name it pushed the name into an ellipsis — and
     stays in the button's label and in the updates panel. */
  variant?: 'sidebar' | 'mobile';
};

const VersionPlate = ({
  className = '',
  variant = 'sidebar',
}: VersionPlateProps) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);
  const user = userState.user;
  const { criticalAnnouncement, hasCritical, hasUnread, unreadCount } =
    useAnnouncementsUnread(Boolean(user));
  const [pulse, setPulse] = useState(false);

  const versionLabel = useMemo(
    () => formatVersionLabel(statusState?.status?.version),
    [statusState?.status?.version],
  );

  // Normal notices stay non-interruptive: the plate pulses at most once per
  // day. A critical announcement uses the same throttle but opens the update
  // panel, reserving automatic interruption for user-impacting changes.
  useEffect(() => {
    if (!user || !hasUnread) return;
    const userKey = String(user.id ?? user.username ?? 'anon');
    if (attentionClaimedFor === userKey) return;
    if (!shouldAutoOpenToday(readLastAutoOpenDay(userKey), todayDayKey(), true))
      return;
    attentionClaimedFor = userKey;
    markAutoOpenedToday(userKey);
    if (hasCritical) {
      openWhatsNewDialog({ pinnedAnnouncement: criticalAnnouncement });
      return;
    }
    setPulse(true);
  }, [criticalAnnouncement, hasCritical, hasUnread, user]);

  if (!user || !versionLabel) return null;

  const label = hasUnread
    ? t('observability.whatsNew.plateUnread', {
        defaultValue: '最新动态：{{total}} 条未读（当前版本 {{version}}）',
        total: unreadCount,
        version: versionLabel,
      })
    : t('observability.whatsNew.plateRead', {
        defaultValue: '最新动态（当前版本 {{version}}）',
        version: versionLabel,
      });

  return (
    <button
      aria-haspopup='dialog'
      aria-label={label}
      data-owns-chrome=''
      className={[
        'version-plate',
        `version-plate--${variant}`,
        hasUnread ? 'has-unread' : 'is-read',
        pulse ? 'is-pulsing' : '',
        className,
      ]
        .filter(Boolean)
        .join(' ')}
      onAnimationEnd={() => setPulse(false)}
      onClick={() => openWhatsNewDialog()}
      title={label}
      type='button'
    >
      {variant === 'mobile' ? (
        <>
          <Bell aria-hidden='true' size={19} strokeWidth={1.7} />
          {hasUnread ? (
            <span aria-hidden='true' className='version-plate__dot' />
          ) : null}
        </>
      ) : (
        <>
          <span aria-hidden='true' className='version-plate__dot' />
          <span className='version-plate__label'>{versionLabel}</span>
        </>
      )}
    </button>
  );
};

export default VersionPlate;
