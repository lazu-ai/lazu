/* A tab that outlives a deploy asks for chunks that no longer exist.
 *
 * Every release replaces the web image, and with it every content-hashed file
 * under /assets. A console left open across that release still holds the old
 * entry module, so the first click into a route it has not visited yet imports
 * a hash the server now answers with 404. React.lazy rethrows that, and the
 * crash boundary used to paint "the view crashed" over what is really "a newer
 * version is out".
 *
 * The cure is a full load of the address the reader was already going to: the
 * fresh index.html names the new chunks. One attempt per window, though — if
 * the chunk is still missing right after a reload, something is genuinely
 * broken and the crash page should say so instead of looping.
 */

const RELOAD_KEY = 'lazu:stale-chunk-reload-at';
const RELOAD_WINDOW_MS = 30_000;

/* Chromium, Firefox and WebKit each word a failed dynamic import differently;
 * Vite's own CSS preloader adds a fourth. */
const STALE_CHUNK_MESSAGES = [
  'Failed to fetch dynamically imported module',
  'error loading dynamically imported module',
  'Importing a module script failed',
  'Unable to preload CSS',
];

export function isStaleChunkError(error: unknown): boolean {
  if (!(error instanceof Error)) return false;
  return STALE_CHUNK_MESSAGES.some((message) =>
    error.message.includes(message),
  );
}

/** Whether a reload may still be tried: false when one already ran moments
 * ago, or when there is no storage to remember that it did. Side-effect free,
 * so a render can ask it. */
export function canReloadForStaleChunk(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    const last = Number(window.sessionStorage.getItem(RELOAD_KEY));
    return !last || Date.now() - last >= RELOAD_WINDOW_MS;
  } catch {
    // Without storage there is no loop guard, and a reload that fails the
    // same way would reload forever. Show the crash page instead.
    return false;
  }
}

/** Reload once to pick up the new release. Returns false when that is not
 * allowed, so the caller shows the failure instead. */
export function reloadForStaleChunk(): boolean {
  if (!canReloadForStaleChunk()) return false;
  try {
    window.sessionStorage.setItem(RELOAD_KEY, String(Date.now()));
  } catch {
    return false;
  }
  window.location.reload();
  return true;
}
