import { useMemo } from 'react';
import { formatInfoValue, modalityLabel } from './modelInfo';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { useModelInfoReviewsQuery } from '../../hooks/models/useModelInfoQuery';
import type { ModelView } from './modelView';
import type { TFunction } from 'i18next';
import type { ReactNode } from 'react';

const ns = 'page.console.modelOps.info';

type Props = {
  onEdit: () => void;
  t: TFunction;
  view: ModelView;
};

type Record_ = Record<string, unknown>;

/* The facts models.dev fills, as an overview: four figures, then what goes
   in and out, what it can do and what it refuses. 编辑资料 swaps this for
   the form. */

const CAPABILITIES: Array<[string, string]> = [
  ['supports_function_calling', '工具调用'],
  ['supports_response_schema', '结构化输出'],
  ['supports_prompt_caching', '提示缓存'],
  ['supports_web_search', '联网'],
  ['supports_computer_use', '电脑操作'],
];

const perMtok = (value: unknown) => {
  const number = Number(value) || 0;
  if (number <= 0) return null;
  // Dollars per token (LiteLLM's unit); a few rows were typed per million.
  return number < 0.001 ? number * 1e6 : number;
};

const money = (value: number | null | undefined) =>
  value == null ? '—' : `$${String(Number(value.toFixed(4)))}`;

const cents = (value: number | null | undefined) =>
  value == null ? '—' : `$${value.toFixed(2)}`;

const shortTokens = (value: unknown) => {
  const number = Number(value) || 0;
  if (number <= 0) return '';
  if (number >= 1_000_000) return `${Math.round(number / 100_000) / 10}M`;
  return `${Math.round(number / 1000)}K`;
};

