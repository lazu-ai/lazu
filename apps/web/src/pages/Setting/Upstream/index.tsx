import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import ProbeStatus from './ProbeStatus';
import { upstreamDefaults, upstreamSchema } from '../schemas';
import { SettingsGroupPage, type SettingSection } from '../settingsRuntime';
import type { TFunction } from 'i18next';

/* ── The shapes these settings are stored in ───────────────────────── */
/* Each of these was a textarea of raw JSON. The encode/decode pairs keep the
   stored shape byte-identical while the reader edits named columns. */

const parseObject = (raw: string): Record<string, unknown> => {
  try {
    const parsed: unknown = JSON.parse(raw || '{}');
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
};

/* Go marshals map keys in sorted order. Encoding in the same order means an
   edit that is undone lands on the exact stored string, instead of counting as
   an unsaved change because the keys came out in a different sequence. */
const sortKeys = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.keys(value as Record<string, unknown>)
        .sort()
        .map((key) => [key, sortKeys((value as Record<string, unknown>)[key])]),
    );
  }
  return value;
};

const stringify = (value: unknown) => JSON.stringify(sortKeys(value));

/** map[string]string — a category or model name to one value. */
const decodePairs = (keyName: string, valueName: string) => (raw: string) =>
  Object.entries(parseObject(raw)).map(([key, value]) => ({
    [keyName]: key,
    [valueName]: String(value ?? ''),
  }));

const encodePairs =
  (keyName: string, valueName: string) =>
  (rows: Array<Record<string, string>>) =>
    stringify(
      rows.reduce<Record<string, string>>((acc, row) => {
        const key = (row[keyName] || '').trim();
        if (!key) return acc;
        acc[key] = row[valueName] ?? '';
        return acc;
      }, {}),
    );

/** map[model]map[header][]string, flattened to one row per header. */
const decodeClaudeHeaders = (raw: string) =>
  Object.entries(parseObject(raw)).flatMap(([model, headers]) => {
    if (!headers || typeof headers !== 'object') return [];
    return Object.entries(headers as Record<string, unknown>).map(
      ([header, values]) => ({
        model,
        header,
        value: Array.isArray(values)
          ? values.map((entry) => String(entry)).join(', ')
          : String(values ?? ''),
      }),
    );
  });

const encodeClaudeHeaders = (rows: Array<Record<string, string>>) =>
  stringify(
    rows.reduce<Record<string, Record<string, string[]>>>((acc, row) => {
      const model = (row.model || '').trim();
      const header = (row.header || '').trim();
      if (!model || !header) return acc;
      acc[model] = acc[model] || {};
      acc[model][header] = (row.value || '')
        .split(',')
        .map((entry) => entry.trim())
        .filter(Boolean);
      return acc;
    }, {}),
  );

/* Gemini publishes a fixed list of harm categories; "default" covers any the
   gateway has not been told about. Thresholds are the API's own enum. */
const GEMINI_SAFETY_CATEGORIES = [
  'default',
  'HARM_CATEGORY_HARASSMENT',
  'HARM_CATEGORY_HATE_SPEECH',
  'HARM_CATEGORY_SEXUALLY_EXPLICIT',
  'HARM_CATEGORY_DANGEROUS_CONTENT',
  'HARM_CATEGORY_CIVIC_INTEGRITY',
];

const GEMINI_THRESHOLDS = [
  'OFF',
  'BLOCK_NONE',
  'BLOCK_ONLY_HIGH',
  'BLOCK_MEDIUM_AND_ABOVE',
  'BLOCK_LOW_AND_ABOVE',
];

/** Every category always has a row, so a missing one cannot look deliberate. */
const decodeGeminiSafety = (raw: string) => {
  const stored = parseObject(raw);
  const known = GEMINI_SAFETY_CATEGORIES.map((category) => ({
    category,
    threshold: String(stored[category] ?? ''),
  }));
  const extra = Object.keys(stored)
    .filter((key) => !GEMINI_SAFETY_CATEGORIES.includes(key))
    .map((category) => ({
      category,
      threshold: String(stored[category] ?? ''),
    }));
  return [...known, ...extra];
};

const encodeGeminiSafety = (rows: Array<Record<string, string>>) =>
  stringify(
    rows.reduce<Record<string, string>>((acc, row) => {
      const category = (row.category || '').trim();
      if (!category || !row.threshold) return acc;
      acc[category] = row.threshold;
      return acc;
    }, {}),
  );

