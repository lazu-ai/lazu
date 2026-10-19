export type StoredStatus = Record<string, unknown>;

type StatusStorage = Pick<Storage, 'getItem' | 'removeItem'>;

const resolveStorage = (): StatusStorage | undefined => {
  return typeof localStorage === 'undefined' ? undefined : localStorage;
};

export const readStoredStatus = (
  storage: StatusStorage | undefined = resolveStorage(),
): StoredStatus | undefined => {
  if (!storage) return undefined;

  const raw = storage.getItem('status');
  if (!raw) return undefined;

  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as StoredStatus;
    }
  } catch {
    // Invalid JSON is handled by clearing the stale cache below.
  }

  storage.removeItem('status');
  return undefined;
};
