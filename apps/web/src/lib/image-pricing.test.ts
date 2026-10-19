import { describe, expect, it } from 'vitest';
import {
  imageUnitPrice,
  modelImageSize,
  readImagePricing,
} from './image-pricing';
import {
  draftFromRow,
  lanePayload,
  validateDraft,
} from '../pages/ModelOps/ratioPricing';

const pro = {
  output_usd: 0.045,
  pixel_threshold: 2610000,
  high_resolution_output_usd: 0.09,
  layer_usd: 0.0225,
  high_resolution_layer_usd: 0.045,
  reference_usd: 0.003,
  free_references: 1,
};
describe('Seedream price estimates and edits', () => {
  it('estimates valid shape presets, high resolution and extra references', () => {
    expect(
      imageUnitPrice(pro, modelImageSize('seedream-5.0-pro', '1024x1024'), 2),
    ).toBeCloseTo(0.048);
    expect(imageUnitPrice(pro, '2K', 3)).toBeCloseTo(0.096);
    expect(modelImageSize('seedream-5.0-lite', '1536x1024')).toBe('2496x1664');
    expect(modelImageSize('gpt-image-2', '1024x1024')).toBe('1024x1024');
  });
  it('rejects incomplete metadata rather than inventing a price', () => {
    expect(readImagePricing(pro)).toEqual(pro);
    expect(readImagePricing({ output_usd: null })).toBeUndefined();
    expect(readImagePricing({ ...pro, pixel_threshold: 0 })).toBeUndefined();
    expect(readImagePricing({ ...pro, free_references: 20 })).toBeUndefined();
  });
  it('preserves all dimensions and saves the edited image headline together', () => {
    const row = {
      model_name: 'seedream-5.0-pro',
      request_usd: 0.045,
      image_pricing: pro,
    };
    const draft = draftFromRow(row, {}, null);
    draft.imagePricing!.output_usd = 0.05;
    expect(pro.output_usd).toBe(0.045);
    expect(validateDraft(draft)).toBeNull();
    expect(lanePayload(draft, row, 'direct', row.model_name)).toMatchObject({
      request_usd: 0.05,
      image_pricing: { ...pro, output_usd: 0.05 },
    });
    expect(validateDraft({ ...draft, mode: 'follow' })).toBe('price');
  });
});
