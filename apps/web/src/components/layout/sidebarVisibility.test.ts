import { describe, expect, it } from 'vitest';
import { shouldRenderSidebarSection } from './sidebarVisibility';

describe('shouldRenderSidebarSection', () => {
  it('keeps the workspace section visible when docs is its only item', () => {
    expect(shouldRenderSidebarSection([{ itemKey: 'docs' }], false)).toBe(true);
  });

  it('hides empty sections without visible modules', () => {
    expect(shouldRenderSidebarSection([], false)).toBe(false);
    expect(shouldRenderSidebarSection([{ itemKey: 'wallet' }], false)).toBe(
      false,
    );
  });
});
