import { describe, expect, it } from 'vitest';
import {
  formatBarRate,
  longContextLabel,
  observationBlockers,
  observationProgress,
} from './observation';
import type { ModelOpsObservation } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

/* The translator the console falls back on: the Chinese default with its
   placeholders filled. */
const t = ((_key: string, options: Record<string, unknown> = {}) =>
  String(options.defaultValue ?? '').replace(/\{\{(\w+)\}\}/g, (_, name) =>
    String(options[name] ?? ''),
  )) as unknown as TFunction;

const DAY = 24 * 60 * 60 * 1000;

/* The bars come from the server with every observation; these are made up
   for the tests and are not any deployment's settings. */
const watch = (patch: Partial<ModelOpsObservation>): ModelOpsObservation =>
  ({
    checks: [
      { kind: 'billing', passed: 3, runs: 3, state: 'pass', value: 1.02 },
      { kind: 'long_context', passed: 3, runs: 3, state: 'pass', value: null },
      { kind: 'tools', passed: 0, runs: 0, state: 'pending', value: null },
    ],
    complete: false,
    day: 4,
    days_required: 6,
    long_context_tokens: 40_000,
    min_samples: 12,
    min_success_rate: 0.96,
    ready: false,
    samples: 96,
    started_at: new Date(Date.now() - 3.5 * DAY).toISOString(),
    success_rate: 0.992,
    success_state: 'pass',
    ...patch,
  }) as ModelOpsObservation;

describe('observation wording', () => {
  it('counts the day, and says when the observation is over', () => {
    expect(observationProgress(watch({}), t)).toBe('第 4 天 / 6');
    expect(observationProgress(watch({ complete: true, day: 6 }), t)).toBe(
      '第 6 天 · 期满',
    );
  });

  it('names what still holds a source back', () => {
    expect(observationBlockers(watch({}), t)).toBe(
      '还差 3 天；工具调用探测尚未运行。满 6 天且四项都过后才能上线。',
    );
  });

  it('names failed checks and a thin record', () => {
    const failing = watch({
      checks: [
        { kind: 'billing', passed: 5, runs: 6, state: 'fail', value: 1.38 },
        {
          detail: 'truncated: billed 20000 of about 40000 input tokens',
          kind: 'long_context',
          passed: 5,
          runs: 6,
          state: 'fail',
          value: 20000,
        },
        { kind: 'tools', passed: 6, runs: 6, state: 'pass', value: null },
      ],
      complete: true,
      day: 6,
      success_state: 'pending',
    });
    expect(observationBlockers(failing, t)).toBe(
      '探测样本还不够 12 次；计费比值、长上下文没有通过。满 6 天且四项都过后才能上线。',
    );
  });

  it('states the bars the server sent', () => {
    expect(
      observationBlockers(
        watch({ success_rate: 0.9, success_state: 'fail' }),
        t,
      ),
    ).toBe(
      '还差 3 天；成功率低于 96%；工具调用探测尚未运行。满 6 天且四项都过后才能上线。',
    );
    expect(longContextLabel(watch({}), t)).toBe('长上下文 40k');
    expect(formatBarRate(0.975)).toBe('97.5%');
  });
});
