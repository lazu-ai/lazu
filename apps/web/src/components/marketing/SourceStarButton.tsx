import { ArrowUpRight, Star } from 'lucide-react';
import { useEffect, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import {
  formatStarCount,
  hasSourceStarred,
  starSource,
  takeSourceStarResultFromLocation,
  type SourceStarOutcome,
} from '../../helpers/sourceStar';
import { usePublishedSource } from '../../hooks/common/usePublishedSource';

type Props = {
  className?: string;
  /** `sm` sits in the header beside the 30px controls; `md` beside the 38px CTAs. */
  size?: 'sm' | 'md';
};

type Phase = 'idle' | 'pending' | 'starred' | 'already' | 'failed';

const phaseFor = (outcome: SourceStarOutcome | null): Phase => {
  if (outcome === 'starred') return 'starred';
  if (outcome === 'already') return 'already';
  if (outcome === 'failed') return 'failed';
  /* Cancelled or closed: back to where it was, without a word. The GitHub
     window was the feedback. */
  return 'idle';
};

/* One star flow per page: the header and the landing button show the same
   phase, and a returned trip is read once for both. */
let currentPhase: Phase = 'idle';
let settled = false;
const listeners = new Set<() => void>();
const setPhase = (next: Phase) => {
  currentPhase = next;
  listeners.forEach((listener) => listener());
};
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};
const readPhase = () => currentPhase;
/* The prerender and the first client render agree on the idle button. */
const readServerPhase = (): Phase => 'idle';

const settleFromBrowser = () => {
  if (settled) return;
  settled = true;
  const returned = takeSourceStarResultFromLocation();
  if (returned) setPhase(phaseFor(returned));
  else if (hasSourceStarred()) setPhase('already');
};

const StarIcon = ({ filled, size }: { filled: boolean; size: number }) => (
  <Star
    aria-hidden='true'
    fill={filled ? 'currentColor' : 'none'}
    size={size}
    strokeWidth={2}
  />
);

/**
 * The GitHub star button: "☆ Star | 1,284", the shape GitHub's own button
 * has. Where the deployment can star for the visitor it asks GitHub once in
 * a popup and reports back without leaving the page; otherwise, and when
 * that fails, it is a link to the repository in the same shape.
 */
export default function SourceStarButton({
  className = '',
  size = 'md',
}: Props) {
  const { t } = useTranslation('marketing');
  const { canStar, url: sourceURL, stars } = usePublishedSource();
  const phase = useSyncExternalStore(subscribe, readPhase, readServerPhase);

  useEffect(settleFromBrowser, []);

  const done = phase === 'starred' || phase === 'already';
  /* A star made on this visit is not in the cached count yet. */
  const shownStars =
    stars === null ? null : phase === 'starred' ? stars + 1 : stars;
  const actionLabel =
    size === 'sm' ? t('landing.star.short') : t('landing.star.long');
  const doneLabel = t('landing.star.starred');
  const iconSize = size === 'sm' ? 13 : 14;
  const classes = [
    'lazu-star',
    `lazu-star--${size}`,
    done ? 'lazu-star--done' : '',
    className,
  ]
    .filter(Boolean)
    .join(' ');
  const ariaLabel =
    shownStars === null
      ? t(done ? 'landing.star.ariaDone' : 'landing.star.aria')
      : t(done ? 'landing.star.ariaDoneCount' : 'landing.star.ariaCount', {
          stars: formatStarCount(shownStars),
        });
  const body = (
    <>
      <span className='lazu-star__action'>
        <StarIcon filled={done} size={iconSize} />
        {/* Both labels hold the same cell, so starring never resizes it. */}
        <span className='lazu-star__label'>
          <span>{done ? doneLabel : actionLabel}</span>
          <span aria-hidden='true'>{done ? actionLabel : doneLabel}</span>
        </span>
        {phase === 'failed' ? (
          <ArrowUpRight aria-hidden='true' size={iconSize - 1} />
        ) : null}
      </span>
      {shownStars === null ? null : (
        <span className='lazu-star__count'>{formatStarCount(shownStars)}</span>
      )}
    </>
  );

  if (!canStar || phase === 'failed') {
    return (
      <a
        aria-label={ariaLabel}
        className={classes}
        href={sourceURL}
        rel='noopener noreferrer'
        target='_blank'
        title={t('landing.star.linkHint')}
      >
        {body}
      </a>
    );
  }

  const onClick = async () => {
    if (phase === 'pending' || done) return;
    setPhase('pending');
    setPhase(phaseFor(await starSource()));
  };

  return (
    <button
      aria-busy={phase === 'pending' || undefined}
      aria-label={ariaLabel}
      className={classes}
      /* It paints its own edge, which the global button reset would wipe. */
      data-owns-chrome=''
      disabled={done}
      onClick={onClick}
      title={done ? undefined : t('landing.star.hint')}
      type='button'
    >
      {body}
    </button>
  );
}
