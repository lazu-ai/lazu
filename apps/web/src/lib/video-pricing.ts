/* Video prices and capabilities as /api/pricing publishes them.
 *
 * A video model is priced one of three ways, the same order the server
 * quotes in: the provider's own reported cost (aggregators), a token price
 * (Ark bills Seedance width × height × fps × seconds / 1024), or a price per
 * second that may differ by resolution tier. */

export type VideoPricing = {
  second_usd?: number;
  resolution_second_usd?: Record<string, number>;
  audio_surcharge_ratio?: number;
  provider_cost_multiplier?: number;
  provider_cost_authoritative?: boolean;
  /** Per resolution tier ("720p": 1.1); overrides the flat multiplier. */
  provider_cost_multiplier_by_resolution?: Record<string, number>;
  /** Public pricing's only provider-cost signal: the multiplier is withheld. */
  provider_cost_billed?: boolean;
};

export type VideoCapabilities = {
  durations?: number[];
  min_seconds?: number;
  max_seconds?: number;
  resolutions?: string[];
  ratios?: string[];
  inputs?: string[];
  max_references?: number;
  audio?: boolean;
  passthrough?: string[];
  default_seconds?: number;
  default_resolution?: string;
  default_ratio?: string;
  fps?: number;
  max_reference_video_seconds?: number;
};

/** What a model row needs to carry for a per-second estimate. */
export type VideoPriceSource = {
  second_usd?: number;
  video_pricing?: VideoPricing;
  output_per_mtok?: number;
};

const SHORT_SIDE: Record<string, number> = {
  '480p': 480,
  '720p': 720,
  '1080p': 1080,
  '2k': 1440,
  '4k': 2160,
};

export const normalizeResolution = (raw: string): string => {
  const value = raw.trim().toLowerCase();
  if (value in SHORT_SIDE) return value;
  if (value === 'hd') return '720p';
  if (value === 'fhd') return '1080p';
  if (value === '2160p' || value === 'uhd') return '4k';
  if (value === '1440p') return '2k';
  const match = /^(\d+)x(\d+)$/.exec(value);
  if (!match) return '';
  const short = Math.min(Number(match[1]), Number(match[2]));
  if (short >= 2000) return '4k';
  if (short >= 1300) return '2k';
  if (short >= 1000) return '1080p';
  if (short >= 700) return '720p';
  return '480p';
};

/** "16:9" with a tier → WIDTHxHEIGHT (even numbers), as providers want. */
export function videoSizeFor(resolution: string, ratio: string): string {
  const short = SHORT_SIDE[normalizeResolution(resolution)] || 720;
  const parts = ratio.split(':').map(Number);
  const [w, h] =
    parts.length === 2 && parts[0] > 0 && parts[1] > 0 ? parts : [16, 9];
  const long = Math.round((short * Math.max(w, h)) / Math.min(w, h) / 2) * 2;
  return w >= h ? `${long}x${short}` : `${short}x${long}`;
}

/** Ark's estimate per second: width × height × fps / 1024 tokens. */
export function videoTokensPerSecond(
  resolution: string,
  ratio: string,
  fps = 24,
): number {
  const [width, height] = videoSizeFor(resolution, ratio)
    .split('x')
    .map(Number);
  return Math.round((width * height * fps) / 1024);
}

/** Price of one second at this resolution and ratio, or null when the model
 *  has no published per-second or per-token price. The order is the
 *  server's: a token price first (Ark's estimate always carries tokens),
 *  then the resolution tier, then the flat per-second price. A clip with
 *  sound pays the audio surcharge on a per-second price. */
export function videoSecondPrice(
  source: VideoPriceSource | undefined,
  resolution: string,
  ratio = '16:9',
  options: { audio?: boolean } = {},
): number | null {
  if (!source) return null;
  const tier = normalizeResolution(resolution);
  if (
    typeof source.output_per_mtok === 'number' &&
    source.output_per_mtok > 0
  ) {
    return (
      (videoTokensPerSecond(tier || '720p', ratio) * source.output_per_mtok) /
      1_000_000
    );
  }
  const video = source.video_pricing;
  let perSecond: number | null = null;
  if (video?.resolution_second_usd) {
    const found = Object.entries(video.resolution_second_usd).find(
      ([key]) => normalizeResolution(key) === tier,
    );
    if (found && found[1] > 0) perSecond = found[1];
  }
  if (
    perSecond === null &&
    typeof video?.second_usd === 'number' &&
    video.second_usd > 0
  ) {
    perSecond = video.second_usd;
  }
  if (
    perSecond === null &&
    typeof source.second_usd === 'number' &&
    source.second_usd > 0
  ) {
    perSecond = source.second_usd;
  }
  if (perSecond === null) return null;
  const surcharge = video?.audio_surcharge_ratio;
  if (options.audio && typeof surcharge === 'number' && surcharge > 0) {
    return perSecond * surcharge;
  }
  return perSecond;
}

/** Whether the server prices a clip as having sound when the request does
 *  not say: Ark (Seedance) makes audio unless the model's row says it
 *  cannot; the other providers only when asked. */
export const videoEstimateHasAudio = (
  model: string,
  caps?: VideoCapabilities | null,
): boolean => /seedance/i.test(model) && caps?.audio !== false;

/** Prices that follow the provider's reported cost: a per-second figure is
 *  only what the hold is taken on, not what the clip will cost. */
export const videoPriceFollowsProviderCost = (
  source: VideoPriceSource | undefined,
): boolean => {
  const video = source?.video_pricing;
  if (!video) return false;
  return (
    video.provider_cost_billed === true ||
    video.provider_cost_authoritative === true ||
    (typeof video.provider_cost_multiplier === 'number' &&
      video.provider_cost_multiplier > 0) ||
    Object.keys(video.provider_cost_multiplier_by_resolution || {}).length > 0
  );
};

