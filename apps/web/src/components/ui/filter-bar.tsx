import React from 'react';
import { cn } from '../../lib/cn';

type FilterBarProps = React.FormHTMLAttributes<HTMLFormElement> & {
  /* Set false when the children bring their own <form> (forms can't nest). */
  searchForm?: boolean;
};

/* A filter bar is a form so that Enter in any of its fields runs the search:
   implicit submission clicks the bar's submit button, which is what a reader
   pressing Enter after typing a keyword expects. The native submit itself is
   always swallowed — the button's own click handler does the work. */
export function FilterBar({
  children,
  className,
  onKeyDownCapture,
  onSubmit,
  searchForm = true,
  ...props
}: FilterBarProps) {
  if (!searchForm) {
    return (
      <div
        className={cn('lazu-filter-bar', className)}
        {...(props as React.HTMLAttributes<HTMLDivElement>)}
      >
        {children}
      </div>
    );
  }
  return (
    <form
      role='search'
      noValidate
      className={cn('lazu-filter-bar', className)}
      onKeyDownCapture={(event) => {
        submitFilterFormFromSelect(event);
        onKeyDownCapture?.(event);
      }}
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit?.(event);
      }}
      {...props}
    >
      {children}
    </form>
  );
}

/* Browsers only submit a form on Enter from a text field. Having just picked a
   condition, focus sits on the closed select trigger, where Enter would reopen
   the list — in a filter form it should run the search like everywhere else.
   Attach as onKeyDownCapture so it runs before the trigger's own handler. */
export function submitFilterFormFromSelect(
  event: React.KeyboardEvent<HTMLFormElement>,
) {
  if (event.key !== 'Enter' || event.nativeEvent.isComposing) return;
  const target = event.target as HTMLElement;
  const trigger = target.closest?.('.lazu-select-trigger');
  if (!trigger || trigger.getAttribute('aria-expanded') === 'true') return;
  const submitter = event.currentTarget.querySelector<HTMLButtonElement>(
    'button[type="submit"]:not(:disabled)',
  );
  if (!submitter) return;
  event.preventDefault();
  event.stopPropagation();
  submitter.click();
}

/* A stray leading or trailing space is never part of what someone is looking
   for, but a LIKE '%name %' query matches nothing. Trim every text filter at
   the moment the search is submitted. */
export function trimFilterValues<T extends object>(filters: T): T {
  const next = { ...filters } as Record<string, unknown>;
  for (const [key, value] of Object.entries(next)) {
    if (typeof value === 'string') next[key] = value.trim();
  }
  return next as T;
}
