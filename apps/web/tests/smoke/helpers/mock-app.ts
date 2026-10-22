// @ts-nocheck
import { expect } from '@playwright/test';
import { extensionMockFor, extensionStatus } from './mock-extensions';

const mockedStatus = {
  setup: true,
  version: 'smoke-test',
  server_address: 'http://127.0.0.1:4173',
  system_name: 'Lazu',
  demo_site_enabled: false,
  self_use_mode_enabled: false,
  announcements: [],
  docs_link: 'https://lazu.ai/docs',
  user_agreement_enabled: false,
  privacy_policy_enabled: false,
  email_verification: true,
  enable_data_export: true,
  micro_usd_per_usd: 1000000,
  ...extensionStatus,
};

const mockedUser = {
  id: 1,
  username: 'admin',
  display_name: 'Admin',
  role: 100,
  group: 'default',
  quota: 5000000,
  used_quota: 125000,
  request_count: 12,
  token: 'smoke-token',
};

const ok = (data = {}) => ({
  data,
  message: '',
  success: true,
});

const page = (items = []) =>
  ok({
    items,
    page: 1,
    page_size: 10,
    total: items.length,
  });

const token = {
  id: 1,
  name: 'Smoke token',
  key: 'sk-smoke',
  status: 1,
  unlimited_quota: false,
  used_quota: 0,
  remain_quota: 500000,
  created_time: 1735689600,
  usage_alert_percent: 80,
  note: 'Claude Code on my Mac',
  model_scope_vendors: [],
  period_cap: { period: 'day', limit_usd: 20, spent_usd: 1.4 },
};

const unlimitedToken = {
  id: 2,
  name: 'Unlimited smoke token',
  key: 'sk-unlimited-smoke',
  status: 1,
  unlimited_quota: true,
  used_quota: 2500000,
  remain_quota: 0,
  created_time: 1735689600,
};

const channel = {
  id: 1,
  name: 'OpenAI smoke channel',
  type: 1,
  status: 1,
  group: 'direct',
  source_type: 'official',
  models: 'gpt-4o-mini',
  priority: 1,
  weight: 70,
};

const cheapChannel = {
  id: 2,
  name: 'Budget smoke channel',
  type: 1,
  status: 1,
  group: 'cheap',
  source_type: 'aggregator',
  models: 'gpt-4o-mini',
  priority: 2,
  weight: 30,
};

const model = {
  id: 1,
  model_name: 'gpt-4o-mini',
  owned_by: 'openai',
  vendor_id: 1,
  status: 1,
  input_per_mtok: 0.15,
  output_per_mtok: 0.6,
  lanes: [
    {
      channel_group: 'direct',
      input_per_mtok: 0.15,
      output_per_mtok: 0.6,
    },
    {
      channel_group: 'cheap',
      input_per_mtok: 0.09,
      output_per_mtok: 0.36,
    },
  ],
};

/* A model whose 稳定档 has no published price. 模型运营 must refuse to list it —
   this is the "$0 billing after deploy" incident class the page guards. */
const unpricedModel = {
  id: 2,
  model_name: 'gemini-image-4',
  owned_by: 'google',
  vendor_id: 1,
  status: 0,
  lanes: [],
};

const noStablePoolModel = {
  id: 3,
  model_name: 'priced-without-stable-pool',
  owned_by: 'openai',
  vendor_id: 1,
  status: 0,
  lanes: [
    {
      channel_group: 'direct',
      input_per_mtok: 0.2,
      output_per_mtok: 0.8,
    },
  ],
};

const adminModels = [model, unpricedModel, noStablePoolModel];

/* Source ledger rows for /api/model_ops/sources. `bad-source` is deliberately
   failing so the tinted-row + explicit-failure-text treatment is covered. */
const probeBuckets = (rates) =>
  rates.map((rate, index) => ({
    start_at: new Date(Date.now() - (8 - index) * 3600_000).toISOString(),
    end_at: new Date(Date.now() - (7 - index) * 3600_000).toISOString(),
    request_count: 40,
    error_count: Math.round(40 * (1 - rate)),
    success_rate: rate,
  }));

