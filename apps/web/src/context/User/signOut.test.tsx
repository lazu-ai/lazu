import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { SESSION_QUERY_KEY, useSession, type Session } from './index';
import { queryKeys } from '../../lib/query-keys';

const captureSession = (client: QueryClient): Session => {
  let session: Session | undefined;
  const Probe = () => {
    session = useSession();
    return null;
  };
  renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <Probe />
    </QueryClientProvider>,
  );
  return session!;
};

describe('signOut', () => {
  it("drops every account's cached reads and keeps the public settings", () => {
    const client = new QueryClient();
    client.setQueryData(queryKeys.status, { system_name: 'Lazu' });
    client.setQueryData(SESSION_QUERY_KEY, { id: 1, username: 'a' });
    client.setQueryData([...queryKeys.user, 'self'], { id: 1 });
    client.setQueryData(queryKeys.dashboard.summary(), { spend: 12 });
    client.setQueryData(queryKeys.console.table('tokens'), { items: [1] });
    client.setQueryData(['projects', 'list'], [{ id: 7 }]);

    captureSession(client).signOut();

    expect(client.getQueryData(queryKeys.status)).toEqual({
      system_name: 'Lazu',
    });
    expect(client.getQueryData(SESSION_QUERY_KEY)).toBeNull();
    expect(client.getQueryData([...queryKeys.user, 'self'])).toBeUndefined();
    expect(client.getQueryData(queryKeys.dashboard.summary())).toBeUndefined();
    expect(
      client.getQueryData(queryKeys.console.table('tokens')),
    ).toBeUndefined();
    expect(client.getQueryData(['projects', 'list'])).toBeUndefined();
  });
});
