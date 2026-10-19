import { realProjectsClient, type ProjectsClient } from './api';
import { isProjectsMockEnabled } from './flag';
import { fetchTokenVendorOptions } from '../../hooks/tokens/useTokensQuery';

let loading: Promise<ProjectsClient> | null = null;

/** Resolves the contract client once. The in-memory mock is loaded through a
 *  dynamic import so it never ships in a production bundle. */
export const loadProjectsClient = (): Promise<ProjectsClient> => {
  if (!loading) {
    loading = isProjectsMockEnabled()
      ? import('./mock').then((module) => module.mockProjectsClient)
      : Promise.resolve(realProjectsClient);
  }
  return loading;
};

/** Keep the development-only catalog inside the same mock boundary as projects. */
export const fetchProjectTokenVendorOptions = () =>
  isProjectsMockEnabled()
    ? import('./mock').then((module) => module.mockTokenVendorOptions())
    : fetchTokenVendorOptions();
