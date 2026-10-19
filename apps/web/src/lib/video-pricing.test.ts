import { describe, expect, it } from 'vitest';
import {
  clampVideoChoice,
  normalizeResolution,
  videoOptionsFor,
  videoSecondPrice,
  videoSizeFor,
  videoTokensPerSecond,
} from './video-pricing';

describe('video options', () => {
  it('reads the capability row and falls back to the common defaults', () => {
    expect(videoOptionsFor(undefined)).toEqual({
      ratios: ['16:9', '9:16'],
      resolutions: ['720p', '1080p'],
      seconds: [5, 8, 10],
    });
    expect(
      videoOptionsFor({
        durations: [4, 6, 8],
        ratios: ['16:9', '9:16', 'adaptive'],
        resolutions: ['720p', '1080P'],
      }),
    ).toEqual({
      ratios: ['16:9', '9:16'],
      resolutions: ['720p', '1080p'],
      seconds: [4, 6, 8],
    });
    expect(videoOptionsFor({ min_seconds: 2, max_seconds: 12 }).seconds).toEqual(
      [2, 4, 5, 6, 8, 10, 12],
    );
  });

  it('keeps a remembered choice only while the model offers it', () => {
    const options = videoOptionsFor({
      default_resolution: '720p',
      default_seconds: 8,
      durations: [4, 6, 8],
      ratios: ['16:9', '9:16'],
      resolutions: ['720p', '1080p'],
    });
    expect(
      clampVideoChoice(
        options,
        { ratio: '9:16', resolution: '1080p', seconds: 5 },
        { default_resolution: '720p', default_seconds: 8 },
      ),
    ).toEqual({ ratio: '9:16', resolution: '1080p', seconds: 8 });
    expect(
      clampVideoChoice(options, { ratio: '1:1', resolution: '480p', seconds: 6 }),
    ).toEqual({ ratio: '16:9', resolution: '720p', seconds: 6 });
  });
});

describe('video prices', () => {
  it('maps sizes and words onto resolution tiers', () => {
    expect(normalizeResolution('1280x720')).toBe('720p');
    expect(normalizeResolution('hd')).toBe('720p');
    expect(normalizeResolution('3840x2160')).toBe('4k');
    expect(videoSizeFor('720p', '16:9')).toBe('1280x720');
    expect(videoSizeFor('1080p', '9:16')).toBe('1080x1920');
    expect(videoSizeFor('720p', '1:1')).toBe('720x720');
  });

  it('prices a second by tier, flat rate, then tokens', () => {
    expect(
      videoSecondPrice(
        {
          second_usd: 0.03,
          video_pricing: {
            resolution_second_usd: { '1080p': 0.1 },
            second_usd: 0.05,
          },
        },
        '1080p',
      ),
    ).toBe(0.1);
    expect(
      videoSecondPrice(
        { second_usd: 0.03, video_pricing: { second_usd: 0.05 } },
        '720p',
      ),
    ).toBe(0.05);
    expect(videoSecondPrice({ second_usd: 0.03 }, '720p')).toBe(0.03);
    // Ark: 1280 × 720 × 24 / 1024 = 21,600 tokens a second at $2 / M.
    expect(videoTokensPerSecond('720p', '16:9')).toBe(21_600);
    expect(videoSecondPrice({ output_per_mtok: 2 }, '720p', '16:9')).toBeCloseTo(
      0.0432,
    );
    expect(videoSecondPrice({}, '720p')).toBeNull();
  });
});
