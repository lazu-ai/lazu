import { z } from 'zod';

const numberField = z.coerce.number().min(0).optional();
/* For settings the backend parses with strconv.Atoi or stores in a Go int. A
   fractional value used to pass validation, fail Atoi, silently become zero and
   still report success — so "1.5 retries" saved as "no retries". */
const intField = z.coerce.number().int().min(0).optional();
/* Money is edited in USD and stored as whole microdollars. A value finer than
   one microdollar used to round to zero on the way out while the page kept
   showing what was typed, so a grant could report success and apply nothing. */
const moneyField = z.coerce
  .number()
  .min(0)
  .refine(
    (value) =>
      Math.abs(value * 1_000_000 - Math.round(value * 1_000_000)) < 1e-6,
    { message: 'At most 6 decimal places (one microdollar).' },
  )
  .optional();
const percentField = z.coerce.number().int().min(0).max(100).optional();
/* thinking_adapter_budget_tokens_percentage is named for a percentage but
   holds a 0-1 fraction the relay multiplies MaxTokens by. */
const fractionField = z.coerce.number().min(0).max(1).optional();
const stringField = z.string().optional();
const boolField = z.boolean().optional();

/* Accounts & access: who may enter, how, and what this deployment looks like.
 *
 * PasswordLoginEnabled is deliberately absent. The option was retired with the
 * GitHub OAuth surface — startup deletes its row and UpdateOption ignores
 * writes to it — so a switch here would silently revert on the next restart.
 */
export const accessDefaults = {
  RegisterEnabled: true,
  PasswordRegisterEnabled: true,
  EmailVerificationEnabled: false,
  EmailCodeAuthEnabled: false,
  EmailDomainRestrictionEnabled: false,
  EmailDomainWhitelist: '',
  SidebarModulePreset: 'saas',
  ProjectsEnabled: false,
  ProjectsAllowedUsers: '',
  // Account tiers are rate policy, so rpm/tpm and the upgrade thresholds sit
  // with access rather than with money. See yunmu-console.md rule 7.
};

export const accessSchema = z.object({
  RegisterEnabled: boolField,
  PasswordRegisterEnabled: boolField,
  EmailVerificationEnabled: boolField,
  EmailCodeAuthEnabled: boolField,
  EmailDomainRestrictionEnabled: boolField,
  EmailDomainWhitelist: stringField,
  SidebarModulePreset: stringField,
  ProjectsEnabled: boolField,
  ProjectsAllowedUsers: stringField,
});

/* Observability: how usage is summarised and how a failing upstream is taken
 * out of rotation.
 *
 * LogConsumeEnabled stays on this surface. It was briefly removed on the
 * assumption that env/yaml could set it, but nothing loads it from either: a
 * deployment with a stored `false` would have had no way back to consumption
 * logs short of editing the database.
 *
 * DataExportDefaultTime stays: the dashboard reads it for its first bucket size.
 */
export const observabilityDefaults = {
  LogConsumeEnabled: true,
  DataExportEnabled: false,
  DataExportDefaultTime: 'hour',
  DataExportInterval: 5,
  AutomaticDisableChannelEnabled: false,
  AutomaticEnableChannelEnabled: false,
  ChannelDisableThreshold: 5,
  AutomaticDisableKeywords: '',
  'performance_setting.monitor_enabled': true,
  'performance_setting.monitor_cpu_threshold': 90,
  'performance_setting.monitor_memory_threshold': 90,
  'performance_setting.monitor_disk_threshold': 95,
  'performance_setting.monitor_active_connections_threshold': 0,
};

export const observabilitySchema = z.object({
  LogConsumeEnabled: boolField,
  DataExportEnabled: boolField,
  DataExportDefaultTime: stringField,
  DataExportInterval: intField,
  AutomaticDisableChannelEnabled: boolField,
  AutomaticEnableChannelEnabled: boolField,
  ChannelDisableThreshold: numberField,
  AutomaticDisableKeywords: stringField,
  'performance_setting.monitor_enabled': boolField,
  'performance_setting.monitor_cpu_threshold': percentField,
  'performance_setting.monitor_memory_threshold': percentField,
  'performance_setting.monitor_disk_threshold': percentField,
  'performance_setting.monitor_active_connections_threshold': intField,
});

/* Billing owns money policy in the order a payment moves through the system:
 * how it arrives, how a request is charged, when the gateway warns, and how
 * the user is told. Per-model prices are Model Operations — a price is an
 * attribute of the model it belongs to. See yunmu-console.md rule 6.
 *
 * QuotaRemindThreshold is gone: it and email_setting.low_balance_threshold_usd
 * held the same number, and the email one won whenever low-balance mail was
 * enabled, so the console field was usually inert. A startup migration folds
 * the old value into the surviving key.
 *
 * quota_display_type is gone: amounts are USD everywhere, and a Tokens display
 * mode contradicted that.
 */
