import { useQueryClient } from '@tanstack/react-query';
import { TriangleAlert } from 'lucide-react';
import {
  useEffect,
  useMemo,
  useState,
  type MutableRefObject,
  type ReactNode,
} from 'react';
import { formatInfoValue, infoSourceLabel, modalityLabel } from './modelInfo';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { Input } from '../../components/ui/input';
import { Select, SelectItem } from '../../components/ui/select';
import { API } from '../../helpers/api';
import { showError, showSuccess } from '../../helpers/utils';
import {
  useModelInfoMutations,
  useModelInfoReviewsQuery,
  useModelMakersQuery,
} from '../../hooks/models/useModelInfoQuery';
import { makerFullLabel } from '../../lib/model-makers';
import type { ModelView } from './modelView';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.info';

type Props = {
  t: TFunction;
  view: ModelView;
  /** The 资料 tab saves this section with the rest of the form: the panel
   *  publishes its save here (null when nothing changed). */
  saveRef: MutableRefObject<(() => Promise<boolean>) | null>;
};

type Record_ = Record<string, unknown>;

const perMtok = (value: unknown) => {
  const number = Number(value) || 0;
  if (number <= 0) return null;
  // The column holds dollars per token (LiteLLM's unit); a few rows were typed
  // per million. Anything that small can only be per token.
  return number < 0.001 ? number * 1e6 : number;
};

const trim = (value: number) => String(Number(value.toFixed(6)));

const CAPABILITY_LABELS: Array<[string, string]> = [
  ['supports_reasoning', '推理'],
  ['supports_function_calling', '工具调用'],
  ['supports_response_schema', '结构化输出'],
  ['supports_vision', '识图'],
  ['supports_pdf_input', 'PDF'],
  ['supports_audio_input', '音频输入'],
  ['supports_audio_output', '音频输出'],
  ['supports_prompt_caching', '提示缓存'],
  ['supports_web_search', '联网'],
];

/**
 * 资料 — the model's facts, each with where it came from. The daily models.dev
 * sync fills the empty ones and proposes changes to the rest; here the
 * operator settles a proposal, fixes the maker or release date, or locks the
 * model so the sync only ever proposes.
 */
