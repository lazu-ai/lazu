import { getMicroUSDPerUSD } from '../../helpers/render';

export const DEFAULT_QUOTA_WARNING_THRESHOLD_USD = 0.5;

const safeMicroUSDPerUSD = () => {
  try {
    return getMicroUSDPerUSD();
  } catch {
    return 1_000_000;
  }
};

type NotificationSettingsShape = {
  warningType: string;
  warningThreshold: number;
  webhookUrl?: string;
  webhookSecret?: string;
  notificationEmail?: string;
  barkUrl?: string;
  gotifyUrl?: string;
  gotifyToken?: string;
  gotifyPriority: number;
};

type MailPrefs = {
  weeklyDigest: boolean;
  productUpdates: boolean;
  timezone: string;
};

export const defaultNotificationSettings: NotificationSettingsShape = {
  warningType: 'email',
  warningThreshold: DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
  webhookUrl: '',
  webhookSecret: '',
  notificationEmail: '',
  barkUrl: '',
  gotifyUrl: '',
  gotifyToken: '',
  gotifyPriority: 5,
};

export const zheOf = (pct: number) => Math.round((100 - pct) * 10) / 100;

export const settingsFromUser = (
  user: { setting?: string } | null | undefined,
  microUSDPerUSD = safeMicroUSDPerUSD(),
): NotificationSettingsShape => {
  if (!user?.setting) return defaultNotificationSettings;
  try {
    const settings = JSON.parse(user.setting) as Record<string, unknown>;
    return {
      warningType:
        typeof settings.notify_type === 'string' && settings.notify_type
          ? settings.notify_type
          : 'email',
      warningThreshold:
        settings.quota_warning_threshold != null
          ? Number(settings.quota_warning_threshold) / microUSDPerUSD
          : DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
      webhookUrl:
        typeof settings.webhook_url === 'string' ? settings.webhook_url : '',
      webhookSecret:
        typeof settings.webhook_secret === 'string'
          ? settings.webhook_secret
          : '',
      notificationEmail:
        typeof settings.notification_email === 'string'
          ? settings.notification_email
          : '',
      barkUrl: typeof settings.bark_url === 'string' ? settings.bark_url : '',
      gotifyUrl:
        typeof settings.gotify_url === 'string' ? settings.gotify_url : '',
      gotifyToken:
        typeof settings.gotify_token === 'string' ? settings.gotify_token : '',
      gotifyPriority:
        typeof settings.gotify_priority === 'number'
          ? settings.gotify_priority
          : 5,
    };
  } catch {
    return defaultNotificationSettings;
  }
};

export const toNotificationPayload = (
  values: NotificationSettingsShape,
  microUSDPerUSD = safeMicroUSDPerUSD(),
) => ({
  notify_type: String(values.warningType || ''),
  quota_warning_threshold: Math.round(
    Number(values.warningThreshold) * microUSDPerUSD,
  ),
  webhook_url: String(values.webhookUrl || ''),
  webhook_secret: String(values.webhookSecret || ''),
  notification_email: String(values.notificationEmail || ''),
  bark_url: String(values.barkUrl || ''),
  gotify_url: String(values.gotifyUrl || ''),
  gotify_token: String(values.gotifyToken || ''),
  gotify_priority:
    Number.parseInt(String(values.gotifyPriority || ''), 10) || 5,
});

export const mailPrefsFromUser = (
  user: { setting?: string } | null | undefined,
): MailPrefs => {
  const prefs: MailPrefs = {
    weeklyDigest: true,
    productUpdates: true,
    timezone: '',
  };
  if (!user?.setting) return prefs;
  try {
    const settings = JSON.parse(user.setting) as Record<string, unknown>;
    prefs.weeklyDigest = settings.weekly_digest_off !== true;
    prefs.productUpdates = settings.product_updates_off !== true;
    prefs.timezone =
      typeof settings.timezone === 'string' ? settings.timezone : '';
  } catch {
    return prefs;
  }
  return prefs;
};