const createSections = (
  t: TFunction,
  probeStatus: SettingSection['status'],
): SettingSection[] => [
  {
    id: 'decisions',
    title: t('page.console.settingV2.upstream.decisions.title', {
      defaultValue: 'Decision models',
    }),
    description: t('page.console.settingV2.upstream.decisions.description', {
      defaultValue:
        'Enable the decision API and optional question generation separately.',
    }),
    fields: [
      {
        key: 'DecisionsEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.upstream.decisions.enabled', {
          defaultValue: 'Enable /v1/decisions',
        }),
      },
      {
        key: 'DecisionQuestionGenerationEnabled',
        kind: 'boolean',
        label: t('page.console.settingV2.upstream.decisions.generation', {
          defaultValue: 'Allow automatic question generation',
        }),
      },
      {
        key: 'DecisionQuestionGenerationDefaultModel',
        kind: 'text',
        label: t('page.console.settingV2.upstream.decisions.model', {
          defaultValue: 'Default question generation model',
        }),
      },
    ],
  },
  {
    id: 'retry',
    title: t('page.console.settingV2.upstream.retry.title', {
      defaultValue: '重试与探活',
    }),
    description: t('page.console.settingV2.upstream.retry.description'),
    status: probeStatus,
    fields: [
      {
        key: 'monitor_setting.auto_test_channel',
        kind: 'group',
        label: t('page.console.settingV2.upstream.field.autoTest'),
        description: t('page.console.settingV2.upstream.field.autoTestHelp'),
        parts: [
          {
            key: 'monitor_setting.auto_test_channel_enabled',
            kind: 'boolean',
          },
          {
            key: 'monitor_setting.auto_test_channel_minutes',
            kind: 'number',
            unit: t('page.console.settingV2.upstream.unit.minutes', {
              defaultValue: '分钟',
            }),
          },
        ],
      },
      {
        key: 'general_setting.ping_interval',
        kind: 'group',
        label: t('page.console.settingV2.upstream.field.pingInterval'),
        description: t(
          'page.console.settingV2.upstream.field.pingIntervalHelp',
        ),
        parts: [
          { key: 'general_setting.ping_interval_enabled', kind: 'boolean' },
          {
            key: 'general_setting.ping_interval_seconds',
            kind: 'number',
            unit: t('page.console.settingV2.upstream.unit.seconds', {
              defaultValue: '秒',
            }),
          },
        ],
      },
      {
        key: 'global.pass_through_request_enabled',
        kind: 'boolean',
        danger: true,
        label: t('page.console.settingV2.upstream.field.passThrough'),
        description: t('page.console.settingV2.upstream.field.passThroughHelp'),
      },
    ],
  },
  /* Rate limiting used to be defined twice — once here under a group called
     "mapping" that held nothing but this one JSON field, and once on the ops
     tab under a different label. Two forms held their own copy, so saving one
     left the other showing a stale value. This is now the only definition. */
  {
    id: 'rate-limit',
    title: t('page.console.settingV2.upstream.rateLimit.title'),
    description: t('page.console.settingV2.upstream.rateLimit.description'),
    fields: [
      {
        key: 'ModelRequestRateLimitEnabled',
        kind: 'boolean',
        danger: true,
        label: t('page.console.settingV2.ops.field.modelRateLimit'),
        description: t('page.console.settingV2.ops.field.modelRateLimitHelp'),
      },
      {
        key: 'ModelRequestRateLimitWindow',
        kind: 'group',
        indent: true,
        label: t('page.console.settingV2.upstream.field.window'),
        description: t('page.console.settingV2.upstream.field.windowHelp'),
        parts: [
          {
            key: 'ModelRequestRateLimitDurationMinutes',
            kind: 'number',
            unit: t('page.console.settingV2.upstream.unit.minutes', {
              defaultValue: '分钟',
            }),
          },
          {
            key: 'ModelRequestRateLimitCount',
            kind: 'number',
            lead: t('page.console.settingV2.upstream.lead.atMost', {
              defaultValue: '内最多',
            }),
            unit: t('page.console.settingV2.upstream.unit.times', {
              defaultValue: '次',
            }),
          },
          {
            key: 'ModelRequestRateLimitSuccessCount',
            kind: 'number',
            lead: t('page.console.settingV2.upstream.lead.ofWhichSuccess', {
              defaultValue: '其中成功',
            }),
            unit: t('page.console.settingV2.upstream.unit.times', {
              defaultValue: '次',
            }),
          },
        ],
      },
    ],
  },
  {
    id: 'gemini',
    title: 'Gemini',
    description: t('page.console.settingV2.upstream.gemini.description'),
    fields: [
      {
        key: 'gemini.thinking_adapter',
        kind: 'group',
        label: 'Thinking Adapter',
        description: t('page.console.settingV2.upstream.field.geminiThinking'),
        parts: [
          { key: 'gemini.thinking_adapter_enabled', kind: 'boolean' },
          /* Stored as a fraction of max tokens, not a percentage: the
             relay multiplies MaxTokens by this value directly. Showing it
             with a % suffix invited someone to type 80 and multiply every
             budget by eighty. */
          {
            key: 'gemini.thinking_adapter_budget_tokens_percentage',
            kind: 'number',
            unit: t('page.console.settingV2.upstream.unit.fraction', {
              defaultValue: '× 上限',
            }),
            width: 'sm',
          },
        ],
      },
      {
        key: 'gemini.safety_settings',
        kind: 'rows',
        label: t('page.console.settingV2.upstream.field.geminiSafetyLabel', {
          defaultValue: '内容安全拦截',
        }),
        description: t('page.console.settingV2.upstream.field.geminiSafety'),
        columns: [
          {
            key: 'category',
            kind: 'text',
            label: t('page.console.settingV2.upstream.rows.category', {
              defaultValue: '类别',
            }),
            readOnlyOnFixedRows: true,
            width: 'lg',
          },
          {
            key: 'threshold',
            kind: 'select',
            label: t('page.console.settingV2.upstream.rows.threshold', {
              defaultValue: '拦截强度',
            }),
            options: GEMINI_THRESHOLDS.map((value) => ({
              label: value,
              value,
            })),
            width: 'md',
          },
        ],
        decode: decodeGeminiSafety,
        encode: encodeGeminiSafety,
        fixedRows: GEMINI_SAFETY_CATEGORIES,
      },
      {
        key: 'gemini.version_settings',
        kind: 'rows',
        label: t('page.console.settingV2.upstream.field.geminiVersionLabel', {
          defaultValue: 'API 版本',
        }),
        description: t('page.console.settingV2.upstream.field.geminiVersion'),
        addLabel: t('page.console.settingV2.upstream.rows.addModel', {
          defaultValue: '添加模型',
        }),
        columns: [
          {
            key: 'model',
            kind: 'text',
            label: t('page.console.settingV2.upstream.rows.model', {
              defaultValue: '模型',
            }),
            placeholder: 'gemini-2.5-pro',
            readOnlyOnFixedRows: true,
            width: 'lg',
          },
          {
            key: 'version',
            kind: 'text',
            label: t('page.console.settingV2.upstream.rows.version', {
              defaultValue: 'API 版本',
            }),
            placeholder: 'v1beta',
            width: 'md',
          },
        ],
        decode: decodePairs('model', 'version'),
        encode: encodePairs('model', 'version'),
        requiredColumns: ['version'],
        fixedRows: ['default'],
      },
    ],
  },
  {
    id: 'claude',
    title: 'Claude',
    description: t('page.console.settingV2.upstream.claude.description'),
    fields: [
      {
        key: 'claude.thinking_adapter',
        kind: 'group',
        label: 'Thinking Adapter',
        description: t('page.console.settingV2.upstream.field.claudeThinking'),
        parts: [
          { key: 'claude.thinking_adapter_enabled', kind: 'boolean' },
          {
            key: 'claude.thinking_adapter_budget_tokens_percentage',
            kind: 'number',
            unit: t('page.console.settingV2.upstream.unit.fraction', {
              defaultValue: '× 上限',
            }),
            width: 'sm',
          },
        ],
      },
      {
        key: 'claude.model_headers_settings',
        kind: 'rows',
        label: t('page.console.settingV2.upstream.field.claudeHeadersLabel', {
          defaultValue: '额外请求头',
        }),
        description: t('page.console.settingV2.upstream.field.claudeHeaders'),
        addLabel: t('page.console.settingV2.upstream.rows.addHeader', {
          defaultValue: '添加请求头',
        }),
        columns: [
          {
            key: 'model',
            kind: 'text',
            label: t('page.console.settingV2.upstream.rows.model', {
              defaultValue: '模型',
            }),
            placeholder: 'claude-opus-5',
            width: 'md',
          },
          {
            key: 'header',
            kind: 'text',
            label: t('page.console.settingV2.upstream.rows.header', {
              defaultValue: '请求头',
            }),
            placeholder: 'anthropic-beta',
            width: 'md',
          },
          {
            key: 'value',
            kind: 'text',
            label: t('page.console.settingV2.upstream.rows.headerValue', {
              defaultValue: '值（逗号分隔）',
            }),
            width: 'lg',
          },
        ],
        decode: decodeClaudeHeaders,
        encode: encodeClaudeHeaders,
        identityColumns: ['model', 'header'],
        requiredColumns: ['header'],
      },
    ],
  },
];

export default function UpstreamSetting() {
  const { t } = useTranslation();
  const sections = useMemo(() => createSections(t, <ProbeStatus />), [t]);

  return (
    <SettingsGroupPage
      defaults={upstreamDefaults}
      schema={upstreamSchema}
      sections={sections}
      title={t('page.console.settingV2.upstream.title')}
    />
  );
}
