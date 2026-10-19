/**
 * The console model and channel queries (`useModelsQuery`, `useChannelsQuery`)
 * are still untyped at the hook boundary — they hand back a bag whose shape is
 * whatever the API returned. This page consumes them through one named type
 * rather than sprinkling `any` across every call site, so the debt has a single
 * home and a single suppression.
 *
 * Replacing it means giving those two shared hooks real return types, which
 * touches every other page that calls them and is deliberately out of scope
 * here. When that happens, delete this file and the type errors will point at
 * everything that needs narrowing.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type ApiRecord = Record<string, any>;
