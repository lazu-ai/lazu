import { surfaceForPath } from '../../extensions';

const consoleRouteLoaders: Record<string, () => Promise<unknown>> = {
  '/console': () => import('../../pages/Dashboard'),
  '/console/key': () => import('../../pages/KeyInspector'),
  '/console/health': () => import('../../pages/ChannelHealth'),
  '/console/model-ops': () => import('../../pages/ModelOps'),
  '/console/personal': () =>
    import('../../components/settings/PersonalSetting'),
  '/console/models': () => import('../../pages/Routes'),
  '/console/search-backend': () => import('../../pages/Console/SearchBackend'),
  '/console/setting': () => import('../../pages/Setting'),
  '/console/token': () => import('../../pages/Token'),
  '/console/usage': () => import('../../pages/Usage'),
  '/console/wallet': () => import('../../pages/Wallet'),
  '/console/organizations': () => import('../../pages/Organizations'),
};

export const preloadConsoleRoute = (pathname: string) => {
  const loader =
    consoleRouteLoaders[pathname] ?? surfaceForPath(pathname)?.preload;
  if (loader) void loader().catch(() => undefined);
};
