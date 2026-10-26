import { useQuery } from '@tanstack/react-query';
import {
  AudioLines,
  Ban,
  Binary,
  Braces,
  Brain,
  Check,
  Copy,
  DatabaseZap,
  Eye,
  FileText,
  Gauge,
  Image as ImageIcon,
  ListOrdered,
  LayoutGrid,
  Mic,
  MoveRight,
  Terminal,
  Type,
  Video,
  Volume2,
  Workflow,
  Wrench,
} from 'lucide-react';
import {
  memo,
  useEffect,
  useMemo,
  useState,
  type ComponentType,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  applyLanePeak,
  contextLengthOf,
  descriptionOf,
  formatContextLength,
  formatTierThreshold,
  formatTokenWindow,
  healthTone,
  laneContextTier,
  laneListPrice,
  modalitiesOf,
  releaseDateOf,
  resolveLanes,
  type CatalogModel,
  type CatalogPricing,
  type ContextTier,
  type Lane,
} from './catalog';
import { catalogQueryOptions } from './catalog-query';
import {
  buildSnippet,
  endpointLabel,
  highlightSnippet,
  SAMPLE_LANGUAGES,
  type SampleLanguage,
} from './code-samples';
import { LaneBlock, LaneHealthLegend } from './lane-block';
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Input,
  LoadingState,
  Modal,
  RefreshButton,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '../../components/ui';
import ConsolePage from '../../components/ui/console-page';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { getExternalAPIBaseURL } from '../../helpers/apiUrl';
import { modelDisplayName } from '../../lib/model-display-name';
import {
  groupByMaker,
  makerBrand,
  makerLabel,
  makerOf,
} from '../../lib/model-makers';
import './model-plaza.css';

const money = (value?: number) =>
  value == null ? null : `$${value < 1 ? value.toFixed(3) : value.toFixed(2)}`;

const capabilityEntries = (capabilities?: Record<string, boolean>) =>
  Object.entries(capabilities || {})
    .filter(([, enabled]) => enabled)
    .map(([name]) => name.replace(/^supports_/, '').replaceAll('_', ' '));

type IconComponent = ComponentType<{ size?: number | string }>;

const CAPABILITY_ICONS: Record<string, IconComponent> = {
  'audio input': Mic,
  'audio output': Volume2,
  'function calling': Wrench,
  'parallel function calling': Workflow,
  'pdf input': FileText,
  'prompt caching': DatabaseZap,
  reasoning: Brain,
  'response schema': Braces,
  vision: Eye,
};

const MODALITY_ICONS: Record<string, IconComponent> = {
  audio: AudioLines,
  embedding: Binary,
  decision: Braces,
  image: ImageIcon,
  rerank: ListOrdered,
  text: Type,
  video: Video,
};

const apiBaseURL = () =>
  getExternalAPIBaseURL(window.location.origin, window.location.hostname);

function CopyButton({
  className,
  iconOnly = false,
  label,
  value,
}: {
  className?: string;
  iconOnly?: boolean;
  label: string;
  value: string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  return (
    <button
      className={className ? `plaza-copy ${className}` : 'plaza-copy'}
      type='button'
      aria-label={label}
      title={iconOnly ? label : undefined}
      onClick={() => {
        navigator.clipboard?.writeText(value).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1600);
        });
      }}
    >
      {copied ? <Check size={13} /> : <Copy size={13} />}
      {iconOnly
        ? null
        : copied
          ? t('page.console.plaza.detail.copied', { defaultValue: '已复制' })
          : label}
    </button>
  );
}

const priceOf = (
  p: CatalogPricing | undefined,
  key: 'input' | 'output' | 'cache_read',
) => {
  if (!p) return undefined;
  if (key === 'input') return p.input_per_million ?? p.input;
  if (key === 'output') return p.output_per_million ?? p.output;
  return p.cache_read_per_million ?? p.cache_read;
};

const laneOffPercent = (lane: Lane, model: CatalogModel) => {
  const list = laneListPrice(lane, model).input;
  const mine = priceOf(lane.your_price, 'input');
  if (list == null || mine == null || list <= 0 || mine >= list) return 0;
  return Math.round((1 - mine / list) * 100);
};

const EXTRA_DIMS: {
  key: string;
  label: string;
  pick: (p?: CatalogPricing) => number | undefined;
  unit?: string;
}[] = [
  { key: 'imageInput', label: '图像输入', pick: (p) => p?.image_input },
  { key: 'imageOutput', label: '图像输出', pick: (p) => p?.image_output },
  { key: 'audioInput', label: '音频输入', pick: (p) => p?.audio_input },
  { key: 'audioOutput', label: '音频输出', pick: (p) => p?.audio_output },
];

