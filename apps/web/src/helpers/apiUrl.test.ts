import { afterEach, describe, expect, it, vi } from 'vitest';
import { buildAPIURL, getExternalAPIBaseURL } from './apiUrl';

describe('buildAPIURL', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('keeps API paths same-origin when no server URL is configured', () => {
    vi.stubEnv('VITE_REACT_APP_SERVER_URL', '');

    expect(buildAPIURL('/api/auth/oidc/login?next=%2Fconsole')).toBe(
      '/api/auth/oidc/login?next=%2Fconsole',
    );
  });

  it('uses the configured API origin without duplicating slashes', () => {
    vi.stubEnv('VITE_REACT_APP_SERVER_URL', 'https://api.example.com/');

    expect(buildAPIURL('/api/auth/oidc/login?next=%2Fconsole')).toBe(
      'https://api.example.com/api/auth/oidc/login?next=%2Fconsole',
    );
  });

  it('uses the configured API base for externally copied examples', () => {
    vi.stubEnv(
      'VITE_REACT_APP_SERVER_URL',
      'https://gateway.example.com/lazu/',
    );

    expect(
      getExternalAPIBaseURL(
        'https://console.example.com',
        'console.example.com',
      ),
    ).toBe('https://gateway.example.com/lazu');
  });

  it('keeps the hosted relay fallback when no API base is configured', () => {
    vi.stubEnv('VITE_REACT_APP_SERVER_URL', '');

    expect(getExternalAPIBaseURL('https://lazu.ai', 'lazu.ai')).toBe(
      'https://api.lazu.ai',
    );
  });
});
