import { createContext } from 'react';

/* The usage page head's action area. The active view (the request log) owns
   its own tools — 列 and 导出 — and portals them here, so they sit in the page
   head beside the title instead of in the filter row. */
export const UsageHeadSlotContext = createContext<HTMLElement | null>(null);
