// The gateway reports its release through `/api/status` → `version`. Formal
// deployments stamp a semver tag; ad-hoc SHA deployments keep the short-hash
// fallback so the entry remains available without pretending a release exists.

const SHA_PATTERN = /^[0-9a-f]+$/i;

// Short-SHA length every git surface uses.
const SHORT_SHA_LENGTH = 7;

// Below this a hex string is far more likely to be a version fragment than a
// commit hash, so it is left intact.
const SHA_MIN_LENGTH = 12;

/**
 * Turn a raw status version into something a human can read in a 22px chip:
 * a commit SHA becomes its short form, a semver-looking string is left alone,
 * and anything else gains a `v` so it always reads as a version rather than a
 * stray number.
 */
export const formatVersionLabel = (raw?: unknown): string => {
  const value = typeof raw === 'string' ? raw.trim() : '';
  if (!value) return '';
  if (value.length >= SHA_MIN_LENGTH && SHA_PATTERN.test(value)) {
    return value.slice(0, SHORT_SHA_LENGTH);
  }
  return value.startsWith('v') ? value : `v${value}`;
};
