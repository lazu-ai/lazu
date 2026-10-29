import { Star } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  hasSourceStarred,
  isSourceStarred,
  starSource,
  takeSourceStarResultFromLocation,
  type SourceStarOutcome,
} from '../../helpers/sourceStar';
import { usePublishedSource } from '../../hooks/common/usePublishedSource';

type Props = { className?: string };

type Phase = 'idle' | 'pending' | 'starred' | 'already' | 'failed';

const phaseFor = (outcome: SourceStarOutcome | null): Phase => {
  if (outcome === 'starred') return 'starred';
  if (outcome === 'already') return 'already';
  if (outcome === 'failed') return 'failed';
  return 'idle';
};

/**
 * "Star it on GitHub": a link to the repository until the deployment says it
 * can star for the visitor, then a button that asks GitHub once and reports
 * back without leaving the page. The prerendered page carries the link.
 */
export default function SourceStarButton({ className = '' }: Props) {
  const { t } = useTranslation('marketing');
  const { canStar: enabled, url: sourceURL } = usePublishedSource();
  const [phase, setPhase] = useState<Phase>('idle');

  /* After mount only: the prerender and the first client render agree on
     the link, then the browser's own record and a returned trip apply. */
  useEffect(() => {
    const returned = takeSourceStarResultFromLocation();
    if (returned) setPhase(phaseFor(returned));
    /* The browser's record never overrides what this visit just learned
       (development runs this effect twice). */ else if (hasSourceStarred())
      setPhase((current) => (current === 'idle' ? 'already' : current));
  }, []);

  const label =
    phase === 'pending'
      ? t('landing.final.starPending')
      : phase === 'starred'
        ? t('landing.final.starDone')
        : phase === 'already'
          ? t('landing.final.starAlready')
          : phase === 'failed'
            ? t('landing.final.starFailed')
            : t('landing.final.openSourceLink');

  if (!enabled || phase === 'failed') {
    return (
      <a
        className={className}
        href={sourceURL}
        rel='noopener noreferrer'
        target='_blank'
      >
        {label}
      </a>
    );
  }

  const done = isSourceStarred(
    phase === 'starred' ? 'starred' : phase === 'already' ? 'already' : null,
  );
  const onClick = async () => {
    if (phase === 'pending' || done) return;
    setPhase('pending');
    const outcome = await starSource();
    setPhase(phaseFor(outcome));
  };

  return (
    <button
      aria-busy={phase === 'pending' || undefined}
      className={`${className ? `${className} ` : ''}lazu-source-star${done ? ' lazu-source-star--done' : ''}`}
      disabled={done}
      onClick={onClick}
      type='button'
    >
      <Star aria-hidden='true' size={13} />
      {label}
    </button>
  );
}
