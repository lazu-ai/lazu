import { describe, expect, it } from 'vitest';
import { buildModelFormSchema } from './ModelMetaForm';
import type { TFunction } from 'i18next';

const t = ((key: string) => key) as unknown as TFunction;

const baseValues = {
  model_name: '  MiniMax-M2.7  ',
  open_source: false,
  status: true,
  sync_official: true,
  tags: [],
  vendor_id: 0,
};

describe('buildModelFormSchema', () => {
  it('preserves model_name case when trimming', () => {
    const schema = buildModelFormSchema(t, { requireVendor: false });
    const parsed = schema.parse(baseValues);
    expect(parsed.model_name).toBe('MiniMax-M2.7');
  });

  it('requires vendor_id by default but allows 0 when requireVendor is false', () => {
    const createSchema = buildModelFormSchema(t);
    expect(createSchema.safeParse(baseValues).success).toBe(false);
    expect(
      createSchema.safeParse({ ...baseValues, vendor_id: 4 }).success,
    ).toBe(true);

    const editSchema = buildModelFormSchema(t, { requireVendor: false });
    const parsed = editSchema.parse(baseValues);
    expect(parsed.vendor_id).toBe(0);
  });
});
