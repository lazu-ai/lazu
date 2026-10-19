import { describe, expect, it } from 'vitest';
import {
  DEFAULT_SIDEBAR_ADMIN_CONFIG,
  getSidebarPresetConfig,
  normalizeSidebarModulesConfig,
} from './sidebarConfig';

describe('normalizeSidebarModulesConfig', () => {
  it('migrates legacy console and personal sections to workspace and account', () => {
    const normalized = normalizeSidebarModulesConfig({
      admin: {
        enabled: true,
        setting: false,
      },
      console: {
        enabled: true,
        detail: false,
        log: true,
        token: true,
      },
      personal: {
        enabled: false,
        personal: false,
        topup: true,
      },
    });

    expect(normalized.console).toBeUndefined();
    expect(normalized.personal).toBeUndefined();
    expect(normalized.workspace.enabled).toBe(true);
    expect(normalized.workspace.detail).toBe(false);
    expect(normalized.workspace.usage).toBe(true);
    expect(normalized.workspace.token).toBe(true);
    expect(normalized.workspace.model_market).toBe(true);
    expect(normalized.account.enabled).toBe(false);
    expect(normalized.account.wallet).toBe(true);
    expect(normalized.account.personal).toBe(false);
    expect(normalized.account.topup).toBeUndefined();
    expect(normalized.account.billing).toBeUndefined();
    expect(normalized.admin.setting).toBe(false);
  });

  it('keeps explicit new section values when both legacy and new keys exist', () => {
    const normalized = normalizeSidebarModulesConfig({
      console: {
        enabled: false,
        log: false,
      },
      workspace: {
        enabled: true,
        usage: true,
      },
    });

    expect(normalized.workspace.enabled).toBe(true);
    expect(normalized.workspace.usage).toBe(true);
  });

  it('keeps the merged wallet visible when either legacy page was enabled', () => {
    const billingOffTopupOn = normalizeSidebarModulesConfig({
      personal: { billing: false, topup: true },
    });
    const billingOnTopupOff = normalizeSidebarModulesConfig({
      personal: { billing: true, topup: false },
    });

    expect(billingOffTopupOn.account.wallet).toBe(true);
    expect(billingOnTopupOff.account.wallet).toBe(true);
  });

  it('preserves an explicit new wallet preference over legacy flags', () => {
    const normalized = normalizeSidebarModulesConfig({
      account: { wallet: false },
      personal: { billing: true, topup: true },
    });

    expect(normalized.account.wallet).toBe(false);
  });

  it('supports custom fallback configs for user sidebar preferences', () => {
    const normalized = normalizeSidebarModulesConfig(
      {
        personal: {
          enabled: true,
          topup: false,
        },
      },
      {
        account: {
          enabled: true,
          personal: true,
          wallet: true,
        },
      },
    );

    expect(normalized.account.enabled).toBe(true);
    expect(normalized.account.wallet).toBe(false);
    expect(normalized.account.personal).toBe(true);
  });

  it('leaves default admin config unchanged when input is empty', () => {
    expect(normalizeSidebarModulesConfig()).toEqual(
      DEFAULT_SIDEBAR_ADMIN_CONFIG,
    );
  });

  it('defaults to the saas preset with secondary modules hidden', () => {
    const normalized = normalizeSidebarModulesConfig();

    expect(normalized.workspace.key_inspector).toBe(false);
    expect(normalized.account.wallet).toBe(true);
    expect(normalized.account.personal).toBe(true);
    expect(normalized.admin.announcements).toBe(true);
  });

  it('supports the self-hosted preset for cloud billing modules', () => {
    const selfHosted = getSidebarPresetConfig('self-hosted');
    const normalized = normalizeSidebarModulesConfig(
      undefined,
      selfHosted.admin,
    );

    // Self-hosted keeps the wallet as a ledger.
    expect(normalized.account.wallet).toBe(true);
    expect(normalized.workspace.token).toBe(true);
    expect(normalized.admin.model_ops).toBe(true);
  });

  it('lets explicit module flags restore secondary modules', () => {
    const normalized = normalizeSidebarModulesConfig({
      workspace: {
        key_inspector: true,
      },
    });

    expect(normalized.workspace.key_inspector).toBe(true);
  });
});

describe('model ops sidebar migration', () => {
  it('keeps 模型运营 visible when any of the three merged pages was enabled', () => {
    // 模型管理 / 定价运营 / 渠道 collapsed into one module key; a stored config
    // predates it, so the merged page inherits their visibility.
    expect(
      normalizeSidebarModulesConfig({
        admin: { enabled: true, models: false, pricing: true, channel: false },
      }).admin.model_ops,
    ).toBe(true);

    expect(
      normalizeSidebarModulesConfig({
        admin: { enabled: true, models: false, pricing: false, channel: false },
      }).admin.model_ops,
    ).toBe(false);
  });

  it('lets an explicit model_ops flag win over the legacy derivation', () => {
    expect(
      normalizeSidebarModulesConfig({
        admin: { enabled: true, model_ops: false, channel: true },
      }).admin.model_ops,
    ).toBe(false);
  });
});
