import { describe, expect, it } from 'vitest';
import { formatVersionLabel } from './versionLabel';

describe('formatVersionLabel', () => {
  it('shortens a deployed commit SHA to its 7-character form', () => {
    expect(formatVersionLabel('ea8fcffa2b604266eec4962b7d84c0902c826f18')).toBe(
      'ea8fcff',
    );
  });

  it('leaves a semver build alone', () => {
    expect(formatVersionLabel('v2.6.0')).toBe('v2.6.0');
    expect(formatVersionLabel('v0.0.0')).toBe('v0.0.0');
  });

  it('prefixes a bare version so it never reads as a stray number', () => {
    expect(formatVersionLabel('2.6.0')).toBe('v2.6.0');
  });

  it('keeps short hex strings intact — they are versions, not hashes', () => {
    // "0.1.2" style builds can be all-hex once dots are dropped; only strings
    // long enough to actually be a hash get truncated.
    expect(formatVersionLabel('abc123')).toBe('vabc123');
  });

  it('renders nothing when the gateway reports no version', () => {
    expect(formatVersionLabel(undefined)).toBe('');
    expect(formatVersionLabel('')).toBe('');
    expect(formatVersionLabel('   ')).toBe('');
    expect(formatVersionLabel(42)).toBe('');
  });
});
