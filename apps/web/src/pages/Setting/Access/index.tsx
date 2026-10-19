import { ArrowRight } from 'lucide-react';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import AccessAddresses from './AccessAddresses';
import RateLimitSummary from './RateLimitSummary';
import { webExtensions } from '../../../extensions';
import { useSettingsNavigationBlocked } from '../headerActions';
import { accessDefaults, accessSchema } from '../schemas';
import { SettingsGroupPage, type SettingSection } from '../settingsRuntime';
import type { TFunction } from 'i18next';

const createSections = (
  t: TFunction,
  rateLimitAction: SettingSection['headerAction'],
): SettingSection[] => [
  {
    id: 'registration',
    title: t('page.console.settingV2.access.registration.title'),
    description: t('page.console.settingV2.access.registration.description'),
    fields: [
      {
        key: 'RegisterEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.access.field.register'),
        description: t('page.console.settingV2.access.field.registerHelp'),
        danger: true,
      },
      {
        key: 'PasswordRegisterEnabled',
        kind: 'boolean',
        indent: true,
        label: t('page.console.settingV2.access.field.passwordRegister'),
        description: t(
          'page.console.settingV2.access.field.passwordRegisterHelp',
        ),
      },
      {
        key: 'EmailVerificationEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.access.field.emailVerification'),
        description: t(
          'page.console.settingV2.access.field.emailVerificationHelp',
        ),
      },
      {
        key: 'EmailCodeAuthEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.access.field.emailCodeAuth'),
        description: t('page.console.settingV2.access.field.emailCodeAuthHelp'),
      },
      {
        key: 'EmailDomainRestrictionEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.access.field.domainRestriction'),
        description: t(
          'page.console.settingV2.access.field.domainRestrictionHelp',
        ),
      },
      {
        key: 'EmailDomainWhitelist',
        kind: 'chips',
        indent: true,
        separator: 'csv',
        label: t('page.console.settingV2.access.field.domainWhitelist'),
        description: t(
          'page.console.settingV2.access.field.domainWhitelistHelp',
        ),
        placeholder: t(
          'page.console.settingV2.access.field.domainPlaceholder',
          {
            defaultValue: 'example.com',
          },
        ),
      },
    ],
  },
  /* Sidebar preset and the product switches describe what this deployment
     looks like, not who may enter it. They were filed under security, where
     anyone hunting for such a toggle would never think to look. */
  {
    id: 'deployment',
    title: t('page.console.settingV2.access.deployment.title'),
    description: t('page.console.settingV2.access.deployment.description'),
    fields: [
      {
        key: 'SidebarModulePreset',
        kind: 'select',
        label: t('page.console.settingV2.access.field.sidebarPreset'),
        description: t('page.console.settingV2.access.field.sidebarPresetHelp'),
        options: [
          {
            label: t('page.console.settingV2.access.option.saas'),
            value: 'saas',
          },
          {
            label: t('page.console.settingV2.access.option.selfHosted'),
            value: 'self-hosted',
          },
        ],
      },
      ...webExtensions().flatMap(
        (extension) => extension.deploymentSettings?.fields(t) ?? [],
      ),
      /* Team projects shipped behind an environment variable that production
         never passed to the container, so the feature was invisible to
         everyone. It is a stored option now, like every other switch on this
         page. */
      {
        key: 'ProjectsEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.access.field.projects'),
        description: t('page.console.settingV2.access.field.projectsHelp'),
      },
      {
        key: 'ProjectsAllowedUsers',
        kind: 'chips',
        indent: true,
        separator: 'csv',
        label: t('page.console.settingV2.access.field.projectsAllowlist'),
        description: t(
          'page.console.settingV2.access.field.projectsAllowlistHelp',
        ),
        placeholder: t(
          'page.console.settingV2.access.field.projectsAllowlistPlaceholder',
          { defaultValue: 'usr_01ABCDEF...' },
        ),
      },
    ],
  },
  /* Extra API addresses, each opened to chosen accounts. The gateway refuses
     a Key on an address its paying account was not opened to. */
  {
    id: 'access-addresses',
    title: t('page.console.settingV2.access.addresses.title', {
      defaultValue: '接入地址',
    }),
    description: t('page.console.settingV2.access.addresses.description', {
      defaultValue: '新加的地址默认谁都不能用，到客户账户里逐个开通。',
    }),
    body: <AccessAddresses />,
    fields: [],
  },
  /* Rate limits are how fast an account may go, not what it is charged, so
     they sit with access rather than with money. They replaced the spend
     tiers; discounts moved to 模型运营 › 专属折扣. */
  {
    id: 'rate-limits',
    title: t('page.console.settingV2.access.rateLimits.title', {
      defaultValue: '速率限制',
    }),
    headerAction: rateLimitAction,
    body: <RateLimitSummary />,
    fields: [],
  },
];

export default function AccessSetting() {
  const { t } = useTranslation();
  const navigationBlocked = useSettingsNavigationBlocked();

  /* The editor is another page, so unsaved settings would be lost on the way.
     A title attribute explained that only on hover; the reason is now on the
     page next to the link it disables. */
  const rateLimitAction = (
    <span className='set-card__act-row'>
      {navigationBlocked ? (
        <span className='set-card__note' role='status'>
          {t('page.console.settingV2.access.rateLimits.saveFirst', {
            defaultValue: '请先保存或撤销本页改动',
          })}
        </span>
      ) : null}
      <Link
        aria-disabled={navigationBlocked || undefined}
        className='set-card__link'
        onClick={(event) => {
          if (navigationBlocked) event.preventDefault();
        }}
        to='/console/setting?tab=rate-limits'
      >
        {t('page.console.settingV2.access.rateLimits.edit', {
          defaultValue: '编辑',
        })}
        <ArrowRight aria-hidden='true' size={14} />
      </Link>
    </span>
  );

  const sections = useMemo(
    () => createSections(t, rateLimitAction),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, navigationBlocked],
  );

  return (
    <SettingsGroupPage
      defaults={{
        ...accessDefaults,
        ...Object.assign(
          {},
          ...webExtensions().map(
            (extension) => extension.deploymentSettings?.defaults ?? {},
          ),
        ),
      }}
      schema={accessSchema.extend(
        Object.assign(
          {},
          ...webExtensions().map(
            (extension) => extension.deploymentSettings?.schema ?? {},
          ),
        ),
      )}
      sections={sections}
      title={t('page.console.settingV2.access.title')}
    />
  );
}
