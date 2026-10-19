export type SelectableToken = {
  id?: number | string;
};

export const getSelectableTokenIds = (
  rows: readonly SelectableToken[],
): Array<number | string> =>
  rows.flatMap((row) => (row.id === undefined ? [] : [row.id]));

export const areAllSelectableTokensSelected = (
  rows: readonly SelectableToken[],
  selectedIds: readonly (number | string)[],
) => {
  const ids = getSelectableTokenIds(rows);
  if (ids.length === 0) return false;
  const selected = new Set(selectedIds.map(String));
  return ids.every((id) => selected.has(String(id)));
};

export const retainVisibleTokenIds = (
  selectedIds: readonly (number | string)[],
  visibleRows: readonly SelectableToken[],
): Array<number | string> => {
  const visible = new Set(getSelectableTokenIds(visibleRows).map(String));
  const next = selectedIds.filter((id) => visible.has(String(id)));
  if (
    next.length === selectedIds.length &&
    next.every((id, index) => id === selectedIds[index])
  ) {
    return selectedIds as Array<number | string>;
  }
  return next;
};
