import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  DEFAULT_SIDEBAR_ADMIN_CONFIG,
  buildEnabledSidebarConfig,
  getSidebarPresetConfig,
  normalizeSidebarModulesConfig,
  type SidebarModulesConfig,
} from './sidebarConfig';
import { readSelf, refreshSelf } from './useSelfQuery';
import { useStatus } from '../../context/Status';

export {
  DEFAULT_SIDEBAR_ADMIN_CONFIG,
  SIDEBAR_SECTION_DEFINITIONS,
  buildEnabledSidebarConfig,
  buildSidebarSectionConfigs,
  filterSidebarModulesConfig,
  getSidebarPresetConfig,
  normalizeSidebarModulesConfig,
} from './sidebarConfig';
export type { SidebarModulesConfig } from './sidebarConfig';

type LoadUserConfigOptions = {
  /** Ask the server again instead of reading the shared copy. */
  fresh?: boolean;
  withLoading?: boolean;
};

type SidebarRefreshEventDetail = {
  skipLoader?: boolean;
  sourceId?: string | null;
};

// 创建一个全局事件系统来同步所有useSidebar实例
const sidebarEventTarget = new EventTarget();
const SIDEBAR_REFRESH_EVENT = 'sidebar-refresh';

export const useSidebar = () => {
  const statusState = useStatus();
  const queryClient = useQueryClient();
  const [userConfig, setUserConfig] = useState(() =>
    buildEnabledSidebarConfig(DEFAULT_SIDEBAR_ADMIN_CONFIG),
  );
  const [loading, setLoading] = useState(false);
  const [ready, setReady] = useState(false);
  const instanceIdRef = useRef<string | null>(null);
  const hasLoadedOnceRef = useRef(false);

  if (!instanceIdRef.current) {
    const randomPart = Math.random().toString(16).slice(2);
    instanceIdRef.current = `sidebar-${Date.now()}-${randomPart}`;
  }

  const sidebarPresetConfig = useMemo(
    () => getSidebarPresetConfig(statusState?.status?.SidebarModulePreset),
    [statusState?.status?.SidebarModulePreset],
  );

  // 获取管理员配置
  const adminConfig = useMemo(() => {
    if (statusState?.status?.SidebarModulesAdmin) {
      try {
        const config = JSON.parse(
          String(statusState.status.SidebarModulesAdmin),
        );
        return normalizeSidebarModulesConfig(config, sidebarPresetConfig.admin);
      } catch (error) {
        return sidebarPresetConfig.admin;
      }
    }
    return sidebarPresetConfig.admin;
  }, [sidebarPresetConfig, statusState?.status?.SidebarModulesAdmin]);

  // 加载用户配置的通用方法
  const loadUserConfig = async ({
    fresh,
    withLoading,
  }: LoadUserConfigOptions = {}) => {
    const shouldShowLoader = Boolean(withLoading);

    try {
      setReady(false);
      if (shouldShowLoader) {
        setLoading(true);
      }

      // Every sidebar, guard and top bar reads the one shared copy; only a
      // refresh after saving these settings asks the server again.
      const self = fresh
        ? await refreshSelf(queryClient)
        : await readSelf(queryClient);
      const sidebarModules = self?.sidebar_modules;
      if (sidebarModules) {
        let config: unknown;
        // 检查sidebar_modules是字符串还是对象
        if (typeof sidebarModules === 'string') {
          config = JSON.parse(sidebarModules);
        } else {
          config = sidebarModules;
        }
        setUserConfig(
          normalizeSidebarModulesConfig(config, sidebarPresetConfig.user),
        );
      } else {
        setUserConfig(buildEnabledSidebarConfig(adminConfig));
      }
    } catch (error) {
      setUserConfig(buildEnabledSidebarConfig(adminConfig));
    } finally {
      if (shouldShowLoader) {
        setLoading(false);
      }
      hasLoadedOnceRef.current = true;
      setReady(true);
    }
  };

  // 刷新用户配置的方法（供外部调用）
  const refreshUserConfig = async () => {
    if (Object.keys(adminConfig).length > 0) {
      await loadUserConfig({ fresh: true, withLoading: false });
    }

    // 触发全局刷新事件，通知所有useSidebar实例更新
    sidebarEventTarget.dispatchEvent(
      new CustomEvent<SidebarRefreshEventDetail>(SIDEBAR_REFRESH_EVENT, {
        detail: { sourceId: instanceIdRef.current, skipLoader: true },
      }),
    );
  };

  // 加载用户配置
  useEffect(() => {
    // 只有当管理员配置加载完成后才加载用户配置
    if (Object.keys(adminConfig).length > 0) {
      loadUserConfig();
    }
  }, [adminConfig]);

  // 监听全局刷新事件
  useEffect(() => {
    const handleRefresh = (event: Event) => {
      const detail = (event as CustomEvent<SidebarRefreshEventDetail>)?.detail;
      if (detail?.sourceId === instanceIdRef.current) {
        return;
      }

      if (Object.keys(adminConfig).length > 0) {
        loadUserConfig({
          withLoading: detail?.skipLoader ? false : undefined,
        });
      }
    };

    sidebarEventTarget.addEventListener(SIDEBAR_REFRESH_EVENT, handleRefresh);

    return () => {
      sidebarEventTarget.removeEventListener(
        SIDEBAR_REFRESH_EVENT,
        handleRefresh,
      );
    };
  }, [adminConfig]);

  // 计算最终的显示配置
  const finalConfig = useMemo(() => {
    const result: SidebarModulesConfig = {};

    // 确保adminConfig已加载
    if (!adminConfig || Object.keys(adminConfig).length === 0) {
      return result;
    }

    // 如果userConfig未加载，等待加载完成
    if (!userConfig) {
      return result;
    }

    // 遍历所有区域
    Object.keys(adminConfig).forEach((sectionKey) => {
      const adminSection = adminConfig[sectionKey];
      const userSection = userConfig[sectionKey];

      // 如果管理员禁用了整个区域，则该区域不显示
      if (!adminSection?.enabled) {
        result[sectionKey] = { enabled: false };
        return;
      }

      // 区域级别：用户可以选择隐藏管理员允许的区域
      // 当userSection存在时检查enabled状态，否则默认为true
      const sectionEnabled = userSection ? userSection.enabled !== false : true;
      result[sectionKey] = { enabled: sectionEnabled };

      // 功能级别：只有管理员和用户都允许的功能才显示
      Object.keys(adminSection).forEach((moduleKey) => {
        if (moduleKey === 'enabled') return;

        const adminAllowed = adminSection[moduleKey];
        // 当userSection存在时检查模块状态，否则默认为true
        const userAllowed = userSection
          ? userSection[moduleKey] !== false
          : true;

        result[sectionKey][moduleKey] =
          adminAllowed && userAllowed && sectionEnabled;
      });
    });

    return result;
  }, [adminConfig, userConfig]);

  // 检查特定功能是否应该显示
  const isModuleVisible = (
    sectionKey: string,
    moduleKey: string | null = null,
  ) => {
    if (moduleKey) {
      return finalConfig[sectionKey]?.[moduleKey] === true;
    }
    return finalConfig[sectionKey]?.enabled === true;
  };

  // 检查区域是否有任何可见的功能
  const hasSectionVisibleModules = (sectionKey: string) => {
    const section = finalConfig[sectionKey];
    if (!section?.enabled) return false;

    return Object.keys(section).some(
      (key) => key !== 'enabled' && section[key] === true,
    );
  };

  // 获取区域的可见功能列表
  const getVisibleModules = (sectionKey: string) => {
    const section = finalConfig[sectionKey];
    if (!section?.enabled) return [];

    return Object.keys(section).filter(
      (key) => key !== 'enabled' && section[key] === true,
    );
  };

  return {
    loading,
    ready,
    adminConfig,
    userConfig,
    finalConfig,
    isModuleVisible,
    hasSectionVisibleModules,
    getVisibleModules,
    refreshUserConfig,
  };
};
