import { expect, it, vi } from 'vitest';
import { startPrerender } from './prerender-startup';

it('reveals the intact server snapshot if initialization rejects', async () => {
  const removeProperty = vi.fn();
  const root = { style: { removeProperty } } as unknown as HTMLElement;
  const render = vi.fn();
  await startPrerender(
    Promise.reject(new Error('locale unavailable')),
    root,
    render,
  );
  expect(removeProperty).toHaveBeenCalledWith('visibility');
  expect(render).not.toHaveBeenCalled();
});

it('keeps the snapshot hidden until successful initialization renders its replacement', async () => {
  const removeProperty = vi.fn();
  const root = { style: { removeProperty } } as unknown as HTMLElement;
  let resolve = () => {};
  const ready = new Promise<void>((done) => {
    resolve = done;
  });
  const render = vi.fn();
  const startup = startPrerender(ready, root, render);
  await Promise.resolve();
  expect(render).not.toHaveBeenCalled();
  expect(removeProperty).not.toHaveBeenCalled();
  resolve();
  await startup;
  expect(render).toHaveBeenCalledOnce();
});
