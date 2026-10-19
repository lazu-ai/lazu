export const getAPIBaseURL = () =>
  String(import.meta.env.VITE_REACT_APP_SERVER_URL || '').trim();

export const getExternalAPIBaseURL = (
  currentOrigin: string,
  currentHostname: string,
) => {
  const configured = getAPIBaseURL();
  if (configured) {
    return new URL(configured, currentOrigin).toString().replace(/\/+$/, '');
  }
  return /(^|\.)lazu\.ai$/.test(currentHostname)
    ? 'https://api.lazu.ai'
    : currentOrigin.replace(/\/+$/, '');
};

export const buildAPIURL = (path: string) => {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  const baseURL = getAPIBaseURL().replace(/\/+$/, '');
  return `${baseURL}${normalizedPath}`;
};