const modelOpsSources = [
  {
    id: '1',
    internal_id: 1,
    name: 'OpenAI smoke channel',
    type: 1,
    type_name: 'OpenAI',
    source_type: 'official',
    needs_classification: false,
    lane: 'direct',
    status: 1,
    base_url: 'https://api.openai.com',
    endpoint: 'api.openai.com',
    key_fingerprint: '02d9',
    weight: 70,
    priority: 1,
    auto_ban: true,
    model_count: 12,
    response_time_ms: 1900,
    test_time: 1735689600,
    cost_input_per_mtok: 0.11,
    cost_output_per_mtok: 0.44,
    cost_request_usd: 0.02,
    cost_second_usd: 0.003,
    cost_row_count: 3,
    health: {
      request_count: 400,
      error_count: 1,
      success_rate: 0.9975,
      avg_latency_ms: 1900,
      last_outcome_at: new Date().toISOString(),
      buckets: probeBuckets([1, 1, 0.99, 1, 1, 1, 0.995, 1]),
    },
  },
  {
    id: '2',
    internal_id: 2,
    name: 'Budget smoke channel',
    type: 1,
    type_name: 'OpenAI',
    source_type: 'aggregator',
    needs_classification: false,
    lane: 'cheap',
    status: 1,
    base_url: 'https://openrouter.ai',
    endpoint: 'openrouter.ai',
    key_fingerprint: '77aa',
    weight: 30,
    priority: 2,
    auto_ban: true,
    model_count: 22,
    response_time_ms: 3800,
    test_time: 1735689600,
    cost_input_per_mtok: 0.06,
    cost_output_per_mtok: 0.24,
    cost_row_count: 2,
    health: {
      request_count: 300,
      error_count: 3,
      success_rate: 0.99,
      avg_latency_ms: 3800,
      last_outcome_at: new Date().toISOString(),
      buckets: probeBuckets([1, 0.98, 0.99, 1, 0.97, 1, 0.99, 1]),
    },
  },
  {
    id: '3',
    internal_id: 3,
    name: 'google-2',
    type: 24,
    type_name: 'Gemini',
    source_type: 'unclassified',
    needs_classification: true,
    lane: 'direct',
    status: 1,
    base_url: 'https://vertex.example',
    endpoint: 'vertex.example',
    key_fingerprint: 'k8s3',
    weight: 5,
    priority: 0,
    auto_ban: true,
    model_count: 28,
    response_time_ms: 9200,
    test_time: 1735689600,
    cost_input_per_mtok: 0.62,
    cost_output_per_mtok: 2.4,
    cost_row_count: 1,
    health: {
      request_count: 120,
      error_count: 24,
      success_rate: 0.8,
      avg_latency_ms: 9200,
      last_outcome_at: new Date().toISOString(),
      buckets: probeBuckets([0.9, 0.6, 0.55, 0.8, 0.5, 0.95, 0.6, 0.55]),
    },
  },
];

const modelOpsPools = {
  'gpt-4o-mini': {
    direct: [
      {
        channel_id: '1',
        channel_internal_id: 1,
        name: 'OpenAI smoke channel',
        source_type: 'official',
        type: 1,
        lane: 'direct',
        weight: 70,
        priority: 1,
        enabled: true,
        channel_status: 1,
        response_time_ms: 1900,
        cost_input_per_mtok: 0.11,
        cost_output_per_mtok: 0.44,
        health: {
          request_count: 400,
          error_count: 1,
          success_rate: 0.9975,
          avg_latency_ms: 1900,
          last_outcome_at: null,
        },
      },
      {
        channel_id: '3',
        channel_internal_id: 3,
        name: 'google-2',
        source_type: 'cloud',
        type: 24,
        lane: 'direct',
        weight: 5,
        priority: 0,
        enabled: true,
        channel_status: 1,
        response_time_ms: 9200,
        cost_input_per_mtok: 0.62,
        cost_output_per_mtok: 2.4,
        health: {
          request_count: 120,
          error_count: 24,
          success_rate: 0.8,
          avg_latency_ms: 9200,
          last_outcome_at: null,
        },
      },
    ],
    cheap: [
      {
        channel_id: '2',
        channel_internal_id: 2,
        name: 'Budget smoke channel',
        source_type: 'aggregator',
        type: 1,
        lane: 'cheap',
        weight: 30,
        priority: 2,
        enabled: true,
        channel_status: 1,
        response_time_ms: 3800,
        cost_input_per_mtok: 0.06,
        cost_output_per_mtok: 0.24,
        health: {
          request_count: 300,
          error_count: 3,
          success_rate: 0.99,
          avg_latency_ms: 3800,
          last_outcome_at: null,
        },
      },
    ],
  },
  'gemini-image-4': {
    direct: [],
  },
  'priced-without-stable-pool': {
    direct: [],
  },
};

const modelOpsRules = {
  window_hours: 48,
  automatic_disable_enabled: true,
  automatic_enable_enabled: false,
  channel_disable_threshold_seconds: 5,
  auto_test_channel_enabled: true,
  auto_test_channel_minutes: 10,
  outcome_telemetry_available: true,
};

/* Channel health fixture. It deliberately covers all three evidence states so
   the heatmap's traffic / probe / idle rendering is exercised: a healthy
   traffic-backed lane, a degraded one, a lane carried only by probes, and a
   lane with nothing at all. */
