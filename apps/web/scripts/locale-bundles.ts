import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import type { Plugin } from 'vite';

export const localeCodes = ['en', 'zh', 'zh-TW', 'ja'] as const;
const coreNamespaces = ['translation', 'marketing', 'errors', 'projects'];

/* Extensions declare their namespaces in src/extensions/<name>/locales.json:
   { "<namespace>": "<path under src with {lang}>" }, where the path is a
   JSON file or a folder of JSON files merged in name order. */
export function extensionLocalePaths(root: string): Record<string, string> {
  const dir = resolve(root, 'src/extensions');
  if (!existsSync(dir)) return {};
  return Object.assign(
    {},
    ...readdirSync(dir, { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => resolve(dir, entry.name, 'locales.json'))
      .filter((file) => existsSync(file))
      .sort()
      .map((file) => JSON.parse(readFileSync(file, 'utf8')) as object),
  ) as Record<string, string>;
}

export const localeNamespaces = (root: string) => [
  ...coreNamespaces,
  ...Object.keys(extensionLocalePaths(root)).filter(
    (namespace) => !coreNamespaces.includes(namespace),
  ),
];
type Resource = Record<string, unknown>;
const record = (v: unknown): v is Resource =>
  !!v && typeof v === 'object' && !Array.isArray(v);

// Keep each language separate: plural resolution must use the fallback's
// own grammar. A single compressed chunk shares repeated keys on the wire.
export function readLocale(
  root: string,
  namespace: string,
  language: string,
  watch = (_file: string) => {},
) {
  const read = (file: string): Resource => {
    const absolute = resolve(root, 'src', file);
    watch(absolute);
    const data = JSON.parse(readFileSync(absolute, 'utf8')) as Resource;
    return record(data.translation) ? data.translation : data;
  };
  const readPath = (path: string): Resource => {
    const located = path.replaceAll('{lang}', language);
    if (located.endsWith('.json')) return read(located);
    return Object.assign(
      {},
      ...readdirSync(resolve(root, 'src', located))
        .filter((f) => f.endsWith('.json'))
        .sort()
        .map((f) => read(`${located}/${f}`)),
    ) as Resource;
  };
  const extensionPath = extensionLocalePaths(root)[namespace];
  const core: Record<string, string> = {
    translation: 'i18n/locales/{lang}.json',
    marketing: 'marketing/marketing.{lang}.json',
    projects: 'features/projects/locales/{lang}.json',
    errors: 'i18n/locales/errors/{lang}',
  };
  if (!core[namespace]) {
    if (!extensionPath)
      throw new Error(`Unknown locale namespace: ${namespace}`);
    return readPath(extensionPath);
  }
  // An extension may add keys to a core namespace; the core's copy wins.
  return {
    ...(extensionPath ? readPath(extensionPath) : {}),
    ...readPath(core[namespace]),
  };
}

/** One language's resources with its fallbacks already applied: each key
 *  takes the most specific language that has a value for it. i18next treats
 *  null as missing (returnNull: false), so null falls through as well; an
 *  empty string is a value. Arrays are values, not trees. */
export function mergeLocaleChain(chain: Resource[]): Resource {
  const merged: Resource = {};
  for (const resource of chain) {
    for (const [key, value] of Object.entries(resource)) {
      if (value === undefined || value === null) continue;
      const current = merged[key];
      merged[key] =
        record(value) && record(current)
          ? mergeLocaleChain([current as Resource, value as Resource])
          : record(value)
            ? mergeLocaleChain([value as Resource])
            : value;
    }
  }
  return merged;
}

/** The resources a language's locale chunk carries: only that language, with
 *  its fallback chain (zh-TW -> zh -> en, others -> en) resolved here at build
 *  time instead of shipping every fallback language in the chunk. */
export function bundledLocale(
  root: string,
  namespace: string,
  language: string,
  watch?: (file: string) => void,
) {
  const chain =
    language === 'zh-TW'
      ? ['en', 'zh', language]
      : language === 'en'
        ? ['en']
        : ['en', language];
  return {
    [language]: mergeLocaleChain(
      chain.map((lang) => readLocale(root, namespace, lang, watch)),
    ),
  };
}

export function packLocaleResources(
  resources: Record<string, Resource>,
): [string[], unknown] {
  const pack = (values: unknown[]): unknown => {
    const mask = values.reduce<number>(
      (bits, value, index) => bits | (value === undefined ? 0 : 1 << index),
      0,
    );
    if (values.every((value) => value === undefined || record(value))) {
      const keys = [
        ...new Set(
          values.flatMap((value) => Object.keys((value || {}) as Resource)),
        ),
      ];
      return [
        -mask,
        Object.fromEntries(
          keys.map((key) => [
            key,
            pack(values.map((value) => (value as Resource | undefined)?.[key])),
          ]),
        ),
      ];
    }
    return [mask, ...values.filter((value) => value !== undefined)];
  };
  return [Object.keys(resources), pack(Object.values(resources))];
}

export function localeBundles(root: string): Plugin {
  const prefix = 'virtual:lazu-locale/';
  return {
    name: 'lazu-locale-bundles',
    resolveId(id) {
      if (id.startsWith(prefix)) return '\0' + id;
    },
    load(id) {
      if (!id.startsWith('\0' + prefix)) return;
      const [namespace, language] = id.slice(prefix.length + 1).split('/');
      if (
        !localeNamespaces(root).some((n) => n === namespace) ||
        !localeCodes.some((l) => l === language)
      )
        throw new Error(`Unknown locale: ${id}`);
      const [languages, tree] = packLocaleResources(
        bundledLocale(root, namespace, language, (f) => this.addWatchFile(f)),
      );
      return `import { unpackLocaleResources } from ${JSON.stringify(resolve(root, 'src/i18n/locale-bundle-codec.ts'))};
export default unpackLocaleResources(${JSON.stringify(languages)}, ${JSON.stringify(tree)});`;
    },
  };
}
