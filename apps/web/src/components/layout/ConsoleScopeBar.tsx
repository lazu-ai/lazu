import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  getConsoleScopeLabel,
  getConsoleScopeMeta,
  useConsoleScope,
} from '../../context/ConsoleScope';
import { useProject } from '../../features/projects/ProjectContext';
import { Select, SelectItem } from '../ui/select';

const ConsoleScopeBar = () => {
  const { t } = useTranslation();
  const { activeScope, availableScopes, setActiveScope } = useConsoleScope();
  const project = useProject();
  const location = useLocation();
  const navigate = useNavigate();
  const customerScopes = availableScopes.filter(
    (scope) =>
      scope.plane === 'customer' &&
      (!project.enabled || !scope.project || scope.id === activeScope.id),
  );
  const platformScopes = availableScopes.filter(
    (scope) => scope.plane === 'platform',
  );
  const scopeCopy = {
    keyMeta: t('console.scope.keyMeta', { defaultValue: 'API Key' }),
    platformAdmin: t('console.scope.platformAdminRole', {
      defaultValue: 'Administrator',
    }),
    platformLabel: t('console.scope.platformLabel', {
      defaultValue: 'Admin console',
    }),
    workspaceFallback: t('console.scope.workspaceFallback', {
      defaultValue: 'API Workspace',
    }),
    workspaceManager: t('console.scope.workspaceManager', {
      defaultValue: 'Workspace administrator',
    }),
    workspaceMember: t('console.scope.workspaceMember', {
      defaultValue: 'Workspace member',
    }),
  };
  const scopeLabel = getConsoleScopeLabel(activeScope, scopeCopy);
  const scopeKindLabel =
    activeScope.kind === 'key_pass'
      ? t('console.scope.currentKey', { defaultValue: 'API key' })
      : activeScope.plane === 'platform'
        ? t('console.scope.currentRange', { defaultValue: 'Current scope' })
        : t('console.scope.currentSpace', {
            defaultValue: 'Current workspace',
          });

  const selectScope = (scopeId: string) => {
    const nextScope = availableScopes.find((scope) => scope.id === scopeId);
    if (!nextScope || project.dirtyEntries.length) return;

    if (nextScope.id === activeScope.id) return;

    const destination =
      nextScope.kind === 'key_pass' ? '/console/usage' : '/console';
    setActiveScope(scopeId);
    if (location.pathname !== destination) navigate(destination);
  };

  const gatewayOverview =
    location.pathname === '/console' && activeScope.plane === 'customer';

  const renderOption = (scope: (typeof availableScopes)[number]) => (
    <SelectItem
      key={scope.id}
      className='console-scope-bar__option'
      value={scope.id}
    >
      <span className='console-scope-bar__option-copy'>
        <strong>{getConsoleScopeLabel(scope, scopeCopy)}</strong>
        <small>
          {scope.kind === 'key_pass'
            ? t('console.scope.currentKey', { defaultValue: 'API key' })
            : scope.plane === 'platform'
              ? scopeCopy.platformLabel
              : t('console.scope.currentSpace', {
                  defaultValue: 'Current workspace',
                })}{' '}
          · {getConsoleScopeMeta(scope, scopeCopy)}
        </small>
      </span>
    </SelectItem>
  );

  return (
    <div
      className={`console-scope-bar ${gatewayOverview ? 'console-scope-bar--gateway' : ''}`}
    >
      <div className='console-scope-bar__copy'>
        {gatewayOverview ? (
          <>
            <span className='console-scope-bar__label'>
              {t('console.scope.workspacePath', {
                defaultValue: 'Workspace /',
              })}
            </span>
            <span className='console-scope-bar__current'>
              {t('console.scope.gatewayOverview', {
                defaultValue: 'Gateway overview',
              })}
            </span>
          </>
        ) : (
          <>
            <span className='console-scope-bar__label'>{scopeKindLabel}</span>
            <strong>{scopeLabel}</strong>
            <span className='console-scope-bar__meta'>
              {getConsoleScopeMeta(activeScope, scopeCopy)}
            </span>
          </>
        )}
      </div>
      {gatewayOverview ? (
        <div className='console-scope-bar__gateway-status'>
          <span>
            {t('console.scope.production', { defaultValue: 'Production' })}
          </span>
        </div>
      ) : null}
      <Select
        disabled={project.dirtyEntries.length > 0}
        aria-label={t('console.scope.switch', {
          defaultValue: 'Switch active scope',
        })}
        align='end'
        className='console-scope-bar__trigger'
        contentClassName='console-scope-bar__menu'
        onValueChange={selectScope}
        triggerContent={t('console.scope.switchShort', {
          defaultValue: 'Switch',
        })}
        value={activeScope.id}
      >
        {[...customerScopes, ...platformScopes].map(renderOption)}
      </Select>
    </div>
  );
};

export default ConsoleScopeBar;