function ExtraPriceChips({
  lanes,
  model,
}: {
  lanes: Lane[];
  model: CatalogModel;
}) {
  const { t } = useTranslation();
  const chips = lanes.flatMap((lane) =>
    EXTRA_DIMS.map((dim) => ({
      ...dim,
      lane,
      value: applyLanePeak(
        dim.pick(lane.your_price) ??
          dim.pick(lane.pricing) ??
          (lane.key === 'stable'
            ? (dim.pick(model.your_price) ?? dim.pick(model.pricing))
            : undefined),
        lane,
      ),
    })).filter((dim) => dim.value != null),
  );
  if (!chips.length) return null;
  return (
    <div className='plaza-xchips'>
      <span className='plaza-xchips__label'>
        {t('page.console.plaza.detail.extraDims', {
          defaultValue: '其他计费维度',
        })}
      </span>
      {chips.map((chip) => (
        <span key={`${chip.lane.key}-${chip.key}`} className='plaza-xchip'>
          <small>
            {chip.lane.key === 'stable'
              ? t('page.console.plaza.lane.stable', { defaultValue: '稳定' })
              : t('page.console.plaza.lane.eco', { defaultValue: 'Agent' })}
          </small>
          {t(`page.console.plaza.detail.price.${chip.key}`, {
            defaultValue: chip.label,
          })}
          <b>{money(chip.value)}</b>
          <small>
            {chip.unit ||
              t('page.console.plaza.detail.unit.perMtokShort', {
                defaultValue: '/M',
              })}
          </small>
        </span>
      ))}
    </div>
  );
}

/** Long-context prices, lane by lane: the base column is what most requests
 *  pay, the right column is what a request pays once its input passes the
 *  threshold. Shown only when a lane is priced by input length. */
