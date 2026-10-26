import { useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ListOrdered, Plus, Search } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { laneHasAnyPrice } from './formatters';
import LanesDialog from './LanesDialog';
import MakerOrderDialog from './MakerOrderDialog';
import ModelInspector, {
  type InspectorTab,
  type SaveTierRequest,
} from './ModelInspector';
import ModelLedger from './ModelLedger';
import ModelOpsNav from './ModelOpsNav';
import { buildModelViews, type ModelRecord, type ModelView } from './modelView';
import { findSellPriceRow, normalizeLaneSlug } from './sellPrices';
import SyncPreviewModal from './SyncPreviewModal';
import ModelUpdates from './UpdatesPage';
import { Button } from '../../components/ui/button';
import ConsolePage from '../../components/ui/console-page';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu';
import { Input } from '../../components/ui/input';
import { Modal } from '../../components/ui/modal';
import { Pagination } from '../../components/ui/pagination';
import { API } from '../../helpers/api';
import { isRoot, showError, showSuccess } from '../../helpers/utils';
import { useModelInfoReviewsQuery } from '../../hooks/models/useModelInfoQuery';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import {
  useModelOpsMutations,
  useModelOpsPoolsQuery,
  useModelOpsSellPricesQuery,
  type ModelOpsLane,
  type SyncPreviewKind,
} from '../../hooks/models/useModelOpsQuery';
import {
  useModelDetailQuery,
  useModelsQuery,
} from '../../hooks/models/useModelsQuery';
import { unwrap } from '../../lib/api-error';
import type { ApiRecord } from './apiRecord';
import type ModelMetaForm from './ModelMetaForm';
import type { ComponentProps } from 'react';
import './model-ops.css';
import './ledger.css';

const ns = 'page.console.modelOps';
const WINDOW_HOURS = 48;

/**
 * 模型运营 · 模型 — the ledger of what we sell.
 *
 * A row opens one centred dialog with the model's price and sources (定价与
 * 来源) and its facts (资料). Each lane is priced as a percent of the official
 * price. 待处理 is a view of this page: what the daily models.dev sync left
 * for a person.
 */
