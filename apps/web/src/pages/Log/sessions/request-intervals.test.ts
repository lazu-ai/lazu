import { describe, expect, it } from 'vitest';
import { requestConcurrency } from './request-intervals';

describe('request concurrency from completion timestamps', () => {
  it('does not count idle time between requests as running', () => {
    const steps = requestConcurrency([
      { created_at: 20, duration_seconds: 10 },
      { created_at: 100, duration_seconds: 10 },
      { created_at: 60, duration_seconds: 10 },
    ]);
    expect(Math.max(...steps.map((step) => step.count))).toBe(1);
    expect(steps).toContainEqual({ count: 0, from: 20, to: 50 });
  });
  it('counts genuine overlap, uses half-open boundaries and ignores unknown durations', () => {
    const steps = requestConcurrency([
      { created_at: 20, duration_seconds: 10 },
      { created_at: 25, duration_seconds: 10 },
      { created_at: 30, duration_seconds: 5 },
      { created_at: 16, duration_seconds: 0 },
    ]);
    expect(steps).toContainEqual({ count: 2, from: 15, to: 20 });
    expect(steps).toContainEqual({ count: 1, from: 25, to: 30 });
  });
});
