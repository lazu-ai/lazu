/* Mocks for features a build compiles in through src/extensions. The public
   tree has none: no extra /api/status fields and no extra endpoints. A build
   with extensions replaces this file with its own. */

export const extensionStatus: Record<string, unknown> = {};

/* The mocked response body for an extension endpoint, or undefined to let
   the core mocks answer. */
export function extensionMockFor(_pathname: string): unknown {
  return undefined;
}
