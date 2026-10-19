import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button, ErrorState, PendingBlock } from '../../../components/ui';
import {
  ComboChart,
  type ComboLineSeries,
} from '../../../components/ui/combo-chart';
import { API } from '../../../helpers/api';
import { unwrap } from '../../../lib/api-error';

type TimingDay = {
  day: string;
  samples: number;
  post_samples: number;
  gateway_pre_p50_ms: number | null;
  gateway_pre_p95_ms: number | null;
  gateway_post_p50_ms: number | null;
  gateway_post_p95_ms: number | null;
};
type GatewayPerformanceResponse = {
  timezone: string;
  days: TimingDay[] | null;
};

export default function GatewayPerformance({ reveal }: { reveal: boolean }) {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: ['ops', 'gateway-performance'],
    queryFn: async () =>
      unwrap<GatewayPerformanceResponse>(
        await API.get('/api/ops/gateway-performance'),
      ),
    staleTime: 60_000,
  });
  const points = query.data?.days ?? [];
  const series: ComboLineSeries[] = [
    {
      key: 'gateway_pre_p50_ms',
      label: t('page.console.ops.gateway.preP50', {
        defaultValue: '准备 P50',
      }),
      values: points.map((point) => point.gateway_pre_p50_ms),
      tone: 'ink',
    },
    {
      key: 'gateway_pre_p95_ms',
      label: t('page.console.ops.gateway.preP95', {
        defaultValue: '准备 P95',
      }),
      values: points.map((point) => point.gateway_pre_p95_ms),
      tone: 'ink',
      dashed: true,
    },
    {
      key: 'gateway_post_p50_ms',
      label: t('page.console.ops.gateway.postP50', {
        defaultValue: '收尾 P50',
      }),
      values: points.map((point) => point.gateway_post_p50_ms),
      tone: 'grey',
    },
    {
      key: 'gateway_post_p95_ms',
      label: t('page.console.ops.gateway.postP95', {
        defaultValue: '收尾 P95',
      }),
      values: points.map((point) => point.gateway_post_p95_ms),
      tone: 'grey',
      dashed: true,
    },
  ];
  const measured = points.some(
    (point) => point.samples > 0 || point.post_samples > 0,
  );
  return (
    <section
      className='ops-gateway-performance'
      aria-labelledby='gateway-performance-title'
    >
      <div className='ops-gateway-performance__heading'>
        <h2 id='gateway-performance-title'>
          {t('page.console.ops.gateway.title', { defaultValue: '网关耗时' })}
        </h2>
        <span className='ops-note'>
          {t('page.console.ops.gateway.range', {
            defaultValue: '近 30 天 · 按日 · UTC · 毫秒',
          })}
        </span>
      </div>
      <div className='ops-gateway-performance__chart'>
        {query.isPending ? (
          <div aria-busy='true'>
            {reveal ? (
              <PendingBlock height={248} />
            ) : (
              <div style={{ height: 248 }} />
            )}
          </div>
        ) : query.isError ? (
          <ErrorState
            action={
              <Button variant='secondary' onClick={() => void query.refetch()}>
                {t('common.retry', { defaultValue: '重试' })}
              </Button>
            }
          />
        ) : !measured ? (
          <p className='ops-note'>
            {t('page.console.ops.gateway.noMeasurements', {
              defaultValue: '暂无网关耗时记录，开始记录后将在这里显示。',
            })}
          </p>
        ) : (
          <ComboChart
            ariaLabel={t('page.console.ops.gateway.chart', {
              defaultValue: '请求准备和响应收尾的每日 P50、P95 耗时',
            })}
            height={248}
            labels={points.map((point) => point.day.slice(5))}
            titles={points.map((point) => `${point.day} UTC`)}
            lines={series}
            left={{ format: (value) => `${value.toFixed(0)} ms` }}
            tooltip={(index) =>
              series.map((line) => ({
                label: line.label,
                value:
                  line.values[index] == null
                    ? '—'
                    : `${line.values[index]?.toFixed(1)} ms`,
                tone: line.tone,
              }))
            }
          />
        )}
      </div>
    </section>
  );
}
