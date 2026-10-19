import { QueryClient } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { readSelf, refreshSelf, SELF_QUERY_KEY } from './useSelfQuery';
const state = vi.hoisted(() => ({ id: '1', get: vi.fn() }));
vi.mock('../../helpers/utils', () => ({
  getUserIdFromLocalStorage: () => state.id,
}));
vi.mock('../../helpers/api', () => ({ API: { get: state.get } }));
vi.mock('../../lib/api-error', () => ({
  unwrap: (response: { data: unknown }) => response.data,
}));
beforeEach(() => {
  state.id = '1';
  state.get.mockReset();
});

it('deduplicates bootstrap and page reads but refreshes revoked membership', async () => {
  const client = new QueryClient();
  state.get.mockResolvedValueOnce({ data: { id: '1', projects: ['team'] } });
  const [shell, page] = await Promise.all([readSelf(client), readSelf(client)]);
  expect(shell).toEqual(page);
  expect(state.get).toHaveBeenCalledTimes(1);
  await readSelf(client);
  expect(state.get).toHaveBeenCalledTimes(1);
  state.get.mockResolvedValueOnce({ data: { id: '1', projects: [] } });
  expect(await refreshSelf(client)).toEqual({ id: '1', projects: [] });
  expect(state.get).toHaveBeenCalledTimes(2);
  client.clear();
});

it('never shares account data across identities and keeps root invalidation', async () => {
  const client = new QueryClient();
  state.get.mockImplementation((_url, config) =>
    Promise.resolve({ data: { id: config.headers['X-Lazu-User'] } }),
  );
  expect(await readSelf(client)).toEqual({ id: '1' });
  state.id = '2';
  expect(await readSelf(client)).toEqual({ id: '2' });
  expect(state.get).toHaveBeenCalledTimes(2);
  await client.invalidateQueries({ queryKey: SELF_QUERY_KEY });
  expect(client.getQueryState([...SELF_QUERY_KEY, '1'])?.isInvalidated).toBe(
    true,
  );
  expect(client.getQueryState([...SELF_QUERY_KEY, '2'])?.isInvalidated).toBe(
    true,
  );
  client.clear();
});
