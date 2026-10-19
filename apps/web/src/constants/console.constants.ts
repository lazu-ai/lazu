import dayjs from 'dayjs';

// ========== 日期预设常量 ==========
export const DATE_RANGE_PRESETS = [
  {
    text: 'common.dateRange.today',
    start: () => dayjs().startOf('day').toDate(),
    end: () => dayjs().endOf('day').toDate(),
  },
  {
    text: 'common.dateRange.last7Days',
    start: () => dayjs().subtract(6, 'day').startOf('day').toDate(),
    end: () => dayjs().endOf('day').toDate(),
  },
  {
    text: 'common.dateRange.thisWeek',
    start: () => dayjs().startOf('week').toDate(),
    end: () => dayjs().endOf('week').toDate(),
  },
  {
    text: 'common.dateRange.last30Days',
    start: () => dayjs().subtract(29, 'day').startOf('day').toDate(),
    end: () => dayjs().endOf('day').toDate(),
  },
  {
    text: 'common.dateRange.thisMonth',
    start: () => dayjs().startOf('month').toDate(),
    end: () => dayjs().endOf('month').toDate(),
  },
];
