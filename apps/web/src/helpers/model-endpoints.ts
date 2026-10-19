// Shared helper: normalize a model record's endpoint list across the three
// shapes the backend may return — array of strings, array on
// `supported_endpoint_types`, or comma-separated string.
//
// Both the admin model page and the marketplace need this; before extracting
// here it was duplicated three times across pages/Model/index.tsx alone.

export function parseModelEndpoints(
  record: Record<string, unknown> | null | undefined,
): string[] {
  if (!record) return [];

  if (Array.isArray(record.endpoints)) {
    return record.endpoints.filter((x): x is string => typeof x === 'string');
  }

  if (Array.isArray(record.supported_endpoint_types)) {
    return record.supported_endpoint_types.filter(
      (x): x is string => typeof x === 'string',
    );
  }

  if (typeof record.endpoints === 'string') {
    return record.endpoints
      .split(',')
      .map((e) => e.trim())
      .filter(Boolean);
  }

  return [];
}
