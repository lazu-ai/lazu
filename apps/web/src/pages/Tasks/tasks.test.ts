import { describe, expect, it } from 'vitest';
import {
  formatTaskCost,
  formatTaskDuration,
  formatTaskRemaining,
  taskSpecText,
  type ConsoleTask,
} from './index';
import type { TFunction } from 'i18next';

const t = ((key: string, values?: Record<string, unknown>) =>
  `${key}${values ? JSON.stringify(values) : ''}`) as unknown as TFunction;

describe('task formatting', () => {
  it('formats costs the way the log does, without trailing zeros', () => {
    expect(formatTaskCost(0)).toBe('$0');
    expect(formatTaskCost(411482)).toBe('$0.4115');
    expect(formatTaskCost(3502000)).toBe('$3.502');
    expect(formatTaskCost(2310000)).toBe('$2.31');
  });

  it('formats durations as minutes and seconds', () => {
    expect(formatTaskDuration(168)).toBe('2:48');
    expect(formatTaskDuration(8)).toBe('0:08');
  });

  it('counts down to deletion in days, hours or minutes', () => {
    const now = 1_000_000;
    expect(formatTaskRemaining(now + 2 * 86400 + 23 * 3600 + 5, now, t)).toBe(
      'page.console.tasks.keep.days{"days":2,"hours":23}',
    );
    expect(formatTaskRemaining(now + 18 * 3600, now, t)).toBe(
      'page.console.tasks.keep.hours{"hours":18}',
    );
    expect(formatTaskRemaining(now + 30, now, t)).toBe(
      'page.console.tasks.keep.minutes{"minutes":1}',
    );
    expect(formatTaskRemaining(now - 1, now, t)).toBe(
      'page.console.tasks.keep.gone',
    );
  });

  it('describes the request in a few words', () => {
    const task = {
      spec: { resolution: '480p', seconds: 4, reference_videos: 1 },
    } as ConsoleTask;
    expect(taskSpecText(task, t)).toBe(
      '480p · page.console.tasks.spec.seconds{"n":4} · page.console.tasks.spec.refVideo{"n":1}',
    );
    expect(taskSpecText({ spec: {} } as ConsoleTask, t)).toBe('—');
  });
});
