import {
  AudioLines,
  Binary,
  Braces,
  Brain,
  DatabaseZap,
  Eye,
  FileText,
  Image as ImageIcon,
  ListOrdered,
  Mic,
  MoveRight,
  Type,
  Video,
  Volume2,
  Workflow,
  Wrench,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  contextLengthOf,
  descriptionOf,
  formatTokenWindow,
  modalitiesOf,
  releaseDateOf,
  type CatalogModel,
} from './catalog';
import type { ComponentType, ReactNode } from 'react';
import './model-plaza.css';

export const money = (value?: number) =>
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

export function DetailSection({
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

/**
 * What a model's detail says about the model itself: its description, the
 * figures (modalities, price, context, output, dates) and its capabilities.
 * The 模型 page wraps it with lanes and samples; the operator's 编辑资料
 * shows it as the preview of what customers will read.
 */
export function ModelFacts({
  children,
  model,
  price,
}: {
  /** Rendered between the figures and the capabilities (the 线路 section). */
  children?: ReactNode;
  model: CatalogModel;
  /** The lowest open input / output price, and the steepest lane discount. */
  price?: { input?: number; off?: number; output?: number };
}) {
  const { t, i18n } = useTranslation();
  const description = descriptionOf(model, i18n.language || 'zh');
  const modality = modalitiesOf(model);
  const context = formatTokenWindow(contextLengthOf(model));
  const maxOutput = formatTokenWindow(model.max_output_tokens);
  const inputLimit = formatTokenWindow(model.input_limit);
  const caps = capabilityEntries(model.capabilities);
  const released = releaseDateOf(model) || null;

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
    <>
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
        {price && (price.input != null || price.output != null) ? (
          <div className='plaza-detail__stat'>
            <span className='plaza-detail__stat-label'>
              {t('page.console.plaza.detail.inOutPrice', {
                defaultValue: '输入 / 输出',
              })}
              {price.off ? <em className='plaza-off'>−{price.off}%</em> : null}
            </span>
            <b>
              {[money(price.input), money(price.output)]
                .filter(Boolean)
                .join(' / ')}
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

      {children}

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
    </>
  );
}
