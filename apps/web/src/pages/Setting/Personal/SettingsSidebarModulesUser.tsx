import { Settings } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useStatus } from '../../../context/Status';
import { API, showSuccess, showError } from '../../../helpers';
import {
  buildSidebarSectionConfigs,
  filterSidebarModulesConfig,
  getSidebarPresetConfig,
  normalizeSidebarModulesConfig,
  useSidebar,
} from '../../../hooks/common/useSidebar';
import { useUserPermissions } from '../../../hooks/common/useUserPermissions';
import { unwrap } from '../../../lib/api-error';
import type { SidebarModulesConfig } from '../../../hooks/common/useSidebar';
import type { ComponentType, ReactNode } from 'react';
import {
  Avatar,
  Button,
  Card,
  Col,
  Row,
  Switch,
  Typography,
} from '@/components/ui/console-primitives';

const { Text } = Typography;
const LegacyAvatar = Avatar as unknown as ComponentType<LegacyProps>;
const LegacyButton = Button as unknown as ComponentType<LegacyProps>;
const LegacyCard = Card as unknown as ComponentType<LegacyProps>;
const LegacyCol = Col as unknown as ComponentType<LegacyProps>;
const LegacyRow = Row as unknown as ComponentType<LegacyProps>;
const LegacySwitch = Switch as unknown as ComponentType<LegacyProps>;
const LegacyText = Text as unknown as ComponentType<LegacyProps>;

type LegacyProps = {
  children?: ReactNode;
  [key: string]: unknown;
};

type SettingsSidebarModulesUserProps = {
  hideCard?: boolean;
};

type UserSelfResponse = {
  data?: {
    sidebar_modules?: SidebarModulesConfig | string;
  };
  message?: string;
  success?: boolean;
};

