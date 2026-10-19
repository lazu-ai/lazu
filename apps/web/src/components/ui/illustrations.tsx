// Inline SVG empty-state / error-state art used across dashboards,
// pricing tables, and admin lists. Originally these replaced the Semi
// UI `Illustration*` set during the frontend rebuild (PR #1) — Semi
// itself is gone, but the names + signatures are kept so the rebuild
// PR didn't have to touch every consumer at once. ~13 files still
// import from here.
//
// For brand-new empty-state UI, prefer composing `<EmptyState />` /
// `<ErrorState />` from Lazu UI directly rather than adding more
// hand-rolled illustrations here.
import { FileQuestion, Inbox, Wrench } from 'lucide-react';
import React from 'react';

type IllustrationProps = {
  style?: React.CSSProperties;
};

type IllustrationFrameProps = IllustrationProps & {
  children: React.ReactNode;
};

function IllustrationFrame({ children, style }: IllustrationFrameProps) {
  return (
    <div className='lazu-illustration' style={style} aria-hidden='true'>
      {children}
    </div>
  );
}

export function IllustrationConstruction({ style }: IllustrationProps) {
  return (
    <IllustrationFrame style={style}>
      <Wrench size='46%' />
    </IllustrationFrame>
  );
}

export function IllustrationConstructionDark({ style }: IllustrationProps) {
  return <IllustrationConstruction style={style} />;
}

export function IllustrationNoContent({ style }: IllustrationProps) {
  return (
    <IllustrationFrame style={style}>
      <Inbox size='46%' />
    </IllustrationFrame>
  );
}

export function IllustrationNoContentDark({ style }: IllustrationProps) {
  return <IllustrationNoContent style={style} />;
}

export function IllustrationNoResult({ style }: IllustrationProps) {
  return (
    <IllustrationFrame style={style}>
      <FileQuestion size='46%' />
    </IllustrationFrame>
  );
}

export function IllustrationNoResultDark({ style }: IllustrationProps) {
  return <IllustrationNoResult style={style} />;
}