const channelHealthBuckets = (
  tone: 'ok' | 'warn' | 'bad' | 'none',
  source: 'traffic' | 'probe' | 'none',
  count = 84,
  hoursPerSlice = 2,
) =>
  Array.from({ length: count }, (_unused, index) => ({
    index,
    start_at: new Date(
      Date.UTC(2026, 8, 1, index * hoursPerSlice),
    ).toISOString(),
    source,
    request_count: source === 'none' ? 0 : source === 'probe' ? 1 : 480,
    error_count: tone === 'bad' ? 60 : tone === 'warn' ? 12 : 1,
    success_rate:
      source === 'none'
        ? undefined
        : tone === 'bad'
          ? 0.88
          : tone === 'warn'
            ? 0.975
            : 0.999,
    tone,
  }));

export const mockedChannelHealth = {
  window: '7d',
  window_seconds: 604800,
  bucket_count: 84,
  bucket_seconds: 7200,
  is_admin: true,
  summary: {
    stable_success_rate: 0.9972,
    eco_success_rate: 0.9831,
    output_tps: 46.8,
    active_issues: 2,
    degraded_count: 1,
    outage_count: 1,
    no_data_count: 1,
    lane_count: 4,
    model_count: 3,
    request_count: 80053,
    provider_count: 3,
    provider_degraded_count: 1,
    provider_outage_count: 0,
  },
  // One section per kind of model, each drawn in its own slice width.
  kinds: [
    { kind: 'llm', bucket_count: 84, bucket_seconds: 7200, row_count: 4 },
    { kind: 'image', bucket_count: 28, bucket_seconds: 21600, row_count: 1 },
    { kind: 'search', bucket_count: 84, bucket_seconds: 7200, row_count: 1 },
  ],
  rows: [
    {
      model_name: 'claude-sonnet-5',
      kind: 'llm',
      lane: 'stable',
      status: 'operational',
      source: 'traffic',
      request_count: 48211,
      error_count: 51,
      success_rate: 0.999,
      avg_latency_ms: 1180,
      p95_latency_ms: 1900,
      ttft_p50_ms: 420,
      ttft_p95_ms: 1100,
      output_tps: 42,
      interruption_rate: 0.004,
      stall_p50_ms: 320,
      input_per_mtok: 3,
      output_per_mtok: 15,
      probe_count: 0,
      channel_count: 2,
      buckets: channelHealthBuckets('ok', 'traffic'),
      sources: [
        {
          channel_id: 1,
          channel_name: 'anthropic-primary',
          status: 'operational',
          source: 'traffic',
          weight: 100,
          request_count: 34230,
          success_rate: 0.999,
          avg_latency_ms: 1120,
          share: 0.71,
          disabled: false,
        },
        {
          channel_id: 2,
          channel_name: 'bedrock-us',
          status: 'operational',
          source: 'traffic',
          weight: 40,
          request_count: 13981,
          success_rate: 0.997,
          avg_latency_ms: 1310,
          share: 0.29,
          disabled: false,
        },
      ],
    },
    {
      model_name: 'claude-sonnet-5',
      kind: 'llm',
      lane: 'eco',
      status: 'degraded',
      source: 'traffic',
      request_count: 31842,
      error_count: 255,
      success_rate: 0.978,
      avg_latency_ms: 2600,
      p95_latency_ms: 5400,
      ttft_p50_ms: 1400,
      ttft_p95_ms: 3200,
      output_tps: 35,
      interruption_rate: 0.031,
      stall_p50_ms: 320,
      input_per_mtok: 3,
      output_per_mtok: 15,
      probe_count: 0,
      channel_count: 1,
      buckets: channelHealthBuckets('warn', 'traffic'),
      sources: [
        {
          channel_id: 3,
          channel_name: 'relay-blossom',
          status: 'degraded',
          source: 'traffic',
          weight: 70,
          request_count: 31842,
          success_rate: 0.978,
          avg_latency_ms: 2600,
          share: 1,
          disabled: false,
        },
      ],
    },
    {
      model_name: 'deepseek-v4-pro',
      kind: 'llm',
      lane: 'eco',
      status: 'operational',
      source: 'probe',
      last_probed_at: new Date(Date.now() - 6 * 60 * 1000).toISOString(),
      request_count: 0,
      error_count: 0,
      success_rate: 1,
      probe_count: 6,
      probe_error_count: 0,
      // A streamed probe measures first token, decode speed, the longest
      // pause and whether the stream finished.
      ttft_p50_ms: 640,
      ttft_p95_ms: 1300,
      output_tps: 55,
      interruption_rate: 0,
      p95_latency_ms: 1900,
      stall_p50_ms: 210,
      channel_count: 1,
      buckets: channelHealthBuckets('ok', 'probe'),
      sources: [
        {
          channel_id: 4,
          channel_name: 'relay-corsair',
          status: 'operational',
          source: 'probe',
          last_probed_at: new Date(Date.now() - 6 * 60 * 1000).toISOString(),
          weight: 30,
          request_count: 0,
          success_rate: 1,
          disabled: false,
        },
      ],
    },
    {
      model_name: 'gemini-2.5-pro',
      kind: 'llm',
      lane: 'stable',
      status: 'unknown',
      source: 'none',
      request_count: 0,
      error_count: 0,
      probe_count: 0,
      channel_count: 1,
      buckets: channelHealthBuckets('none', 'none'),
      sources: [
        {
          channel_id: 5,
          channel_name: 'google-vertex',
          status: 'unknown',
          source: 'none',
          weight: 60,
          request_count: 0,
          disabled: false,
        },
      ],
    },
    {
      // Image generation has no first token: it is judged by how long an
      // image takes, on a wall of three-hour slices.
      model_name: 'gpt-image-2',
      kind: 'image',
      lane: 'stable',
      status: 'operational',
      source: 'traffic',
      request_count: 8,
      error_count: 0,
      success_rate: 1,
      latency_p50_ms: 8300,
      latency_p95_ms: 10300,
      probe_count: 2,
      channel_count: 1,
      buckets: channelHealthBuckets('ok', 'traffic', 28, 6),
    },
    {
      // A search backend is a row per provider, with no lane.
      model_name: 'Tavily',
      kind: 'search',
      lane: '',
      status: 'operational',
      source: 'probe',
      request_count: 2,
      error_count: 0,
      success_rate: 1,
      latency_p50_ms: 2000,
      latency_p95_ms: 7900,
      probe_count: 24,
      channel_count: 1,
      buckets: channelHealthBuckets('ok', 'probe'),
    },
  ],
  // The by-upstream projection of the very same window. Kept consistent with
  // the lanes above so the smoke test can assert the two views agree.
  providers: [
    {
      channel_id: 3,
      channel_name: 'relay-blossom',
      source_type: 'aggregator',
      channel_type: 1,
      status: 'degraded',
      source: 'traffic',
      disabled: false,
      weight: 70,
      request_count: 31842,
      error_count: 255,
      success_rate: 0.978,
      avg_latency_ms: 2600,
      p95_latency_ms: 5400,
      output_tps: 35,
      stall_p50_ms: 320,
      input_per_mtok: 3,
      output_per_mtok: 15,
      share: 0.4,
      model_count: 1,
      lane_count: 1,
      probe_count: 0,
      buckets: channelHealthBuckets('warn', 'traffic'),
      lanes: [
        {
          model_name: 'claude-sonnet-5',
          lane: 'eco',
          status: 'degraded',
          source: 'traffic',
          success_rate: 0.978,
          request_count: 31842,
          share: 1,
          weight: 70,
          disabled: false,
        },
      ],
    },
    {
      channel_id: 1,
      channel_name: 'anthropic-primary',
      source_type: 'official',
      channel_type: 1,
      status: 'operational',
      source: 'traffic',
      disabled: false,
      weight: 100,
      request_count: 34230,
      error_count: 34,
      success_rate: 0.999,
      avg_latency_ms: 1120,
      p95_latency_ms: 1900,
      output_tps: 42,
      stall_p50_ms: 320,
      input_per_mtok: 3,
      output_per_mtok: 15,
      share: 0.43,
      model_count: 1,
      lane_count: 1,
      probe_count: 0,
      buckets: channelHealthBuckets('ok', 'traffic'),
      lanes: [
        {
          model_name: 'claude-sonnet-5',
          lane: 'stable',
          status: 'operational',
          source: 'traffic',
          success_rate: 0.999,
          request_count: 34230,
          share: 0.71,
          weight: 100,
          disabled: false,
        },
      ],
    },
    {
      channel_id: 4,
      channel_name: 'relay-corsair',
      source_type: 'aggregator',
      channel_type: 1,
      status: 'operational',
      source: 'probe',
      last_probed_at: new Date(Date.now() - 6 * 60 * 1000).toISOString(),
      disabled: false,
      weight: 30,
      request_count: 0,
      error_count: 0,
      success_rate: 1,
      model_count: 1,
      lane_count: 1,
      probe_count: 6,
      buckets: channelHealthBuckets('ok', 'probe'),
      last_probe: {
        probed_at: '2026-09-01T11:58:00Z',
        model_name: 'deepseek-v4-pro',
        lane: 'eco',
        success: true,
        latency_ms: 1204,
      },
      lanes: [
        {
          model_name: 'deepseek-v4-pro',
          lane: 'eco',
          status: 'operational',
          source: 'probe',
          last_probed_at: new Date(Date.now() - 6 * 60 * 1000).toISOString(),
          success_rate: 1,
          request_count: 0,
          weight: 30,
          disabled: false,
        },
      ],
    },
  ],
  updated_at: '2026-09-01T12:00:00Z',
};