export const billingDefaults = {
  QuotaForNewUser: 0,
  MinTopUpUSD: 5,
  PreConsumedQuota: 500,
  'email_setting.low_balance_enabled': true,
  'email_setting.low_balance_threshold_usd': 20,
  'email_setting.low_balance_remind_days': '[1,3,7,15,30]',
  'email_setting.balance_change_enabled': true,
};

export const billingSchema = z.object({
  QuotaForNewUser: moneyField,
  /* Whole dollars, stored as typed (not microUSD): it is the quantity of $1
     checkout units a payment must reach. The server holds the same range. */
  MinTopUpUSD: z.coerce.number().int().min(1).max(10_000).optional(),
  PreConsumedQuota: intField,
  'email_setting.low_balance_enabled': boolField,
  'email_setting.low_balance_threshold_usd': numberField,
  'email_setting.low_balance_remind_days': stringField,
  'email_setting.balance_change_enabled': boolField,
});

/* Site identity, the documents a visitor has to agree to, and the mail this
 * deployment sends on its own behalf.
 *
 * Notice is gone: the web app never requested /api/notice, and announcements —
 * which carry a timeline, a read state and optional mail — took the job over.
 */
export const brandingDefaults = {
  SystemName: 'Lazu',
  Logo: '',
  Footer: '',
  'general_setting.docs_link': '',
  'legal.user_agreement_zh': '',
  'legal.user_agreement_en': '',
  'legal.user_agreement_zh_tw': '',
  'legal.user_agreement_ja': '',
  'legal.privacy_policy_zh': '',
  'legal.privacy_policy_en': '',
  'legal.privacy_policy_zh_tw': '',
  'legal.privacy_policy_ja': '',
  'email_setting.admin_email': '',
};

export const brandingSchema = z.object({
  SystemName: stringField,
  Logo: stringField,
  Footer: stringField,
  'general_setting.docs_link': stringField,
  'legal.user_agreement_zh': stringField,
  'legal.user_agreement_en': stringField,
  'legal.user_agreement_zh_tw': stringField,
  'legal.user_agreement_ja': stringField,
  'legal.privacy_policy_zh': stringField,
  'legal.privacy_policy_en': stringField,
  'legal.privacy_policy_zh_tw': stringField,
  'legal.privacy_policy_ja': stringField,
  'email_setting.admin_email': stringField,
});

export const upstreamDefaults = {
  DecisionsEnabled: false,
  DecisionQuestionGenerationEnabled: false,
  DecisionQuestionGenerationDefaultModel: 'gpt-6-luna',
  'monitor_setting.auto_test_channel_enabled': false,
  'monitor_setting.auto_test_channel_minutes': 10,
  'general_setting.ping_interval_enabled': false,
  'general_setting.ping_interval_seconds': 0,
  'global.pass_through_request_enabled': false,
  ModelRequestRateLimitEnabled: false,
  ModelRequestRateLimitDurationMinutes: 0,
  ModelRequestRateLimitCount: 0,
  ModelRequestRateLimitSuccessCount: 0,
  'gemini.safety_settings': '',
  'gemini.version_settings': '',
  'claude.model_headers_settings': '',
  'claude.thinking_adapter_enabled': false,
  'claude.thinking_adapter_budget_tokens_percentage': 0.8,
};

export const upstreamSchema = z.object({
  DecisionsEnabled: boolField,
  DecisionQuestionGenerationEnabled: boolField,
  DecisionQuestionGenerationDefaultModel: stringField,
  'monitor_setting.auto_test_channel_enabled': boolField,
  /* Zero is not "never": the hosted scheduler skips it and the legacy runner
     sleeps for zero and probes in a loop. The server refuses it too. */
  'monitor_setting.auto_test_channel_minutes': z.coerce
    .number()
    .int()
    .min(1)
    .optional(),
  'general_setting.ping_interval_enabled': boolField,
  'general_setting.ping_interval_seconds': intField,
  'global.pass_through_request_enabled': boolField,
  ModelRequestRateLimitEnabled: boolField,
  ModelRequestRateLimitDurationMinutes: intField,
  ModelRequestRateLimitCount: intField,
  ModelRequestRateLimitSuccessCount: intField,
  'gemini.safety_settings': stringField,
  'gemini.version_settings': stringField,
  'claude.model_headers_settings': stringField,
  'claude.thinking_adapter_enabled': boolField,
  'claude.thinking_adapter_budget_tokens_percentage': fractionField,
});
