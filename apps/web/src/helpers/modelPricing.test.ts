import { describe, expect, it } from 'vitest';
import {
  getPrimaryModelPrice,
  hasConfiguredModelPrice,
  resolveModelBrand,
} from './modelPricing';

describe('modelPricing helpers', () => {
  it('treats missing backend pricing status as no configured price', () => {
    const record = {
      model_name: 'gemini-2.5-pro',
      pricing_status: 'missing',
    };

    expect(hasConfiguredModelPrice(record)).toBe(false);
    expect(getPrimaryModelPrice(record)).toBeNull();
  });

  it('keeps explicitly configured zero prices distinct from missing prices', () => {
    const record = {
      has_price: true,
      input_per_mtok: 0,
      model_name: 'free-model',
    };

    expect(hasConfiguredModelPrice(record)).toBe(true);
    expect(getPrimaryModelPrice(record)).toBe(0);
  });

  it('uses canonical model family branding before channel icons', () => {
    const brand = resolveModelBrand({
      icon: 'OpenAI',
      model_name: 'gemini-2.5-pro',
      owner_by: 'openai-compatible-channel',
      vendor_icon: 'OpenAI',
      vendor_name: 'openai-compatible-channel',
    });

    expect(brand.name).toBe('Google');
    expect(brand.iconKey).toBe('Gemini.Color');
  });
});
