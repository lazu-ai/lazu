import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getCommunityPlatform,
  getCommunityURL,
  getDocsBaseURL,
  getDocsPageURL,
  getDocsURL,
  getFooterModels,
  getSourceURL,
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

  it("links a docs page in the reader's language", () => {
    vi.stubEnv('VITE_BRAND_DOCS_URL', '');
    expect(getDocsPageURL('compare/litellm')).toBe(
      'https://lazu.ai/docs/compare/litellm',
    );
    expect(getDocsPageURL('/compare/litellm', 'zh-TW')).toBe(
      'https://lazu.ai/docs/zh-TW/compare/litellm',
    );
    expect(getDocsPageURL('compare/litellm', 'fr')).toBe(
      'https://lazu.ai/docs/compare/litellm',
    );
  });

  it('lists footer models only when the build names them', () => {
    vi.stubEnv('VITE_BRAND_FOOTER_MODELS', '');
    expect(getFooterModels()).toEqual([]);
    vi.stubEnv(
      'VITE_BRAND_FOOTER_MODELS',
      ' anthropic/claude-opus-5-5=Claude Opus 5.5, baai/BAAI/bge-m3 = BGE-M3 ,' +
        'no-label=,=Nameless,flat=Flat,../etc=Up,https://x.test/a=Away',
    );
    expect(getFooterModels()).toEqual([
      { path: '/models/anthropic/claude-opus-5-5', label: 'Claude Opus 5.5' },
      { path: '/models/baai/BAAI/bge-m3', label: 'BGE-M3' },
    ]);
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

  it('names the chat app a community link opens, when it knows the host', () => {
    expect(getCommunityPlatform('https://t.me/example_group')).toBe('Telegram');
    expect(getCommunityPlatform('https://www.discord.gg/abc')).toBe('Discord');
    expect(getCommunityPlatform('https://forum.example.com/')).toBeNull();
    expect(getCommunityPlatform('https://t.me.example.com/')).toBeNull();
    expect(getCommunityPlatform(null)).toBeNull();
  });

  it('points the GitHub entry at the build, the deployment, then upstream', () => {
    vi.stubEnv('VITE_BRAND_SOURCE_URL', '');
    expect(getSourceURL()).toBe('https://github.com/lazu-ai/lazu');
    expect(getSourceURL('https://github.com/example/fork/')).toBe(
      'https://github.com/example/fork',
    );
    expect(getSourceURL('not a url')).toBe('https://github.com/lazu-ai/lazu');
    vi.stubEnv('VITE_BRAND_SOURCE_URL', 'https://codeberg.org/example/lazu');
    expect(getSourceURL('https://github.com/example/fork')).toBe(
      'https://codeberg.org/example/lazu',
    );
  });
});