export default function ModelOverview({ onEdit, t, view }: Props) {
  const record = view.record as Record_;
  const reviews = useModelInfoReviewsQuery();
  const sources = (record.info_sources as Record<string, string>) || {};

  const pending = useMemo(
    () =>
      (reviews.data?.items || []).filter(
        (row) =>
          row.model_name === view.name &&
          row.status === 'pending' &&
          (row.kind === 'diff' || row.kind === 'first_run'),
      ),
    [reviews.data, view.name],
  );
  const upstream = (field: string) =>
    pending.find((row) => row.field === field)?.upstream_value;

  /* Where a figure came from, when that is not models.dev. */
  const sourceBadge = (field: string) => {
    switch (sources[field]) {
      case 'manual':
        return (
          <Badge variant='info'>
            {t(`${ns}.overview.edited`, { defaultValue: '本站改过' })}
          </Badge>
        );
      case 'inferred':
        return (
          <Badge variant='warning'>
            {t(`${ns}.source.inferred`, { defaultValue: '推断' })}
          </Badge>
        );
      case 'legacy':
        return (
          <Badge variant='warning'>
            {t(`${ns}.source.pending`, { defaultValue: '待确认' })}
          </Badge>
        );
      default:
        return null;
    }
  };

  const tokenUpstream = (field: string) => {
    const value = upstream(field);
    return value
      ? t(`${ns}.overview.upstream`, {
          defaultValue: '上游 {{value}}',
          value: shortTokens(value) || formatInfoValue(field, value, t),
        })
      : '';
  };

  const context = Number(record.context_length) || 0;
  const maxOutput = Number(record.max_output_tokens) || 0;
  const cutoff = String(record.knowledge_cutoff || '');
  const officialIn = perMtok(record.official_input_price);
  const officialOut = perMtok(record.official_output_price);
  const tier = record.official_context_tier as
    | { threshold?: number; input_per_mtok?: number; output_per_mtok?: number }
    | undefined;
  const cacheRead = Number(record.official_cache_read_per_mtok) || 0;
  const cacheWrite = Number(record.official_cache_write_per_mtok) || 0;

  const modalities = (record.modalities as {
    input?: string[];
    output?: string[];
  }) || { input: [], output: [] };
  const capabilities = (record.capabilities as Record<string, boolean>) || {};
  const reasoning = (record.reasoning_options as {
    efforts?: string[];
    toggle?: boolean;
  }) || { efforts: [] };
  const unsupported = (record.unsupported_params as string[]) || [];

  const efforts = reasoning.efforts || [];
  const thinking =
    efforts.length > 1
      ? t(`${ns}.overview.thinkingRange`, {
          defaultValue: '思考 {{from}} – {{to}}',
          from: efforts[0],
          to: efforts[efforts.length - 1],
        })
      : efforts.length === 1
        ? t(`${ns}.overview.thinkingOne`, {
            defaultValue: '思考 {{level}}',
            level: efforts[0],
          })
        : capabilities.supports_reasoning
          ? t(`${ns}.overview.thinking`, { defaultValue: '思考' })
          : '';
  const capabilityPills = [
    ...CAPABILITIES.filter(([key]) => capabilities[key]).map(([key, label]) =>
      t(`${ns}.capability.${key}`, { defaultValue: label }),
    ),
    thinking,
  ].filter(Boolean);

  const stat = (
    key: string,
    label: string,
    value: ReactNode,
    caption?: string,
    badge?: ReactNode,
  ) => (
    <div className='mdl-stat' key={key}>
      <p className='mdl-cap'>{label}</p>
      <p className='mdl-stat__value'>
        {value}
        {badge}
      </p>
      {caption ? <p className='mdl-cap'>{caption}</p> : null}
    </div>
  );

  const pills = (values: string[], struck = false) =>
    values.length ? (
      <span className='mdl-pills'>
        {values.map((value) => (
          <span
            className={struck ? 'mdl-pill mdl-pill--struck' : 'mdl-pill'}
            key={value}
          >
            {value}
          </span>
        ))}
      </span>
    ) : (
      <span className='mdl-none'>—</span>
    );

  const inputs = (modalities.input || []).map((value) =>
    modalityLabel(value, t),
  );
  const outputs = (modalities.output || []).map((value) =>
    modalityLabel(value, t),
  );

  return (
    <div className='mdl-overview'>
      <div className='mdl-stats'>
        {stat(
          'context',
          t(`${ns}.field.context_length`, { defaultValue: '上下文' }),
          shortTokens(context) || '—',
          tokenUpstream('context_length') ||
            (context
              ? t(`${ns}.overview.tokens`, {
                  defaultValue: '{{value}} token',
                  value: context.toLocaleString('en-US'),
                })
              : ''),
          sourceBadge('context_length'),
        )}
        {stat(
          'maxOutput',
          t(`${ns}.field.max_output_tokens`, { defaultValue: '最大输出' }),
          shortTokens(maxOutput) || '—',
          tokenUpstream('max_output_tokens'),
          sourceBadge('max_output_tokens'),
        )}
        {stat(
          'cutoff',
          t(`${ns}.field.knowledge_cutoff`, { defaultValue: '知识截止' }),
          cutoff || '—',
          undefined,
          cutoff ? sourceBadge('knowledge_cutoff') : null,
        )}
        {stat(
          'price',
          t(`${ns}.overview.officialPrice`, { defaultValue: '官方价' }),
          officialIn != null || officialOut != null
            ? `${money(officialIn)} / ${money(officialOut)}`
            : '—',
          Number(tier?.threshold) > 0
            ? t(`${ns}.overview.longContext`, {
                defaultValue: '长上下文 {{input}} / {{output}}',
                input: cents(tier?.input_per_mtok),
                output: cents(tier?.output_per_mtok),
              })
            : '',
          sourceBadge('official_price'),
        )}
        <div className='mdl-stats__edit'>
          <Button size='sm' variant='secondary' onClick={onEdit}>
            {t(`${ns}.overview.edit`, { defaultValue: '编辑资料' })}
          </Button>
          <span className='mdl-cap' title={String(record.models_dev_id || '')}>
            {record.info_locked
              ? t(`${ns}.overview.locked`, { defaultValue: '已锁定' })
              : record.models_dev_id
                ? 'models.dev'
                : t(`${ns}.source.manual`, { defaultValue: '手填' })}
          </span>
        </div>
      </div>

      <div className='mdl-facts'>
        <span className='mdl-facts__label'>
          {t(`${ns}.overview.io`, { defaultValue: '输入 → 输出' })}
        </span>
        {inputs.length || outputs.length ? (
          <span className='mdl-pills'>
            {inputs.map((value) => (
              <span className='mdl-pill' key={`in-${value}`}>
                {value}
              </span>
            ))}
            <span aria-hidden='true' className='mdl-arrow'>
              →
            </span>
            {outputs.map((value) => (
              <span className='mdl-pill' key={`out-${value}`}>
                {value}
              </span>
            ))}
          </span>
        ) : (
          <span className='mdl-none'>—</span>
        )}
        <span className='mdl-facts__label'>
          {t(`${ns}.field.capabilities`, { defaultValue: '能力' })}
        </span>
        {pills(capabilityPills)}
        {unsupported.length ? (
          <>
            <span className='mdl-facts__label'>
              {t(`${ns}.overview.unsupported`, { defaultValue: '不支持' })}
            </span>
            {pills(unsupported, true)}
          </>
        ) : null}
        {cacheRead || cacheWrite ? (
          <>
            <span className='mdl-facts__label'>
              {t(`${ns}.overview.officialCache`, {
                defaultValue: '官方缓存',
              })}
            </span>
            <span className='mdl-facts__text'>
              {[
                cacheRead
                  ? t(`${ns}.overview.cacheRead`, {
                      defaultValue: '读 {{value}}',
                      value: cents(cacheRead),
                    })
                  : '',
                cacheWrite
                  ? t(`${ns}.overview.cacheWrite`, {
                      defaultValue: '写 5 分钟 {{value}}',
                      value: cents(cacheWrite),
                    })
                  : '',
              ]
                .filter(Boolean)
                .join(' · ')}
            </span>
          </>
        ) : null}
      </div>
    </div>
  );
}