function ContextTierTable({
  lanes,
  model,
}: {
  lanes: Lane[];
  model: CatalogModel;
}) {
  const { t } = useTranslation();
  const tiered = lanes
    .filter((lane) => lane.available !== false)
    .map((lane) => ({ lane, tier: laneContextTier(lane) }))
    .filter((entry): entry is { lane: Lane; tier: ContextTier } =>
      Boolean(entry.tier),
    );
  if (!tiered.length) return null;
  const threshold = formatTierThreshold(tiered[0].tier.threshold);
  const laneLabel = (lane: Lane) =>
    lane.key === 'stable'
      ? t('page.console.plaza.lane.stableFull', { defaultValue: '稳定线路' })
      : t('page.console.plaza.lane.ecoFull', { defaultValue: 'Agent 专线' });
  const base = (lane: Lane, key: 'input' | 'output' | 'cache_read') =>
    applyLanePeak(
      priceOf(lane.your_price, key) ??
        (key === 'cache_read'
          ? (priceOf(lane.pricing, 'cache_read') ??
            (lane.key === 'stable'
              ? priceOf(model.pricing, 'cache_read')
              : undefined))
          : laneListPrice(lane, model)[key]),
      lane,
    );
  const baseWrite = (lane: Lane, key: 'cache_write_5m' | 'cache_write_1h') =>
    applyLanePeak(
      lane.your_price?.[`${key}_per_million`] ??
        lane.your_price?.[key] ??
        lane.pricing?.[`${key}_per_million`] ??
        lane.pricing?.[key],
      lane,
    );
  const allRows: Array<{
    key: string;
    label: string;
    base: (lane: Lane) => number | undefined;
    tier: (tier: ContextTier) => number | undefined;
  }> = [
    {
      key: 'input',
      label: t('page.console.plaza.tier.input', { defaultValue: '输入' }),
      base: (lane) => base(lane, 'input'),
      tier: (tier) => tier.input,
    },
    {
      key: 'output',
      label: t('page.console.plaza.tier.output', { defaultValue: '输出' }),
      base: (lane) => base(lane, 'output'),
      tier: (tier) => tier.output,
    },
    {
      key: 'cacheRead',
      label: t('page.console.plaza.tier.cacheRead', { defaultValue: '缓存读' }),
      base: (lane) => base(lane, 'cache_read'),
      tier: (tier) => tier.cache_read,
    },
    {
      key: 'cacheWrite5m',
      label: t('page.console.plaza.detail.price.cacheWrite5m', {
        defaultValue: '缓存写入 5m',
      }),
      base: (lane) => baseWrite(lane, 'cache_write_5m'),
      tier: (tier) => tier.cache_write_5m,
    },
    {
      key: 'cacheWrite1h',
      label: t('page.console.plaza.detail.price.cacheWrite1h', {
        defaultValue: '缓存写入 1h',
      }),
      base: (lane) => baseWrite(lane, 'cache_write_1h'),
      tier: (tier) => tier.cache_write_1h,
    },
  ];
  const rows = allRows.filter((row) =>
    tiered.some(
      ({ lane, tier }) => row.base(lane) != null || row.tier(tier) != null,
    ),
  );

  return (
    <div className='plaza-tier'>
      <div className='plaza-tier__head'>
        <b>
          {t('page.console.plaza.tier.title', {
            defaultValue: '价格 · 每百万 token',
          })}
        </b>
        <span>
          {t('page.console.plaza.tier.subtitle', {
            defaultValue: '按输入长度分两段',
          })}
        </span>
      </div>
      <div className='plaza-lanetable__wrap'>
        <table className='plaza-lanetable plaza-tier__table'>
          <thead>
            <tr className='plaza-tier__lanes'>
              <th />
              {tiered.map(({ lane }) => (
                <th colSpan={2} key={lane.key}>
                  {laneLabel(lane)}
                </th>
              ))}
            </tr>
            <tr>
              <th />
              {tiered.map(({ lane, tier }) => [
                <th key={`${lane.key}-base`}>
                  {t('page.console.plaza.tier.below', {
                    defaultValue: '≤ {{threshold}} 输入',
                    threshold: formatTierThreshold(tier.threshold),
                  })}
                </th>,
                <th key={`${lane.key}-long`}>
                  {t('page.console.plaza.tier.above', {
                    defaultValue: '> {{threshold}} 输入',
                    threshold: formatTierThreshold(tier.threshold),
                  })}
                </th>,
              ])}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.key}>
                <td>{row.label}</td>
                {tiered.map(({ lane, tier }) => [
                  <td className='plaza-tier__base' key={`${lane.key}-base`}>
                    <b>{money(row.base(lane)) ?? '—'}</b>
                  </td>,
                  <td className='plaza-tier__long' key={`${lane.key}-long`}>
                    {money(
                      row.tier(tier) != null
                        ? applyLanePeak(row.tier(tier), lane)
                        : row.base(lane),
                    ) ?? '—'}
                  </td>,
                ])}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className='plaza-tier__rule'>
        {t('page.console.plaza.tier.rule', {
          defaultValue:
            '按一次请求的输入 token 数判定，缓存命中的部分也算在内。超过 {{threshold}} 时，这次请求的输入和输出都按高段价计；没超过就按左边的价格，大多数请求都在这一段。',
          threshold,
        })}
      </p>
    </div>
  );
}

function CodeSamples({ model }: { model: CatalogModel }) {
  const { t } = useTranslation();
  const endpoints = (model.supported_endpoints || []).filter(
    (endpoint) => endpoint.path,
  );
  const fallback = endpoints.length
    ? endpoints
    : [{ type: 'openai', path: '/v1/chat/completions', method: 'POST' }];
  const [endpointIdx, setEndpointIdx] = useState(0);
  const [language, setLanguage] = useState<SampleLanguage>('curl');
  const endpointPanel = (endpoint: (typeof fallback)[number]) => {
    const activeCode = buildSnippet(language, endpoint, model.id, apiBaseURL());
    return (
      <Tabs
        className='plaza-code__language-tabs'
        value={language}
        onValueChange={(value) => setLanguage(value as SampleLanguage)}
      >
        <div className='plaza-code__panel'>
          <div className='plaza-code__bar'>
            <TabsList className='plaza-code__langs'>
              {SAMPLE_LANGUAGES.map((lang) => (
                <TabsTrigger key={lang.key} value={lang.key}>
                  {lang.label}
                </TabsTrigger>
              ))}
            </TabsList>
            <span className='plaza-code__ep'>
              <b>{String(endpoint.method || 'POST').toUpperCase()}</b>
              {endpoint.path}
              {endpoint.mode ? (
                <small>
                  {' · '}
                  {endpoint.mode === 'native'
                    ? t('page.console.plaza.detail.endpoint.native', {
                        defaultValue: '原生',
                      })
                    : t('page.console.plaza.detail.endpoint.bridged', {
                        defaultValue: '转换',
                      })}
                </small>
              ) : null}
            </span>
            <CopyButton
              className='plaza-copy--dark'
              label={t('page.console.plaza.detail.copy', {
                defaultValue: '复制',
              })}
              value={activeCode}
            />
          </div>
          {SAMPLE_LANGUAGES.map((lang) => {
            const code = buildSnippet(
              lang.key,
              endpoint,
              model.id,
              apiBaseURL(),
            );
            return (
              <TabsContent
                key={lang.key}
                className='plaza-code__language-panel'
                value={lang.key}
              >
                <pre>{highlightSnippet(code, lang.key)}</pre>
              </TabsContent>
            );
          })}
        </div>
      </Tabs>
    );
  };

  if (fallback.length === 1) {
    return <div className='plaza-code'>{endpointPanel(fallback[0])}</div>;
  }

  return (
    <Tabs
      className='plaza-code'
      value={String(endpointIdx)}
      onValueChange={(value) => setEndpointIdx(Number(value))}
    >
      <TabsList className='plaza-code__eptabs'>
        {fallback.map((endpoint, index) => (
          <TabsTrigger
            key={`${endpoint.type}-${endpoint.path}`}
            value={String(index)}
          >
            {endpointLabel(endpoint.type)}
          </TabsTrigger>
        ))}
      </TabsList>
      {fallback.map((endpoint, index) => (
        <TabsContent
          key={`${endpoint.type}-${endpoint.path}`}
          className='plaza-code__endpoint-panel'
          value={String(index)}
        >
          {endpointPanel(endpoint)}
        </TabsContent>
      ))}
    </Tabs>
  );
}

