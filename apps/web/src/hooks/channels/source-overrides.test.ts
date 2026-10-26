import { describe, expect, it } from 'vitest';
import {
  compactOverride,
  fitsStatusCodeMapping,
  isHeaderOverride,
  isParamOverride,
  isStatusCodeMapping,
  overrideFormText,
} from './source-overrides';

describe('source request overrides', () => {
  it('accepts empty text as "no override"', () => {
    for (const check of [
      isParamOverride,
      isHeaderOverride,
      isStatusCodeMapping,
    ]) {
      expect(check('')).toBe(true);
      expect(check('  ')).toBe(true);
      expect(check(undefined)).toBe(true);
    }
    expect(compactOverride('  ')).toBe('');
    expect(compactOverride('{}')).toBe('');
  });

  it('wants a JSON object for each field', () => {
    expect(isParamOverride('{"temperature":0.2}')).toBe(true);
    expect(
      isParamOverride('{"operations":[{"path":"top_p","mode":"delete"}]}'),
    ).toBe(true);
    expect(isParamOverride('[1]')).toBe(false);
    expect(isParamOverride('{oops')).toBe(false);
  });

  it('wants header values as strings', () => {
    expect(isHeaderOverride('{"X-Api-Key":"{api_key}"}')).toBe(true);
    expect(isHeaderOverride('{"X-Retries":3}')).toBe(false);
    expect(isHeaderOverride('{" ":"x"}')).toBe(false);
  });

  it('wants status codes on both sides, as strings', () => {
    expect(isStatusCodeMapping('{"400":"500","429":"503"}')).toBe(true);
    expect(isStatusCodeMapping('{"400":500}')).toBe(false);
    expect(isStatusCodeMapping('{"bad":"500"}')).toBe(false);
    expect(isStatusCodeMapping('{"400":"700"}')).toBe(false);
  });

  it('keeps the status mapping within its column', () => {
    const many = Object.fromEntries(
      Array.from({ length: 40 }, (_, i) => [String(400 + i), '500']),
    );
    expect(fitsStatusCodeMapping(JSON.stringify(many, null, 2))).toBe(true);
    const tooMany = `{${Array.from({ length: 100 }, (_, i) => `"${100 + i}":"500"`).join(',')}}`;
    expect(fitsStatusCodeMapping(tooMany)).toBe(false);
  });

  it('round-trips a stored value through the form unchanged', () => {
    const stored = '{"X-Api-Key":"{api_key}","X-Org":"lazu"}';
    const shown = overrideFormText(stored);
    expect(shown).toContain('\n');
    expect(compactOverride(shown)).toBe(stored);
    expect(overrideFormText(null)).toBe('');
    expect(overrideFormText('{}')).toBe('');
  });
});