const usageLog = {
  id: 1,
  request_id: 'req-smoke-usage',
  user_id: 1,
  username: 'admin',
  channel_name: 'OpenAI smoke channel',
  token_name: 'Smoke token',
  model_name: 'gpt-4o-mini',
  prompt_tokens: 12,
  completion_tokens: 24,
  amount_microusd: 36,
  created_at: 1735689600,
};

const fileObject = {
  id: 'file-smoke123',
  object: 'file',
  bytes: 128,
  created_at: 1735689600,
  filename: 'smoke.txt',
  purpose: 'user_data',
  status: 'processed',
  expires_at: 1738281600,
};

/** Two families with both lanes priced, so the model plaza renders its
 *  signature card grid (list price struck through, viewer price highlighted)
 *  without a backend. */
const mockedCatalog = [
  {
    id: 'gpt-4o-mini',
    family: 'OpenAI',
    accessible: true,
    context_length: 128000,
    context_window: 128000,
    capabilities: { vision: true, tools: true, json: true },
    pricing: { input_per_million: 0.15, output_per_million: 0.6 },
    your_price: { input_per_million: 0.132, output_per_million: 0.528 },
    lanes: [
      {
        key: 'stable',
        available: true,
        input_per_million: 0.15,
        health: {
          availability: 0.998,
          latency_p95_ms: 820,
          observed_window: 'sample',
          status: 'available',
        },
      },
      {
        key: 'eco',
        available: true,
        input_per_million: 0.09,
        discount_percent: 40,
        // Sampled only by the gateway's probes. The catalog does not say so:
        // it is measured, and it renders like any measured lane.
        health: {
          availability: 0.972,
          observed_window: 'sample',
          status: 'degraded',
        },
      },
    ],
  },
  {
    id: 'gpt-4o',
    family: 'OpenAI',
    accessible: true,
    context_length: 128000,
    context_window: 128000,
    capabilities: { vision: true, tools: true },
    pricing: { input_per_million: 2.5, output_per_million: 10 },
    lanes: [
      {
        key: 'stable',
        available: true,
        input_per_million: 2.5,
        health: { availability: 0.995, latency_p95_ms: 1180 },
      },
      { key: 'eco', available: false },
    ],
  },
  {
    id: 'claude-sonnet-4-6',
    family: 'Anthropic',
    accessible: true,
    context_length: 200000,
    context_window: 200000,
    capabilities: { vision: true, tools: true, json: true },
    pricing: { input_per_million: 3, output_per_million: 15 },
    lanes: [
      {
        key: 'stable',
        available: true,
        input_per_million: 3,
        health: { availability: 0.999, latency_p95_ms: 960 },
      },
      {
        key: 'eco',
        available: true,
        input_per_million: 2.1,
        discount_percent: 30,
        health: { availability: 0.964, latency_p95_ms: 2100 },
      },
    ],
  },
  {
    id: 'deepseek-v4-flash',
    family: 'DeepSeek',
    accessible: true,
    context_length: 64000,
    context_window: 64000,
    capabilities: { tools: true },
    pricing: { input_per_million: 0.14, output_per_million: 0.28 },
    lanes: [
      {
        key: 'stable',
        available: true,
        input_per_million: 0.14,
        health: { availability: 0.991, latency_p95_ms: 640 },
      },
      {
        key: 'eco',
        available: true,
        input_per_million: 0.07,
        discount_percent: 50,
        health: { availability: 0.943, latency_p95_ms: 2450 },
      },
    ],
  },
];

