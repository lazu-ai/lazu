import { webExtensions } from '../../extensions';

type SidebarSectionConfig = Record<string, unknown>;
export type SidebarModulesConfig = Record<string, SidebarSectionConfig>;
export type SidebarModulePresetName = 'saas' | 'self-hosted';

type SidebarPresetConfig = {
  admin: SidebarModulesConfig;
  user: SidebarModulesConfig;
};

type SidebarSectionDefinition = {
  description: string;
  key: string;
  modules: Array<{
    description: string;
    key: string;
    title: string;
  }>;
  title: string;
};

export const SIDEBAR_SECTION_DEFINITIONS: SidebarSectionDefinition[] = [
  {
    key: 'workspace',
    title: 'sidebar.section.workspace.title',
    description: 'sidebar.section.workspace.description',
    modules: [
      {
        key: 'detail',
        title: 'sidebar.module.detail.title',
        description: 'sidebar.module.detail.description',
      },
      {
        key: 'model_market',
        title: 'sidebar.module.modelMarket.title',
        description: 'sidebar.module.modelMarket.description',
      },
      {
        key: 'channel_health',
        title: 'sidebar.module.channelHealth.title',
        description: 'sidebar.module.channelHealth.description',
      },
      {
        key: 'token',
        title: 'sidebar.module.token.title',
        description: 'sidebar.module.token.description',
      },
      {
        key: 'usage',
        title: 'sidebar.module.usage.title',
        description: 'sidebar.module.usage.description',
      },
      {
        key: 'key_inspector',
        title: 'sidebar.module.keyInspector.title',
        description: 'sidebar.module.keyInspector.description',
      },
    ],
  },
  {
    key: 'account',
    title: 'sidebar.section.account.title',
    description: 'sidebar.section.account.description',
    modules: [
      {
        key: 'wallet',
        title: 'sidebar.module.wallet.title',
        description: 'sidebar.module.wallet.description',
      },
      {
        key: 'personal',
        title: 'sidebar.module.personal.title',
        description: 'sidebar.module.personal.description',
      },
    ],
  },
  {
    key: 'admin',
    title: 'sidebar.section.admin.title',
    description: 'sidebar.section.admin.description',
    modules: [
      {
        /* 模型管理 + 定价运营 + 渠道 are one page (模型运营) now, so they are one
           toggle. The i18n keys still point at the old 模型管理 strings until the
           consolidated locale pass re-words them. */
        key: 'model_ops',
        title: 'sidebar.module.models.title',
        description: 'sidebar.module.models.description',
      },
      {
        key: 'search_backend',
        title: 'sidebar.module.searchBackend.title',
        description: 'sidebar.module.searchBackend.description',
      },
      {
        key: 'user',
        title: 'sidebar.module.user.title',
        description: 'sidebar.module.user.description',
      },
      {
        key: 'setting',
        title: 'sidebar.module.setting.title',
        description: 'sidebar.module.setting.description',
      },
      {
        key: 'announcements',
        title: 'sidebar.module.announcements.title',
        description: 'sidebar.module.announcements.description',
      },
    ],
  },
];

const SAAS_SIDEBAR_ADMIN_CONFIG: SidebarModulesConfig = {
  workspace: {
    enabled: true,
    // These are feature gates for content embedded in retained surfaces, not
    // sidebar destinations. Keeping them out of SIDEBAR_SECTION_DEFINITIONS
    // prevents the old routes from reappearing.
    access: true,
    detail: true,
    files: true,
    model_market: true,
    channel_health: true,
    token: true,
    usage: true,
    key_inspector: false,
  },
  account: {
    enabled: true,
    wallet: true,
    personal: true,
  },
  admin: {
    enabled: true,
    model_ops: true,
    search_backend: true,
    user: true,
    setting: true,
    announcements: true,
  },
};

const SAAS_SIDEBAR_USER_CONFIG: SidebarModulesConfig = {
  workspace: {
    enabled: true,
    access: true,
    detail: true,
    files: true,
    model_market: true,
    channel_health: true,
    token: true,
    usage: true,
    key_inspector: false,
  },
  account: {
    enabled: true,
    wallet: true,
    personal: true,
  },
};

/* Self-hosted keeps the wallet — it is the ledger as well as the till, and
   the recharge panel hides itself when no payment method is configured. */