export type VideoOptions = {
  ratios: string[];
  resolutions: string[];
  seconds: number[];
};

const DEFAULT_OPTIONS: VideoOptions = {
  ratios: ['16:9', '9:16'],
  resolutions: ['720p', '1080p'],
  seconds: [5, 8, 10],
};

/** The pickers a model offers: its capability row, else the common defaults. */
export function videoOptionsFor(caps?: VideoCapabilities | null): VideoOptions {
  if (!caps) return DEFAULT_OPTIONS;
  let seconds: number[] = [];
  if (caps.durations && caps.durations.length > 0) {
    seconds = [...caps.durations];
  } else if (caps.min_seconds || caps.max_seconds) {
    const min = Math.max(1, caps.min_seconds || 1);
    const max = Math.max(min, caps.max_seconds || min);
    const steps = [2, 4, 5, 6, 8, 10, 12, 15].filter(
      (value) => value >= min && value <= max,
    );
    seconds = steps.length > 0 ? steps : [min];
  }
  const resolutions = (caps.resolutions || [])
    .map((value) => normalizeResolution(value) || value)
    .filter((value, index, all) => all.indexOf(value) === index);
  const ratios = (caps.ratios || []).filter((value) => value !== 'adaptive');
  return {
    ratios: ratios.length > 0 ? ratios : DEFAULT_OPTIONS.ratios,
    resolutions:
      resolutions.length > 0 ? resolutions : DEFAULT_OPTIONS.resolutions,
    seconds: seconds.length > 0 ? seconds : DEFAULT_OPTIONS.seconds,
  };
}

/** Keeps a remembered choice only when the model still offers it. */
export function clampVideoChoice(
  options: VideoOptions,
  current: { ratio: string; resolution: string; seconds: number },
  caps?: VideoCapabilities | null,
) {
  const resolution = options.resolutions.includes(current.resolution)
    ? current.resolution
    : normalizeResolution(caps?.default_resolution || '') &&
        options.resolutions.includes(
          normalizeResolution(caps?.default_resolution || ''),
        )
      ? normalizeResolution(caps?.default_resolution || '')
      : options.resolutions[0];
  const seconds = options.seconds.includes(current.seconds)
    ? current.seconds
    : caps?.default_seconds && options.seconds.includes(caps.default_seconds)
      ? caps.default_seconds
      : options.seconds[0];
  const ratio = options.ratios.includes(current.ratio)
    ? current.ratio
    : caps?.default_ratio && options.ratios.includes(caps.default_ratio)
      ? caps.default_ratio
      : options.ratios[0];
  return { ratio, resolution, seconds };
}

export const readVideoCapabilities = (
  value: unknown,
): VideoCapabilities | undefined => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return undefined;
  }
  const row = value as Record<string, unknown>;
  const numbers = (list: unknown) =>
    Array.isArray(list)
      ? list.map(Number).filter((n) => Number.isFinite(n) && n > 0)
      : undefined;
  const strings = (list: unknown) =>
    Array.isArray(list)
      ? list.filter((s): s is string => typeof s === 'string')
      : undefined;
  const number = (n: unknown) =>
    typeof n === 'number' && Number.isFinite(n) ? n : undefined;
  return {
    audio: typeof row.audio === 'boolean' ? row.audio : undefined,
    default_ratio:
      typeof row.default_ratio === 'string' ? row.default_ratio : undefined,
    default_resolution:
      typeof row.default_resolution === 'string'
        ? row.default_resolution
        : undefined,
    default_seconds: number(row.default_seconds),
    durations: numbers(row.durations),
    fps: number(row.fps),
    inputs: strings(row.inputs),
    max_reference_video_seconds: number(row.max_reference_video_seconds),
    max_references: number(row.max_references),
    max_seconds: number(row.max_seconds),
    min_seconds: number(row.min_seconds),
    passthrough: strings(row.passthrough),
    ratios: strings(row.ratios),
    resolutions: strings(row.resolutions),
  };
};

export const readVideoPricing = (value: unknown): VideoPricing | undefined => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return undefined;
  }
  const row = value as Record<string, unknown>;
  const number = (n: unknown) =>
    typeof n === 'number' && Number.isFinite(n) ? n : undefined;
  const tiers =
    row.resolution_second_usd &&
    typeof row.resolution_second_usd === 'object' &&
    !Array.isArray(row.resolution_second_usd)
      ? Object.fromEntries(
          Object.entries(row.resolution_second_usd as Record<string, unknown>)
            .map(([key, price]) => [key, number(price)] as const)
            .filter(
              (entry): entry is readonly [string, number] =>
                typeof entry[1] === 'number',
            ),
        )
      : undefined;
  const multipliers =
    row.provider_cost_multiplier_by_resolution &&
    typeof row.provider_cost_multiplier_by_resolution === 'object' &&
    !Array.isArray(row.provider_cost_multiplier_by_resolution)
      ? Object.fromEntries(
          Object.entries(
            row.provider_cost_multiplier_by_resolution as Record<
              string,
              unknown
            >,
          )
            .map(([key, value]) => [key, number(value)] as const)
            .filter(
              (entry): entry is readonly [string, number] =>
                typeof entry[1] === 'number',
            ),
        )
      : undefined;
  return {
    audio_surcharge_ratio: number(row.audio_surcharge_ratio),
    provider_cost_authoritative: row.provider_cost_authoritative === true,
    provider_cost_billed: row.provider_cost_billed === true,
    provider_cost_multiplier: number(row.provider_cost_multiplier),
    provider_cost_multiplier_by_resolution: multipliers,
    resolution_second_usd: tiers,
    second_usd: number(row.second_usd),
  };
};