export default function ModelInfoPanel({ saveRef, t, view }: Props) {
  const record = view.record as Record_;
  const queryClient = useQueryClient();
  const makers = useModelMakersQuery();
  const reviews = useModelInfoReviewsQuery();
  const { resolveMutation } = useModelInfoMutations(t);
  const sources = (record.info_sources as Record<string, string>) || {};

  const [maker, setMaker] = useState('');
  const [releaseDate, setReleaseDate] = useState('');
  const [locked, setLocked] = useState(false);

  useEffect(() => {
    setMaker(String(record.maker || record.maker_resolved || ''));
    setReleaseDate(String(record.release_date || ''));
    setLocked(Boolean(record.info_locked));
  }, [
    record.id,
    record.maker,
    record.maker_resolved,
    record.release_date,
    record.info_locked,
  ]);

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
  const pendingFor = (field: string) =>
    pending.find((row) => row.field === field);
  const releaseReview = pendingFor('release_date');

  const dirty =
    maker !== String(record.maker || record.maker_resolved || '') ||
    releaseDate !== String(record.release_date || '') ||
    locked !== Boolean(record.info_locked);

  const save = async () => {
    try {
      // Only the facts this section edits travel; the whole-record model
      // update would also rewrite columns it never showed.
      const body: Record_ = {
        id: Number(record.id),
        info_locked: locked,
        // An inferred maker shown in the picker is not a choice the operator
        // made; only a real change is written (and then marked 手填).
        maker:
          !record.maker && maker === String(record.maker_resolved || '')
            ? ''
            : maker,
        release_date: releaseDate.trim(),
      };
      const response = await API.put('/api/model_info/model', body);
      if (!response?.data?.success) {
        throw new Error(
          response?.data?.message ||
            t(`${ns}.saveFailed`, { defaultValue: '资料没有保存' }),
        );
      }
      queryClient.invalidateQueries({ queryKey: ['console'] });
      return true;
    } catch (error) {
      showError(error);
      return false;
    }
  };
  saveRef.current = dirty ? save : null;

  const source = (field: string, fallback = '') =>
    infoSourceLabel(sources[field], t) || fallback;

  const row = (
    label: ReactNode,
    value: ReactNode,
    sourceText: string,
    key: string,
  ) => (
    <div className='mo-info__row' key={key}>
      <span className='mo-info__label'>{label}</span>
      <span className='mo-info__value'>{value}</span>
      <span className='mo-info__src'>{sourceText}</span>
    </div>
  );

  const pills = (values: string[]) =>
    values.length ? (
      <span className='mo-info__pills'>
        {values.map((value) => (
          <span className='mo-info__pill' key={value}>
            {value}
          </span>
        ))}
      </span>
    ) : (
      <span className='mo-info__none'>—</span>
    );

  const modalities = (record.modalities as {
    input?: string[];
    output?: string[];
  }) || { input: [], output: [] };
  const reasoning = (record.reasoning_options as {
    efforts?: string[];
    toggle?: boolean;
    mode?: string;
    off?: string;
  }) || { efforts: [] };
  const capabilities = (record.capabilities as Record<string, boolean>) || {};
  const unsupported = (record.unsupported_params as string[]) || [];
  const context = Number(record.context_length) || 0;
  const inputLimit = Number(record.input_limit) || 0;
  const maxOutput = Number(record.max_output_tokens) || 0;
  const officialIn = perMtok(record.official_input_price);
  const officialOut = perMtok(record.official_output_price);
  const officialTier = record.official_context_tier as
    | {
        threshold?: number;
        input_per_mtok?: number;
        output_per_mtok?: number;
      }
    | undefined;
  const officialTierThreshold = Number(officialTier?.threshold) || 0;
  const upstreamHint = (field: string) => {
    const review = pendingFor(field);
    return review ? (
      <span className='mo-info__upstream'>
        {t(`${ns}.upstream`, {
          defaultValue: '· 上游 {{value}}',
          value: formatInfoValue(field, review.upstream_value, t),
        })}
      </span>
    ) : null;
  };

  const makerOptions = (makers.data?.makers || []).map((item) => item.key);

  return (
    <>
      <div className='mo-info'>
        {row(
          t(`${ns}.field.maker`, { defaultValue: '厂商' }),
          <Select
            aria-label={t(`${ns}.field.maker`, { defaultValue: '厂商' })}
            size='sm'
            value={maker || 'none'}
            onValueChange={(value) => setMaker(value === 'none' ? '' : value)}
          >
            <SelectItem value='none'>
              {t(`${ns}.makerNone`, { defaultValue: '未归类' })}
            </SelectItem>
            {makerOptions.map((key) => (
              <SelectItem key={key} value={key}>
                {makerFullLabel(key, t)}
              </SelectItem>
            ))}
          </Select>,
          source(
            'maker',
            record.maker
              ? ''
              : t(`${ns}.source.inferred`, { defaultValue: '推断' }),
          ),
          'maker',
        )}
        {row(
          <label htmlFor='mo-info-release'>
            {t(`${ns}.field.release_date`, { defaultValue: '发布日期' })}
          </label>,
          <Input
            id='mo-info-release'
            placeholder='YYYY-MM-DD'
            value={releaseDate}
            onChange={(event) => setReleaseDate(event.target.value)}
          />,
          source('release_date'),
          'release',
        )}
        {releaseReview ? (
          <div className='mo-info__conflict' role='note'>
            <TriangleAlert aria-hidden='true' size={16} />
            <div>
              <b>
                {t(`${ns}.conflict.title`, {
                  defaultValue: 'models.dev 记的是 {{date}}',
                  date: releaseReview.upstream_value,
                })}
              </b>
              <p>
                {releaseReview.kind === 'first_run'
                  ? t(`${ns}.conflict.firstRun`, {
                      count: Number(releaseReview.note) || 0,
                      defaultValue:
                        '差了 {{count}} 天。这里原来记的多半是上架那天。',
                    })
                  : t(`${ns}.conflict.diff`, {
                      defaultValue:
                        '这个名字可能已经指向新版本，也可能是上游记错了。',
                    })}
              </p>
              <div className='mo-info__conflict-actions'>
                <Button
                  disabled={resolveMutation.isPending}
                  size='xs'
                  variant='secondary'
                  onClick={() =>
                    resolveMutation.mutate({
                      action: 'accept',
                      id: releaseReview.id,
                    })
                  }
                >
                  {t(`${ns}.conflict.accept`, {
                    defaultValue: '改成 {{date}}',
                    date: releaseReview.upstream_value,
                  })}
                </Button>
                <Button
                  disabled={resolveMutation.isPending}
                  size='xs'
                  variant='secondary'
                  onClick={() =>
                    resolveMutation.mutate({
                      action: 'keep',
                      id: releaseReview.id,
                    })
                  }
                >
                  {t(`${ns}.conflict.keep`, {
                    defaultValue: '保留，不再提示',
                  })}
                </Button>
              </div>
            </div>
          </div>
        ) : null}
        {row(
          t(`${ns}.field.knowledge_cutoff`, { defaultValue: '知识截止' }),
          String(record.knowledge_cutoff || '') || (
            <span className='mo-info__none'>—</span>
          ),
          source('knowledge_cutoff'),
          'knowledge',
        )}
        {row(
          t(`${ns}.field.context_length`, { defaultValue: '上下文' }),
          <>
            {context ? context.toLocaleString('en-US') : '—'}
            {upstreamHint('context_length')}
          </>,
          source('context_length'),
          'context',
        )}
        {row(
          t(`${ns}.field.input_limit`, { defaultValue: '输入上限' }),
          inputLimit ? (
            inputLimit.toLocaleString('en-US')
          ) : (
            <span className='mo-info__none'>
              {t(`${ns}.inputLimitSame`, { defaultValue: '和上下文相同' })}
            </span>
          ),
          inputLimit
            ? source('input_limit')
            : record.models_dev_id
              ? t(`${ns}.source.notListed`, {
                  defaultValue: 'models.dev 未单列',
                })
              : '',
          'inputLimit',
        )}
        {row(
          t(`${ns}.field.max_output_tokens`, { defaultValue: '最大输出' }),
          <>
            {maxOutput ? maxOutput.toLocaleString('en-US') : '—'}
            {upstreamHint('max_output_tokens')}
          </>,
          source('max_output_tokens'),
          'maxOutput',
        )}
        {row(
          t(`${ns}.officialPriceLabel`, { defaultValue: '官方价 $/M' }),
          officialIn != null || officialOut != null ? (
            <>
              {t(`${ns}.officialPriceValue`, {
                defaultValue: '输入 {{input}} · 输出 {{output}}',
                input: officialIn != null ? trim(officialIn) : '—',
                output: officialOut != null ? trim(officialOut) : '—',
              })}
              {upstreamHint('official_price')}
            </>
          ) : (
            <span className='mo-info__none'>—</span>
          ),
          source('official_price'),
          'price',
        )}
        {row(
          t(`${ns}.officialTierLabel`, { defaultValue: '官方长上下文价 $/M' }),
          officialTierThreshold > 0 ? (
            <>
              {t(`${ns}.officialTierValue`, {
                defaultValue:
                  '输入超过 {{threshold}}：输入 {{input}} · 输出 {{output}}',
                threshold: officialTierThreshold.toLocaleString('en-US'),
                input:
                  officialTier?.input_per_mtok != null
                    ? trim(officialTier.input_per_mtok)
                    : '—',
                output:
                  officialTier?.output_per_mtok != null
                    ? trim(officialTier.output_per_mtok)
                    : '—',
              })}
              {upstreamHint('official_context_tier')}
            </>
          ) : (
            <>
              <span className='mo-info__none'>—</span>
              {upstreamHint('official_context_tier')}
            </>
          ),
          source('official_context_tier'),
          'priceTier',
        )}
        {row(
          t(`${ns}.field.modalitiesIn`, { defaultValue: '输入' }),
          pills(
            (modalities.input || []).map((value) => modalityLabel(value, t)),
          ),
          source('modalities'),
          'modIn',
        )}
        {row(
          t(`${ns}.field.modalitiesOut`, { defaultValue: '输出' }),
          pills(
            (modalities.output || []).map((value) => modalityLabel(value, t)),
          ),
          source('modalities'),
          'modOut',
        )}
        {row(
          t(`${ns}.field.capabilities`, { defaultValue: '能力' }),
          pills(
            CAPABILITY_LABELS.filter(([key]) => capabilities[key]).map(
              ([key, label]) =>
                t(`${ns}.capability.${key}`, { defaultValue: label }),
            ),
          ),
          source('capabilities'),
          'caps',
        )}
        {row(
          t(`${ns}.field.reasoning_options`, { defaultValue: '思考深度' }),
          reasoning.toggle ||
            reasoning.efforts?.length ||
            reasoning.mode ||
            reasoning.off ? (
            <span className='mo-info__pills'>
              {reasoning.toggle ? (
                <span className='mo-info__pill'>
                  {t(`${ns}.thinkingToggle`, { defaultValue: '可关闭' })}
                </span>
              ) : null}
              {(reasoning.efforts || []).map((level) => (
                <code className='mo-info__level' key={level}>
                  {level}
                </code>
              ))}
              {reasoning.mode ? (
                <span className='mo-info__pill'>
                  {t(`${ns}.thinkingMode.${reasoning.mode}`, {
                    defaultValue: reasoning.mode,
                  })}
                </span>
              ) : null}
              {reasoning.off ? (
                <span className='mo-info__pill'>
                  {t(`${ns}.thinkingOff.${reasoning.off}`, {
                    defaultValue: reasoning.off,
                  })}
                </span>
              ) : null}
            </span>
          ) : (
            <span className='mo-info__none'>—</span>
          ),
          source('reasoning_options'),
          'reasoning',
        )}
        {row(
          t(`${ns}.field.unsupported_params`, {
            defaultValue: '不支持的参数',
          }),
          unsupported.length ? (
            <span className='mo-info__pills'>
              {unsupported.map((param) => (
                <code className='mo-info__param' key={param}>
                  {param}
                </code>
              ))}
            </span>
          ) : (
            <span className='mo-info__none'>
              {sources.unsupported_params
                ? t(`${ns}.none`, { defaultValue: '无' })
                : '—'}
            </span>
          ),
          source('unsupported_params'),
          'unsupported',
        )}
        {row(
          t(`${ns}.field.open_weights`, { defaultValue: '开放权重' }),
          sources.open_weights || record.open_weights
            ? record.open_weights
              ? t(`${ns}.yes`, { defaultValue: '是' })
              : t(`${ns}.no`, { defaultValue: '否' })
            : '—',
          source('open_weights'),
          'weights',
        )}
        {row(
          'models.dev ID',
          record.models_dev_id ? (
            <code className='mo-info__id'>{String(record.models_dev_id)}</code>
          ) : (
            <span className='mo-info__none'>
              {t(`${ns}.notMatched`, { defaultValue: '没有匹配到' })}
            </span>
          ),
          record.models_dev_id
            ? t(`${ns}.source.autoMatched`, { defaultValue: '自动匹配' })
            : '',
          'mdid',
        )}
        <div className='mo-info__lock'>
          <Checkbox
            aria-label={t(`${ns}.lock`, { defaultValue: '锁定这份资料' })}
            checked={locked}
            onCheckedChange={(checked: unknown) => setLocked(checked === true)}
          />
          <span>
            <b>{t(`${ns}.lock`, { defaultValue: '锁定这份资料' })}</b>
            <span>
              {t(`${ns}.lockHint`, {
                defaultValue:
                  '每日同步不再改这个模型的任何字段，只在资料更新里提示差异。',
              })}
            </span>
          </span>
        </div>
      </div>
    </>
  );
}
