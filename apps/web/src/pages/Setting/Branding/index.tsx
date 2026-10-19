import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import EmailStatus from './EmailStatus';
import { brandingDefaults, brandingSchema } from '../schemas';
import {
  SettingsGroupPage,
  type SettingDoc,
  type SettingSection,
} from '../settingsRuntime';
import type { TFunction } from 'i18next';

// Legal documents are maintained per UI locale; endonyms are shown as-is,
// mirroring LANGUAGE_OPTIONS in the header language switcher.
const LEGAL_LOCALES = [
  { suffix: 'zh', label: '简体中文' },
  { suffix: 'en', label: 'English' },
  { suffix: 'zh_tw', label: '繁體中文' },
  { suffix: 'ja', label: '日本語' },
];

const localeDocs = (prefix: string): SettingDoc[] =>
  LEGAL_LOCALES.map((locale) => ({
    key: `${prefix}${locale.suffix}`,
    label: locale.label,
  }));

const createSections = (
  t: TFunction,
  emailStatus: SettingSection['status'],
): SettingSection[] => [
  {
    id: 'brand',
    title: t('page.console.settingV2.branding.brand.title'),
    description: t('page.console.settingV2.branding.brand.description'),
    fields: [
      {
        key: 'SystemName',
        kind: 'text',
        label: t('page.console.settingV2.branding.field.systemName'),
        description: t('page.console.settingV2.branding.field.systemNameHelp'),
      },
      {
        key: 'Logo',
        kind: 'text',
        width: 'md',
        label: 'Logo',
        description: t('page.console.settingV2.branding.field.logoHelp'),
        placeholder: 'https://example.com/logo.svg',
      },
      {
        key: 'Footer',
        kind: 'textarea',
        label: t('page.console.settingV2.branding.field.footer', {
          defaultValue: '页脚',
        }),
        description: t('page.console.settingV2.branding.field.footerHelp'),
      },
      {
        key: 'general_setting.docs_link',
        kind: 'text',
        width: 'md',
        label: t('page.console.settingV2.branding.field.docsLink'),
        description: t('page.console.settingV2.branding.field.docsLinkHelp'),
      },
    ],
  },
  /* Eight textareas — four languages of two documents — were eight rows of the
     tallest kind this page has, for content edited about once a year. One row
     per document says which languages are filled and opens the editor on the
     language you click. */
  {
    id: 'legal',
    title: t('page.console.settingV2.branding.legal.title', {
      defaultValue: '法律文本',
    }),
    description: t('page.console.settingV2.branding.legal.description'),
    fields: [
      {
        key: 'legal.user_agreement',
        kind: 'docs',
        docs: localeDocs('legal.user_agreement_'),
        label: t('page.console.settingV2.branding.legal.userAgreement.title'),
        placeholder: t(
          'page.console.settingV2.branding.legal.userAgreement.placeholder',
        ),
      },
      {
        key: 'legal.privacy_policy',
        kind: 'docs',
        docs: localeDocs('legal.privacy_policy_'),
        label: t('page.console.settingV2.branding.legal.privacyPolicy.title'),
        placeholder: t(
          'page.console.settingV2.branding.legal.privacyPolicy.placeholder',
        ),
      },
    ],
  },
  {
    id: 'email',
    title: t('page.console.settingV2.branding.emailTemplates.title'),
    description: t(
      'page.console.settingV2.branding.emailTemplates.description',
    ),
    status: emailStatus,
    /* email_setting.from_name is not here on purpose. SMTP takes its display
       name from AppConfig.SMTP.FromName and Resend from its configured From
       address, so the runtime option reaches no outgoing message; `common`
       cannot import the setting package to read it without an import cycle.
       A switch that reports success and changes nothing is worse than no
       switch, so the sender name stays deployment state until delivery
       actually consults the option. */
    fields: [
      /* One address receives every operational alert — disk, error rate,
         payment documents, Stripe disputes and backups — so it lives with the
         platform's mail rather than under billing. */
      {
        key: 'email_setting.admin_email',
        kind: 'text',
        width: 'md',
        label: t('page.console.settingV2.billing.field.adminEmail'),
        description: t('page.console.settingV2.billing.field.adminEmailHelp'),
        placeholder: 'ops@example.com',
      },
    ],
  },
];

export default function BrandingSetting() {
  const { t } = useTranslation();
  const sections = useMemo(() => createSections(t, <EmailStatus />), [t]);

  return (
    <SettingsGroupPage
      defaults={brandingDefaults}
      schema={brandingSchema}
      sections={sections}
      title={t('page.console.settingV2.branding.title')}
    />
  );
}
