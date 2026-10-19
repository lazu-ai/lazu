import { describe, expect, it } from 'vitest';
import { getUsageLogOwnerScope } from './usageLogOwnerScope';

describe('usage-log deep-link filters', () => {
  it('keeps the display owner and immutable user ID together', () => {
    expect(
      getUsageLogOwnerScope('?owner=alice-new&user_id=42&key=production'),
    ).toMatchObject({
      key: 'production',
      userID: '42',
      username: 'alice-new',
    });
  });

  it('does not invent an immutable scope for legacy owner-only links', () => {
    expect(getUsageLogOwnerScope('?owner=alice-old')).toMatchObject({
      userID: '',
      username: 'alice-old',
    });
  });

  it('reads the model a plaza card links with', () => {
    expect(getUsageLogOwnerScope('?model=gpt-5').model).toBe('gpt-5');
    expect(getUsageLogOwnerScope('?model_name=claude-opus-5').model).toBe(
      'claude-opus-5',
    );
    expect(getUsageLogOwnerScope('?owner=alice').model).toBe('');
  });

  it('reads the log type the overview failure notice links with', () => {
    expect(getUsageLogOwnerScope('?type=5').logType).toBe('5');
    // An unknown type is dropped rather than sent to the API.
    expect(getUsageLogOwnerScope('?type=99').logType).toBe('');
    expect(getUsageLogOwnerScope('?owner=alice').logType).toBe('');
  });
});
