import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useStatus } from './index';
import { queryKeys } from '../../lib/query-keys';

const STORED = { system_name: 'Stored copy', quota_per_unit: 500000 };

const Probe = () => {
  const { status } = useStatus();
  return <span>{JSON.stringify(status ?? null)}</span>;
};

const render = (client: QueryClient) =>
  renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <Probe />
    </QueryClientProvider>,
  );

describe('useStatus', () => {
  const original = globalThis.localStorage;

  beforeEach(() => {
    const items = new Map<string, string>([['status', JSON.stringify(STORED)]]);
    globalThis.localStorage = {
      getItem: (key: string) => items.get(key) ?? null,
      removeItem: (key: string) => void items.delete(key),
    } as Storage;
  });

  afterEach(() => {
    globalThis.localStorage = original;
  });

  it('serves the stored copy before the first fetch returns', () => {
    expect(render(new QueryClient())).toContain('Stored copy');
  });

  it('keeps serving the stored copy after the first fetch fails', async () => {
    const client = new QueryClient();
    await client
      .fetchQuery({
        queryKey: queryKeys.status,
        queryFn: () => Promise.reject(new Error('502')),
        retry: false,
      })
      .catch(() => undefined);
    expect(client.getQueryState(queryKeys.status)?.status).toBe('error');
    expect(render(client)).toContain('Stored copy');
  });

  it('prefers the fetched settings once they arrive', () => {
    const client = new QueryClient();
    client.setQueryData(queryKeys.status, { system_name: 'Live' });
    const html = render(client);
    expect(html).toContain('Live');
    expect(html).not.toContain('Stored copy');
  });
});
