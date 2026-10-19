import { describe, expect, it } from 'vitest';
import { rangeToQueryWindow, resolvePreset } from './time-picker-v3';

describe('dashboard query window', () => {
  it('preserves exact hour endpoints for the rolling 24-hour preset', () => {
    const range = resolvePreset('24h', new Date(2026, 6, 29, 15, 37));
    if (!range) throw new Error('24h preset failed to resolve');

    const window = rangeToQueryWindow({ presetId: '24h', range });

    expect(window.endTimestamp - window.startTimestamp).toBe(24 * 60 * 60);
    expect(window.from.getHours()).toBe(15);
    expect(window.to.getHours()).toBe(15);
  });
});
