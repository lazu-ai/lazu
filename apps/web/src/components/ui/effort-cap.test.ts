import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import {
  EffortCapControl,
  effortCapValue,
  effortRank,
  lowerEffortCap,
} from './effort-cap';

describe('reasoning-effort cap helpers', () => {
  it('reads "" and unknown values as no cap', () => {
    expect(effortRank('')).toBe(5);
    expect(effortRank(undefined)).toBe(5);
    expect(effortRank('ultra')).toBe(5);
    expect(effortRank('minimal')).toBe(0);
    expect(effortRank('high')).toBe(3);
  });

  it('stores max as no cap', () => {
    expect(effortCapValue('max')).toBe('');
    expect(effortCapValue('medium')).toBe('medium');
  });

  it('takes the lower of a Key cap and a member cap', () => {
    expect(lowerEffortCap('', '')).toBe('');
    expect(lowerEffortCap('medium', '')).toBe('medium');
    expect(lowerEffortCap('', 'high')).toBe('high');
    expect(lowerEffortCap('xhigh', 'high')).toBe('high');
    expect(lowerEffortCap('low', 'high')).toBe('low');
  });
});

it('renders inherited levels above the member ceiling as unavailable', () => {
  const html = renderToStaticMarkup(
    createElement(EffortCapControl, {
      value: '',
      ceiling: 'medium',
      'aria-label': 'Cap',
    }),
  );
  for (const level of ['high', 'xhigh', 'max']) {
    const stop = html.match(
      new RegExp(`<i[^>]*data-level="${level}"[^>]*>`),
    )?.[0];
    expect(stop).toContain('data-state="out"');
  }
  // The cap reads at the ceiling, and the slider cannot go past it.
  expect(html).toContain('aria-valuenow="2"');
  expect(html).toContain('aria-valuemax="2"');
});
