export type SidebarVisibilityItem = {
  itemKey: string;
};

export const shouldRenderSidebarSection = (
  items: readonly SidebarVisibilityItem[],
  hasVisibleModules: boolean,
) =>
  items.length > 0 &&
  (hasVisibleModules || items.some((item) => item.itemKey === 'docs'));