function DetailSection({
  icon: Icon,
  title,
  children,
}: {
  icon: IconComponent;
  title: string;
  children: ReactNode;
}) {
  return (
    <section className='plaza-detail__section'>
      <h3>
        <Icon size={13} />
        {title}
      </h3>
      {children}
    </section>
  );
}

function ModelDetail({ model }: { model: CatalogModel }) {
  const { t, i18n } = useTranslation();
  const description = descriptionOf(model, i18n.language || 'zh');
  const modality = modalitiesOf(model);
  const context = formatTokenWindow(contextLengthOf(model));
  const maxOutput = formatTokenWindow(model.max_output_tokens);
  const inputLimit = formatTokenWindow(model.input_limit);
  const caps = capabilityEntries(model.capabilities);
  const lanes = resolveLanes(model);
  const released = releaseDateOf(model) || null;
  const efforts = model.reasoning_options?.efforts || [];
  const canSwitchOffThinking = Boolean(model.reasoning_options?.toggle);
  const unsupported = model.unsupported_params || [];

  // Best (lowest) effective in/out price across lanes, for the hero card;
  // the off% is the steepest lane discount so the header matches the table.
  const bestPrice = (key: 'input' | 'output') => {
    const values = lanes
      .filter((lane) => lane.available !== false)
      .map((lane) =>
        applyLanePeak(
          priceOf(lane.your_price, key) ?? laneListPrice(lane, model)[key],
          lane,
        ),
      )
      .filter((value): value is number => value != null);
    return values.length ? Math.min(...values) : undefined;
  };
  const bestOff = Math.max(
    0,
    ...lanes.map((lane) => laneOffPercent(lane, model)),
  );
  const heroIn = bestPrice('input');
  const heroOut = bestPrice('output');

  const renderModalities = (values: string[]) =>
    values.length ? (
      <span className='plaza-detail__modality'>
        {values.map((value) => {
          const Icon = MODALITY_ICONS[value] || Type;
          return (
            <span key={value} className='plaza-detail__modality-item'>
              <Icon size={12} />
              {t(`page.console.plaza.detail.modality.${value}`, {
                defaultValue: value,
              })}
            </span>
          );
        })}
      </span>
    ) : (
      '—'
    );

  return (
    <div className='plaza-detail'>
      {description ? <p className='plaza-detail__desc'>{description}</p> : null}

      <div className='plaza-detail__stats'>
        <div className='plaza-detail__stat'>
          <span className='plaza-detail__stat-label'>
            {t('page.console.plaza.detail.modalityHeading', {
              defaultValue: '模态',
            })}
          </span>
          <b className='plaza-detail__modality-flow'>
            {renderModalities(modality.input)}
            <MoveRight size={13} />
            {renderModalities(modality.output)}
          </b>
        </div>
        {heroIn != null || heroOut != null ? (
          <div className='plaza-detail__stat'>
            <span className='plaza-detail__stat-label'>
              {t('page.console.plaza.detail.inOutPrice', {
                defaultValue: '输入 / 输出',
              })}
              {bestOff ? <em className='plaza-off'>−{bestOff}%</em> : null}
            </span>
            <b>
              {[money(heroIn), money(heroOut)].filter(Boolean).join(' / ')}
              <small>
                {t('page.console.plaza.detail.unit.perMtokShort', {
                  defaultValue: ' /M',
                })}
              </small>
            </b>
          </div>
        ) : null}
        <div className='plaza-detail__stat'>
          <span className='plaza-detail__stat-label'>
            {t('page.console.plaza.detail.context', {
              defaultValue: '上下文',
            })}
          </span>
          <b>{context ?? '—'}</b>
        </div>
        {inputLimit ? (
          <div className='plaza-detail__stat'>
            <span className='plaza-detail__stat-label'>
              {t('page.console.plaza.detail.inputLimit', {
                defaultValue: '输入上限',
              })}
            </span>
            <b>{inputLimit}</b>
            <small className='plaza-detail__stat-note'>
              {t('page.console.plaza.detail.inputLimitNote', {
                defaultValue: '其余留给输出',
              })}
            </small>
          </div>
        ) : null}
        <div className='plaza-detail__stat'>
          <span className='plaza-detail__stat-label'>
            {t('page.console.plaza.detail.maxOutput', {
              defaultValue: '最大输出',
            })}
          </span>
          <b>{maxOutput ?? '—'}</b>
        </div>
        {released ? (
          <div className='plaza-detail__stat'>
            <span className='plaza-detail__stat-label'>
              {t('page.console.plaza.detail.releasedDate', {
                defaultValue: '发布日期',
              })}
            </span>
            <b>{released}</b>
          </div>
        ) : null}
        {model.knowledge_cutoff ? (
          <div className='plaza-detail__stat'>
            <span className='plaza-detail__stat-label'>
              {t('page.console.plaza.detail.knowledgeCutoff', {
                defaultValue: '知识截止',
              })}
            </span>
            <b>{model.knowledge_cutoff}</b>
          </div>
        ) : null}
      </div>

      <DetailSection
        icon={Workflow}
        title={t('page.console.plaza.detail.lanes', { defaultValue: '渠道' })}
      >
        <div className='plaza-detail__lanes-head'>
          <LaneHealthLegend className='plaza-detail__legend' />
        </div>
        <div className='plaza-detail__lanes'>
          {lanes.map((lane) => (
            <LaneBlock
              key={lane.key}
              lane={lane}
              model={model}
              variant='detail'
            />
          ))}
        </div>
        <ExtraPriceChips lanes={lanes} model={model} />
        <ContextTierTable lanes={lanes} model={model} />
      </DetailSection>

      {caps.length ? (
        <DetailSection
          icon={Brain}
          title={t('page.console.plaza.capsHeading', { defaultValue: '能力' })}
        >
          <div className='plaza-detail__caps'>
            {caps.map((name) => {
              const Icon = CAPABILITY_ICONS[name];
              return (
                <span key={name} className='plaza-cap'>
                  {Icon ? <Icon size={12} /> : null}
                  {name}
                </span>
              );
            })}
          </div>
        </DetailSection>
      ) : null}

      {efforts.length || canSwitchOffThinking ? (
        <DetailSection
          icon={Gauge}
          title={t('page.console.plaza.detail.thinking', {
            defaultValue: '思考深度',
          })}
        >
          <p className='plaza-detail__hint'>
            {t('page.console.plaza.detail.thinkingHint', {
              defaultValue: 'reasoning.effort 可选值，从低到高',
            })}
          </p>
          <div className='plaza-detail__levels'>
            {canSwitchOffThinking ? (
              <span className='plaza-cap'>
                {t('page.console.plaza.detail.thinkingToggle', {
                  defaultValue: '可关闭',
                })}
              </span>
            ) : null}
            {efforts.map((level) => (
              <code key={level} className='plaza-level'>
                {level}
              </code>
            ))}
          </div>
        </DetailSection>
      ) : null}

      {unsupported.length ? (
        <DetailSection
          icon={Ban}
          title={t('page.console.plaza.detail.unsupported', {
            defaultValue: '不支持的参数',
          })}
        >
          <div className='plaza-detail__levels'>
            {unsupported.map((param) => (
              <code key={param} className='plaza-param-off'>
                {param}
              </code>
            ))}
            <span className='plaza-detail__hint'>
              {unsupported.includes('temperature')
                ? t('page.console.plaza.detail.unsupportedTemperature', {
                    defaultValue: '这个模型固定采样，请求里别带这个参数',
                  })
                : t('page.console.plaza.detail.unsupportedHint', {
                    defaultValue: '请求里别带这些参数',
                  })}
            </span>
          </div>
        </DetailSection>
      ) : null}

      <DetailSection
        icon={Terminal}
        title={t('page.console.plaza.detail.example', {
          defaultValue: '代码示例',
        })}
      >
        <CodeSamples model={model} />
      </DetailSection>

      {model.deprecation_date ? (
        <p className='plaza-detail__meta'>
          {t('page.console.plaza.detail.deprecation', {
            defaultValue: '弃用日期 {{date}}',
            date: model.deprecation_date,
          })}
        </p>
      ) : null}
    </div>
  );
}

