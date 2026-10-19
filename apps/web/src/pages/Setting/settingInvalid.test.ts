import { describe, expect, it } from 'vitest';
import { readSettingInvalid } from './settingInvalid';
import { LazuApiError } from '../../lib/api-error';
import type { TFunction } from 'i18next';

const t = ((key: string, values?: Record<string, unknown>) =>
  `${key}${values ? JSON.stringify(values) : ''}`) as unknown as TFunction;

const refusal = (details: Record<string, unknown>, code = 'setting_invalid') =>
  new LazuApiError({
    code,
    type: 'invalid_request_error',
    lazu: 'LZ-7401',
    message: '',
    requestId: 'req_1',
    status: 400,
    docs: '',
    details,
  });

describe('readSettingInvalid', () => {
  it('names the field and turns the rule into its sentence', () => {
    expect(
      readSettingInvalid(
        refusal({
          field: 'monitor_setting.auto_test_channel_minutes',
          rule: 'min_integer',
          limit: 1,
        }),
        t,
      ),
    ).toEqual({
      field: 'monitor_setting.auto_test_channel_minutes',
      message: 'page.console.settingV2.runtime.invalid.min_integer{"limit":1}',
    });
  });

  it('prefixes list items with their position', () => {
    const result = readSettingInvalid(
      refusal({
        field: 'console_setting.faq',
        rule: 'too_long',
        index: 3,
        limit: 200,
      }),
      t,
    );
    expect(result?.message).toContain(
      'page.console.settingV2.runtime.invalid.item',
    );
    expect(result?.message).toContain('"index":3');
  });

  it('ignores other codes and unknown rules fall back to format', () => {
    expect(
      readSettingInvalid(refusal({ field: 'x' }, 'invalid_request'), t),
    ).toBeNull();
    expect(
      readSettingInvalid(refusal({ field: 'x', rule: 'weird' }), t)?.message,
    ).toBe('page.console.settingV2.runtime.invalid.format{}');
  });
});