const SELF_HOSTED_SIDEBAR_ADMIN_CONFIG: SidebarModulesConfig = {
  ...SAAS_SIDEBAR_ADMIN_CONFIG,
  account: SAAS_SIDEBAR_ADMIN_CONFIG.account,
  admin: SAAS_SIDEBAR_ADMIN_CONFIG.admin,
};

const SELF_HOSTED_SIDEBAR_USER_CONFIG: SidebarModulesConfig = {
  ...SAAS_SIDEBAR_USER_CONFIG,
  account: SAAS_SIDEBAR_USER_CONFIG.account,
};

export const SIDEBAR_MODULE_PRESETS: Record<
  SidebarModulePresetName,
  SidebarPresetConfig
> = {
  saas: {
    admin: SAAS_SIDEBAR_ADMIN_CONFIG,
    user: SAAS_SIDEBAR_USER_CONFIG,
  },
  'self-hosted': {
    admin: SELF_HOSTED_SIDEBAR_ADMIN_CONFIG,
    user: SELF_HOSTED_SIDEBAR_USER_CONFIG,
  },
};

export const DEFAULT_SIDEBAR_MODULE_PRESET: SidebarModulePresetName = 'saas';

export const normalizeSidebarModulePresetName = (
  preset: unknown,
): SidebarModulePresetName => {
  if (preset === 'self-hosted' || preset === 'self_hosted') {
    return 'self-hosted';
  }
  return DEFAULT_SIDEBAR_MODULE_PRESET;
};

/* Workspace modules extensions add are on by default in both presets, like
   the core's. Merged at call time: the extension list is not ready while
   modules load. */
const withExtensionModules = (
  config: SidebarModulesConfig,
): SidebarModulesConfig => {
  const modules = Object.fromEntries(
    webExtensions().flatMap((extension) =>
      (extension.defaultModules?.workspace ?? []).map((key) => [key, true]),
    ),
  );
  if (Object.keys(modules).length === 0 || !config.workspace) return config;
  return { ...config, workspace: { ...modules, ...config.workspace } };
};

export const getSidebarPresetConfig = (
  preset: unknown = DEFAULT_SIDEBAR_MODULE_PRESET,
): SidebarPresetConfig => {
  const config =
    SIDEBAR_MODULE_PRESETS[normalizeSidebarModulePresetName(preset)];
  return {
    admin: withExtensionModules(config.admin),
    user: withExtensionModules(config.user),
  };
};

export const DEFAULT_SIDEBAR_ADMIN_CONFIG: SidebarModulesConfig =
  SIDEBAR_MODULE_PRESETS[DEFAULT_SIDEBAR_MODULE_PRESET].admin;

export const DEFAULT_SIDEBAR_USER_CONFIG: SidebarModulesConfig =
  SIDEBAR_MODULE_PRESETS[DEFAULT_SIDEBAR_MODULE_PRESET].user;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value && typeof value === 'object' && !Array.isArray(value));

const LEGACY_SIDEBAR_SECTION_MAPPINGS = [
  {
    from: 'console',
    to: 'workspace',
    modules: {
      detail: 'detail',
      log: 'usage',
      model_market: 'model_market',
      token: 'token',
      usage: 'usage',
    },
  },
  {
    from: 'personal',
    to: 'account',
    modules: {
      billing: 'wallet',
      personal: 'personal',
      topup: 'wallet',
    },
  },
] as const;

