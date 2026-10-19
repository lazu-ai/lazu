import { useSearchParams } from 'react-router-dom';
import { useConsoleScope } from '../../context/ConsoleScope';

/** Lists and detail pages must resolve the same explicit drilldown scope. */
export function useUsageScope() {
  const { activeScope, availableScopes } = useConsoleScope();
  const [searchParams] = useSearchParams();
  const explicitScope = searchParams.get('scope');
  const admin =
    !searchParams.get('project_id') &&
    (explicitScope === 'platform' ||
      (explicitScope !== 'self' && activeScope.plane === 'platform')) &&
    availableScopes.some((scope) => scope.plane === 'platform');
  return { activeScope, admin };
}
