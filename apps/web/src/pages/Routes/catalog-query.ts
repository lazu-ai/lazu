import { readCatalogItems } from './catalog';
import { API } from '../../helpers/api';
import { getExternalAPIBaseURL } from '../../helpers/apiUrl';
import { getUserIdFromLocalStorage } from '../../helpers/utils';

export const catalogQueryOptions = (mode: 'public' | 'console') => ({
  queryKey: [
    'model-plaza-catalog',
    mode,
    mode === 'public' ? null : getUserIdFromLocalStorage(),
  ],
  queryFn: async () => {
    const response = await API.get(
      mode === 'public'
        ? `${getExternalAPIBaseURL(window.location.origin, window.location.hostname)}/models.json`
        : '/api/user/models/catalog',
      { skipErrorHandler: true },
    );
    return readCatalogItems(response.data);
  },
});
