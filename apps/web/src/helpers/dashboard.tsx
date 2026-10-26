import {
  timestamp2string,
  timestamp2string1,
  copy,
  showSuccess,
} from './utils';
import { notify } from '../components/ui/toast';
import {
  STORAGE_KEYS,
  DEFAULT_TIME_INTERVALS,
  DEFAULTS,
  ILLUSTRATION_SIZE,
} from '../constants/dashboard.constants';
import type { LucideIcon } from 'lucide-react';
import type { ComponentType, Dispatch, ReactNode, SetStateAction } from 'react';
import { Progress, Divider, Empty } from '@/components/ui/console-primitives';
import {
  IllustrationConstruction,
  IllustrationConstructionDark,
} from '@/components/ui/illustrations';

type LegacyProps = {
  children?: ReactNode;
  [key: string]: unknown;
};

const LegacyProgress = Progress as unknown as ComponentType<LegacyProps>;
const LegacyDivider = Divider as unknown as ComponentType<LegacyProps>;
const LegacyEmpty = Empty as unknown as ComponentType<LegacyProps>;

type Translate = (key: string) => string;
type DashboardTimeType = keyof typeof DEFAULT_TIME_INTERVALS;
type NumberMap = Map<string, number>;
type ChartSpec = Record<string, unknown> & {
  title?: Record<string, unknown>;
};
type ChartSpecSetter = Dispatch<SetStateAction<ChartSpec>>;
type IconComponent = LucideIcon;
type FormFieldComponent = ComponentType<Record<string, unknown>>;

type UptimeStatusConfig = {
  color?: string;
  text?: string;
};
type UptimeStatusMap = Record<string | number, UptimeStatusConfig>;

type MonitorItem = {
  group?: string;
  name?: string;
  status?: string | number;
  uptime?: number;
};

type DashboardRawItem = {
  amount_microusd?: number;
  count?: number;
  created_at: number;
  model_name?: string;
  token_used?: number;
};

type ProcessedDashboardData = {
  totalQuota: number;
  totalTimes: number;
  totalTokens: number;
  uniqueModels: Set<string>;
  timePoints: string[];
  timeQuotaMap: NumberMap;
  timeTokensMap: NumberMap;
  timeCountMap: NumberMap;
};

type TrendData = {
  balance: number[];
  usedQuota: number[];
  requestCount: number[];
  times: number[];
  consumeQuota: number[];
  tokens: number[];
  rpm: number[];
  tpm: number[];
};

type AggregatedModelData = {
  amountMicrousd: number;
  count: number;
  model: string;
  time: string;
};

type InitializeMaps = (key: string, ...maps: NumberMap[]) => void;
type UpdateMapValue = (map: NumberMap, key: string, value: number) => void;

const isDashboardTimeType = (value: string): value is DashboardTimeType =>
  value in DEFAULT_TIME_INTERVALS;

// ========== 时间相关工具函数 ==========
export const getDefaultTime = (): DashboardTimeType => {
  const stored = localStorage.getItem(STORAGE_KEYS.DATA_EXPORT_DEFAULT_TIME);
  return stored && isDashboardTimeType(stored) ? stored : 'hour';
};

export const getTimeInterval = (
  timeType: string,
  isSeconds = false,
): number => {
  const intervals =
    DEFAULT_TIME_INTERVALS[isDashboardTimeType(timeType) ? timeType : 'hour'] ||
    DEFAULT_TIME_INTERVALS.hour;
  return isSeconds ? intervals.seconds : intervals.minutes;
};

export const getInitialTimestamp = (): string => {
  const defaultTime = getDefaultTime();
  const now = new Date().getTime() / 1000;

  switch (defaultTime) {
    case 'hour':
      return timestamp2string(now - 86400);
    case 'week':
      return timestamp2string(now - 86400 * 30);
    default:
      return timestamp2string(now - 86400 * 7);
  }
};

// ========== 数据处理工具函数 ==========
export const updateMapValue: UpdateMapValue = (map, key, value) => {
  if (!map.has(key)) {
    map.set(key, 0);
  }
  map.set(key, (map.get(key) || 0) + value);
};

export const initializeMaps: InitializeMaps = (key, ...maps) => {
  maps.forEach((map) => {
    if (!map.has(key)) {
      map.set(key, 0);
    }
  });
};

// ========== 图表相关工具函数 ==========
export const updateChartSpec = (
  setterFunc: ChartSpecSetter,
  newData: unknown[],
  subtitle: string,
  newColors: Record<string, string>,
  dataId: string,
) => {
  setterFunc((prev) => ({
    ...prev,
    data: [{ id: dataId, values: newData }],
    title: {
      ...prev.title,
      subtext: subtitle,
    },
    color: {
      specified: newColors,
    },
  }));
};

