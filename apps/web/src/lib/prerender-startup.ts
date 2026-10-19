/** If initialization rejects, keep the complete server snapshot usable.
 * Rendering the app with unavailable translations can replace it with keys. */
export function startPrerender(
  ready: Promise<unknown>,
  root: HTMLElement,
  render: () => void,
): Promise<void> {
  return ready.then(render, () => {
    root.style.removeProperty('visibility');
  });
}
