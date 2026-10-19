import { useSelfQuery } from './useSelfQuery';
import i18n from '../../i18n/i18n';
import { errorMessage } from '../../lib/api-error';

type SidebarModulePermissions = Record<string, boolean | undefined>;
type SidebarSectionPermissions = false | SidebarModulePermissions;

type UserPermissions = {
  sidebar_modules?: Record<string, SidebarSectionPermissions | undefined>;
  sidebar_settings?: boolean;
};

/**
 * 用户权限钩子 - 从后端获取用户权限，替代前端角色判断
 * 确保权限控制的安全性，防止前端绕过
 */
export const useUserPermissions = () => {
  const self = useSelfQuery();
  const permissions = (self.data?.permissions ??
    null) as UserPermissions | null;
  const loading = self.isPending;
  const error = self.error
    ? errorMessage(self.error, i18n.t('permissions.loadFailed'))
    : null;
  const loadPermissions = self.refetch;

  // 检查是否有边栏设置权限
  const hasSidebarSettingsPermission = () => {
    return permissions?.sidebar_settings === true;
  };

  // 检查是否允许访问特定的边栏区域
  const isSidebarSectionAllowed = (sectionKey: string): boolean => {
    if (!permissions?.sidebar_modules) return true;
    const sectionPerms = permissions.sidebar_modules[sectionKey];
    return sectionPerms !== false;
  };

  // 检查是否允许访问特定的边栏模块
  const isSidebarModuleAllowed = (
    sectionKey: string,
    moduleKey: string,
  ): boolean => {
    if (!permissions?.sidebar_modules) return true;
    const sectionPerms = permissions.sidebar_modules[sectionKey];

    // 如果整个区域被禁用
    if (sectionPerms === false) return false;

    // 如果区域存在但模块被禁用
    if (sectionPerms && sectionPerms[moduleKey] === false) return false;

    return true;
  };

  // 获取允许的边栏区域列表
  const getAllowedSidebarSections = (): string[] => {
    if (!permissions?.sidebar_modules) return [];

    return Object.keys(permissions.sidebar_modules).filter((sectionKey) =>
      isSidebarSectionAllowed(sectionKey),
    );
  };

  // 获取特定区域允许的模块列表
  const getAllowedSidebarModules = (sectionKey: string): string[] => {
    if (!permissions?.sidebar_modules) return [];
    const sectionPerms = permissions.sidebar_modules[sectionKey];

    if (sectionPerms === false) return [];
    if (!sectionPerms || typeof sectionPerms !== 'object') return [];

    return Object.keys(sectionPerms).filter(
      (moduleKey) =>
        moduleKey !== 'enabled' && sectionPerms[moduleKey] === true,
    );
  };

  return {
    permissions,
    loading,
    error,
    loadPermissions,
    hasSidebarSettingsPermission,
    isSidebarSectionAllowed,
    isSidebarModuleAllowed,
    getAllowedSidebarSections,
    getAllowedSidebarModules,
  };
};

export default useUserPermissions;
