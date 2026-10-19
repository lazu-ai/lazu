import { describe, expect, it } from 'vitest';
import { rateLimitRetryAfter } from './query-client';

const rejected = (status: number, details: Record<string, unknown> = {}) => ({
  response: {
    status,
    headers: {},
    data: { success: false, error: { code: 'too_many_attempts', details } },
  },
});

describe('rateLimitRetryAfter', () => {
  it('reads the wait the server asked for on a 429', () => {
    expect(rateLimitRetryAfter(rejected(429, { retry_after_seconds: 3 }))).toBe(
      3,
    );
  });

  it('assumes a short wait when a 429 names none', () => {
    expect(rateLimitRetryAfter(rejected(429))).toBe(1);
  });

  it('does not treat other failures as rate limits', () => {
    expect(rateLimitRetryAfter(rejected(500))).toBeNull();
    expect(rateLimitRetryAfter(new Error('boom'))).toBeNull();
  });
});