const mockedPricedModels = [
  {
    model_name: 'gpt-4o-mini',
    billing_mode: 'per_token',
    cost_input_per_million: 0.11,
    cost_output_per_million: 0.44,
    stable: { enabled: true, input_per_million: 0.15, output_per_million: 0.6 },
    eco: { enabled: true, input_per_million: 0.09, output_per_million: 0.36 },
  },
  {
    model_name: 'priced-without-stable-pool',
    billing_mode: 'per_token',
    stable: { enabled: true, input_per_million: 0.2, output_per_million: 0.8 },
    eco: { enabled: false },
  },
  {
    model_name: 'claude-sonnet-4-6',
    billing_mode: 'per_token',
    cost_input_per_million: 2.4,
    cost_output_per_million: 12,
    stable: { enabled: true, input_per_million: 3, output_per_million: 15 },
    eco: { enabled: true, input_per_million: 2.1, output_per_million: 10.5 },
  },
  {
    model_name: 'deepseek-v4-flash',
    billing_mode: 'per_token',
    cost_input_per_million: 0.1,
    cost_output_per_million: 0.2,
    stable: {
      enabled: true,
      input_per_million: 0.14,
      output_per_million: 0.28,
      peak_hours_utc: '1-4,6-10',
      peak_multiplier: 2,
    },
    eco: { enabled: true, input_per_million: 0.07, output_per_million: 0.14 },
  },
  {
    model_name: 'gpt-image-1',
    billing_mode: 'per_call',
    stable: { enabled: true, per_call: 0.04 },
    eco: { enabled: false },
  },
];

