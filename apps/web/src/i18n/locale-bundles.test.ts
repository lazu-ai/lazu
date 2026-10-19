import { resolve } from 'node:path';
import { createInstance } from 'i18next';
import { describe, expect, it } from 'vitest';
import {
  unpackLocaleResources,
  type PackedLocaleNode,
} from './locale-bundle-codec';
import {
  bundledLocale,
  packLocaleResources,
  localeCodes,
  localeNamespaces,
  readLocale,
} from '../../scripts/locale-bundles';
const root = resolve(import.meta.dirname, '../..');
const keys = (value: Record<string, unknown>, prefix = ''): string[] =>
  Object.entries(value).flatMap(([key, v]) =>
    v && typeof v === 'object' && !Array.isArray(v)
      ? keys(v as Record<string, unknown>, prefix + key + '.')
      : [prefix + key],
  );

describe('compiled locale fallbacks', () => {
  for (const ns of localeNamespaces(root))
    for (const lng of localeCodes) {
      it(`${ns}/${lng} preserves all existing translations and plural lookups`, async () => {
        const original = createInstance();
        const compiled = createInstance();
        const resources = Object.fromEntries(
          localeCodes.map((language) => [
            language,
            { [ns]: readLocale(root, ns, language) },
          ]),
        );
        await original.init({
          lng,
          ns: [ns],
          defaultNS: ns,
          resources,
          fallbackLng: lng === 'zh-TW' ? ['zh', 'en'] : ['en'],
          interpolation: { escapeValue: false },
        });
        const originalBundle = bundledLocale(root, ns, lng);
        const [languages, packed] = packLocaleResources(originalBundle);
        const bundled = unpackLocaleResources(
          languages,
          packed as PackedLocaleNode,
        );
        expect(bundled).toEqual(originalBundle);
        const merged = Object.assign({}, ...Object.values(bundled));
        await compiled.init({
          lng,
          ns: [ns],
          defaultNS: ns,
          resources: Object.fromEntries(
            Object.entries(bundled).map(([lang, value]) => [
              lang,
              { [ns]: value },
            ]),
          ),
          fallbackLng: lng === 'zh-TW' ? ['zh', 'en'] : ['en'],
          interpolation: { escapeValue: false },
        });
        for (const key of keys(merged)) {
          for (const count of [0, 1, 2]) {
            expect(
              compiled.t(key, { count }),
              `${lng}:${ns}:${key}:${count}`,
            ).toEqual(original.t(key, { count }));
          }
        }
      });
    }
});

it('preserves missing, null, arrays and different shapes across languages', () => {
  const resources = {
    en: {
      empty: '',
      nullable: null,
      list: ['a'],
      mixed: 'text',
      only: 'English',
    },
    zh: { empty: '空', nullable: '有', list: [], mixed: { key: '值' } },
  };
  const [languages, packed] = packLocaleResources(resources);
  expect(unpackLocaleResources(languages, packed as PackedLocaleNode)).toEqual(
    resources,
  );
});
