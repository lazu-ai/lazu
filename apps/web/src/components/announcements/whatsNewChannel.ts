// The open channel between the What's New entry points (version plate in the
// sidebar brand row, version plate in the mobile topbar) and the panel itself.
//
// It lives in its own module so an entry point can request the panel without
// importing the panel — the panel pulls in the markdown renderer and the whole
// announcement timeline, and the sidebar has no business carrying that weight.

import type { Announcement } from '../../helpers/announcement';

export const WHATS_NEW_OPEN_EVENT = 'lazu:open-whats-new';

export type WhatsNewOpenDetail = {
  pinnedAnnouncement?: Announcement | null;
};

export const openWhatsNewDialog = (detail?: WhatsNewOpenDetail) => {
  window.dispatchEvent(new CustomEvent(WHATS_NEW_OPEN_EVENT, { detail }));
};
