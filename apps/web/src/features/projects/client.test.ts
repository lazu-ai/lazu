import { beforeEach, describe, expect, it, vi } from 'vitest';
const fixtures = vi.hoisted(() => ({ mock: true, pricing: vi.fn() }));
vi.mock('./flag', () => ({
  isProjectsMockEnabled: () => fixtures.mock,
  readMockScenario: () => 'owner',
}));
vi.mock('../../helpers/api', () => ({ API: {} }));
vi.mock('../../hooks/tokens/useTokensQuery', () => ({
  fetchTokenVendorOptions: fixtures.pricing,
}));
import { fetchProjectTokenVendorOptions, loadProjectsClient } from './client';

beforeEach(() => vi.clearAllMocks());
describe('project token catalog boundary', () => {
  it('can load the catalog and create a mock key without any pricing request', async () => {
    fixtures.mock = true;
    const catalog = await fetchProjectTokenVendorOptions();
    expect(catalog.vendors.length).toBeGreaterThan(0);
    const client = await loadProjectsClient();
    const models = await client.listProjectModels('p_orbit');
    expect(catalog.models.map((entry) => entry.model)).toEqual(models);
    const created = await client.createToken('p_orbit', {
      name: 'mock-catalog-check',
      model_limits: [],
      cap_microusd: 20000000,
      expired_time: 1893456000,
      usage_alert_percent: 80,
      note: 'original',
    });
    expect(created.token.name).toBe('mock-catalog-check');
    expect(created.token.cap_microusd).toBe(20000000);
    const detail = await client.getToken(created.token.id, 'p_orbit');
    expect(detail).toMatchObject({
      expired_time: 1893456000,
      usage_alert_percent: 80,
      note: 'original',
    });
    await client.updateTokenFull(created.token.id, 'p_orbit', {
      name: 'edited',
      expected_version: detail.version,
      cap_microusd: 30000000,
      model_limits: [],
      note: 'updated',
    });
    expect(await client.getToken(created.token.id, 'p_orbit')).toMatchObject({
      cap_microusd: 30000000,
      expired_time: 1893456000,
      usage_alert_percent: 80,
      note: 'updated',
    });
    expect(fixtures.pricing).not.toHaveBeenCalled();
  });
  it('uses actual pricing outside mock mode', async () => {
    fixtures.mock = false;
    fixtures.pricing.mockResolvedValue({ models: [], vendors: [] });
    await expect(fetchProjectTokenVendorOptions()).resolves.toEqual({
      models: [],
      vendors: [],
    });
    expect(fixtures.pricing).toHaveBeenCalledOnce();
  });
});
