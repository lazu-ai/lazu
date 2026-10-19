import { useSyncExternalStore } from 'react';

/* Read a brand colour token for libraries that need a literal colour (VChart
 * specs, canvas drawing).
 *
 * The dark palette is declared on `body[theme-mode="dark"]`, not on `:root`,
 * so the token has to be read from <body>: reading it from <html> returns the
 * light value in the dark theme. Call this when the spec is built, not at
 * module load — the stylesheet may not have applied yet when a lazy chunk
 * evaluates.
 */
export function readBrandToken(name: `--${string}`, fallback: string): string {
  if (typeof window === 'undefined' || !document.body) return fallback;
  const value = getComputedStyle(document.body).getPropertyValue(name).trim();
  return value || fallback;
}

const subscribeToBodyTheme = (onChange: () => void) => {
  if (typeof MutationObserver === 'undefined' || !document.body) {
    return () => undefined;
  }
  const observer = new MutationObserver(onChange);
  observer.observe(document.body, {
    attributeFilter: ['theme-mode'],
    attributes: true,
  });
  return () => observer.disconnect();
};

const bodyThemeSnapshot = () => document.body?.getAttribute('theme-mode') ?? '';

/** The theme <body> is painted in right now. ThemeProvider writes
 *  `theme-mode` in an effect, after its children have rendered, so a spec
 *  keyed on the context's theme would read the previous palette. Key colour
 *  reads on this instead: it changes only once the attribute has. */
export function useBodyThemeMode(): string {
  return useSyncExternalStore(
    subscribeToBodyTheme,
    bodyThemeSnapshot,
    () => '',
  );
}