/** Every open lane is measurably down. Worth a card-level badge: it is the one
 *  thing a reader scanning the grid must not have to open a card to learn.
 *  Lanes with no evidence at all stay silent — "no data" is not "down". */
const lanesAreDown = (lanes: Lane[]) => {
  const open = lanes.filter((lane) => lane.available !== false);
  return (
    open.length > 0 &&
    open.every(
      (lane) => lane.health != null && healthTone(lane.health) === 'bad',
    )
  );
};

type ModelPlazaMode = 'public' | 'console';

export function ModelCatalogPlaza({
  mode = 'console',
}: {
  mode?: ModelPlazaMode;
}) {
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const [activeProvider, setActiveProvider] = useState('all');
  const [selected, setSelected] = useState<CatalogModel | null>(null);
  const isPublic = mode === 'public';

  const catalog = useQuery(catalogQueryOptions(mode));

  // A background refresh that fails keeps the directory already on screen;
  // only a load with nothing to show becomes an error.
  const catalogFailed = catalog.isError && !catalog.data;

  // A viewer on a price plan sees one line saying so; the per-lane marks
  // carry the figures.
  const viewerOnPlan =
    !isPublic &&
    (catalog.data || []).some((model) =>
      (model.lanes || []).some((lane) => (lane.plan_discount_pct ?? 0) > 0),
    );

  // The catalog arrives in display order — the operator's maker order, then
  // newest release first — so grouping keeps both orders as sent.
  const makerGroups = useMemo(
    () =>
      groupByMaker(
        catalog.data || [],
        (model) =>
          makerOf(model.maker, {
            id: model.id,
            name: model.name,
            owned_by: model.owned_by,
          }),
        (model) => model.maker_rank,
      ).map(({ maker, rows }) => ({
        brand: makerBrand(maker, {
          id: rows[0]?.id,
          name: rows[0]?.name,
          owned_by: rows[0]?.owned_by,
        }),
        label: makerLabel(maker, t),
        maker: maker || 'other',
        models: rows,
      })),
    [catalog.data, t],
  );

  const providerOptions = useMemo(
    () =>
      makerGroups.map(({ brand, label, maker, models }) => ({
        brand,
        count: models.length,
        label,
        maker,
      })),
    [makerGroups],
  );

  useEffect(() => {
    if (
      activeProvider !== 'all' &&
      !providerOptions.some(({ maker }) => maker === activeProvider)
    ) {
      setActiveProvider('all');
    }
  }, [activeProvider, providerOptions]);

  const groups = useMemo(() => {
    const query = search.trim().toLowerCase();
    return makerGroups
      .filter(
        ({ maker }) => activeProvider === 'all' || maker === activeProvider,
      )
      .map((group) => ({
        ...group,
        models: group.models.filter((model) =>
          query
            ? `${model.id} ${model.family || ''} ${modelDisplayName(model.id, model.name)} ${group.label} ${group.brand.companyName}`
                .toLowerCase()
                .includes(query)
            : true,
        ),
      }))
      .filter((group) => group.models.length > 0);
  }, [activeProvider, makerGroups, search]);

  const totalModels = catalog.data?.length || 0;

  const allProvidersLabel = t('page.console.plaza.provider.all', {
    defaultValue: '全部',
  });
  const selectedBrand = selected
    ? makerBrand(selected.maker, {
        id: selected.id,
        name: selected.name,
        owned_by: selected.owned_by,
      })
    : null;

  return (
    <ConsolePage
      className={isPublic ? 'plaza-page plaza-public' : 'plaza-page'}
      badge='MODELS'
      title={t('page.console.plaza.title', { defaultValue: '模型' })}
      description={
        totalModels
          ? t('page.console.plaza.descriptionCounted', {
              count: totalModels,
              defaultValue:
                '{{count}} 个模型 · 计费单位与档位价格见卡片 · 点卡片看详情与调用示例',
            })
          : t('page.console.plaza.description', {
              defaultValue: '点任意卡片查看完整详情与试用。',
            })
      }
      actions={
        <RefreshButton
          refreshing={catalog.isFetching}
          onClick={() => catalog.refetch()}
        >
          {t('page.console.plaza.refresh', { defaultValue: '刷新' })}
        </RefreshButton>
      }
    >
      {viewerOnPlan ? (
        <div className='plaza-discbar'>
          <b>
            {t('page.console.plaza.discount.title', {
              defaultValue: '专属折扣',
            })}
          </b>
          <Link className='plaza-discbar__link' to='/console/personal'>
            {t('page.console.plaza.discount.details', {
              defaultValue: '明细',
            })}
          </Link>
        </div>
      ) : null}

      <div className='plaza-filter'>
        <Input
          aria-label={t('page.console.plaza.search', {
            defaultValue: '搜索模型',
          })}
          placeholder={t('page.console.plaza.search', {
            defaultValue: '搜索模型',
          })}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
      </div>

      {!catalog.isLoading && catalog.data && providerOptions.length ? (
        <nav
          aria-label={t('page.console.plaza.provider.filterLabel', {
            defaultValue: '按厂商筛选模型',
          })}
          className='plaza-provider-nav'
        >
          <button
            aria-label={t('page.console.plaza.provider.optionLabel', {
              company: allProvidersLabel,
              count: catalog.data?.length || 0,
              defaultValue: '{{company}}，{{count}} 个模型',
            })}
            aria-pressed={activeProvider === 'all'}
            className='plaza-provider-chip'
            onClick={() => setActiveProvider('all')}
            type='button'
          >
            <span className='plaza-provider-chip__all' aria-hidden='true'>
              <LayoutGrid size={15} />
            </span>
            <span>{allProvidersLabel}</span>
            <span className='plaza-provider-chip__count' aria-hidden='true'>
              {catalog.data?.length || 0}
            </span>
          </button>
          {providerOptions.map(({ brand, count, label, maker }) => (
            <button
              aria-label={t('page.console.plaza.provider.optionLabel', {
                company: label,
                count,
                defaultValue: '{{company}}，{{count}} 个模型',
              })}
              aria-pressed={activeProvider === maker}
              className='plaza-provider-chip'
              key={maker}
              onClick={() => setActiveProvider(maker)}
              type='button'
            >
              <ProviderLogo brand={brand} decorative size={25} />
              <span>{label}</span>
              <span className='plaza-provider-chip__count' aria-hidden='true'>
                {count}
              </span>
            </button>
          ))}
        </nav>
      ) : null}

      {catalog.isLoading ? <LoadingState /> : null}

      {catalogFailed ? (
        <ErrorState
          action={
            <Button onClick={() => catalog.refetch()} variant='ghost'>
              {t('legacy.i18n.retry.e2d53a6d3a')}
            </Button>
          }
          title={t('page.console.plaza.error.title', {
            defaultValue: '模型目录加载失败',
          })}
          error={catalog.error}
        />
      ) : null}

      {!catalog.isLoading && !catalogFailed && !groups.length ? (
        <EmptyState
          title={t('page.console.plaza.empty.title', {
            defaultValue: '没有可用模型',
          })}
          description={t('page.console.plaza.empty.description', {
            defaultValue: '当前的模型策略没有匹配到任何模型。',
          })}
        />
      ) : null}

      {/* The key to the bars on every card; it names no figure, so it paints
          with the frame instead of waiting for the catalog. */}
      {catalogFailed ? null : <LaneHealthLegend className='plaza-legend' />}

      {/* One continuous grid. Grouping by maker left a row with one card and
          two empty slots for every small maker; the maker now rides on each
          card and in the filter above, and the grid keeps the catalog's own
          order (maker order, then newest release first). */}
      {groups.length ? (
        <div className='plaza-grid'>
          {groups.flatMap(({ brand, label, models }) =>
            models.map((model) => (
              <CatalogCard
                key={model.id}
                model={model}
                isPublic={isPublic}
                onSelect={setSelected}
                maker={{ brand, label }}
              />
            )),
          )}
        </div>
      ) : null}

      <Modal
        className='lazu-modal-content--wide'
        open={Boolean(selected)}
        title={
          selected ? (
            <span className='plaza-detail__title'>
              {selectedBrand ? (
                <ProviderLogo brand={selectedBrand} decorative size={28} />
              ) : null}
              <span className='plaza-detail__title-id'>
                {modelDisplayName(selected.id, selected.name)}
              </span>
              <CopyButton
                className='plaza-copy--title'
                label={t('page.console.plaza.detail.copyId', {
                  defaultValue: '复制模型 ID',
                })}
                value={selected.id}
              />
              {selected.accessible === false ? (
                <Badge variant='warning'>
                  {t('page.console.plaza.unavailable', {
                    defaultValue: '不可用',
                  })}
                </Badge>
              ) : (
                <Badge variant='success'>
                  {t('page.console.plaza.available', { defaultValue: '可用' })}
                </Badge>
              )}
              {selected.is_new_release ? (
                <Badge variant='info'>
                  {t('page.console.plaza.newRelease', {
                    defaultValue: '新发布',
                  })}
                </Badge>
              ) : null}
              {selected.open_source ? (
                <Badge>
                  {t('page.console.plaza.detail.openSource', {
                    defaultValue: '开源',
                  })}
                </Badge>
              ) : null}
            </span>
          ) : null
        }
        description={
          selected && selectedBrand
            ? [selectedBrand.companyName, selected.family]
                .filter(Boolean)
                .filter((value, index, all) => all.indexOf(value) === index)
                .join(' · ')
            : undefined
        }
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
        footer={
          selected ? (
            <Button variant='ghost' onClick={() => setSelected(null)}>
              {t('page.console.plaza.close', { defaultValue: '关闭' })}
            </Button>
          ) : null
        }
        body={
          selected ? <ModelDetail key={selected.id} model={selected} /> : null
        }
      />
    </ConsolePage>
  );
}

