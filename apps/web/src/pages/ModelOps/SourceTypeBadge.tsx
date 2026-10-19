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

/** 来源类型 badge. Carries a text label, never colour alone — the four types are
 *  a taxonomy an operator has to read, not a severity. */
export default function SourceTypeBadge({
  t,
  value,
}: {
  t: TFunction;
  value: ModelOpsSourceType;
}) {
  const known = DEFAULTS[value] ? value : 'unclassified';
  return (
    <span
      className={`model-ops__srctype model-ops__srctype--${known}`}
      title={
        known === 'unclassified'
          ? t(`${ns}.unclassifiedHint`, {
              defaultValue:
                '尚未分类。系统不会替你猜来源类型，请在编辑里指定。',
            })
          : undefined
      }
    >
      {sourceTypeLabel(value, t)}
    </span>
  );
}
