import { describe, expect, it } from 'vitest';
import {
  buildSourceModelRows,
  compileIgnore,
  serveUpstreamIds,
  stopServing,
  suggestPublicName,
} from './sourceModels';

const catalog = new Set(['claude-opus-5-5', 'gpt-5.5']);

describe('source models', () => {
  it('sells a prefixed upstream ID under the catalog name', () => {
    expect(suggestPublicName('vendor/claude-opus-5-5', catalog)).toBe(
      'claude-opus-5-5',
    );
    expect(suggestPublicName('vendor/unknown', catalog)).toBe('vendor/unknown');
  });

  it('never puts two upstream IDs on one name', () => {
    const next = serveUpstreamIds(
      [],
      {},
      ['vendor/claude-opus-5-5', 'other/claude-opus-5-5'],
      catalog,
    );
    expect(next.models).toEqual(['claude-opus-5-5', 'other/claude-opus-5-5']);
    expect(next.mapping).toEqual({
      'claude-opus-5-5': 'vendor/claude-opus-5-5',
    });
  });

  it('drops the mapping with the model it served', () => {
    const next = stopServing(
      ['claude-opus-5-5', 'gpt-5.5'],
      { 'claude-opus-5-5': 'vendor/claude-opus-5-5' },
      ['claude-opus-5-5'],
    );
    expect(next).toEqual({ mapping: {}, models: ['gpt-5.5'] });
  });

  it('marks served, gone, stopped and new rows', () => {
    const rows = buildSourceModelRows(
      ['claude-opus-5-5', 'gpt-5.5'],
      { 'claude-opus-5-5': 'vendor/claude-opus-5-5' },
      {
        listed_at: 1,
        new: ['fresh-1'],
        stopped: ['old-1'],
        upstream: ['vendor/claude-opus-5-5', 'fresh-1', 'quiet-1'],
      },
      catalog,
    );
    expect(rows.map((row) => [row.name, row.served, row.status])).toEqual([
      ['claude-opus-5-5', true, 'in'],
      ['gpt-5.5', true, 'gone'],
      ['old-1', false, 'gone'],
      ['fresh-1', false, 'new'],
      ['quiet-1', false, 'in'],
    ]);
  });

  it('reads globs whole and re: as a regular expression', () => {
    const ignore = compileIgnore('*-preview\nre:^text-embedding-\nre:(');
    expect(ignore.invalid).toEqual(['re:(']);
    expect(ignore.match('gemini-3-preview')).toBe(true);
    expect(ignore.match('gemini-3-preview-2')).toBe(false);
    expect(ignore.match('text-embedding-3-large')).toBe(true);
  });
});
