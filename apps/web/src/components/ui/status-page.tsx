/* Status page — the full-screen states a reader lands on instead of the page
 * they asked for: 404, 403, a crash, a feature that is switched off, and the
 * one-shot outcomes (an invite, a payment, a removal) that have nothing else
 * to show.
 *
 * Every one is the same object, centred: a large status mark, one title, one
 * sentence, at most one pill of context and at most two actions. The mark is
 * the HTTP code where one exists, otherwise a single English word (Error, Off,
 * Expired, Paid …). It is the only place the display serif appears, because
 * the serif has Latin glyphs only — a Chinese title set in it falls back to
 * Songti. Titles and prose stay on the sans.
 *
 * Card-scale states (loading, empty, an error inside a surface) are a
 * different object with no figure at all; see loading-state / empty-state /
 * error-state. Waiting never gets a status page.
 */
import { cn } from '../../lib/cn';
import type { ReactNode } from 'react';

type StatusPageProps = {
  /* Primary way out. */
  action?: ReactNode;
  className?: string;
  description: ReactNode;
  /* Standalone routes paint the Yunmu ground themselves; states rendered
     inside the console shell inherit the ground it already paints. */
  ground?: boolean;
  /* One line under the actions, e.g. "Did you mean Models?". Leave it out
     unless it helps; an empty hint is noise. */
  hint?: ReactNode;
  /* A sheet that replaces the current view without a navigation — the crash
     boundary — must announce itself, because no route change will. */
  live?: boolean;
  /* HTTP code, or one English word. */
  mark: string;
  /* The one fact that lets the reader match this page to what they did: the
     address, a trade number, a project. */
  pill?: ReactNode;
  secondaryAction?: ReactNode;
  title: ReactNode;
};

export function StatusPage({
  action,
  className,
  description,
  ground = false,
  hint,
  live = false,
  mark,
  pill,
  secondaryAction,
  title,
}: StatusPageProps) {
  const isCode = /^\d+$/.test(mark);
  return (
    <div
      className={cn(
        'lazu-status-page',
        ground && 'lazu-status-page--ground',
        className,
      )}
    >
      <section
        className='lazu-status-page__body'
        role={live ? 'alert' : undefined}
      >
        <div
          aria-hidden='true'
          className={cn(
            'lazu-status-page__mark',
            !isCode && 'lazu-status-page__mark--word',
          )}
        >
          {mark}
        </div>
        <h1 className='lazu-status-page__title'>{title}</h1>
        <p className='lazu-status-page__description'>{description}</p>
        {pill ? <div className='lazu-status-page__pill'>{pill}</div> : null}
        {action || secondaryAction ? (
          <div className='lazu-status-page__actions'>
            {action}
            {secondaryAction}
          </div>
        ) : null}
        {hint ? <div className='lazu-status-page__hint'>{hint}</div> : null}
      </section>
    </div>
  );
}