export const getTrendSpec = (data: number[], color: string): ChartSpec => ({
  type: 'line',
  data: [{ id: 'trend', values: data.map((val, idx) => ({ x: idx, y: val })) }],
  xField: 'x',
  yField: 'y',
  height: 40,
  width: 100,
  axes: [
    {
      orient: 'bottom',
      visible: false,
    },
    {
      orient: 'left',
      visible: false,
    },
  ],
  padding: 0,
  autoFit: false,
  legends: { visible: false },
  tooltip: { visible: false },
  crosshair: { visible: false },
  line: {
    style: {
      stroke: color,
      lineWidth: 2,
    },
  },
  point: {
    visible: false,
  },
  background: {
    fill: 'transparent',
  },
});

// ========== UI 工具函数 ==========
export const createSectionTitle = (Icon: IconComponent, text: ReactNode) => (
  <div className='flex items-center gap-2'>
    <Icon size={16} />
    {text}
  </div>
);

export const createFormField = (
  Component: FormFieldComponent,
  props: Record<string, unknown>,
  FORM_FIELD_PROPS: Record<string, unknown>,
) => <Component {...FORM_FIELD_PROPS} {...props} />;

// ========== 操作处理函数 ==========
export const handleCopyUrl = async (url: string, t: Translate) => {
  if (await copy(url)) {
    notify.success(t('legacy.i18n.copy_successful.c1ef062e06'));
  }
};

export const handleSpeedTest = (apiUrl: string) => {
  const encodedUrl = encodeURIComponent(apiUrl);
  const speedTestUrl = `https://www.tcptest.cn/http/${encodedUrl}`;
  window.open(speedTestUrl, '_blank', 'noopener,noreferrer');
};

// ========== 状态映射函数 ==========
export const getUptimeStatusColor = (
  status: string | number,
  uptimeStatusMap: UptimeStatusMap,
): string => uptimeStatusMap[status]?.color || '#8b9aa7';

export const getUptimeStatusText = (
  status: string | number,
  uptimeStatusMap: UptimeStatusMap,
  t: Translate,
): string =>
  uptimeStatusMap[status]?.text
    ? t(uptimeStatusMap[status].text)
    : t('legacy.i18n.unknown.d9c32a4c3d');

// ========== 监控列表渲染函数 ==========
export const renderMonitorList = (
  monitors: MonitorItem[] | undefined,
  getUptimeStatusColor: (status: string | number) => string,
  getUptimeStatusText: (status: string | number) => string,
  t: Translate,
) => {
  if (!monitors || monitors.length === 0) {
    return (
      <div className='flex justify-center items-center py-4'>
        <LegacyEmpty
          image={<IllustrationConstruction style={ILLUSTRATION_SIZE} />}
          darkModeImage={
            <IllustrationConstructionDark style={ILLUSTRATION_SIZE} />
          }
          title={t('legacy.i18n.no_monitoring_data.891376b8fb')}
        />
      </div>
    );
  }

  const grouped: Record<string, MonitorItem[]> = {};
  monitors.forEach((m) => {
    const g = m.group || '';
    if (!grouped[g]) grouped[g] = [];
    grouped[g].push(m);
  });

  const renderItem = (monitor: MonitorItem, idx: number) => {
    const status = monitor.status ?? '';
    const name = monitor.name || '';

    return (
      <div
        key={idx}
        className='p-2 hover:bg-white rounded-lg transition-colors'
      >
        <div className='flex items-center justify-between mb-1'>
          <div className='flex items-center gap-2'>
            <div
              className='w-2 h-2 rounded-full flex-shrink-0'
              style={{ backgroundColor: getUptimeStatusColor(status) }}
            />
            <span className='text-sm font-medium text-gray-900'>{name}</span>
          </div>
          <span className='text-xs text-gray-500'>
            {((monitor.uptime || 0) * 100).toFixed(2)}%
          </span>
        </div>
        <div className='flex items-center gap-2'>
          <span className='text-xs text-gray-500'>
            {getUptimeStatusText(status)}
          </span>
          <div className='flex-1'>
            <LegacyProgress
              percent={(monitor.uptime || 0) * 100}
              showInfo={false}
              aria-label={`${name} uptime`}
              stroke={getUptimeStatusColor(status)}
            />
          </div>
        </div>
      </div>
    );
  };

  return Object.entries(grouped).map(([gname, list]) => (
    <div key={gname || 'default'} className='mb-2'>
      {gname && (
        <>
          <div className='text-md font-semibold text-gray-500 px-2 py-1'>
            {gname}
          </div>
          <LegacyDivider />
        </>
      )}
      {list.map(renderItem)}
    </div>
  ));
};

