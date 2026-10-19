import { describe, expect, it } from 'vitest';
import { readStoredStatus } from './statusStorage';

const createStorage = (value: string | null) => {
  const removedKeys: string[] = [];
  const storage: Pick<Storage, 'getItem' | 'removeItem'> = {
    getItem: () => value,
    removeItem: (key) => removedKeys.push(key),
  };

  return { removedKeys, storage };
};

describe('readStoredStatus', () => {
  it('returns a saved status object', () => {
    const { storage } = createStorage('{"email_verification":true}');

    expect(readStoredStatus(storage)).toEqual({ email_verification: true });
  });

  it('returns undefined when no status is saved', () => {
    const { removedKeys, storage } = createStorage(null);

    expect(readStoredStatus(storage)).toBeUndefined();
    expect(removedKeys).toEqual([]);
  });

  it.each(['not-json', '[]', '"legacy"', 'false'])(
    'clears an invalid saved status: %s',
    (value) => {
      const { removedKeys, storage } = createStorage(value);

      expect(readStoredStatus(storage)).toBeUndefined();
      expect(removedKeys).toEqual(['status']);
    },
  );
});
