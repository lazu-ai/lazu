/** Synthetic review records; never read a production account or credential. */
export function reviewPayload(url: string) {
  const path = new URL(url).pathname;
  if (path === '/api/search/backends')
    return {
      success: true,
      data: [
        {
          id: 7,
          name: 'Jina',
          provider: 'jina',
          type: 76,
          masked_key: 'sk-searc••••••••a91f',
          status: 1,
          priority: 10,
          capabilities: ['web_search', 'web_fetch'],
          last_used_at: 1788677040,
        },
        {
          id: 8,
          name: 'Serper',
          provider: 'serper',
          type: 77,
          masked_key: 'sk-searc••••••••b72e',
          status: 1,
          priority: 20,
          capabilities: ['web_search'],
          last_used_at: 1788611880,
        },
        {
          id: 9,
          name: 'Tavily',
          provider: 'tavily',
          type: 75,
          masked_key: 'sk-searc••••••••c83a',
          status: 1,
          priority: 30,
          capabilities: ['web_search', 'answer'],
          last_used_at: null,
        },
      ],
    };
  if (/\/api\/(token|search\/backends)\/\d+\/reveal$/.test(path))
    return {
      success: true,
      data: { key: 'visual-fixture-only-' + 'x'.repeat(96) },
    };
  if (path === '/api/token/' || path === '/api/token')
    return {
      success: true,
      data: {
        page: 1,
        page_size: 10,
        total: 5,
        items: [
          'production',
          'design-workflow',
          'ci-agent',
          'research',
          'legacy-test',
        ].map((name, i) => ({
          id: i + 1,
          name,
          masked_key: `sk-axg••••••••000${i + 1}`,
          can_reveal_key: true,
          status: 1,
          used_quota: [68200000, 47600000, 12400000, 2800000, 1200000][i],
          remain_quota: [31800000, 2400000, 7600000, 17200000, 8800000][i],
          unlimited_quota: false,
          expired_time: -1,
          created_time: 1788566400,
        })),
      },
    };
  if (path === '/api/user/topup/info')
    return {
      success: true,
      data: {
        enable_stripe_topup: true,
        stripe_min_topup: 1,
        pay_methods: [{ name: 'Stripe', type: 'stripe', status: 'available' }],
      },
    };
  if (path === '/api/user/self/identities')
    return { success: true, data: { has_password: true, identities: [] } };
  if (path === '/api/user/summary')
    return {
      success: true,
      data: { total_customers: 28, low_balance_active: 4 },
    };
  if (path === '/api/data/dashboard')
    return {
      success: true,
      data: {
        totals: {
          amount_microusd: 2486400000,
          request_count: 42000,
          outcome_count: 42500,
          error_count: 540,
          active_users: 32,
          p95_ttft_ms: 2840,
        },
        by_user: [
          {
            id: 7,
            name: 'North Studio',
            amount_microusd: 912600000,
            request_count: 12840,
          },
          {
            id: 8,
            name: 'Orbit Lab',
            amount_microusd: 648200000,
            request_count: 6030,
          },
        ],
        time_series: [12, 17, 14, 20, 16, 21, 25].map((n, i) => ({
          bucket: Math.floor(Date.now() / 1000) - (6 - i) * 86400,
          amount_microusd: n * 1000000,
        })),
      },
    };
  return undefined;
}
