// Starring the published source from the site.
//
// The page opens the API's start route in a popup. GitHub asks the visitor
// to authorize a star-only App, the API stars the repository with the
// one-time token, and the popup lands on /auth/source-star, which reports
// the outcome back here and closes. When the popup is blocked the whole
// page makes the trip instead and comes back with ?star=<result>.

const SOURCE_STAR_START_PATH = '/api/source/star/start';
export const SOURCE_STAR_MESSAGE_TYPE = 'lazu:source-star';

export type SourceStarResult = 'starred' | 'already' | 'cancelled' | 'failed';
/** What the page learns when the popup went away without reporting. */
export type SourceStarOutcome = SourceStarResult | 'closed';

const RESULTS: ReadonlySet<string> = new Set([
  'starred',
  'already',
  'cancelled',
  'failed',
]);

export const parseSourceStarResult = (
  raw: string | null | undefined,
): SourceStarResult | null =>
  raw && RESULTS.has(raw) ? (raw as SourceStarResult) : null;

export const isSourceStarred = (result: SourceStarOutcome | null) =>
  result === 'starred' || result === 'already';

/* A star is kept per browser so the button stays "starred" on the next
   visit; the repository itself is the record, this is only a convenience. */
const STORAGE_KEY = 'lazu.source-starred';

const rememberSourceStarred = () => {
  try {
    window.localStorage.setItem(STORAGE_KEY, '1');
  } catch {
    /* storage may be unavailable; the button simply asks again */
  }
};

export const hasSourceStarred = () => {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
};

/** The page's own path, where the flow returns when it has no popup. */
const currentPagePath = () => window.location.pathname + window.location.search;

const sourceStarStartURL = (popup: boolean) =>
  `${SOURCE_STAR_START_PATH}?return=${encodeURIComponent(currentPagePath())}${
    popup ? '&popup=1' : ''
  }`;

const POPUP_WIDTH = 600;
const POPUP_HEIGHT = 720;

const popupFeatures = () => {
  const left = Math.max(
    0,
    window.screenX + (window.outerWidth - POPUP_WIDTH) / 2,
  );
  const top = Math.max(
    0,
    window.screenY + (window.outerHeight - POPUP_HEIGHT) / 2,
  );
  return `popup=yes,width=${POPUP_WIDTH},height=${POPUP_HEIGHT},left=${Math.round(left)},top=${Math.round(top)}`;
};

/**
 * Starts the star flow and resolves with its outcome. When the browser
 * refuses the popup the page navigates away and the promise never settles.
 */
export const starSource = (): Promise<SourceStarOutcome> => {
  const popup = window.open(
    sourceStarStartURL(true),
    'lazu-source-star',
    popupFeatures(),
  );
  if (!popup) {
    window.location.assign(sourceStarStartURL(false));
    return new Promise(() => {});
  }
  return new Promise((resolve) => {
    let closedPoll: number | undefined;
    const finish = (outcome: SourceStarOutcome) => {
      window.removeEventListener('message', onMessage);
      window.clearInterval(closedPoll);
      if (isSourceStarred(outcome)) rememberSourceStarred();
      resolve(outcome);
    };
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.source !== popup) {
        return;
      }
      const data = event.data as { type?: unknown; result?: unknown } | null;
      if (!data || data.type !== SOURCE_STAR_MESSAGE_TYPE) return;
      finish(
        parseSourceStarResult(
          typeof data.result === 'string' ? data.result : null,
        ) ?? 'failed',
      );
    };
    window.addEventListener('message', onMessage);
    closedPoll = window.setInterval(() => {
      if (popup.closed) finish('closed');
    }, 500);
  });
};

/**
 * The outcome a full-page trip came back with, taken off the address so a
 * reload does not repeat it.
 */
export const takeSourceStarResultFromLocation = (): SourceStarResult | null => {
  const params = new URLSearchParams(window.location.search);
  const result = parseSourceStarResult(params.get('star'));
  if (!result) return null;
  params.delete('star');
  const query = params.toString();
  window.history.replaceState(
    window.history.state,
    '',
    window.location.pathname +
      (query ? `?${query}` : '') +
      window.location.hash,
  );
  if (isSourceStarred(result)) rememberSourceStarred();
  return result;
};
