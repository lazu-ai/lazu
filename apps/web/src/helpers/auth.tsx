import React, { useEffect, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, Navigate, useLocation } from 'react-router-dom';
import { safeJsonParse } from './utils';
import { Button, StatusPage, recordStatusIssue } from '../components/ui';
import {
  canAccessConsoleScope,
  useConsoleScope,
  type ConsoleScopeKind,
} from '../context/ConsoleScope';
import { StatusContext } from '../context/Status';
import { useSidebar } from '../hooks/common/useSidebar';

type StoredUser = {
  role?: number | string;
  token?: string;
};

type RouteGuardProps = {
  children: ReactNode;
  allowedScopes?: ConsoleScopeKind[];
};

type ModuleRouteProps = RouteGuardProps & {
  fallback?: string;
  module: string;
  section: string;
};

const parseStoredUser = (raw: string | null): StoredUser | null => {
  const user = safeJsonParse(raw, null);
  return user && typeof user === 'object' ? (user as StoredUser) : null;
};

export function authHeader(): Record<string, string> {
  // return authorization header with jwt token
  const user = parseStoredUser(localStorage.getItem('user'));

  if (user && user.token) {
    return { Authorization: 'Bearer ' + user.token };
  }
  return {};
}

function PrivateRoute({
  allowedScopes = ['workspace_manager', 'member'],
  children,
}: RouteGuardProps) {
  const { activeScope, availableScopes } = useConsoleScope();
  const location = useLocation();
  const raw = localStorage.getItem('user');
  const user = parseStoredUser(raw);
  if (!user) {
    if (raw) {
      localStorage.removeItem('user');
    }
    return <Navigate to='/login' state={{ from: location }} />;
  }
  // Console access is gated by the site role, not by which scope the viewer
  // happens to have selected (BRAND.md § 02 permissions: site role and org
  // role are separate axes). A platform selection must not lock operators
  // out of workspace pages, and refreshed roles apply immediately.
  if (availableScopes.some((scope) => scope.plane === 'platform')) {
    const platformOnly =
      allowedScopes.length === 1 && allowedScopes[0] === 'platform';
    if (platformOnly || activeScope.plane === 'platform') {
      return children;
    }
  }
  if (!canAccessConsoleScope(activeScope.kind, allowedScopes)) {
    if (activeScope.plane === 'platform') {
      return <Navigate to='/console' replace />;
    }
    return <Navigate to='/forbidden' state={{ from: location }} replace />;
  }
  return children;
}

export function RootRoute({ children }: RouteGuardProps) {
  const location = useLocation();
  const { availableScopes } = useConsoleScope();
  const raw = localStorage.getItem('user');
  if (!raw) {
    return <Navigate to='/login' state={{ from: location }} />;
  }
  const user = parseStoredUser(raw);
  if (!user) {
    localStorage.removeItem('user');
    return <Navigate to='/login' state={{ from: location }} />;
  }
  // Use the refreshed session role, independently of the selected project.
  if (availableScopes.some((scope) => scope.plane === 'platform')) {
    return children;
  }
  return <Navigate to='/forbidden' state={{ from: location }} replace />;
}

export function ModuleRoute({
  children,
  fallback = '/console',
  module,
  section,
}: ModuleRouteProps) {
  const { t } = useTranslation();
  const { isModuleVisible, ready } = useSidebar();
  const visible = isModuleVisible(section, module);

  useEffect(() => {
    if (ready && !visible) {
      recordStatusIssue({
        kind: 'unavailable',
        message: t('page.system.unavailable.status', {
          defaultValue: '{{module}} 在当前工作区或角色下未启用。',
          module,
        }),
        source: t('page.system.unavailable.source', {
          defaultValue: '功能可用性',
        }),
      });
    }
  }, [module, ready, t, visible]);

  if (ready && !visible) {
    return (
      <StatusPage
        action={
          <Button render={<Link to={fallback} />} variant='primary'>
            {t('page.system.unavailable.action', { defaultValue: '回到总览' })}
          </Button>
        }
        description={t('page.system.unavailable.description', {
          defaultValue:
            '你所在的工作区或角色没有开启这项功能。需要的话，请管理员在设置里打开。',
        })}
        mark='Off'
        title={t('page.system.unavailable.title', {
          defaultValue: '这项功能没有开启',
        })}
      />
    );
  }

  return children;
}

export { PrivateRoute };
