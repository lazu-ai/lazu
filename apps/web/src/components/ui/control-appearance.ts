import { createContext, useContext } from 'react';

// React context follows portals; an ancestor CSS selector does not. Carry only
// the selected control recipe, never the page's layout or heading styles.
export const ControlAppearanceContext = createContext<'default' | 'review'>(
  'default',
);

export function useControlAppearanceClass() {
  return useContext(ControlAppearanceContext) === 'review'
    ? 'console-review-controls'
    : undefined;
}
