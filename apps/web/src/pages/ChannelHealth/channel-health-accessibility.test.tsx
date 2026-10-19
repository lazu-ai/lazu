import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { HeatCells, InactiveSourceBadge, RowHealthStatus } from './index';

const BUCKETS = [
  {
    index: 0,
    start_at: '2026-09-01T00:00:00Z',
    source: 'traffic' as const,
    request_count: 12,
    error_count: 0,
    success_rate: 1,
    tone: 'ok' as const,
  },
  {
    index: 1,
    start_at: '2026-09-01T01:00:00Z',
    source: 'probe' as const,
    request_count: 1,
    error_count: 1,
    success_rate: 0,
    tone: 'bad' as const,
  },
];

describe('channel health bucket accessibility', () => {
  it('renders every historical bucket as a keyboard-operable labelled trigger when it can be inspected', () => {
    const html = renderToStaticMarkup(
      <HeatCells
        buckets={BUCKETS}
        cellLabel={(bucket) => `bucket ${bucket.index}`}
        onInspect={() => {}}
      />,
    );

    expect(html.match(/<button/g)).toHaveLength(2);
    expect(html).toContain('aria-label="bucket 0"');
    expect(html).toContain('aria-label="bucket 1"');
    expect(html.match(/data-owns-chrome="true"/g)).toHaveLength(2);
    expect(html).toContain('type="button"');
    expect(html).not.toContain('<i');
  });

  /* Both branches added a test for this rule; this is the union. Without an
     inspect handler a bucket has nothing to activate, so it is decoration
     rather than a control: a focusable button that does nothing is worse for
     keyboard users than a cell the reader skips. The row it sits in carries the
     readable summary. */
  it('renders cells with nothing to inspect as decoration, not controls', () => {
    const html = renderToStaticMarkup(
      <HeatCells
        buckets={BUCKETS}
        cellLabel={(bucket) => `bucket ${bucket.index}`}
      />,
    );

    expect(html).not.toContain('<button');
    expect(html.match(/aria-hidden="true"/g)).toHaveLength(2);
    expect(html).toContain('title="bucket 0"');
    expect(html).not.toContain('<i');
  });
});

describe('channel health status accessibility', () => {
  it('renders the operational state as visible text alongside its shape and rate', () => {
    const html = renderToStaticMarkup(
      <RowHealthStatus
        label='Degraded'
        row={{
          model_name: 'gpt-test',
          lane: 'stable',
          status: 'degraded',
          source: 'traffic',
          request_count: 2,
          error_count: 0,
          success_rate: 1,
          probe_count: 0,
          channel_count: 1,
          buckets: [],
        }}
      />,
    );

    expect(html).toContain('chealth-status-mark--degraded');
    expect(html).toContain('aria-hidden="true"');
    expect(html).toContain('Degraded');
    expect(html).toContain('100.0%');
  });

  it('renders a visible neutral label for an inactive historical source', () => {
    const html = renderToStaticMarkup(
      <InactiveSourceBadge label='Disabled · historical data' />,
    );

    expect(html).toContain('lazu-badge--neutral');
    expect(html).toContain('Disabled · historical data');
  });
});
