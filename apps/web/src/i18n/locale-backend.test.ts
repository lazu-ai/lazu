import { createInstance } from 'i18next';
import { describe, expect, it } from 'vitest';
import { chunkResourceFor } from './locale-bundle-codec';
import type { BackendModule, ReadCallback } from 'i18next';

// Single-language chunks, fallbacks already merged in at build time.
const chunks: Record<string, Record<string, Record<string, unknown>>> = {
  en: { en: { hello: 'Hello', only: 'English only' } },
  zh: { zh: { hello: '你好', only: 'English only' } },
  'zh-TW': { 'zh-TW': { hello: '妳好', only: 'English only' } },
};

const instance = (start: string) => {
  const i18n = createInstance();
  let target = start;
  i18n.on('languageChanging', (language) => {
    target = language;
  });
  const backend: BackendModule = {
    type: 'backend',
    init() {},
    read(language: string, _namespace: string, callback: ReadCallback) {
      // As in i18n.ts: fallback languages are served from the chunk of the
      // language being loaded.
      const chain = target === 'zh-TW' ? ['zh-TW', 'zh', 'en'] : [target, 'en'];
      const bundleLanguage = chain.includes(language) ? target : language;
      queueMicrotask(() =>
        callback(
          null,
          chunkResourceFor(chunks[bundleLanguage], language, bundleLanguage),
        ),
      );
    },
  };
  return i18n.use(backend);
};

describe('locale backend with single-language chunks', () => {
  for (const [start, next, expected] of [
    ['zh', 'en', 'Hello'],
    ['zh-TW', 'zh', '你好'],
    ['zh-TW', 'en', 'Hello'],
  ] as const) {
    it(`switching from ${start} to ${next} loads ${next}'s own chunk`, async () => {
      const i18n = instance(start);
      await i18n.init({
        lng: start,
        load: 'currentOnly',
        fallbackLng: { 'zh-TW': ['zh', 'en'], zh: ['en'], default: ['en'] },
        supportedLngs: ['zh', 'zh-TW', 'en'],
        interpolation: { escapeValue: false },
      });
      expect(i18n.t('only')).toBe('English only');
      await i18n.changeLanguage(next);
      expect(i18n.t('hello')).toBe(expected);
    });
  }
});
