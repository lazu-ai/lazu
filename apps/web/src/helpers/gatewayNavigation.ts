export type GatewayScopeKind =
  | 'workspace_manager'
  | 'member'
  | 'key_pass'
  | 'platform';

type GatewayNavigationCandidate = {
  allowed?: boolean;
  href: string;
  module: string;
  section: string;
};

type GatewayNavigationOptions = {
  canAdmin: boolean;
  isModuleVisible: (sectionKey: string, moduleKey: string) => boolean;
  scopeKind: GatewayScopeKind;
};

export function resolveGatewayHref({
  canAdmin,
  isModuleVisible,
  scopeKind,
}: GatewayNavigationOptions) {
  const candidates: GatewayNavigationCandidate[] =
    scopeKind === 'platform'
      ? [
          /* Platform is now the DEFAULT plane for admins rather than an opt-in
             ops mode, so the brand link is a way home, not a jump to a
             diagnostic. An admin has their own workspace — 总览 first, with the
             admin surfaces as fallbacks for deployments that hide it. */
          { section: 'workspace', module: 'detail', href: '/console' },
          {
            /* 模型管理 / 定价运营 / 渠道 merged into 模型运营; the old `channel` and
               `models` module keys no longer exist. */
            section: 'admin',
            module: 'model_ops',
            href: '/console/model-ops',
            allowed: canAdmin,
          },
          {
            section: 'admin',
            module: 'user',
            href: '/console/organizations',
            allowed: canAdmin,
          },
          {
            section: 'admin',
            module: 'search_backend',
            href: '/console/search-backend',
            allowed: canAdmin,
          },
          {
            section: 'admin',
            module: 'setting',
            href: '/console/setting',
            allowed: canAdmin,
          },
        ]
      : scopeKind === 'key_pass'
        ? [
            { section: 'workspace', module: 'usage', href: '/console/usage' },
            {
              section: 'workspace',
              module: 'model_market',
              href: '/console/models',
            },
            {
              section: 'workspace',
              module: 'key_inspector',
              href: '/console/key',
            },
            {
              section: 'account',
              module: 'personal',
              href: '/console/personal',
            },
          ]
        : [
            { section: 'workspace', module: 'detail', href: '/console' },
            { section: 'workspace', module: 'token', href: '/console/token' },
            {
              section: 'workspace',
              module: 'model_market',
              href: '/console/models',
            },
            { section: 'workspace', module: 'usage', href: '/console/usage' },
            {
              section: 'workspace',
              module: 'key_inspector',
              href: '/console/key',
            },
            {
              section: 'account',
              module: 'personal',
              href: '/console/personal',
            },
          ];

  return (
    candidates.find(
      (candidate) =>
        candidate.allowed !== false &&
        isModuleVisible(candidate.section, candidate.module),
    )?.href || '/console'
  );
}
