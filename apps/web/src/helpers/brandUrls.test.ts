import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getCommunityURL,
  getDocsBaseURL,
  getDocsURL,
  getSupportEmail,
} from './brandUrls';

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

  it('names the brand support address unless the build sets another', () => {
    vi.stubEnv('VITE_BRAND_SUPPORT_EMAIL', '');
    expect(getSupportEmail()).toBe('support@lazu.ai');
    vi.stubEnv('VITE_BRAND_SUPPORT_EMAIL', ' help@example.com ');
    expect(getSupportEmail()).toBe('help@example.com');
  });

  it('shows a community link only when the build names an http(s) URL', () => {
    vi.stubEnv('VITE_BRAND_COMMUNITY_URL', '');
    expect(getCommunityURL()).toBeNull();
    vi.stubEnv('VITE_BRAND_COMMUNITY_URL', 'javascript:alert(1)');
    expect(getCommunityURL()).toBeNull();
    vi.stubEnv('VITE_BRAND_COMMUNITY_URL', 'https://t.me/example_group');
    expect(getCommunityURL()).toBe('https://t.me/example_group');
  });
});
