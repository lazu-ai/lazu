import { describe, expect, it } from 'vitest';
import {
  areAllSelectableTokensSelected,
  getSelectableTokenIds,
  retainVisibleTokenIds,
} from './tokenSelection';

describe('token selection under client-side status filters', () => {
  const visible = [{ id: 1 }, { id: 3 }];

  it('selects and evaluates only visible token rows', () => {
    expect(getSelectableTokenIds(visible)).toEqual([1, 3]);
    expect(areAllSelectableTokensSelected(visible, [1, 3])).toBe(true);
    expect(areAllSelectableTokensSelected(visible, [1, 2])).toBe(false);
  });

  it('drops hidden token ids before bulk actions can use them', () => {
    expect(retainVisibleTokenIds([1, 2, 3], visible)).toEqual([1, 3]);
  });

  it('ignores rows without a stable id', () => {
    expect(getSelectableTokenIds([{ id: 1 }, {}])).toEqual([1]);
  });
});
