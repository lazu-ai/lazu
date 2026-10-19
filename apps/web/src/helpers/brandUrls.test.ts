import { afterEach, describe, expect, it, vi } from 'vitest';
import { getDocsBaseURL, getDocsURL } from './brandUrls';

describe('brand URLs', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('uses the canonical docs URL by default', () => {
    vi.stubEnv('VITE_BRAND_DOCS_URL', '');
    expect(getDocsBaseURL()).toBe('https://lazu.ai/docs');
    expect(getDocsURL('changelog')).toBe('https://lazu.ai/docs/changelog');
  });

  it('allows preview builds to override the docs origin', () => {
    vi.stubEnv('VITE_BRAND_DOCS_URL', 'https://docs-preview.example.com/');
    expect(getDocsURL('/changelog')).toBe(
      'https://docs-preview.example.com/changelog',
    );
  });
});
