export type ImagePricing = {
  output_usd: number;
  pixel_threshold?: number;
  high_resolution_output_usd?: number;
  layer_usd?: number;
  high_resolution_layer_usd?: number;
  reference_usd?: number;
  free_references?: number;
};

/** The three shape presets (square, portrait, landscape) become valid model-specific dimensions. */
export function modelImageSize(model: string, size: string): string {
  const name = model.toLowerCase().replace(/\./g, '-');
  if (!/^(?:(?:dola|doubao)-)?seedream-/.test(name)) return size;
  const pro = name.includes('seedream-5-0-pro');
  return (
    (
      {
        '1024x1024': pro ? '1536x1536' : '2048x2048',
        '1536x1024': pro ? '1872x1248' : '2496x1664',
        '1024x1536': pro ? '1248x1872' : '1664x2496',
      } as Record<string, string>
    )[size] || size
  );
}

export function imageUnitPrice(
  price: ImagePricing,
  size: string,
  references = 0,
): number {
  const [width, height] = size.split('x').map(Number);
  const pixels =
    width * height ||
    (
      {
        '1K': 1024 ** 2,
        '1.5K': 1536 ** 2,
        '2K': 2048 ** 2,
        '3K': 3072 ** 2,
        '4K': 4096 ** 2,
      } as Record<string, number>
    )[size] ||
    0;
  const output =
    price.pixel_threshold && pixels > price.pixel_threshold
      ? (price.high_resolution_output_usd ?? price.output_usd)
      : price.output_usd;
  return (
    output +
    Math.max(0, references - (price.free_references ?? 0)) *
      (price.reference_usd ?? 0)
  );
}

/** Reject malformed price metadata instead of showing an invented zero. */
export function readImagePricing(value: unknown): ImagePricing | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    return undefined;
  const row = value as Record<string, unknown>;
  if (
    typeof row.output_usd !== 'number' ||
    !Number.isFinite(row.output_usd) ||
    row.output_usd <= 0 ||
    row.output_usd > 1000
  )
    return undefined;
  const result: ImagePricing = { output_usd: row.output_usd };
  for (const key of [
    'pixel_threshold',
    'high_resolution_output_usd',
    'layer_usd',
    'high_resolution_layer_usd',
    'reference_usd',
    'free_references',
  ] as const) {
    const n = row[key];
    if (n == null) continue;
    if (typeof n !== 'number' || !Number.isFinite(n) || n < 0) return undefined;
    if (key === 'pixel_threshold' || key === 'free_references') {
      if (!Number.isSafeInteger(n) || (key === 'free_references' && n > 14))
        return undefined;
    } else if (n > 1000) return undefined;
    result[key] = n;
  }
  if (result.high_resolution_output_usd != null && !result.pixel_threshold)
    return undefined;
  if (
    result.high_resolution_layer_usd != null &&
    (!result.pixel_threshold || result.layer_usd == null)
  )
    return undefined;
  return result;
}
