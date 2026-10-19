import { describe, expect, it } from 'vitest';
import { withModelInfo } from './modelRegistration';
import type { ModelInfoPrefill } from '../../hooks/models/useModelInfoQuery';

const prefill: ModelInfoPrefill = {
  model_name: 'gpt-example',
  filled: [],
  capabilities: { supports_vision: true, supports_reasoning: true },
};
describe('registration provenance', () => {
  it('marks changed capabilities manual, including disabling every capability', () => {
    for (const capabilities of [{ supports_reasoning: true }, {}]) {
      expect(
        withModelInfo({ model_name: prefill.model_name, capabilities }, prefill)
          .prefilled_fields,
      ).not.toContain('capabilities');
    }
  });
  it('ignores object key order and absent false flags', () => {
    expect(
      withModelInfo(
        {
          model_name: prefill.model_name,
          capabilities: {
            supports_reasoning: true,
            supports_vision: true,
            supports_web_search: false,
          },
        },
        prefill,
      ).prefilled_fields,
    ).toContain('capabilities');
  });
  it('cannot attribute a different model to the previous lookup', () => {
    expect(
      withModelInfo(
        { model_name: 'another-model', capabilities: prefill.capabilities },
        prefill,
      ).prefilled_fields,
    ).toEqual([]);
  });
});

it('records deliberately cleared prefill facts as manual', () => {
  const result = withModelInfo(
    { model_name: prefill.model_name, release_date: '', maker: '' },
    { ...prefill, release_date: '2026-09-22', maker: 'openai' },
  );
  expect(result.manual_fields).toEqual(
    expect.arrayContaining(['release_date', 'maker']),
  );
});

it('records explicitly typed matching defaults as manual', () => {
  const result = withModelInfo(
    {
      model_name: 'gpt-example',
      release_date: '2026-09-01',
      official_input_per_mtok: 2,
    },
    { ...prefill, release_date: '2026-09-01', official_input_per_mtok: 2 },
    new Set(['release_date', 'official_input_per_mtok']),
  );
  expect(result.manual_fields).toEqual(
    expect.arrayContaining(['release_date', 'official_price']),
  );
  expect(result.prefilled_fields).not.toContain('release_date');
  expect(result.prefilled_fields).not.toContain('official_price');
});

it('distinguishes untouched defaults from explicit false without lookup data', () => {
  const values = {
    model_name: 'custom-model',
    open_source: false,
    capabilities: {},
  };
  expect(withModelInfo(values).manual_fields).toEqual([]);
  const result = withModelInfo(
    values,
    undefined,
    new Set(['open_source', 'capabilities']),
  );
  expect(result.manual_fields).toEqual(
    expect.arrayContaining(['open_weights', 'capabilities']),
  );
  expect(result.open_weights).toBe(false);
});