const FAILURE_STATUS = Symbol('failure-status');
// Non-2xx answers a test asked for with fail(). The browser logs each one as
// "Failed to load resource"; those lines are expected, not page errors.
let intentionalFailures = 0;

/** A server failure in the real envelope: `{success:false, error:{...}}` with
 *  a real HTTP status. Return it from `payloadFor`. */
export const fail = (
  status: number,
  code: string,
  type = 'api_error',
  details: Record<string, unknown> = {},
) =>
  Object.assign(
    {
      success: false,
      error: {
        code,
        type,
        message: `mock ${code}`,
        request_id: 'req_mock',
        details,
      },
    },
    { [FAILURE_STATUS]: status },
  );

const fulfillment = (payload: unknown) => {
  const status =
    payload && typeof payload === 'object' && FAILURE_STATUS in payload
      ? Number((payload as Record<symbol, unknown>)[FAILURE_STATUS])
      : 200;
  if (status >= 400) intentionalFailures += 1;
  return {
    status,
    contentType: 'application/json',
    body: JSON.stringify(payload),
  };
};

function payloadFor(
  url,
  fixtures = { catalog: mockedCatalog, status: mockedStatus, user: mockedUser },
) {
  const { pathname } = new URL(url);

  if (pathname === '/api/status') return ok(fixtures.status);
  if (pathname === '/api/user/self') return ok(fixtures.user);
  if (pathname === '/api/user/telegram') {
    return ok({ available: false, linked: false });
  }
  const extensionBody = extensionMockFor(pathname);
  if (extensionBody !== undefined) return extensionBody;
  if (pathname === '/api/user/' || pathname === '/api/user') {
    return page([fixtures.user]);
  }
  if (pathname === '/api/user/role') return ok({ 1: 'User', 10: 'Admin' });
  if (pathname === '/api/user/models') return ok(['gpt-4o-mini']);
  if (pathname === '/api/user/topup/info') {
    return ok({ amount_options: [10, 50], min_topup: 1, topup_link: '' });
  }
  if (pathname.startsWith('/api/user/topup')) return page([]);
  if (pathname === '/api/user/wallet/ledger') {
    return ok({
      opening_balance_microusd: 5_250_000,
      items: [
        {
          closing_balance_microusd: 5_000_000,
          credit_microusd: 0,
          date: new Date().toISOString().slice(0, 10),
          spend_microusd: 250_000,
        },
      ],
    });
  }
  if (pathname === '/api/user/models/catalog') return ok(fixtures.catalog);
  if (pathname === '/models.json') return ok(fixtures.catalog);
  if (pathname === '/api/pricing/sell_prices') {
    return ok(
      mockedPricedModels.flatMap((model) =>
        (['stable', 'eco'] as const)
          .filter((lane) => model[lane])
          .map((lane) => ({
            model_name: model.model_name,
            channel_group: lane === 'eco' ? 'cheap' : 'direct',
            active: model[lane].enabled !== false,
            input_per_mtok: model[lane].input_per_million,
            output_per_mtok: model[lane].output_per_million,
            request_usd: model[lane].per_call,
            second_usd: model[lane].per_second,
            peak_hours_utc: model[lane].peak_hours_utc,
            peak_multiplier: model[lane].peak_multiplier,
          })),
      ),
    );
  }
  if (pathname.startsWith('/api/user')) return page([fixtures.user]);

  // A single Key reads back as one record, not a page of them: the editor
  // fills itself from this, and a list here leaves every field blank.
  const tokenDetail = /^\/api\/token\/(\d+)$/.exec(pathname);
  if (tokenDetail) {
    const found = [token, unlimitedToken].find(
      (candidate) => String(candidate.id) === tokenDetail[1],
    );
    if (found) return ok(found);
  }
  if (pathname.startsWith('/api/token')) return page([token, unlimitedToken]);

  // Must precede the /api/channel prefix match below: channel_health is a
  // separate endpoint, not a channel-list route.
  if (pathname === '/api/channel_health') {
    return ok(mockedChannelHealth);
  }
  if (pathname === '/api/channel/models') {
    return ok({ 1: ['gpt-4o-mini'], 3: ['claude-sonnet-4-6'] });
  }
  if (/^\/api\/channel\/\d+\/key$/.test(pathname)) {
    return ok({ key: 'sk-smoke-channel-key' });
  }
  if (pathname.startsWith('/api/channel')) return page([channel, cheapChannel]);

  if (pathname === '/api/price-plans') return ok([]);
  if (pathname === '/api/pricing') {
    // /api/pricing returns the model list as a top-level `data` array
    // alongside sibling keys — not the paginated { items } envelope.
    return {
      success: true,
      message: '',
      data: [],
      vendors: [],
      supported_endpoint: {},
    };
  }
  if (pathname === '/api/model_ops/lanes') {
    return ok({
      items: [
        {
          slug: 'direct',
          name: '稳定档',
          enabled: true,
          builtin: true,
          ever_enabled: true,
          sort: 0,
          sources: 2,
          priced: 2,
        },
        {
          slug: 'cheap',
          name: '便宜档',
          enabled: true,
          builtin: true,
          ever_enabled: true,
          sort: 1,
          sources: 1,
          priced: 1,
        },
      ],
    });
  }
  if (pathname === '/api/model_ops/sources') {
    return ok({
      items: modelOpsSources.map((source) => ({
        ...source,
        model_names: source.id === '3' ? ['gemini-image-4'] : ['gpt-4o-mini'],
      })),
      rules: modelOpsRules,
    });
  }
  if (pathname === '/api/model_ops/pools') {
    return ok({ items: modelOpsPools, window_hours: 48 });
  }
  if (pathname === '/api/model_ops/spend') {
    return ok({
      items: [{ id: 0, name: 'gpt-4o-mini', amount_microusd: 208_110_000 }],
    });
  }
  if (pathname === '/api/model_ops/pool_weight/batch') {
    return ok({ updated: 1, weight: 0 });
  }
  if (pathname === '/api/model_ops/pool_weight') {
    return ok({ model: 'gpt-4o-mini', lane: 'direct', weight: 1 });
  }
  if (pathname === '/api/model_ops/models/tier_config') {
    return ok({ model: 'gpt-4o-mini', tiers: 2 });
  }
  if (pathname === '/api/model_ops/models/status') {
    return ok({ updated: 1, status: 1 });
  }
  if (pathname === '/api/models/sync_upstream/preview') {
    return ok({
      missing: ['gpt-5-preview'],
      conflicts: [
        {
          model_name: 'gpt-4o-mini',
          fields: [{ field: 'description', local: 'a', upstream: 'b' }],
        },
      ],
      source: { locale: 'zh', models_url: '', vendors_url: '' },
    });
  }
  if (pathname.endsWith('/preview')) {
    return ok({
      local_total: 2,
      matched_count: 1,
      unmatched_count: 1,
      matched: [{ local_model: 'gpt-4o-mini' }],
      unmatched: ['gemini-image-4'],
    });
  }
  if (pathname.startsWith('/api/models/rankings')) return page([model]);
  if (pathname === '/api/models/' || pathname === '/api/models/search') {
    // The list endpoint carries full-catalog vendor counts alongside the page,
    // which is what the 厂牌 filter counts are built from.
    return ok({
      items: adminModels,
      total: adminModels.length,
      page: 1,
      page_size: 100,
      vendor_counts: { 1: adminModels.length },
    });
  }
  if (pathname.startsWith('/api/models')) return page([model]);
  if (pathname.startsWith('/api/vendors'))
    return page([{ id: 'vendor_openai', internal_id: 1, name: 'OpenAI' }]);

  if (pathname.startsWith('/api/group')) {
    return ok({ default: { desc: 'Default group' } });
  }
  if (pathname === '/api/data/overview') return ok(mockOverview());
  if (
    pathname === '/api/data/dashboard' ||
    pathname === '/api/data/dashboard/self'
  ) {
    return ok({
      totals: {
        amount_microusd: 250000,
        outcome_count: 5,
        request_count: 3,
      },
      by_model: [{ id: 0, name: 'gpt-4o-mini', amount_microusd: 208_110_000 }],
      time_series: [
        {
          amount_microusd: 250000,
          bucket: Math.floor(Date.now() / 1000),
          request_count: 3,
        },
      ],
    });
  }
  if (pathname.includes('/stat')) return ok({});
  if (pathname.startsWith('/api/log')) return page([usageLog]);
  if (pathname.startsWith('/api/option')) return ok({});
  if (pathname.startsWith('/api/uptime/status')) {
    return ok({ overall_status: 'up', services: [] });
  }
  if (pathname.startsWith('/api/reports/monthly/subscription')) {
    return ok({ enabled: false });
  }
  if (pathname.startsWith('/api/reports/monthly/latest')) return ok(null);
  if (pathname.startsWith('/api/reports/monthly')) return page([]);
  if (pathname.startsWith('/api/home_page_content'))
    return ok({ enabled: false });
  if (pathname.startsWith('/api/workers')) return ok({});
  if (pathname.startsWith('/api/files')) {
    return {
      object: 'list',
      data: [fileObject],
      has_more: false,
    };
  }
  if (pathname.startsWith('/api/upload')) return ok([]);

  if (pathname === '/v1/search') {
    return {
      query: 'lazu search smoke',
      results: [
        {
          title: 'Lazu smoke search result',
          url: 'https://example.com/lazu-search-smoke',
          snippet: 'Mocked search result for the search console smoke test.',
          content: null,
          published_at: null,
          score: 0.99,
          source: 'web',
        },
      ],
      usage: {
        web_search_requests: 1,
        web_search_billable_units: 1,
      },
      provider_trace: {
        backend: 'tavily-smoke',
      },
      route_receipt: {
        selection: 'auto',
        selected_backend: 'tavily-smoke',
      },
    };
  }

  return page([]);
}