export default function ModelOpsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const models = useModelsQuery(
    Boolean(searchParams.get('model')),
  ) as unknown as ApiRecord;
  const { t } = models;

  const [syncKind, setSyncKind] = useState<SyncPreviewKind | null>(null);
  const [savingTier, setSavingTier] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<ModelView | null>(null);
  const [makersOpen, setMakersOpen] = useState(false);
  const reviewsQuery = useModelInfoReviewsQuery();
  const pendingView = searchParams.get('view') === 'pending';
  const pendingCount = reviewsQuery.data?.pending_count;
  const pendingRelease = useMemo(
    () =>
      new Set(
        (reviewsQuery.data?.items || [])
          .filter(
            (row) =>
              row.status === 'pending' &&
              row.field === 'release_date' &&
              (row.kind === 'diff' || row.kind === 'first_run'),
          )
          .map((row) => row.model_name),
      ),
    [reviewsQuery.data],
  );
  const inspectorOpenedFromLedger = useRef(false);

  const canEditPrice = isRoot();
  const lanesQuery = useChannelLanesQuery();
  const lanes = useMemo(() => lanesQuery.data ?? [], [lanesQuery.data]);
  const laneSlugs = useMemo(
    () => lanes.filter((lane) => lane.enabled).map((lane) => lane.slug),
    [lanes],
  );
  const laneRatios = useMemo(
    () =>
      Object.fromEntries(
        lanes.map((lane) => [lane.slug, Number(lane.price_ratio) || 100]),
      ),
    [lanes],
  );

  /* The open row lives in the URL so a link to one model is shareable and the
     browser's back button closes the inspector. */
  const selectedId = searchParams.get('model');
  // A shared inspector link may point at a row on another page (or outside the
  // current filter). Resolve that record directly instead of clearing the URL
  // merely because it is not in the ledger slice currently on screen.
  /* `model=new` is 新增模型: the dialog opens on 资料 with no row behind it.
     The dialog's page is in the URL too (`panel=info`). */
  const isNew = selectedId === 'new';
  const panelTab = ((): InspectorTab => {
    const raw = searchParams.get('panel');
    return raw === 'meta' || raw === 'info'
      ? 'info'
      : isNew
        ? 'info'
        : 'pricing';
  })();
  const selectedDetailQuery = useModelDetailQuery(
    isNew ? null : selectedId,
    Boolean(selectedId) && !isNew,
  );
  const setSelectedId = useCallback(
    (next: string | null, replace = false, tab?: InspectorTab) => {
      const params = new URLSearchParams(searchParams);
      if (next) params.set('model', next);
      else params.delete('model');
      // The models.dev prefill belongs to one registration only.
      if (next !== 'new') params.delete('prefill');
      if (!next || !tab || tab === 'pricing') params.delete('panel');
      else params.set('panel', tab);
      setSearchParams(params, {
        replace: replace || Boolean(searchParams.get('model')),
      });
    },
    [searchParams, setSearchParams],
  );
  const setPanelTab = useCallback(
    (tab: InspectorTab) => {
      const params = new URLSearchParams(searchParams);
      if (tab === 'pricing') params.delete('panel');
      else params.set('panel', tab);
      setSearchParams(params, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  /* ?tab=sources / ?tab=adjustments are bookmarked from when this page had
     tabs; send them to the pages that replaced them. */
  const legacyTab = searchParams.get('tab');
  useEffect(() => {
    if (legacyTab === 'sources')
      navigate('/console/model-ops/sources', { replace: true });
    else if (legacyTab === 'adjustments') {
      navigate('/console/price-plans', { replace: true });
    }
  }, [legacyTab, navigate]);

  const modelNames = useMemo(() => {
    const names = new Set(
      ((models.models || []) as ModelRecord[])
        .map((record) => String(record.model_name || ''))
        .filter(Boolean),
    );
    const selectedName = String(selectedDetailQuery.data?.model_name || '');
    if (selectedName) names.add(selectedName);
    return [...names];
  }, [models.models, selectedDetailQuery.data]);

  const poolsQuery = useModelOpsPoolsQuery(modelNames);
  const sellPricesQuery = useModelOpsSellPricesQuery(canEditPrice);
  const ops = useModelOpsMutations(t);

  const views = useMemo(
    () =>
      buildModelViews({
        lanes: laneSlugs,
        pools: poolsQuery.data || {},
        poolsLoading: poolsQuery.isLoading,
        records: (models.models || []) as ModelRecord[],
        sellPrices: sellPricesQuery.data || [],
        spend: {},
        t,
        vendorMap: models.vendorMap,
      }),
    [
      models.models,
      models.vendorMap,
      poolsQuery.data,
      poolsQuery.isLoading,
      sellPricesQuery.data,
      t,
    ],
  );

  const selectedIndex = views.findIndex(
    (view) => String(view.id) === selectedId,
  );
  const selectedDetail = useMemo(() => {
    if (!selectedDetailQuery.data) return null;
    return (
      buildModelViews({
        lanes: laneSlugs,
        pools: poolsQuery.data || {},
        poolsLoading: poolsQuery.isLoading,
        records: [selectedDetailQuery.data as ModelRecord],
        sellPrices: sellPricesQuery.data || [],
        spend: {},
        t,
        vendorMap: models.vendorMap,
      })[0] || null
    );
  }, [
    models.vendorMap,
    poolsQuery.data,
    poolsQuery.isLoading,
    selectedDetailQuery.data,
    sellPricesQuery.data,
    t,
  ]);
  const newView = useMemo<ModelView>(
    () => ({
      blockReason: null,
      cheap: undefined,
      cheapPool: [],
      description: '',
      direct: undefined,
      directPool: [],
      id: 'new',
      lanePools: {},
      lanePrices: {},
      listed: false,
      name: '',
      neitherPriced: true,
      offender: undefined,
      pool: [],
      record: { id: 'new' } as ModelRecord,
      spend: undefined,
      vendorName: '',
    }),
    [],
  );
  const selected = isNew
    ? newView
    : selectedIndex >= 0
      ? views[selectedIndex]
      : selectedDetail;

  /* Only clear a stale link after the detail endpoint has confirmed it no
     longer resolves. Selections outside the current page stay inspectable. */
  useEffect(() => {
    if (
      selectedId &&
      !models.isLoading &&
      selectedIndex < 0 &&
      selectedDetailQuery.isError
    ) {
      setSelectedId(null, true);
    }
  }, [
    models.isLoading,
    selectedDetailQuery.isError,
    selectedId,
    selectedIndex,
    setSelectedId,
  ]);

  const closeInspector = useCallback(() => {
    if (inspectorOpenedFromLedger.current) {
      navigate(-1);
      return;
    }
    setSelectedId(null, true);
  }, [navigate, setSelectedId]);

  const pricingOwners = useMemo(() => {
    const result = new Map<string, string>();
    const records = [...((models.models || []) as ModelRecord[])];
    const selectedRecord = selectedDetailQuery.data as ModelRecord | undefined;
    if (
      selectedRecord &&
      !records.some((record) => record.id === selectedRecord.id)
    ) {
      records.push(selectedRecord);
    }
    for (const record of records) {
      const modelName = String(record.model_name || '');
      for (const rawLane of (record.lanes || []) as Array<ApiRecord>) {
        const lane = normalizeLaneSlug(String(rawLane.channel_group ?? ''));
        const owner = String(rawLane.model_name || '');
        if (modelName && owner) result.set(`${modelName}:${lane}`, owner);
      }
    }
    return result;
  }, [models.models, selectedDetailQuery.data]);

  const currentRow = (modelName: string) => (lane: string) =>
    findSellPriceRow(
      sellPricesQuery.data || [],
      modelName,
      lane as ModelOpsLane,
      pricingOwners.get(`${modelName}:${lane}`),
    );

  const handleSaveTiers = async (modelName: string, tiers: SaveTierRequest) => {
    setSavingTier(true);
    try {
      unwrap(
        await API.put('/api/model_ops/models/tier_config', {
          model: modelName,
          tiers: tiers.map((tier) => ({
            lane: tier.lane,
            price: canEditPrice ? tier.price : undefined,
            weights: tier.weights,
          })),
        }),
      );
      queryClient.invalidateQueries({ queryKey: ['console'] });
      showSuccess(t(`${ns}.models.saved`, { defaultValue: '已保存' }));
    } catch (error) {
      showError(error);
      throw error;
    } finally {
      setSavingTier(false);
    }
  };

  const runSync = async (kind: SyncPreviewKind) => {
    setSyncKind(null);
    if (kind === 'upstream') models.syncMutation.mutate();
    if (kind === 'litellm') models.syncLiteLLMMutation.mutate();
    if (kind === 'openrouter') models.syncOpenRouterMutation.mutate();
  };

  const syncBusy = Boolean(
    models.syncMutation.isPending ||
    models.syncLiteLLMMutation.isPending ||
    models.syncOpenRouterMutation.isPending,
  );

  const selectedIds = useMemo(
    () =>
      new Set<number | string>(
        ((models.selectedModels || []) as ModelRecord[]).map(
          (record) => record.id,
        ),
      ),
    [models.selectedModels],
  );
  const selectionContext = JSON.stringify([
    models.page,
    models.pageSize,
    models.filters,
  ]);
  const selectedPageRef = useRef(selectionContext);
  useEffect(() => {
    if (selectedPageRef.current === selectionContext) return;
    selectedPageRef.current = selectionContext;
    models.setSelectedModels([]);
  }, [selectionContext, models.setSelectedModels]);
  const bulkListBlockReason = useMemo(
    () =>
      ((models.selectedModels || []) as ModelRecord[])
        .map((record) =>
          views.find((view) => String(view.id) === String(record.id)),
        )
        .find((view) => view?.blockReason)?.blockReason || null,
    [models.selectedModels, views],
  );

  const setFilter = (patch: Record<string, string>) => {
    const next = { ...models.draftFilters, ...patch };
    models.setDraftFilters(next);
    models.submitFilters(next);
  };
  const setPendingView = (on: boolean) => {
    const params = new URLSearchParams(searchParams);
    if (on) params.set('view', 'pending');
    else params.delete('view');
    setSearchParams(params);
  };

  /* Page-scoped, like the list it counts. */
  const riskCount = useMemo(
    () => views.filter((view) => !laneHasAnyPrice(view.direct)).length,
    [views],
  );
  const status = models.draftFilters.status || 'offered';
  const tier = models.draftFilters.tier || 'all';

  return (
    <ConsolePage
      appearance='review'
      className='model-ops-page--compact'
      title={t(`${ns}.title`, { defaultValue: '模型运营' })}
      tabs={<ModelOpsNav t={t} />}
      actions={
        <>
          <LanesDialog t={t} />
          <Button
            size='sm'
            variant='secondary'
            onClick={() => setMakersOpen(true)}
          >
            <ListOrdered size={14} />
            {t(`${ns}.makers.open`, { defaultValue: '厂商顺序' })}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger>
              <Button loading={syncBusy} size='sm' variant='secondary'>
                {t(`${ns}.action.sync`, { defaultValue: '同步' })}
                <ChevronDown size={14} />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuItem onSelect={() => setSyncKind('upstream')}>
                {t(`${ns}.action.syncUpstream`, {
                  defaultValue: '同步上游目录',
                })}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setSyncKind('litellm')}>
                {t(`${ns}.action.syncLiteLLM`, {
                  defaultValue: '同步 LiteLLM',
                })}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setSyncKind('openrouter')}>
                {t(`${ns}.action.syncOpenRouter`, {
                  defaultValue: '同步 OpenRouter',
                })}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            size='sm'
            variant='secondary'
            onClick={() => setSelectedId('new', false, 'info')}
          >
            <Plus size={14} />
            {t(`${ns}.action.addModel`, { defaultValue: '新增模型' })}
          </Button>
          <Button
            size='sm'
            variant='primary'
            onClick={() => navigate('/console/model-ops/sources?new=1')}
          >
            <Plus size={14} />
            {t(`${ns}.action.connectSource`, { defaultValue: '新增来源' })}
          </Button>
        </>
      }
    >
      <div className='mo-toolbar'>
        <span className='lazu-input-affix mo-toolbar__search'>
          <span className='lazu-input-affix__prefix'>
            <Search aria-hidden='true' size={14} />
          </span>
          <Input
            aria-label={t(`${ns}.models.searchLabel`, {
              defaultValue: '搜索模型',
            })}
            disabled={pendingView}
            placeholder={t(`${ns}.models.searchPlaceholder`, {
              defaultValue: '搜索模型 / 厂商…',
            })}
            value={models.draftFilters.keyword}
            onChange={(event) =>
              models.setDraftFilters((current: Record<string, string>) => ({
                ...current,
                keyword: event.target.value,
              }))
            }
            onBlur={() => models.submitFilters()}
            onKeyDown={(event) => {
              if (event.key === 'Enter') models.submitFilters();
            }}
          />
        </span>
        <div
          aria-label={t(`${ns}.models.view`, { defaultValue: '视图' })}
          className='mo-chips'
          role='group'
        >
          {(
            [
              [
                'offered',
                t(`${ns}.models.chipOffered`, { defaultValue: '在售' }),
                !pendingView && status === 'offered' && tier === 'all',
                () => {
                  setPendingView(false);
                  setFilter({ status: 'offered', tier: 'all' });
                },
                null,
              ],
              [
                'all',
                t(`${ns}.models.chipAll`, { defaultValue: '全部' }),
                !pendingView && status === 'all' && tier === 'all',
                () => {
                  setPendingView(false);
                  setFilter({ status: 'all', tier: 'all' });
                },
                null,
              ],
              ...(riskCount > 0 || tier === 'atRisk'
                ? [
                    [
                      'risk',
                      t(`${ns}.models.chipRisk`, { defaultValue: '未定价' }),
                      !pendingView && tier === 'atRisk',
                      () => {
                        setPendingView(false);
                        setFilter({ status: 'all', tier: 'atRisk' });
                      },
                      tier === 'atRisk' ? null : riskCount,
                    ] as const,
                  ]
                : []),
              [
                'pending',
                t(`${ns}.models.chipPending`, { defaultValue: '待处理' }),
                pendingView,
                () => setPendingView(true),
                pendingCount ? pendingCount : null,
              ],
            ] as Array<
              readonly [string, string, boolean, () => void, number | null]
            >
          ).map(([key, label, active, onClick, count]) => (
            <Button
              aria-pressed={active}
              className={`mo-chip${active ? ' is-on' : ''}`}
              key={key}
              size='sm'
              variant='secondary'
              onClick={onClick}
            >
              {label}
              {count ? <span className='mo-chip__n'>{count}</span> : null}
            </Button>
          ))}
        </div>
      </div>

      {pendingView ? (
        <ModelUpdates />
      ) : (
        <>
          {models.selectedModels?.length ? (
            <div className='model-ops__selection-actions'>
              <span className='mo-sub'>
                {t(`${ns}.models.selected`, {
                  count: models.selectedModels.length,
                  defaultValue: '已选 {{count}} 个模型',
                })}
              </span>
              <Button
                disabled={Boolean(bulkListBlockReason)}
                size='sm'
                title={bulkListBlockReason || undefined}
                variant='secondary'
                onClick={() =>
                  ops.batchStatusMutation.mutate({
                    ids: models.selectedModels.map((m: ModelRecord) => m.id),
                    status: 1,
                  })
                }
              >
                {t(`${ns}.models.bulkList`, { defaultValue: '批量上架' })}
              </Button>
              <Button
                size='sm'
                variant='secondary'
                onClick={() =>
                  ops.batchStatusMutation.mutate({
                    ids: models.selectedModels.map((m: ModelRecord) => m.id),
                    status: 0,
                  })
                }
              >
                {t(`${ns}.models.bulkUnlist`, { defaultValue: '批量下架' })}
              </Button>
              <Button
                size='sm'
                variant='ghost'
                onClick={() => models.setSelectedModels([])}
              >
                {t('common.cancel', { defaultValue: '取消' })}
              </Button>
            </div>
          ) : null}

          {models.isLoading ? (
            <div className='mo-ledger'>
              <div className='mo-ledger__empty'>
                <span className='mo-state'>
                  <span aria-hidden='true' className='mo-state__spinner' />
                  {t(`${ns}.models.loading`, {
                    defaultValue: '正在读取模型目录',
                  })}
                </span>
              </div>
            </div>
          ) : views.length === 0 ? (
            <div className='mo-ledger'>
              <div className='mo-ledger__empty'>
                <div className='lazu-state-title'>
                  {t(`${ns}.models.emptyTitle`, {
                    defaultValue: '没有符合条件的模型',
                  })}
                </div>
              </div>
            </div>
          ) : (
            <ModelLedger
              laneRatios={laneRatios}
              pendingRelease={pendingRelease}
              poolsLoading={poolsQuery.isLoading}
              selectedId={selectedId}
              selectedIds={selectedIds}
              t={t}
              views={views}
              onSelect={(id) => {
                inspectorOpenedFromLedger.current = true;
                setSelectedId(id);
              }}
              onToggleListed={(view, listed) =>
                ops.batchStatusMutation.mutate({
                  ids: [view.id],
                  status: listed ? 1 : 0,
                })
              }
              onToggleSelection={(view, checked) =>
                models.setSelectedModels((current: ModelRecord[]) =>
                  checked
                    ? current.some((item) => item.id === view.id)
                      ? current
                      : [...current, view.record]
                    : current.filter((item) => item.id !== view.id),
                )
              }
            />
          )}

          {!models.isLoading && models.total > models.pageSize ? (
            <Pagination
              currentPage={models.page}
              pageSize={models.pageSize}
              showSizeChanger
              showTotal
              total={models.total}
              onPageChange={models.setPage}
              onPageSizeChange={models.setPageSize}
            />
          ) : null}
        </>
      )}

      {/* The dialog seeds each lane from its current price, so it waits for
          the prices: opened early, a priced lane would read as 未开. */}
      {selected && (!canEditPrice || sellPricesQuery.isSuccess) ? (
        <ModelInspector
          canEditPrice={canEditPrice}
          currentRow={currentRow(selected.name)}
          hasNext={
            !isNew && selectedIndex >= 0 && selectedIndex < views.length - 1
          }
          hasPrev={!isNew && selectedIndex > 0}
          isNew={isNew}
          isSavingTier={
            savingTier || (canEditPrice && !sellPricesQuery.isSuccess)
          }
          metaForm={{
            createVendor: async (name: string) =>
              ops.createVendorMutation.mutateAsync(name),
            creatingVendor: ops.createVendorMutation.isPending,
            models: models as unknown as ComponentProps<
              typeof ModelMetaForm
            >['models'],
          }}
          prefillModel={searchParams.get('prefill') || undefined}
          t={t}
          tab={panelTab}
          view={selected}
          windowHours={WINDOW_HOURS}
          onClose={closeInspector}
          onDelete={(view) => setPendingDelete(view)}
          onMetaSaved={() => {
            queryClient.invalidateQueries({ queryKey: ['console'] });
            if (isNew) closeInspector();
          }}
          onNext={() => {
            if (selectedIndex >= 0)
              setSelectedId(
                String(views[selectedIndex + 1].id),
                true,
                panelTab,
              );
          }}
          onPrev={() => {
            if (selectedIndex > 0)
              setSelectedId(
                String(views[selectedIndex - 1].id),
                true,
                panelTab,
              );
          }}
          onSaveTiers={handleSaveTiers}
          onTabChange={setPanelTab}
        />
      ) : null}

      <Modal
        open={Boolean(pendingDelete)}
        title={t(`${ns}.models.deleteTitle`, { defaultValue: '删除模型' })}
        description={t(`${ns}.models.deleteDescription`, {
          defaultValue:
            '删除后该模型会从工作台目录消失，已发布的定价版本会保留在审计里。',
        })}
        body={<p className='mo-name'>{pendingDelete?.name || ''}</p>}
        footer={
          <>
            <Button
              disabled={models.manageModelMutation.isPending}
              variant='secondary'
              onClick={() => setPendingDelete(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              variant='danger'
              loading={models.manageModelMutation.isPending}
              onClick={async () => {
                try {
                  if (pendingDelete) {
                    await models.manageModelMutation.mutateAsync({
                      action: 'delete',
                      id: pendingDelete.id,
                    });
                    if (String(pendingDelete.id) === selectedId) {
                      setSelectedId(null);
                    }
                  }
                  setPendingDelete(null);
                } catch {
                  /* Mutation reports the error; preserve confirmation for retry. */
                }
              }}
            >
              {t('page.console.tables.delete')}
            </Button>
          </>
        }
        onOpenChange={(open: boolean) => {
          if (!open && !models.manageModelMutation.isPending)
            setPendingDelete(null);
        }}
      />

      <MakerOrderDialog open={makersOpen} t={t} onOpenChange={setMakersOpen} />
      <SyncPreviewModal
        busy={syncBusy}
        kind={syncKind}
        t={t}
        onConfirm={(kind) => void runSync(kind)}
        onOpenChange={(open: boolean) => {
          if (!open) setSyncKind(null);
        }}
      />
    </ConsolePage>
  );
}
