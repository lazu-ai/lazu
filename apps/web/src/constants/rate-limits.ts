export type RateLimitCeiling = {
  rpm: number;
  tpm: number;
};

// Preset shares of the account's rate-limit ceiling offered as one-click fills.
// Ordered strict -> relaxed; all strictly below the ceiling so a preset is
// always a real sub-cap of the account plan.
export const RATE_LIMIT_PRESETS: Array<{ fraction: number; key: string }> = [
  { fraction: 0.2, key: 'strict' },
  { fraction: 0.5, key: 'standard' },
  { fraction: 0.8, key: 'relaxed' },
];

/** Absolute RPM/TPM a preset fraction maps to for a given ceiling (min 1). */
export function presetValues(
  ceiling: RateLimitCeiling,
  fraction: number,
): RateLimitCeiling {
  return {
    rpm: Math.max(1, Math.round(ceiling.rpm * fraction)),
    tpm: Math.max(1, Math.round(ceiling.tpm * fraction)),
  };
}
