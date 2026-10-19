import { describe, expect, it } from 'vitest';
import { groupByMaker, makerOf } from './model-makers';

type Row = { id: string; maker?: string; maker_rank?: number };

describe('groupByMaker', () => {
  it('keeps the server order when ranks agree with it', () => {
    const rows: Row[] = [
      { id: 'claude-opus-5-5', maker: 'anthropic', maker_rank: 0 },
      { id: 'claude-fable-5-1', maker: 'anthropic', maker_rank: 0 },
      { id: 'gpt-6-sol', maker: 'openai', maker_rank: 1 },
    ];
    const groups = groupByMaker(
      rows,
      (row) => row.maker || '',
      (row) => row.maker_rank,
    );
    expect(groups.map((group) => group.maker)).toEqual(['anthropic', 'openai']);
    expect(groups[0].rows.map((row) => row.id)).toEqual([
      'claude-opus-5-5',
      'claude-fable-5-1',
    ]);
  });

  it('orders groups by the default maker order when no rank is sent', () => {
    const rows: Row[] = [
      { id: 'MiniMax-M2.7' },
      { id: 'mystery-model' },
      { id: 'claude-sonnet-5' },
      { id: 'deepseek-v4-pro' },
      { id: 'gpt-5.5' },
    ];
    const groups = groupByMaker(rows, (row) =>
      makerOf(row.maker, { id: row.id }),
    );
    expect(groups.map((group) => group.maker)).toEqual([
      'anthropic',
      'openai',
      'deepseek',
      'minimax',
      '',
    ]);
  });

  it('follows an operator rank over the default order', () => {
    const rows: Row[] = [
      { id: 'claude-sonnet-5', maker: 'anthropic', maker_rank: 1 },
      { id: 'deepseek-v4-pro', maker: 'deepseek', maker_rank: 0 },
    ];
    const groups = groupByMaker(
      rows,
      (row) => row.maker || '',
      (row) => row.maker_rank,
    );
    expect(groups.map((group) => group.maker)).toEqual([
      'deepseek',
      'anthropic',
    ]);
  });
});

describe('makerOf', () => {
  it('prefers the maker the server sent', () => {
    expect(makerOf('zhipu', { id: 'claude-x' })).toBe('zhipu');
  });

  it('infers the lab from the model id otherwise', () => {
    expect(makerOf(undefined, { id: 'kimi-k3' })).toBe('moonshot');
    expect(makerOf(undefined, { id: 'glm-5.3' })).toBe('zhipu');
    expect(makerOf(undefined, { id: 'qwen3.8-max' })).toBe('alibaba');
    expect(makerOf(undefined, { id: 'seed-2.0-pro' })).toBe('bytedance');
    expect(makerOf(undefined, { id: 'seed-2.1-turbo' })).toBe('bytedance');
  });
});
