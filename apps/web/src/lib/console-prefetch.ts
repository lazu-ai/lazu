import { queryClient } from './query-client';
import { webExtensions } from '../extensions';
import { getUserIdFromLocalStorage, isRoot } from '../helpers/utils';
import { catalogQueryOptions } from '../pages/Routes/catalog-query';

/** Read-only destinations only. Query caches, including speculative reads,
 * are keyed by the same account as the mounted page. */
export async function prefetchConsoleData(path: string) {
  const identity = getUserIdFromLocalStorage();
  if (identity === -1) return;
  const sameAccount = () => getUserIdFromLocalStorage() === identity;
  if (path === '/console') {
    await Promise.all(
      webExtensions().map((extension) =>
        extension.prefetchConsole?.(identity, sameAccount),
      ),
    );
  } else if (path === '/console/models') {
    await queryClient.prefetchQuery(catalogQueryOptions('console'));
  } else if (path === '/console/health') {
    const { channelHealthQueryOptions } =
      await import('../hooks/health/useChannelHealthQuery');
    if (sameAccount())
      await queryClient.prefetchQuery(channelHealthQueryOptions('7d'));
  } else if (path === '/console/model-ops' && isRoot()) {
    const [{ modelsQueryOptions }, { modelOpsPoolsQueryOptions }] =
      await Promise.all([
        import('../hooks/models/useModelsQuery'),
        import('../hooks/models/useModelOpsQuery'),
      ]);
    if (!sameAccount()) return;
    const page = await queryClient.fetchQuery(modelsQueryOptions());
    if (!sameAccount()) return;
    const names = [
      ...new Set(
        page.items
          .map((model) => String(model.model_name || ''))
          .filter(Boolean),
      ),
    ]
      .sort()
      .join(',');
    if (names)
      await queryClient.prefetchQuery(modelOpsPoolsQueryOptions(names));
  }
}
