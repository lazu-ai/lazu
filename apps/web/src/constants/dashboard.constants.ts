// ========== UI 配置常量 ==========
export const CHART_CONFIG = { mode: 'desktop-browser' };

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
