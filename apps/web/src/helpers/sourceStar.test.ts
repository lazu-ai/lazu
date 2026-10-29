import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  formatStarCount,
  hasSourceStarred,
  shownStarCount,
  isSourceStarred,
  parseSourceStarResult,
  takeSourceStarResultFromLocation,
} from './sourceStar';

/* The tests run in node: this is the slice of a browser the helpers read. */
type FakeWindow = {
  location: { pathname: string; search: string; hash: string };
  history: {
    state: unknown;
    replaceState: (state: unknown, title: string, url: string) => void;
  };
  localStorage: {
    getItem: (k: string) => string | null;
    setItem: (k: string, v: string) => void;
  };
};

const fakeWindow = (): FakeWindow => {
  const store = new Map<string, string>();
  const win: FakeWindow = {
    location: { pathname: '/', search: '', hash: '' },
    history: {
      state: null,
      replaceState: (_state, _title, url) => {
        const parsed = new URL(url, 'http://localhost');
        win.location.pathname = parsed.pathname;
        win.location.search = parsed.search;
        win.location.hash = parsed.hash;
      },
    },
    localStorage: {
      getItem: (key) => store.get(key) ?? null,
      setItem: (key, value) => void store.set(key, value),
    },
  };
  return win;
};

const visit = (url: string) =>
  (globalThis as { window: FakeWindow }).window.history.replaceState(
    null,
    '',
    url,
  );

beforeEach(() => {
  (globalThis as { window?: FakeWindow }).window = fakeWindow();
});

afterEach(() => {
  delete (globalThis as { window?: FakeWindow }).window;
});

describe('sourceStar', () => {
  it('accepts only the outcomes the API reports', () => {
    expect(parseSourceStarResult('starred')).toBe('starred');
    expect(parseSourceStarResult('already')).toBe('already');
    expect(parseSourceStarResult('cancelled')).toBe('cancelled');
    expect(parseSourceStarResult('failed')).toBe('failed');
    expect(parseSourceStarResult('closed')).toBeNull();
    expect(parseSourceStarResult(null)).toBeNull();
    expect(isSourceStarred('already')).toBe(true);
    expect(isSourceStarred('cancelled')).toBe(false);
  });

  it('takes a returned outcome off the address and remembers a star', () => {
    visit('/zh/faq?star=starred&x=1#top');
    expect(takeSourceStarResultFromLocation()).toBe('starred');
    const { location } = (globalThis as { window: FakeWindow }).window;
    expect(location.pathname + location.search).toBe('/zh/faq?x=1');
    expect(location.hash).toBe('#top');
    expect(hasSourceStarred()).toBe(true);
    expect(takeSourceStarResultFromLocation()).toBeNull();
  });

  it('leaves the address alone when nothing came back', () => {
    visit('/faq?x=1');
    expect(takeSourceStarResultFromLocation()).toBeNull();
    expect((globalThis as { window: FakeWindow }).window.location.search).toBe(
      '?x=1',
    );
    expect(hasSourceStarred()).toBe(false);
  });

  it('does not remember a declined or failed trip', () => {
    visit('/?star=cancelled');
    expect(takeSourceStarResultFromLocation()).toBe('cancelled');
    expect(hasSourceStarred()).toBe(false);
  });
});

describe('formatStarCount', () => {
  it('sets counts the way GitHub does', () => {
    expect(formatStarCount(0)).toBe('0');
    expect(formatStarCount(1284)).toBe('1,284');
    expect(formatStarCount(9999)).toBe('9,999');
    expect(formatStarCount(10_000)).toBe('10k');
    expect(formatStarCount(12_345)).toBe('12.3k');
    expect(formatStarCount(12_399)).toBe('12.3k');
  });
});

describe('shownStarCount', () => {
  it('shows the count only from ten stars up', () => {
    expect(shownStarCount(null)).toBeNull();
    expect(shownStarCount(0)).toBeNull();
    expect(shownStarCount(9)).toBeNull();
    expect(shownStarCount(10)).toBe(10);
    expect(shownStarCount(1284)).toBe(1284);
  });
});
