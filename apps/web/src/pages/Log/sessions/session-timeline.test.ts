import { describe, expect, it } from 'vitest';
import { sessionTimeline } from './session-format';
import type { TFunction } from 'i18next';

// Echo the key and its values so the test reads what was chosen.
const t = ((key: string, values: Record<string, unknown> = {}) =>
  `${key.split('.').slice(-1)[0]} ${JSON.stringify(values)}`) as TFunction;

describe('session mini timeline', () => {
  it('says how long it worked and how long it stopped', () => {
    const { bars, caption } = sessionTimeline(
      {
        last_at: 1000 + 23 * 3_600 + 30,
        segments: [
          { start: 1000, end: 1007 },
          { start: 1000 + 23 * 3_600, end: 1000 + 23 * 3_600 + 30 },
        ],
        started_at: 1000,
      },
      t,
    );
    expect(bars).toHaveLength(2);
    expect(bars[0].left).toBe(0);
    expect(bars[1].left + bars[1].width).toBeLessThanOrEqual(100);
    expect(caption).toContain('stopped');
    expect(caption).toContain('"count":2');
    expect(caption).toContain('{\\"hours\\":23}');
  });

  it('falls back to the whole span without segments', () => {
    const { bars, caption } = sessionTimeline(
      { last_at: 122, started_at: 100 },
      t,
    );
    expect(bars).toEqual([{ left: 0, width: 100 }]);
    expect(caption).toContain('one');
    expect(caption).toContain('{\\"n\\":22}');
  });

  it('names the span when there are more than two stretches', () => {
    const { caption } = sessionTimeline(
      {
        last_at: 8 * 3_600,
        segments: [
          { start: 0, end: 600 },
          { start: 3_600, end: 4_200 },
          { start: 8 * 3_600 - 600, end: 8 * 3_600 },
        ],
        started_at: 0,
      },
      t,
    );
    expect(caption).toContain('across');
    expect(caption).toContain('{\\"hours\\":8}');
  });
});
