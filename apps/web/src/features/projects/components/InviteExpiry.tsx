import { useTranslation } from 'react-i18next';
import { inviteExpiry } from '../inviteExpiry';

export function InviteExpiry({
  expiresAt,
  now,
  expired = false,
  compact = false,
}: {
  expiresAt: number;
  now: number;
  expired?: boolean;
  /** One line for table rows; the absolute deadline moves to the tooltip. */
  compact?: boolean;
}) {
  const { t, i18n } = useTranslation('projects');
  const state = inviteExpiry(expiresAt, now);
  const date = new Intl.DateTimeFormat(i18n.language, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'shortOffset',
  }).format(new Date(expiresAt * 1000));
  if (compact)
    return (
      <span
        className='pj-invite-expiry pj-invite-expiry--compact'
        data-soon={(!expired && state.soon) || undefined}
        title={date}
      >
        <time dateTime={new Date(expiresAt * 1000).toISOString()}>
          {t(expired ? 'projects.invite.expiredBadge' : state.key, state)}
        </time>
      </span>
    );
  return (
    <span
      className='pj-invite-expiry'
      data-soon={(!expired && state.soon) || undefined}
    >
      <span>
        {t(expired ? 'projects.invite.expiredBadge' : state.key, state)}
      </span>
      <small>
        <time dateTime={new Date(expiresAt * 1000).toISOString()}>{date}</time>
      </small>
    </span>
  );
}
