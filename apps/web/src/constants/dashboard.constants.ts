// ========== UI 配置常量 ==========
export const CHART_CONFIG = { mode: 'desktop-browser' };

export const CARD_PROPS = {
  shadows: '',
  bordered: true,
  headerLine: true,
};

export const FORM_FIELD_PROPS = {
  className: 'w-full mb-2 !rounded-lg',
  size: 'large',
};

export const ICON_BUTTON_CLASS = 'text-white hover:bg-opacity-80 !rounded-full';
export const FLEX_CENTER_GAP2 = 'flex items-center gap-2';

export const ILLUSTRATION_SIZE = { width: 96, height: 96 };

// ========== 时间相关常量 ==========
export const TIME_OPTIONS = [
  { label: 'common.timeUnit.hour', value: 'hour' },
  { label: 'common.timeUnit.day', value: 'day' },
  { label: 'common.timeUnit.week', value: 'week' },
];

export const DEFAULT_TIME_INTERVALS = {
  hour: { seconds: 3600, minutes: 60 },
  day: { seconds: 86400, minutes: 1440 },
  week: { seconds: 604800, minutes: 10080 },
};

// ========== 默认时间设置 ==========
export const DEFAULT_TIME_RANGE = {
  HOUR: 'hour',
  DAY: 'day',
  WEEK: 'week',
};

// ========== 图表默认配置 ==========
export const DEFAULT_CHART_SPECS = {
  PIE: {
    type: 'pie',
    outerRadius: 0.8,
    innerRadius: 0.5,
    padAngle: 0.6,
    valueField: 'value',
    categoryField: 'type',
    pie: {
      style: {
        cornerRadius: 10,
      },
      state: {
        hover: {
          outerRadius: 0.85,
          stroke: '#000',
          lineWidth: 1,
        },
        selected: {
          outerRadius: 0.85,
          stroke: '#000',
          lineWidth: 1,
        },
      },
    },
    legends: {
      visible: true,
      orient: 'left',
    },
    label: {
      visible: true,
    },
  },

  BAR: {
    type: 'bar',
    stack: true,
    legends: {
      visible: true,
      selectMode: 'single',
    },
    bar: {
      state: {
        hover: {
          stroke: '#000',
          lineWidth: 1,
        },
      },
    },
  },

  LINE: {
    type: 'line',
    legends: {
      visible: true,
      selectMode: 'single',
    },
  },
};

// ========== 公告类型配置 ==========
// `tone` is the Yunmu-facing axis: it selects a token pair in
// announcement-timeline.css. `dotColor` is retained for legacy legend
// consumers, but it also resolves through canonical tokens instead of carrying
// a second palette in application code.
export const ANNOUNCEMENT_TYPE_CONFIG = {
  default: {
    color: 'grey',
    dotColor: 'var(--lazu-text-faint)',
    labelKey: 'common.status.default',
    tone: 'neutral',
  },
  // Deploy-generated release notes are what the gateway actually publishes, so
  // they get a first-class type instead of falling through to "默认".
  release_note: {
    color: 'blue',
    dotColor: 'var(--lazu-evidence)',
    labelKey: 'common.status.releaseNote',
    tone: 'feature',
  },
  operational: {
    color: 'grey',
    dotColor: 'var(--lazu-text-faint)',
    labelKey: 'page.console.announcements.kind.operational',
    tone: 'neutral',
  },
  maintenance: {
    color: 'orange',
    dotColor: 'var(--lazu-warning)',
    labelKey: 'page.console.announcements.kind.maintenance',
    tone: 'notice',
  },
  incident: {
    color: 'red',
    dotColor: 'var(--lazu-danger)',
    labelKey: 'page.console.announcements.kind.incident',
    tone: 'danger',
  },
  model_update: {
    color: 'green',
    dotColor: 'var(--lazu-success)',
    labelKey: 'page.console.announcements.kind.model_update',
    tone: 'success',
  },
  ongoing: {
    color: 'blue',
    dotColor: 'var(--lazu-evidence)',
    labelKey: 'common.status.ongoing',
    tone: 'feature',
  },
  success: {
    color: 'green',
    dotColor: 'var(--lazu-success)',
    labelKey: 'common.status.success',
    tone: 'success',
  },
  warning: {
    color: 'orange',
    dotColor: 'var(--lazu-warning)',
    labelKey: 'common.status.warning',
    tone: 'notice',
  },
  error: {
    color: 'red',
    dotColor: 'var(--lazu-danger)',
    labelKey: 'common.status.error',
    tone: 'danger',
  },
};

// ========== 公告图例数据 ==========
export const ANNOUNCEMENT_LEGEND_DATA = Object.entries(
  ANNOUNCEMENT_TYPE_CONFIG,
).map(([type, config]) => ({
  color: config.color,
  label: config.labelKey,
  type,
}));

// ========== Uptime 状态映射 ==========
export const UPTIME_STATUS_MAP = {
  1: { color: '#10b981', label: 'uptime.status.up', text: 'uptime.text.up' }, // UP
  0: {
    color: '#ef4444',
    label: 'uptime.status.down',
    text: 'uptime.text.down',
  }, // DOWN
  2: {
    color: '#f59e0b',
    label: 'uptime.status.pending',
    text: 'uptime.text.pending',
  }, // PENDING
  3: {
    color: '#3b82f6',
    label: 'uptime.status.maintenance',
    text: 'uptime.text.maintenance',
  }, // MAINTENANCE
};

// ========== 本地存储键名 ==========
export const STORAGE_KEYS = {
  DATA_EXPORT_DEFAULT_TIME: 'data_export_default_time',
};

// ========== 默认值 ==========
export const DEFAULTS = {
  PAGE_SIZE: 20,
  CHART_HEIGHT: 96,
  MODEL_TABLE_PAGE_SIZE: 10,
  MAX_TREND_POINTS: 7,
};
