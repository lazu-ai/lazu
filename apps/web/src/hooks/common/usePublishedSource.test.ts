import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';
import { countNewSourceStar } from './usePublishedSource';
import { queryKeys } from '../../lib/query-keys';

const source = (stars: number | null) => ({
  enabled: true,
  url: 'https://github.com/example-org/example',
  stars,
});

describe('countNewSourceStar', () => {
  it('puts the count up by one at once and marks the answer stale', async () => {
    const client = new QueryClient();
    client.setQueryData(queryKeys.publishedSource, source(1284));
    await countNewSourceStar(client);
    expect(client.getQueryData(queryKeys.publishedSource)).toEqual(
      source(1285),
    );
    expect(client.getQueryState(queryKeys.publishedSource)?.isInvalidated).toBe(
      true,
    );
  });

  it('leaves an unknown count unknown', async () => {
    const client = new QueryClient();
    client.setQueryData(queryKeys.publishedSource, source(null));
    await countNewSourceStar(client);
    expect(client.getQueryData(queryKeys.publishedSource)).toEqual(
      source(null),
    );
  });
});
