import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { observabilityDefaults, observabilitySchema } from '../schemas';
import { SettingsGroupPage, type SettingSection } from '../settingsRuntime';
import type { TFunction } from 'i18next';

/* Usage rollups, channel health and the gateway's own load shedding.
 *
 * Channel auto-disable used to sit under "运维" next to Gemini's thinking
 * budget. Knowing an upstream is unhealthy and configuring one are different
 * jobs, so they no longer share a tab.
 */
const createSections = (t: TFunction): SettingSection[] => [
  {
    id: 'usage-rollup',
    title: t('page.console.settingV2.obs.rollup.title', {
      defaultValue: '用量汇总',
    }),
    description: t('page.console.settingV2.obs.rollup.description'),
    fields: [
      {
        key: 'DataExportDefaultTime',
        kind: 'select',
        label: t('page.console.settingV2.obs.field.defaultTimeRange'),
        description: t('page.console.settingV2.obs.field.defaultTimeRangeHelp'),
        options: [
          { label: t('page.console.settingV2.ops.option.hour'), value: 'hour' },
          { label: t('page.console.settingV2.ops.option.day'), value: 'day' },
          { label: t('page.console.settingV2.ops.option.week'), value: 'week' },
        ],
      },
    ],
  },
  {
    id: 'channel-health',
    title: t('page.console.settingV2.obs.health.title'),
    description: t('page.console.settingV2.obs.health.description'),
    fields: [
      {
        key: 'AutomaticDisableChannel',
        kind: 'group',
        danger: true,
        label: t('page.console.settingV2.ops.field.autoDisable'),
        description: t('page.console.settingV2.ops.field.autoDisableHelp'),
        parts: [
          { key: 'AutomaticDisableChannelEnabled', kind: 'boolean' },
          /* Seconds of probe response time, not a failure count: the probe
             runner multiplies it by 1000 and disables a channel whose probe
             takes longer. Read as "5 failures", it disabled healthy channels
             that were merely slower than five seconds. Hard errors are handled
             separately, by the keyword list below. */
          {
            key: 'ChannelDisableThreshold',
            kind: 'number',
            lead: t('page.console.settingV2.obs.lead.responseOver', {
              defaultValue: '探活响应超过',
            }),
            unit: t('page.console.settingV2.upstream.unit.seconds', {
              defaultValue: '秒',
            }),
          },
        ],
      },
      {
        key: 'AutomaticEnableChannelEnabled',
        kind: 'boolean',
        indent: true,
        label: t('page.console.settingV2.ops.field.autoEnable'),
        description: t('page.console.settingV2.ops.field.autoEnableHelp'),
      },
      {
        key: 'AutomaticDisableKeywords',
        kind: 'textarea',
        label: t('page.console.settingV2.obs.field.disableKeywords'),
        description: t('page.console.settingV2.obs.field.disableKeywordsHelp'),
      },
    ],
  },
  {
    id: 'host-guard',
    title: t('page.console.settingV2.obs.host.title', {
      defaultValue: '主机保护',
    }),
    description: t('page.console.settingV2.obs.host.description'),
    fields: [
      {
        key: 'performance_setting.monitor_enabled',
        kind: 'boolean',
        label: t('page.console.settingV2.obs.field.hostMonitor'),
        description: t('page.console.settingV2.obs.field.hostMonitorHelp'),
      },
      {
        key: 'performance_setting.thresholds',
        kind: 'group',
        indent: true,
        label: t('page.console.settingV2.obs.field.hostThresholds'),
        description: t('page.console.settingV2.obs.field.hostThresholdsHelp'),
        parts: [
          {
            key: 'performance_setting.monitor_cpu_threshold',
            kind: 'number',
            lead: 'CPU',
            unit: '%',
          },
          {
            key: 'performance_setting.monitor_memory_threshold',
            kind: 'number',
            lead: t('page.console.settingV2.obs.lead.memory', {
              defaultValue: '内存',
            }),
            unit: '%',
          },
          {
            key: 'performance_setting.monitor_disk_threshold',
            kind: 'number',
            lead: t('page.console.settingV2.obs.lead.disk', {
              defaultValue: '磁盘',
            }),
            unit: '%',
          },
        ],
      },
      {
        key: 'performance_setting.monitor_active_connections_threshold',
        kind: 'number',
        indent: true,
        width: 'xs',
        unit: t('page.console.settingV2.obs.unit.connections', {
          defaultValue: '个',
        }),
        label: t('page.console.settingV2.obs.field.hostConnections'),
        description: t('page.console.settingV2.obs.field.hostConnectionsHelp'),
      },
    ],
  },
];

export default function ObservabilitySetting() {
  const { t } = useTranslation();
  const sections = useMemo(() => createSections(t), [t]);

  return (
    <SettingsGroupPage
      defaults={observabilityDefaults}
      schema={observabilitySchema}
      sections={sections}
      title={t('page.console.settingV2.obs.title')}
    />
  );
}
