import { describe, expect, it } from 'vitest';
import { cacheRatio } from './session-format';

describe('cacheRatio', () => {
  it('is the share of input served from cache', () => {
    expect(cacheRatio(750, 1_000)).toBe(0.75);
  });

  it('caps sessions that hold more cache reads than input at 100%', () => {
    // 44b04745-…: folded while Anthropic prompt tokens left cache reads out.
    expect(cacheRatio(40_415_562, 5_891)).toBe(1);
  });

  it('has no share without input', () => {
    expect(cacheRatio(0, 0)).toBeNull();
  });
});