const migrateLegacySidebarModulesConfig = (
  inputConfig: Record<string, unknown>,
): Record<string, unknown> => {
  const migrated = { ...inputConfig };

  /* 模型管理 / 定价运营 / 渠道 collapsed into one 模型运营 module. A stored config
     predates that key, so derive it: if the operator had any of the three
     visible, the merged page stays visible. */
  const adminSection = inputConfig.admin;
  if (isRecord(adminSection) && adminSection.model_ops === undefined) {
    const legacyFlags = [
      adminSection.models,
      adminSection.pricing,
      adminSection.channel,
    ].filter((value) => value !== undefined);
    if (legacyFlags.length) {
      migrated.admin = {
        ...adminSection,
        model_ops: legacyFlags.some(Boolean),
      };
    }
  }

  LEGACY_SIDEBAR_SECTION_MAPPINGS.forEach(({ from, modules, to }) => {
    const legacySection = inputConfig[from];
    if (!isRecord(legacySection)) {
      return;
    }

    const targetSection = isRecord(inputConfig[to])
      ? { ...inputConfig[to] }
      : {};

    if (
      targetSection.enabled === undefined &&
      legacySection.enabled !== undefined
    ) {
      targetSection.enabled = legacySection.enabled;
    }

    /* Billing and top-up were independent legacy pages. The selected IA
       merges them into Wallet, so either enabled source must keep the merged
       destination reachable. An explicit new `account.wallet` still wins. */
    if (from === 'personal' && targetSection.wallet === undefined) {
      const legacyWalletFlags = [
        legacySection.billing,
        legacySection.topup,
      ].filter((value) => value !== undefined);
      if (legacyWalletFlags.length) {
        targetSection.wallet = legacyWalletFlags.some(Boolean);
      }
    }

    Object.entries(modules).forEach(([legacyModuleKey, targetModuleKey]) => {
      if (
        targetSection[targetModuleKey] === undefined &&
        legacySection[legacyModuleKey] !== undefined
      ) {
        targetSection[targetModuleKey] = legacySection[legacyModuleKey];
      }
    });

    migrated[to] = targetSection;
    delete migrated[from];
  });

  /* Billing and top-up merged into one wallet page; a config stored before
     that merge only knows the two old keys. */
  const account = isRecord(migrated.account) ? { ...migrated.account } : null;
  if (account) {
    if (account.wallet === undefined) {
      const legacy = [account.topup, account.billing].filter(
        (value) => value !== undefined,
      );
      if (legacy.length) account.wallet = legacy.some(Boolean);
    }
    delete account.topup;
    delete account.billing;
    migrated.account = account;
  }

  return migrated;
};

export const buildSidebarSectionConfigs = (t: (key: string) => string) =>
  SIDEBAR_SECTION_DEFINITIONS.map((section) => ({
    ...section,
    title: t(section.title),
    description: t(section.description),
    modules: section.modules.map((module) => ({
      ...module,
      title: t(module.title),
      description: t(module.description),
    })),
  }));

export const normalizeSidebarModulesConfig = (
  inputConfig: unknown = undefined,
  fallbackConfig: SidebarModulesConfig = DEFAULT_SIDEBAR_ADMIN_CONFIG,
): SidebarModulesConfig => {
  const config = isRecord(inputConfig)
    ? migrateLegacySidebarModulesConfig(inputConfig)
    : {};
  const normalized: SidebarModulesConfig = { ...fallbackConfig };

  Object.entries(config).forEach(([sectionKey, sectionConfig]) => {
    if (isRecord(sectionConfig)) {
      normalized[sectionKey] = sectionConfig;
    }
  });

  const sectionKeys = new Set([
    ...Object.keys(fallbackConfig),
    ...Object.keys(config),
  ]);

  sectionKeys.forEach((sectionKey) => {
    const fallbackSection = isRecord(fallbackConfig[sectionKey])
      ? fallbackConfig[sectionKey]
      : {};
    const sectionConfig = isRecord(config[sectionKey])
      ? config[sectionKey]
      : {};
    const mergedSection = {
      ...fallbackSection,
      ...sectionConfig,
    };

    normalized[sectionKey] = mergedSection;
  });

  return normalized;
};

export const buildEnabledSidebarConfig = (
  config: SidebarModulesConfig = {},
): SidebarModulesConfig => {
  const enabledConfig: SidebarModulesConfig = {};

  Object.entries(config).forEach(([sectionKey, sectionConfig]) => {
    if (!sectionConfig?.enabled) {
      return;
    }

    enabledConfig[sectionKey] = { enabled: true };
    Object.entries(sectionConfig).forEach(([moduleKey, enabled]) => {
      if (moduleKey !== 'enabled' && enabled === true) {
        enabledConfig[sectionKey][moduleKey] = true;
      }
    });
  });

  return enabledConfig;
};

export const filterSidebarModulesConfig = (
  config: SidebarModulesConfig = {},
  isSectionAllowed: (sectionKey: string) => boolean = () => true,
  isModuleAllowed: (sectionKey: string, moduleKey: string) => boolean = () =>
    true,
): SidebarModulesConfig => {
  const filteredConfig: SidebarModulesConfig = {};

  Object.entries(config).forEach(([sectionKey, sectionConfig]) => {
    if (!isSectionAllowed(sectionKey) || !sectionConfig) {
      return;
    }

    filteredConfig[sectionKey] = { ...sectionConfig };
    Object.keys(sectionConfig).forEach((moduleKey) => {
      if (moduleKey !== 'enabled' && !isModuleAllowed(sectionKey, moduleKey)) {
        delete filteredConfig[sectionKey][moduleKey];
      }
    });
  });

  return filteredConfig;
};
