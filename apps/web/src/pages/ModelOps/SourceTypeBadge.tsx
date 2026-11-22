import type { ModelOpsSourceType } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.sourceType';

const DEFAULTS: Record<ModelOpsSourceType, string> = {
  aggregator: '聚合',
  cloud: '云厂商',
  official: '官方',
  reverse: '非官方',
  unclassified: '未分类',
};

export const sourceTypeLabel = (value: ModelOpsSourceType, t: TFunction) =>
  t(`${ns}.${value}`, { defaultValue: DEFAULTS[value] || value });
