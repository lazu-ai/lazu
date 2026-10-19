import { describe, expect, it } from 'vitest';
import {
  canAccessConsoleScope,
  getConsoleScopeLabel,
  getConsoleScopes,
  getDefaultConsoleScopeId,
} from '.';

describe('Console scope model', () => {
  it('renders a member-only customer scope for ordinary sessions', () => {
    expect(getConsoleScopes({ role: 1, workspace_name: 'Acme' })).toEqual([
      expect.objectContaining({
        kind: 'member',
        label: 'Acme',
        plane: 'customer',
      }),
    ]);
  });

  it('renders Workspace Manager and Platform planes for operators', () => {
    const scopes = getConsoleScopes({ role: 100, workspace_name: 'Acme' });
    expect(scopes.map((scope) => scope.kind)).toEqual([
      'workspace_manager',
      'platform',
    ]);
    expect(getDefaultConsoleScopeId(scopes)).toBe('workspace:default');
  });

  it('preserves the project default for a serialized admin role', () => {
    expect(getDefaultConsoleScopeId(getConsoleScopes({ role: '100' }))).toBe(
      'workspace:default',
    );
  });

  it('defaults non-operators to their workspace scope', () => {
    const scopes = getConsoleScopes({ role: 1, workspace_name: 'Acme' });
    expect(getDefaultConsoleScopeId(scopes)).toBe('workspace:default');
  });

  it('renders an explicit Key Pass when the session carries key scope', () => {
    const scopes = getConsoleScopes({ role: 1, key_name: 'production' });
    expect(scopes).toContainEqual(
      expect.objectContaining({ kind: 'key_pass', meta: 'API Key' }),
    );
  });

  it('preserves an explicit workspace name that matches the fallback label', () => {
    const [scope] = getConsoleScopes({
      role: 1,
      workspace_name: 'API 工作台',
    });

    expect(
      getConsoleScopeLabel(scope, { workspaceFallback: 'API Workspace' }),
    ).toBe('API 工作台');
  });

  it('keeps customer and Platform Operations scopes mutually exclusive', () => {
    expect(
      canAccessConsoleScope('platform', ['member', 'workspace_manager']),
    ).toBe(false);
    expect(canAccessConsoleScope('member', ['platform'])).toBe(false);
    expect(canAccessConsoleScope('key_pass', ['member', 'key_pass'])).toBe(
      true,
    );
  });
});
