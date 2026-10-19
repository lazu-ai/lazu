import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  canReloadForStaleChunk,
  isStaleChunkError,
  reloadForStaleChunk,
} from './stale-chunk';

describe('stale chunk recovery', () => {
  const reload = vi.fn();

  beforeEach(() => {
    const store = new Map<string, string>();
    vi.stubGlobal('window', {
      location: { reload },
      sessionStorage: {
        getItem: (key: string) => store.get(key) ?? null,
        setItem: (key: string, value: string) => store.set(key, value),
      },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    reload.mockReset();
  });

  it('recognises each browser wording of a missing chunk', () => {
    for (const message of [
      'Failed to fetch dynamically imported module: https://lazu.ai/assets/index-abc.js',
      'error loading dynamically imported module: https://lazu.ai/assets/index-abc.js',
      'Importing a module script failed.',
      'Unable to preload CSS for /assets/index-abc.css',
    ]) {
      expect(isStaleChunkError(new TypeError(message))).toBe(true);
    }
    expect(isStaleChunkError(new TypeError('x is undefined'))).toBe(false);
    expect(
      isStaleChunkError('Failed to fetch dynamically imported module'),
    ).toBe(false);
  });

  it('reloads once, then lets the crash page through', () => {
    expect(canReloadForStaleChunk()).toBe(true);
    expect(reloadForStaleChunk()).toBe(true);
    expect(reload).toHaveBeenCalledTimes(1);

    expect(canReloadForStaleChunk()).toBe(false);
    expect(reloadForStaleChunk()).toBe(false);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('never reloads without storage to remember the attempt', () => {
    vi.stubGlobal('window', {
      location: { reload },
      get sessionStorage(): Storage {
        throw new Error('SecurityError');
      },
    });
    expect(reloadForStaleChunk()).toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });
});
