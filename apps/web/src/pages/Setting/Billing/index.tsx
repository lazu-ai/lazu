import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { billingDefaults, billingSchema } from '../schemas';
import { SettingsGroupPage, type SettingSection } from '../settingsRuntime';
import type { TFunction } from 'i18next';

/* Billing owns money policy and nothing else, in the order a payment moves
 * through the system: how it arrives, how a request is charged, when the user
 * is warned, and how they are told.
 *
 * What used to be here has gone where it belongs. The per-model sell-price
 * editor was a second form over the same `model_sell_prices` columns that
 * Model Operations already edits in its row expander, and `/console/pricing`
 * has redirected to Model Operations for some time; a setting has one owner,
 * so the duplicate is gone. Customer discounts are price plans (模型运营 ›
 * 专属折扣) — an operations object, not a settings form. Account rpm/tpm
 * ceilings are rate policy, so they sit under Accounts & access as 速率限制.
 *
 * Spec: brand/foundations/yunmu-console.md, "Locked settings surfaces" rule 6.
 */
/* The same normalisation low_balance_notify_gate.go applies before sending:
   positive, de-duplicated, sorted, always starting at day 1, and the default
   schedule when nothing is left. Saving anything else reloads as a cadence the
   mail does not follow. */
const DEFAULT_REMIND_DAYS = ['1', '3', '7', '15', '30'];
const normalizeRemindDays = (items: string[]) => {
  const days = [
    ...new Set(
      items
        .map((item) => Number(item))
        .filter((d) => Number.isInteger(d) && d > 0),
    ),
  ].sort((a, b) => a - b);
  if (!days.length) return DEFAULT_REMIND_DAYS;
  if (days[0] !== 1) days.unshift(1);
  return days.map(String);
};

const createSections = (t: TFunction): SettingSection[] => [
  {
    id: 'inflow',
    title: t('page.console.settingV2.billing.inflow.title', {
      defaultValue: '钱怎么进来',
    }),
    description: t('page.console.settingV2.billing.inflow.description'),
    /* No Stripe status band here. "Stripe 已连接" told a reader nothing they
       could act on — the credentials come from the deployment either way, and
       a payment that fails shows up in the wallet, not in a settings header. */
    fields: [
      {
        key: 'QuotaForNewUser',
        kind: 'money',
        prefix: '$',
        width: 'sm',
        label: t('page.console.settingV2.ops.field.quotaForNewUser'),
        description: t('page.console.settingV2.ops.field.quotaForNewUserHelp'),
      },
      /* Whole dollars, not money: the floor counts $1 checkout units, and the
         wallet and the pay endpoint both read it from /api/status. */
      {
        key: 'MinTopUpUSD',
        kind: 'number',
        prefix: '$',
        width: 'sm',
        label: t('page.console.settingV2.billing.field.minTopUp'),
        description: t('page.console.settingV2.billing.field.minTopUpHelp'),
      },
    ],
  },
  {
    id: 'charging',
    title: t('page.console.settingV2.billing.charging.title', {
      defaultValue: '请求怎么算',
    }),
    description: t('page.console.settingV2.billing.charging.description'),
    fields: [
      /* Not an amount: the gateway reserves max(prompt tokens, this) before a
         streaming call, so it is a token floor. It carried a money label and a
         money help string for as long as it has existed. */
      {
        key: 'PreConsumedQuota',
        kind: 'number',
        unit: t('page.console.settingV2.billing.field.tokensUnit', {
          defaultValue: 'tokens',
        }),
        width: 'sm',
        label: t('page.console.settingV2.billing.field.preConsumed'),
        description: t('page.console.settingV2.billing.field.preConsumedHelp'),
      },
    ],
  },
  {
    id: 'refusal',
    title: t('page.console.settingV2.billing.refusal.title', {
      defaultValue: '余额不足时',
    }),
    description: t('page.console.settingV2.billing.refusal.description'),
    fields: [
      {
        key: 'email_setting.low_balance_enabled',
        kind: 'boolean',
        label: t('page.console.settingV2.billing.field.lowBalance'),
        description: t('page.console.settingV2.billing.field.lowBalanceHelp'),
      },
      {
        key: 'email_setting.low_balance_threshold_usd',
        kind: 'number',
        indent: true,
        prefix: '$',
        width: 'sm',
        label: t('page.console.settingV2.billing.field.lowBalanceThreshold'),
        description: t(
          'page.console.settingV2.billing.field.lowBalanceThresholdHelp',
        ),
      },
      {
        key: 'email_setting.low_balance_remind_days',
        kind: 'chips',
        chipKind: 'number',
        lockedChips: ['1'],
        normalizeList: normalizeRemindDays,
        indent: true,
        label: t('page.console.settingV2.billing.field.remindDays'),
        description: t('page.console.settingV2.billing.field.remindDaysHelp'),
        placeholder: '7',
      },
    ],
  },
  {
    id: 'notice',
    title: t('page.console.settingV2.billing.notice.title', {
      defaultValue: '通知邮件',
    }),
    description: t('page.console.settingV2.billing.notice.description'),
    fields: [
      /* Wording and layout are fixed in code (brand/foundations/emails.md);
         the editable templates and payment-mail switches are retired. */
      {
        key: 'email_setting.balance_change_enabled',
        kind: 'boolean',
        label: t('page.console.settingV2.billing.field.balanceChange'),
        description: t(
          'page.console.settingV2.billing.field.balanceChangeHelp',
        ),
      },
    ],
  },
];

export default function BillingSetting() {
  const { t } = useTranslation();
  const sections = useMemo(() => createSections(t), [t]);

  return (
    <SettingsGroupPage
      defaults={billingDefaults}
      schema={billingSchema}
      sections={sections}
      title={t('page.console.settingV2.billing.title')}
    />
  );
}
