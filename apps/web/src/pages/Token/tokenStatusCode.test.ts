/* The project token payload types `status` as a string and carries the legacy
 * integer beside it as `status_code`. This page reads the integer, so a Key
 * must resolve the same whichever shape the server sent — otherwise every Key
 * renders as disabled and the enable toggle offers the wrong verb. */
import { describe, expect, it } from 'vitest';
import { tokenStatusCode } from './tokenStatusCode';

describe('tokenStatusCode', () => {
  it('reads the legacy integer status', () => {
    expect(tokenStatusCode({ status: 1 })).toBe(1);
    expect(tokenStatusCode({ status: 2 })).toBe(2);
  });

  it('reads the contract string status', () => {
    expect(tokenStatusCode({ status: 'enabled' })).toBe(1);
    expect(tokenStatusCode({ status: 'disabled' })).toBe(2);
    expect(tokenStatusCode({ status: 'expired' })).toBe(3);
    expect(tokenStatusCode({ status: 'exhausted' })).toBe(4);
  });

  it('prefers status_code when the payload carries both', () => {
    expect(tokenStatusCode({ status: 'enabled', status_code: 1 })).toBe(1);
    expect(tokenStatusCode({ status: 'disabled', status_code: 2 })).toBe(2);
  });

  it('returns 0 rather than guessing when there is no readable status', () => {
    expect(tokenStatusCode({})).toBe(0);
    expect(tokenStatusCode(null)).toBe(0);
    expect(tokenStatusCode({ status: 'retired' })).toBe(0);
  });
});
