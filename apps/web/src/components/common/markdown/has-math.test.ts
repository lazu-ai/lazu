import { describe, expect, it } from 'vitest';
import { hasMath } from './has-math';

describe('hasMath', () => {
  it('finds dollar delimiters', () => {
    expect(hasMath('the area is $\\pi r^2$')).toBe(true);
  });

  it('finds a fenced math block without any dollar sign', () => {
    expect(hasMath('Formula:\n\n```math\nx^2\n```\n')).toBe(true);
    expect(hasMath('~~~math\nx^2\n~~~')).toBe(true);
  });

  it('leaves plain text and other code fences alone', () => {
    expect(hasMath('no formulas here')).toBe(false);
    expect(hasMath('```ts\nconst mathematics = 1;\n```')).toBe(false);
  });
});