export default ModelCatalogPlaza;

const CatalogCard = memo(function CatalogCard({
  model,
  isPublic,
  onSelect,
  maker,
}: {
  model: CatalogModel;
  isPublic: boolean;
  onSelect: (model: CatalogModel) => void;
  maker?: { brand: ReturnType<typeof makerBrand>; label: string };
}) {
  const { t } = useTranslation();
  const context = contextLengthOf(model);
  const lanes = resolveLanes(model);
  const released = releaseDateOf(model);
  const allDown = lanesAreDown(lanes);
  return (
    <article key={model.id} className='plaza-card'>
      <button
        aria-label={model.id}
        className='plaza-card__open'
        onClick={() => onSelect(model)}
        type='button'
      />
      <span className='plaza-card__top'>
        <b>{modelDisplayName(model.id, model.name)}</b>
        <CopyButton
          className='plaza-copy--card'
          iconOnly
          label={t('page.console.plaza.detail.copyId', {
            defaultValue: '复制模型 ID',
          })}
          value={model.id}
        />
        {/* Only exceptions earn a badge. Every card carrying a green
                        "可用" chip made the word carry no information at all —
                        54 of them said the same thing. */}
        {model.accessible === false ||
        !lanes.some((lane) => lane.available !== false) ? (
          <Badge variant='warning'>
            {t('page.console.plaza.unavailable', {
              defaultValue: '不可用',
            })}
          </Badge>
        ) : allDown ? (
          <Badge variant='danger'>
            {t('page.console.plaza.health.unavailable', {
              defaultValue: '当前不可用',
            })}
          </Badge>
        ) : model.is_new_release ? (
          <Badge variant='info'>
            {t('page.console.plaza.newRelease', {
              defaultValue: '新发布',
            })}
          </Badge>
        ) : null}
        {maker ? (
          <span className='plaza-card__maker'>
            <ProviderLogo brand={maker.brand} decorative size={18} />
            {maker.label}
          </span>
        ) : null}
      </span>
      {released || context ? (
        <span className='plaza-card__meta'>
          {[
            released
              ? t('page.console.plaza.releasedOn', {
                  date: released,
                  defaultValue: '{{date}} 发布',
                })
              : null,
            context
              ? t('page.console.plaza.contextWindow', {
                  defaultValue: '{{size}} 上下文',
                  size: formatContextLength(context),
                })
              : null,
          ]
            .filter(Boolean)
            .join(' · ')}
        </span>
      ) : null}
      <span className='plaza-card__lanes'>
        {lanes.map((lane) => (
          <LaneBlock
            key={lane.key}
            lane={lane}
            model={model}
            // The badge above already says every lane is down;
            // each lane repeating it would say it three times.
            quietStatus={allDown}
            variant='card'
          />
        ))}
      </span>
      {!isPublic ? (
        <span className='plaza-card__facts'>
          {isPublic ? null : (
            <Link
              className='plaza-card__usage'
              to={`/console/usage?model=${encodeURIComponent(model.id)}`}
            >
              {t('page.console.plaza.viewUsage', {
                defaultValue: '查看用量 →',
              })}
            </Link>
          )}
        </span>
      ) : null}
    </article>
  );
});
