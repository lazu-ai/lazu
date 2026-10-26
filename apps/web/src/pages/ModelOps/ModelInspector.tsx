import { ChevronLeft, ChevronRight, TriangleAlert, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { formatPerMtok } from './formatters';
import LanePricing from './LanePricing';
import ModelInfoPanel from './ModelInfoPanel';
import ModelMetaForm from './ModelMetaForm';
import { GoLiveDialog } from './observation';
import {
  derivedPrice,
  draftFromRow,
  effectiveRatio,
  lanePayload,
  lanePriceChanged,
  officialOf,
  validateDraft,
  type LaneDraft,
} from './ratioPricing';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { IconButton } from '../../components/ui/icon-button';
import { Modal, ModalFrame, ModalTitle } from '../../components/ui/modal';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import {
  useModelOpsMutations,
  type ModelOpsLane,
  type ModelOpsPoolEntry,
} from '../../hooks/models/useModelOpsQuery';
import type { ModelView } from './modelView';
import type { SellPriceRow } from './sellPrices';
import type { TFunction } from 'i18next';
import type { ComponentProps } from 'react';

const ns = 'page.console.modelOps';

/** Two pages: 定价与来源 and 资料. `meta` and `info` are both 资料 — the
 *  links that named the old pages keep working. */
export type InspectorTab = 'info' | 'meta' | 'pricing';

export type SaveTierRequest = Array<{
  lane: string;
  price?: Record<string, unknown>;
  weights: Array<{ channel_id: string; weight: number }>;
}>;

type ModelInspectorProps = {
  canEditPrice: boolean;
  /** The active sell-price row a lane carries now. */
  currentRow: (lane: string) => SellPriceRow | undefined;
  hasNext: boolean;
  hasPrev: boolean;
  /** `true` for 新增模型: no row behind the dialog, only the 资料 page. */
  isNew?: boolean;
  isSavingTier: boolean;
  metaForm: Pick<
    ComponentProps<typeof ModelMetaForm>,
    'createVendor' | 'creatingVendor' | 'models'
  >;
  onClose: () => void;
  onDelete: (view: ModelView) => void;
  onMetaSaved: () => void;
  onNext: () => void;
  onPrev: () => void;
  onSaveTiers: (modelName: string, tiers: SaveTierRequest) => Promise<void>;
  onTabChange: (tab: InspectorTab) => void;
  /** A model id to look up on models.dev when registering a new model. */
  prefillModel?: string;
  t: TFunction;
  tab: InspectorTab;
  view: ModelView;
  windowHours: number;
};

const shortTokens = (value: unknown) => {
  const number = Number(value) || 0;
  if (number <= 0) return '';
  if (number >= 1_000_000) return `${Math.round(number / 100_000) / 10}M`;
  return `${Math.round(number / 1000)}K`;
};

/** One model, centred over the ledger it was opened from. */
export default function ModelInspector({
  canEditPrice,
  currentRow,
  hasNext,
  hasPrev,
  isNew = false,
  isSavingTier,
  metaForm,
  onClose,
  onDelete,
  onMetaSaved,
  onNext,
  onPrev,
  onSaveTiers,
  onTabChange,
  prefillModel,
  t,
  tab,
  view,
  windowHours,
}: ModelInspectorProps) {
  const page = isNew || tab !== 'pricing' ? 'info' : 'pricing';
  const lanesQuery = useChannelLanesQuery();
  const lanes = useMemo(() => lanesQuery.data ?? [], [lanesQuery.data]);
  const laneSlugs = useMemo(() => {
    const enabled = lanes.filter((lane) => lane.enabled).map((l) => l.slug);
    // 稳定 leads and is always present: the router falls back to it.
    return ['direct', ...enabled.filter((slug) => slug !== 'direct')];
  }, [lanes]);
  const laneName = (slug: string) => {
    const lane = lanes.find((item) => item.slug === slug);
    const name = lane?.name || slug;
    return t(`${ns}.price.laneName`, {
      defaultValue: '{{name}}线路',
      name,
    });
  };
  const laneRatio = (slug: string) =>
    Number(lanes.find((item) => item.slug === slug)?.price_ratio) || 100;

  const record = view.record as Record<string, unknown>;
  const official = useMemo(() => officialOf(record), [record]);
  const ops = useModelOpsMutations(t);
  const infoSaveRef = useRef<(() => Promise<boolean>) | null>(null);

  const [drafts, setDrafts] = useState<Record<string, LaneDraft>>({});
  const [initial, setInitial] = useState<Record<string, LaneDraft>>({});
  const [problem, setProblem] = useState('');
  const [preview, setPreview] = useState<{
    changes: Array<{ after: string; before: string; label: string }>;
    tiers: SaveTierRequest;
  } | null>(null);
  /* The confirmation keeps its rows while it animates closed. */
  const shownPreview = useRef(preview);
  if (preview) shownPreview.current = preview;
  const [goLive, setGoLive] = useState<{
    entry: ModelOpsPoolEntry;
    lane: string;
  } | null>(null);
  const [promote, setPromote] = useState<{
    entry: ModelOpsPoolEntry;
    lane: string;
  } | null>(null);

  const seed = (slugs: string[]) =>
    Object.fromEntries(
      slugs.map((slug) => {
        const weights: Record<string, string> = {};
        (view.lanePools[slug] ?? []).forEach((entry) => {
          weights[entry.channel_id] = String(entry.weight);
        });
        return [slug, draftFromRow(currentRow(slug), weights, official)];
      }),
    );

  /* Drafts belong to the open model: moving to the next one must never carry
     an unsaved price across. */
  useEffect(() => {
    const next = seed(laneSlugs);
    setDrafts(next);
    setInitial(structuredClone(next));
    setProblem('');
    setPreview(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view.id]);

  // The lane registry can arrive after the dialog opens.
  useEffect(() => {
    const missing = laneSlugs.filter((slug) => !drafts[slug]);
    if (!missing.length) return;
    const additions = seed(missing);
    setDrafts((current) => ({ ...current, ...additions }));
    setInitial((current) => ({ ...current, ...structuredClone(additions) }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [drafts, laneSlugs]);

  const patch = (lane: string, next: Partial<LaneDraft>) =>
    setDrafts((current) => ({
      ...current,
      [lane]: { ...current[lane], ...next },
    }));

  const describe = (draft: LaneDraft, lane: string) => {
    if (!draft.exists)
      return t(`${ns}.price.laneOff`, { defaultValue: '未开' });
    if (draft.mode === 'follow' && official) {
      return t(`${ns}.price.ratioText`, {
        defaultValue: '官方价的 {{ratio}}%',
        ratio: effectiveRatio(draft, laneRatio(lane)),
      });
    }
    return t(`${ns}.price.fixed`, { defaultValue: '固定价' });
  };

  const buildPreview = () => {
    setProblem('');
    const tiers: SaveTierRequest = [];
    const changes: Array<{ after: string; before: string; label: string }> = [];
    for (const lane of laneSlugs) {
      const draft = drafts[lane];
      const before = initial[lane];
      if (!draft || !before) continue;
      const issue = validateDraft(draft);
      if (issue) {
        setProblem(
          issue === 'ratio'
            ? t(`${ns}.price.badRatio`, {
                defaultValue: '比例要在 0 到 1000 之间。',
              })
            : issue === 'weight'
              ? t(`${ns}.models.invalidWeight`, {
                  defaultValue: '来源权重必须是大于或等于 0 的整数。',
                })
              : t(`${ns}.price.badPrice`, {
                  defaultValue: '价格必须是非负数字。',
                }),
        );
        return;
      }
      const name = laneName(lane);
      const priceChanged = canEditPrice && lanePriceChanged(before, draft);
      if (priceChanged) {
        const was = describe(before, lane);
        const now = describe(draft, lane);
        if (was !== now) changes.push({ after: now, before: was, label: name });
        const a = derivedPrice(
          before,
          official,
          laneRatio(lane),
          currentRow(lane),
        );
        const b = derivedPrice(
          draft,
          official,
          laneRatio(lane),
          currentRow(lane),
        );
        (['input', 'output', 'cache_read', 'cache_write_5m'] as const).forEach(
          (key) => {
            if (!before.exists || a[key] !== b[key]) {
              changes.push({
                after: formatPerMtok(b[key]),
                before: before.exists ? formatPerMtok(a[key]) : '—',
                label: `${name} · ${
                  {
                    cache_read: t('pricing.cacheRead', {
                      defaultValue: '缓存读取',
                    }),
                    cache_write_5m: t(`${ns}.price.cacheWrite`, {
                      defaultValue: '缓存写入',
                    }),
                    input: t('pricing.input', { defaultValue: '输入' }),
                    output: t('pricing.output', { defaultValue: '输出' }),
                  }[key]
                }`,
              });
            }
          },
        );
        if (draft.mode === 'fixed' && draft.billingMode !== 'per_token') {
          const unit =
            draft.billingMode === 'per_call'
              ? [before.perCall, draft.perCall]
              : [before.perSecond, draft.perSecond];
          if (unit[0] !== unit[1]) {
            changes.push({
              after: unit[1] || '—',
              before: unit[0] || '—',
              label: name,
            });
          }
        }
      }
      const weights = (view.lanePools[lane] ?? [])
        .filter(
          (entry) =>
            (draft.weights[entry.channel_id] ?? String(entry.weight)) !==
            String(entry.weight),
        )
        .map((entry) => {
          changes.push({
            after: draft.weights[entry.channel_id],
            before: String(entry.weight),
            label: `${name} · ${entry.name} ${t(`${ns}.tier.weight`, { defaultValue: '权重' })}`,
          });
          return {
            channel_id: entry.channel_id,
            weight: Number(draft.weights[entry.channel_id]),
          };
        });
      if (priceChanged || weights.length) {
        tiers.push({
          lane,
          price: priceChanged
            ? lanePayload(draft, currentRow(lane), lane, view.name)
            : undefined,
          weights,
        });
      }
    }
    if (!tiers.length) {
      setProblem(t('pricing.noChanges', { defaultValue: '尚未修改任何设置' }));
      return;
    }
    setPreview({ changes, tiers });
  };

  const facts = [
    record.release_date
      ? t(`${ns}.inspector.released`, {
          date: String(record.release_date),
          defaultValue: '{{date}} 发布',
        })
      : '',
    shortTokens(record.context_length)
      ? t(`${ns}.inspector.context`, {
          defaultValue: '上下文 {{value}}',
          value: shortTokens(record.context_length),
        })
      : '',
    shortTokens(record.max_output_tokens)
      ? t(`${ns}.inspector.maxOutput`, {
          defaultValue: '最大输出 {{value}}',
          value: shortTokens(record.max_output_tokens),
        })
      : '',
  ].filter(Boolean);

  return (
    <ModalFrame
      busy={isSavingTier}
      className='mo-dialog lazu-modal-content--xwide'
      hideClose
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <div className='mo-dialog__head'>
        <div className='mo-dialog__title-block'>
          <ModalTitle className='mo-dialog__title'>
            {isNew ? (
              t(`${ns}.action.addModel`, { defaultValue: '新增模型' })
            ) : (
              <>
                <span className='mo-name'>{view.name}</span>
                {view.vendorName ? (
                  <Badge variant='neutral'>{view.vendorName}</Badge>
                ) : null}
                <Badge variant={view.listed ? 'success' : 'neutral'}>
                  {view.listed
                    ? t(`${ns}.models.listed`, { defaultValue: '已上架' })
                    : t(`${ns}.models.unlisted`, { defaultValue: '未上架' })}
                </Badge>
              </>
            )}
          </ModalTitle>
          {facts.length ? (
            <div className='mo-sub'>{facts.join(' · ')}</div>
          ) : null}
        </div>
        <div className='mo-dialog__tools'>
          {!isNew ? (
            <>
              <IconButton
                aria-label={t(`${ns}.inspector.prev`, {
                  defaultValue: '上一个',
                })}
                disabled={!hasPrev}
                size='sm'
                variant='ghost'
                onClick={onPrev}
              >
                <ChevronLeft size={16} />
              </IconButton>
              <IconButton
                aria-label={t(`${ns}.inspector.next`, {
                  defaultValue: '下一个',
                })}
                disabled={!hasNext}
                size='sm'
                variant='ghost'
                onClick={onNext}
              >
                <ChevronRight size={16} />
              </IconButton>
            </>
          ) : null}
          <IconButton
            aria-label={t('common.close', { defaultValue: '关闭' })}
            size='sm'
            variant='ghost'
            onClick={onClose}
          >
            <X size={16} />
          </IconButton>
        </div>
      </div>

      <Tabs
        className='mo-dialog__tabs'
        value={page}
        onValueChange={(value) => onTabChange(value as InspectorTab)}
      >
        <TabsList variant='line'>
          <TabsTrigger disabled={isNew} value='pricing'>
            {t(`${ns}.inspector.pricingTab`, { defaultValue: '定价与来源' })}
          </TabsTrigger>
          <TabsTrigger value='info'>
            {t(`${ns}.inspector.infoTab`, { defaultValue: '资料' })}
          </TabsTrigger>
        </TabsList>
      </Tabs>

      {page === 'info' ? (
        <ModelMetaForm
          beforeSave={async () =>
            infoSaveRef.current ? infoSaveRef.current() : true
          }
          createVendor={metaForm.createVendor}
          creatingVendor={metaForm.creatingVendor}
          editingModel={isNew ? null : view.record}
          key={isNew ? 'new' : String(view.id)}
          lead={
            isNew ? null : (
              <ModelInfoPanel
                key={String(view.id)}
                saveRef={infoSaveRef}
                t={t}
                view={view}
              />
            )
          }
          models={metaForm.models}
          onCancel={onClose}
          onDelete={isNew ? undefined : () => onDelete(view)}
          onSaved={onMetaSaved}
          prefillModel={isNew ? prefillModel : undefined}
        />
      ) : (
        <>
          <div className='mo-dialog__body'>
            {view.blockReason ? (
              <div
                className={`mo-notice mo-notice--${view.neitherPriced ? 'bad' : 'warn'}`}
              >
                <TriangleAlert aria-hidden='true' size={14} />
                <span>{view.blockReason}</span>
              </div>
            ) : null}
            <LanePricing
              canEditPrice={canEditPrice}
              currentRow={currentRow}
              drafts={drafts}
              laneName={laneName}
              laneRatio={laneRatio}
              lanes={laneSlugs}
              official={official}
              pools={view.lanePools}
              t={t}
              windowHours={windowHours}
              onGoLive={(lane, entry) => setGoLive({ entry, lane })}
              onPatch={patch}
              onPromote={(lane, entry) => setPromote({ entry, lane })}
            />
            {problem ? (
              <div className='mo-notice mo-notice--bad' role='alert'>
                <TriangleAlert aria-hidden='true' size={14} />
                <span>{problem}</span>
              </div>
            ) : null}
          </div>
          <div className='mo-dialog__foot'>
            <span />
            <div className='mo-dialog__foot-actions'>
              <Button size='sm' variant='secondary' onClick={onClose}>
                {t('common.cancel', { defaultValue: '取消' })}
              </Button>
              <Button
                loading={isSavingTier}
                size='sm'
                variant='primary'
                onClick={buildPreview}
              >
                {t('common.save', { defaultValue: '保存' })}
              </Button>
            </div>
          </div>
        </>
      )}

      <Modal
        busy={isSavingTier}
        open={preview !== null}
        title={t(`${ns}.price.confirmTitle`, { defaultValue: '确认改动' })}
        body={
          <div className='lazu-table-wrap'>
            <p className='mo-name'>{view.name}</p>
            <table className='lazu-table'>
              <thead>
                <tr>
                  <th />
                  <th>{t('pricing.before', { defaultValue: '当前' })}</th>
                  <th>{t('pricing.after', { defaultValue: '修改后' })}</th>
                </tr>
              </thead>
              <tbody>
                {shownPreview.current?.changes.map((change) => (
                  <tr key={change.label}>
                    <td>{change.label}</td>
                    <td>{change.before}</td>
                    <td>{change.after}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        }
        footer={
          <>
            <Button
              disabled={isSavingTier}
              variant='secondary'
              onClick={() => setPreview(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={isSavingTier}
              onClick={async () => {
                if (!preview) return;
                try {
                  await onSaveTiers(view.name, preview.tiers);
                  setInitial(structuredClone(drafts));
                  setPreview(null);
                } catch {
                  /* The page reports the API error; keep the diff for a retry. */
                }
              }}
            >
              {t('pricing.confirmSave', { defaultValue: '确认保存' })}
            </Button>
          </>
        }
        onOpenChange={(open: boolean) => {
          if (!open) setPreview(null);
        }}
      />

      <Modal
        busy={ops.promoteRouteMutation.isPending}
        className='mo-golive-dialog'
        closeLabel={t('common.close', { defaultValue: '关闭' })}
        open={Boolean(promote)}
        title={t(`${ns}.tier.promoteTitle`, {
          defaultValue: '让 {{name}} 升为主用',
          name: promote?.entry.name || '',
        })}
        description={t(`${ns}.tier.promoteBody`, {
          defaultValue:
            '在 {{model}} 的{{lane}}上，它将和其他主用一起按权重分担流量。',
          lane: promote ? laneName(promote.lane) : '',
          model: view.name,
        })}
        footer={
          <>
            <Button
              disabled={ops.promoteRouteMutation.isPending}
              variant='secondary'
              onClick={() => setPromote(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={ops.promoteRouteMutation.isPending}
              variant='primary'
              onClick={async () => {
                if (!promote) return;
                try {
                  await ops.promoteRouteMutation.mutateAsync({
                    entry: promote.entry,
                    lane: promote.lane as ModelOpsLane,
                    model: view.name,
                  });
                  setPromote(null);
                } catch {
                  /* Reported by the mutation; the dialog stays for a retry. */
                }
              }}
            >
              {t(`${ns}.tier.promoteConfirm`, { defaultValue: '确认升为主用' })}
            </Button>
          </>
        }
        onOpenChange={(open: boolean) => {
          if (!open && !ops.promoteRouteMutation.isPending) setPromote(null);
        }}
      />

      <GoLiveDialog
        busy={ops.goLiveRouteMutation.isPending}
        laneName={goLive ? laneName(goLive.lane) : ''}
        modelName={view.name}
        name={goLive?.entry.name || ''}
        observation={goLive?.entry.observation ?? null}
        open={Boolean(goLive?.entry.observation)}
        t={t}
        onConfirm={async () => {
          if (!goLive) return;
          try {
            await ops.goLiveRouteMutation.mutateAsync({
              entry: goLive.entry,
              lane: goLive.lane as ModelOpsLane,
              model: view.name,
            });
            setGoLive(null);
          } catch {
            /* Reported by the mutation; the dialog stays for a retry. */
          }
        }}
        onOpenChange={(open) => {
          if (!open && !ops.goLiveRouteMutation.isPending) setGoLive(null);
        }}
      />
    </ModalFrame>
  );
}
