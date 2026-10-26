/** Locale trees have almost identical keys. Store each key once, with the
 * translations next to each other, so gzip can also share Chinese text.
 * Presence bits preserve missing keys, null, empty strings and plural forms.
 * The reconstructed resources remain separate i18next languages. */
export type PackedLocaleNode = [number, ...unknown[]];

export function unpackLocaleResources(
  languages: string[],
  tree: PackedLocaleNode,
): Record<string, Record<string, unknown>> {
  const unpack = (node: PackedLocaleNode): unknown[] => {
    const [flag, ...values] = node;
    const mask = Math.abs(flag);
    if (flag >= 0) {
      let index = 0;
      return languages.map((_, bit) =>
        mask & (1 << bit) ? values[index++] : undefined,
      );
    }
    const records = languages.map((_, bit) =>
      mask & (1 << bit)
        ? (Object.create(null) as Record<string, unknown>)
        : undefined,
    );
    for (const [key, child] of Object.entries(
      values[0] as Record<string, PackedLocaleNode>,
    )) {
      const decoded = unpack(child);
      records.forEach((record, bit) => {
        if (record && decoded[bit] !== undefined) record[key] = decoded[bit];
      });
    }
    return records;
  };
  const resources = unpack(tree);
  return Object.fromEntries(
    languages.map((language, index) => [
      language,
      resources[index] as Record<string, unknown>,
    ]),
  );
}

/** What the locale backend answers for `language` from a chunk built for
 * `bundleLanguage`. A chunk carries only its own language, with fallbacks
 * merged in at build time. A fallback language it does not carry gets no
 * data: i18next then registers no bundle and loads that language's own
 * chunk if the user switches to it. An empty object would be cached as a
 * loaded, empty language and show raw keys after the switch. */
export function chunkResourceFor(
  resources: Record<string, Record<string, unknown> | undefined>,
  language: string,
  bundleLanguage: string,
): Record<string, unknown> | null {
  const own = resources[language];
  if (own) return own;
  return language === bundleLanguage ? {} : null;
}