export default function SettingsSidebarModulesUser({
  hideCard = false,
}: SettingsSidebarModulesUserProps) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const statusState = useStatus();

  const {
    loading: permissionsLoading,
    hasSidebarSettingsPermission,
    isSidebarSectionAllowed,
    isSidebarModuleAllowed,
  } = useUserPermissions();

  const { refreshUserConfig } = useSidebar();
  const [sidebarModulesUser, setSidebarModulesUser] =
    useState<SidebarModulesConfig>(() => normalizeSidebarModulesConfig());

  const sidebarPresetConfig = useMemo(
    () => getSidebarPresetConfig(statusState?.status?.SidebarModulePreset),
    [statusState?.status?.SidebarModulePreset],
  );

  const adminConfig = useMemo(() => {
    if (statusState?.status?.SidebarModulesAdmin) {
      try {
        return normalizeSidebarModulesConfig(
          JSON.parse(statusState.status.SidebarModulesAdmin),
          sidebarPresetConfig.admin,
        );
      } catch {
        return sidebarPresetConfig.admin;
      }
    }

    return sidebarPresetConfig.admin;
  }, [sidebarPresetConfig, statusState?.status?.SidebarModulesAdmin]);

  const defaultConfig = useMemo(
    () =>
      filterSidebarModulesConfig(
        adminConfig,
        isSidebarSectionAllowed,
        isSidebarModuleAllowed,
      ),
    [adminConfig, isSidebarSectionAllowed, isSidebarModuleAllowed],
  );

  const sectionConfigs = useMemo(
    () =>
      buildSidebarSectionConfigs(t)
        .filter((section) => isSidebarSectionAllowed(section.key))
        .map((section) => ({
          ...section,
          modules: section.modules.filter(
            (module) =>
              isSidebarModuleAllowed(section.key, module.key) &&
              adminConfig[section.key]?.[module.key],
          ),
        }))
        .filter(
          (section) =>
            section.modules.length > 0 && adminConfig[section.key]?.enabled,
        ),
    [t, adminConfig, isSidebarSectionAllowed, isSidebarModuleAllowed],
  );

  function handleSectionChange(sectionKey: string) {
    return (checked: boolean) => {
      const newModules: SidebarModulesConfig = {
        ...sidebarModulesUser,
        [sectionKey]: {
          ...sidebarModulesUser[sectionKey],
          enabled: checked,
        },
      };
      setSidebarModulesUser(newModules);
    };
  }

  function handleModuleChange(sectionKey: string, moduleKey: string) {
    return (checked: boolean) => {
      const newModules: SidebarModulesConfig = {
        ...sidebarModulesUser,
        [sectionKey]: {
          ...sidebarModulesUser[sectionKey],
          [moduleKey]: checked,
        },
      };
      setSidebarModulesUser(newModules);
    };
  }

  function resetSidebarModules() {
    setSidebarModulesUser(defaultConfig);
    showSuccess(t('legacy.i18n.reset_to_default_configuration.5b31c4e459'));
  }

  async function onSubmit() {
    setLoading(true);
    try {
      unwrap(
        await API.put('/api/user/self', {
          sidebar_modules: JSON.stringify(sidebarModulesUser),
        }),
      );
      showSuccess(t('legacy.i18n.saved_successfully.7e68eb622d'));
      await refreshUserConfig();
    } catch (error) {
      showError(
        error,
        t('legacy.i18n.save_failed_please_try_again.18b4e26f24'),
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    const loadConfigs = async () => {
      try {
        const userRes = await API.get<UserSelfResponse>('/api/user/self');
        if (userRes.data.success && userRes.data.data?.sidebar_modules) {
          const rawSidebarModules = userRes.data.data.sidebar_modules;
          const userConf =
            typeof rawSidebarModules === 'string'
              ? JSON.parse(rawSidebarModules)
              : rawSidebarModules;
          const normalizedUserConfig = normalizeSidebarModulesConfig(
            userConf,
            defaultConfig,
          );
          const filteredUserConfig = filterSidebarModulesConfig(
            normalizedUserConfig,
            isSidebarSectionAllowed,
            isSidebarModuleAllowed,
          );
          setSidebarModulesUser(filteredUserConfig);
        } else {
          setSidebarModulesUser(defaultConfig);
        }
      } catch (error) {
        console.error('Failed to load user sidebar configuration:', error);
        setSidebarModulesUser(defaultConfig);
      }
    };

    if (!permissionsLoading && hasSidebarSettingsPermission()) {
      loadConfigs();
    }
  }, [
    defaultConfig,
    permissionsLoading,
    hasSidebarSettingsPermission,
    isSidebarSectionAllowed,
    isSidebarModuleAllowed,
  ]);

  if (!permissionsLoading && !hasSidebarSettingsPermission()) {
    return null;
  }

  if (permissionsLoading) {
    return null;
  }

  const content = (
    <>
      <div className='flex items-center mb-4'>
        <LegacyAvatar size='small' color='purple' className='mr-3 shadow-md'>
          <Settings size={16} />
        </LegacyAvatar>
        <div>
          <LegacyText className='text-lg font-medium'>
            {t('legacy.i18n.personal_settings_in_left_sidebar.87be97a967')}
          </LegacyText>
          <div className='text-xs text-gray-600'>
            {t(
              'legacy.i18n.personalize_the_display_content_of_the_left_sidebar.0c65a3d39f',
            )}
          </div>
        </div>
      </div>

      <div className='mb-4'>
        <LegacyText type='secondary' className='text-sm text-gray-600'>
          {t(
            'legacy.i18n.you_can_customize_the_sidebar_functions_to_display.36a889067c',
          )}
        </LegacyText>
      </div>

      {sectionConfigs.map((section) => (
        <div key={section.key} className='mb-6'>
          <div className='flex justify-between items-center mb-4 p-4 bg-gray-50 rounded-xl border border-gray-200'>
            <div>
              <div className='font-semibold text-base text-gray-900 mb-1'>
                {section.title}
              </div>
              <LegacyText className='text-xs text-gray-600'>
                {section.description}
              </LegacyText>
            </div>
            <LegacySwitch
              checked={Boolean(sidebarModulesUser[section.key]?.enabled)}
              onChange={handleSectionChange(section.key)}
              size='default'
            />
          </div>

          <LegacyRow gutter={[12, 12]}>
            {section.modules.map((module) => (
              <LegacyCol key={module.key} xs={24} sm={12} md={8} lg={6} xl={6}>
                <LegacyCard
                  className={`!rounded-xl border border-gray-200 hover:border-blue-300 transition-all duration-200 ${
                    sidebarModulesUser[section.key]?.enabled ? '' : 'opacity-50'
                  }`}
                  bodyStyle={{ padding: '16px' }}
                  hoverable
                >
                  <div className='flex justify-between items-center h-full'>
                    <div className='flex-1 text-left'>
                      <div className='font-semibold text-sm text-gray-900 mb-1'>
                        {module.title}
                      </div>
                      <LegacyText className='text-xs text-gray-600 leading-relaxed block'>
                        {module.description}
                      </LegacyText>
                    </div>
                    <div className='ml-4'>
                      <LegacySwitch
                        checked={Boolean(
                          sidebarModulesUser[section.key]?.[module.key],
                        )}
                        onChange={handleModuleChange(section.key, module.key)}
                        size='default'
                        disabled={!sidebarModulesUser[section.key]?.enabled}
                      />
                    </div>
                  </div>
                </LegacyCard>
              </LegacyCol>
            ))}
          </LegacyRow>
        </div>
      ))}

      <div className='flex justify-end gap-3 mt-6 pt-4 border-t border-gray-200'>
        <LegacyButton
          type='tertiary'
          onClick={resetSidebarModules}
          className='!rounded-lg'
        >
          {t('legacy.i18n.reset_to_default.a2381f8d14')}
        </LegacyButton>
        <LegacyButton
          type='primary'
          onClick={onSubmit}
          loading={loading}
          className='!rounded-lg'
        >
          {t('legacy.i18n.save_settings.bb79ec7c15')}
        </LegacyButton>
      </div>
    </>
  );

  if (hideCard) {
    return content;
  }

  return (
    <LegacyCard className='!rounded-2xl shadow-sm border-0'>
      {content}
    </LegacyCard>
  );
}
