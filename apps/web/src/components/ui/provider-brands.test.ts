import { describe, expect, it } from 'vitest';
import {
  resolveProviderBrand,
  resolveProviderBrandDescriptor,
} from './provider-brands';

describe('provider brand resolution', () => {
  it.each([
    ['claude-sonnet-4-6', 'Anthropic'],
    ['gemini-2.5-pro', 'Google'],
    ['glm-4.5', 'Zhipu AI'],
    ['qwen3-max', 'Alibaba Cloud'],
    ['kimi-k2', 'Moonshot AI'],
    ['llama-4-maverick', 'Meta'],
    ['doubao-seed-1-6', 'ByteDance'],
    ['seed-2.0-pro', 'ByteDance'],
    ['seed-2.1-turbo', 'ByteDance'],
    ['dola-seed-2-1-turbo-260628', 'ByteDance'],
  ])('maps %s to its actual company', (id, companyName) => {
    const brand = resolveProviderBrand({ id });
    expect(brand.companyName).toBe(companyName);
    expect(brand.logoSrc).toMatch(/(?:\.svg$|^data:image\/svg\+xml)/);
  });

  it('uses model identity before the serving channel metadata', () => {
    expect(
      resolveProviderBrand({ id: 'seed-2.1-turbo', provider: 'OpenAI' }).id,
    ).toBe('seed');
    expect(
      resolveProviderBrand({
        id: 'claude-sonnet-4-6',
        provider: 'OpenAI',
      }).companyName,
    ).toBe('Anthropic');
    expect(
      resolveProviderBrand({
        id: 'llama-3-sonar-large-32k-chat',
        provider: 'Perplexity',
      }).companyName,
    ).toBe('Meta');
  });

  it.each(['claude-sonnet-4-6', 'gpt-4o-mini', 'kimi-k2'])(
    'keeps the monochrome mark for %s inline and themeable',
    (id) => {
      const brand = resolveProviderBrand({ id });
      expect(brand.monochrome).toBe(true);
      expect(brand.logoSvg).toContain('<svg');
      expect(brand.logoSvg).toContain('currentColor');
    },
  );

  it('keeps named custom companies but does not invent an official logo', () => {
    expect(resolveProviderBrand({ provider: 'Acme Gateway' })).toEqual({
      companyName: 'Acme Gateway',
      id: 'custom-acme-gateway',
    });
  });

  it('uses explicit custom provider metadata instead of a generic family', () => {
    expect(
      resolveProviderBrand({ family: 'Custom', provider: 'Acme Gateway' }),
    ).toEqual({
      companyName: 'Acme Gateway',
      id: 'custom-acme-gateway',
    });
  });

  it('keeps non-Latin custom providers distinct', () => {
    expect(resolveProviderBrand({ provider: '云厂商甲' }).id).toBe(
      'custom-云厂商甲',
    );
    expect(resolveProviderBrand({ provider: '云厂商乙' }).id).toBe(
      'custom-云厂商乙',
    );
  });

  it.each([
    ['01.AI', '01.AI'],
    ['01.AI.size=large', '01.AI'],
    ['X.AI.size=small', 'xAI'],
    ['Z.AI.size=large', 'Zhipu AI'],
    ['yi-spark', '01.AI'],
    ['360GPT_S2_V9', '360 AI'],
  ])('resolves dotted and legacy descriptor %s', (descriptor, companyName) => {
    expect(resolveProviderBrandDescriptor(descriptor).companyName).toBe(
      companyName,
    );
  });

  it('still resolves legacy icon descriptors', () => {
    expect(
      resolveProviderBrandDescriptor('claude.size=large').companyName,
    ).toBe('Anthropic');
  });
});
