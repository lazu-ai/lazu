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
      /* Kept, and first: nothing loads LogConsumeEnabled from env or yaml, so
         this switch is the only way a deployment that turned consumption logs
         off can turn them back on. Everything below reads those logs. */
      {
        key: 'LogConsumeEnabled',
        kind: 'boolean',
        danger: true,
        label: t('page.console.settingV2.ops.field.logConsume'),
        description: t('page.console.settingV2.ops.field.logConsumeHelp'),
      },
      /* Named "数据导出" for as long as it has existed, but it exports nothing:
         it drives SaveUsageDataCache, which folds consumption logs into the
         usage table the dashboard reads. */
      {
        key: 'DataExport',
        kind: 'group',
        label: t('page.console.settingV2.obs.field.rollup'),
        description: t('page.console.settingV2.obs.field.rollupHelp'),
        parts: [
          { key: 'DataExportEnabled', kind: 'boolean' },
          {
            key: 'DataExportInterval',
            kind: 'number',
            lead: t('page.console.settingV2.obs.lead.every', {
              defaultValue: '每',
            }),
            unit: t('page.console.settingV2.upstream.unit.minutes', {
              defaultValue: '分钟',
            }),
          },
        ],
      },
      /* Kept after all: getDefaultTime reads it to pick the dashboard's first
         bucket size. It was removed as write-only, which was wrong, and left a
         stored value in force with no way to change it. Month is not offered —
         the dashboard only knows hour, day and week, and quietly treated a
         stored "month" as hour. */
      {
        key: 'DataExportDefaultTime',
        kind: 'select',
        indent: true,
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
