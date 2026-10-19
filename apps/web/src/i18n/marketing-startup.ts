/** Locale roots are valid with and without a trailing slash. */
export const isMarketingStartupPath = (path: string) =>
  /^\/(?:zh-TW|zh|ja)$/.test(path) ||
  /^\/(?:zh-TW\/|zh\/|ja\/)?(?:about|faq|privacy|terms)?\/?$/.test(path);