/** A settled 24-hour overview (internal/model/overview.go shape). */
export function mockOverview(overrides = {}) {
  const hour = 3600;
  const end = Math.floor(Date.now() / 1000 / hour) * hour + hour;
  const series = Array.from({ length: 24 }, (_, index) => ({
    start: end - hour * (24 - index),
    end: end - hour * (23 - index),
    partial: index === 23,
    request_count: 3,
    outcome_count: 3,
    error_count: 0,
    amount_microusd: 250000,
    upstream_cost_microusd: 150000,
    gross_margin_microusd: 100000,
    prompt_tokens: 1200,
    completion_tokens: 300,
    cached_tokens: 0,
  }));
  const totals = {
    request_count: 72,
    outcome_count: 72,
    error_count: 0,
    success_rate: 1,
    amount_microusd: 6_000_000,
    upstream_cost_microusd: 3_600_000,
    gross_margin_microusd: 2_400_000,
    cost_sample_count: 72,
    prompt_tokens: 28_800,
    completion_tokens: 7_200,
    cached_tokens: 0,
    p95_latency_ms: 900,
    p95_ttft_ms: 320,
  };
  return {
    start: end - hour * 24,
    end,
    bucket: 'hour',
    tz_offset: 0,
    totals,
    previous: { ...totals, amount_microusd: 6_000_000 },
    series,
    models: [
      {
        id: 0,
        name: 'gpt-4o-mini',
        request_count: 72,
        error_count: 0,
        amount_microusd: 6_000_000,
        prompt_tokens: 28_800,
        completion_tokens: 7_200,
        cached_input_tokens: 0,
        last_used_at: end - hour,
      },
    ],
    keys: [],
    ...overrides,
  };
}