// ========== 数据处理函数 ==========
export const processRawData = (
  data: DashboardRawItem[],
  dataExportDefaultTime: string,
  initializeMaps: InitializeMaps,
  updateMapValue: UpdateMapValue,
): ProcessedDashboardData => {
  const result: ProcessedDashboardData = {
    totalQuota: 0,
    totalTimes: 0,
    totalTokens: 0,
    uniqueModels: new Set<string>(),
    timePoints: [],
    timeQuotaMap: new Map<string, number>(),
    timeTokensMap: new Map<string, number>(),
    timeCountMap: new Map<string, number>(),
  };

  data.forEach((item) => {
    const modelName = item.model_name || '-';
    const tokenUsed = Number(item.token_used || 0);
    const amountMicrousd = Number(item.amount_microusd || 0);
    const count = Number(item.count || 0);
    result.uniqueModels.add(modelName);
    result.totalTokens += tokenUsed;
    result.totalQuota += amountMicrousd;
    result.totalTimes += count;

    const timeKey = timestamp2string1(item.created_at, dataExportDefaultTime);
    if (!result.timePoints.includes(timeKey)) {
      result.timePoints.push(timeKey);
    }

    initializeMaps(
      timeKey,
      result.timeQuotaMap,
      result.timeTokensMap,
      result.timeCountMap,
    );
    updateMapValue(result.timeQuotaMap, timeKey, amountMicrousd);
    updateMapValue(result.timeTokensMap, timeKey, tokenUsed);
    updateMapValue(result.timeCountMap, timeKey, count);
  });

  result.timePoints.sort();
  return result;
};

export const calculateTrendData = (
  timePoints: string[],
  timeQuotaMap: NumberMap,
  timeTokensMap: NumberMap,
  timeCountMap: NumberMap,
  dataExportDefaultTime: string,
): TrendData => {
  const quotaTrend = timePoints.map((time) => timeQuotaMap.get(time) || 0);
  const tokensTrend = timePoints.map((time) => timeTokensMap.get(time) || 0);
  const countTrend = timePoints.map((time) => timeCountMap.get(time) || 0);

  const rpmTrend: number[] = [];
  const tpmTrend: number[] = [];

  if (timePoints.length >= 2) {
    const interval = getTimeInterval(dataExportDefaultTime);

    for (let i = 0; i < timePoints.length; i++) {
      rpmTrend.push((timeCountMap.get(timePoints[i]) || 0) / interval);
      tpmTrend.push((timeTokensMap.get(timePoints[i]) || 0) / interval);
    }
  }

  return {
    balance: [],
    usedQuota: [],
    requestCount: [],
    times: countTrend,
    consumeQuota: quotaTrend,
    tokens: tokensTrend,
    rpm: rpmTrend,
    tpm: tpmTrend,
  };
};

export const aggregateDataByTimeAndModel = (
  data: DashboardRawItem[],
  dataExportDefaultTime: string,
) => {
  const aggregatedData = new Map<string, AggregatedModelData>();

  data.forEach((item) => {
    const timeKey = timestamp2string1(item.created_at, dataExportDefaultTime);
    const modelKey = item.model_name || '-';
    const key = `${timeKey}-${modelKey}`;

    if (!aggregatedData.has(key)) {
      aggregatedData.set(key, {
        time: timeKey,
        model: modelKey,
        amountMicrousd: 0,
        count: 0,
      });
    }

    const existing = aggregatedData.get(key);
    if (!existing) return;
    existing.amountMicrousd += Number(item.amount_microusd || 0);
    existing.count += Number(item.count || 0);
  });

  return aggregatedData;
};

export const generateChartTimePoints = (
  aggregatedData: Map<string, AggregatedModelData>,
  data: DashboardRawItem[],
  dataExportDefaultTime: string,
): string[] => {
  let chartTimePoints = Array.from(
    new Set([...aggregatedData.values()].map((d) => d.time)),
  );

  if (chartTimePoints.length < DEFAULTS.MAX_TREND_POINTS) {
    const lastTime = Math.max(...data.map((item) => item.created_at));
    const interval = getTimeInterval(dataExportDefaultTime, true);

    chartTimePoints = Array.from(
      { length: DEFAULTS.MAX_TREND_POINTS },
      (_, i) =>
        timestamp2string1(lastTime - (6 - i) * interval, dataExportDefaultTime),
    );
  }

  return chartTimePoints;
};