export async function setupMockedApp(page, options = {}) {
  const status = { ...mockedStatus, ...(options.status || {}) };
  const user = { ...mockedUser, ...(options.user || {}) };
  const language = options.language || 'zh';
  // null exercises the real first-login default without a saved scope.
  const consoleScope =
    options.consoleScope === undefined
      ? 'workspace:default'
      : options.consoleScope;
  const fixtures = { catalog: mockedCatalog, status, user };
  const pageErrors = [];
  const consoleErrors = [];
  intentionalFailures = 0;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  page.on('console', (message) => {
    if (message.type() === 'error') {
      consoleErrors.push(message.text());
    }
  });

  await page.route('**/api/**', async (route) => {
    const override = await options.payloadFor?.(
      route.request().url(),
      route.request(),
    );
    await route.fulfill(
      fulfillment(
        override === undefined
          ? payloadFor(route.request().url(), fixtures)
          : override,
      ),
    );
  });

  await page.route('**/models.json', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(ok(fixtures.catalog)),
    });
  });

  await page.route('**/v1/**', async (route) => {
    const override = await options.payloadFor?.(
      route.request().url(),
      route.request(),
    );
    await route.fulfill(
      fulfillment(
        override === undefined
          ? payloadFor(route.request().url(), fixtures)
          : override,
      ),
    );
  });

  await page.addInitScript(
    ({ consoleScope, language, status, user }) => {
      localStorage.setItem('status', JSON.stringify(status));
      localStorage.setItem('user', JSON.stringify(user));
      if (consoleScope !== null) {
        localStorage.setItem(
          `lazu:console-scope:${user.id || user.username || 'anonymous'}`,
          consoleScope,
        );
      }
      if (!localStorage.getItem('i18nextLng')) {
        localStorage.setItem('i18nextLng', language);
      }
      localStorage.setItem('system_name', 'Lazu');
      localStorage.setItem(
        'micro_usd_per_usd',
        String(status.micro_usd_per_usd),
      );
      localStorage.setItem('enable_data_export', 'true');
    },
    { consoleScope, language, status, user },
  );

  return {
    async expectNoUnexpectedErrors() {
      expect(pageErrors).toEqual([]);
      const resourceFailure =
        /^Failed to load resource: the server responded with a status of [45]\d\d/;
      expect(
        intentionalFailures > 0
          ? consoleErrors.filter((line) => !resourceFailure.test(line))
          : consoleErrors,
      ).toEqual([]);
    },
  };
}

export async function expectRouteReady(page, routePath) {
  await page.goto(routePath, { waitUntil: 'domcontentloaded' });
  await page
    .waitForLoadState('networkidle', { timeout: 5_000 })
    .catch(() => {});
  await expect(page).toHaveURL(new RegExp(routePath.replace(/[/?]/g, '\\$&')));
  await expect(page.locator('#root')).toContainText(/\S/);
}
