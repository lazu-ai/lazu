import { ChevronLeft, ChevronRight, Plus, Search, X, Zap } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { healthTone } from './formatters';
import LanesDialog from './LanesDialog';
import ModelOpsNav from './ModelOpsNav';
import { ObservationDialog } from './ObservationDialog';
import SourceForm, { type SourceStep } from './SourceForm';
import SourceLedger from './SourceLedger';
import SourceTypeBadge from './SourceTypeBadge';
import {
  Badge,
  Button,
  ConsolePage,
  IconButton,
  Input,
  Modal,
  ModalFrame,
  ModalTitle,
  Tabs,
  TabsList,
  TabsTrigger,
} from '../../components/ui';
import { useChannelsQuery } from '../../hooks/channels/useChannelsQuery';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import {
  useModelOpsMutations,
  useModelOpsSourcesQuery,
  type ModelOpsSource,
} from '../../hooks/models/useModelOpsQuery';
import type { ComponentProps } from 'react';
import './model-ops.css';
import './ledger.css';

const ns = 'page.console.modelOps';
const WINDOW_HOURS = 48;

/**
 * 模型运营 · 来源 — the supply side. A source is one upstream credential plus
 * the models it can serve; connecting one is how models enter the catalogue.
 * A row opens one centred dialog: 凭据 / 模型 / 线路. Adding a source walks
 * the same three as steps.
 */
export default function SourcesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const channels = useChannelsQuery({ listEnabled: false });
  const { t } = channels;
  const modelFilter = searchParams.get('model')?.trim() || '';
  const sourcesQuery = useModelOpsSourcesQuery(modelFilter, WINDOW_HOURS);
  const ops = useModelOpsMutations(t);

  const [keyword, setKeyword] = useState('');
  /* One row of chips: all, a lane, watched, or failing. */
  const [view, setView] = useState('all');
  /* Lanes are user-managed now, so every list of them comes from the registry:
     a hard-coded pair would hide whatever an operator added. */
  const lanesQuery = useChannelLanesQuery();
  const lanes = lanesQuery.data ?? [];
  const [step, setStep] = useState<SourceStep>('credential');
  const [pendingDelete, setPendingDelete] = useState<ModelOpsSource | null>(
    null,
  );
  const [observed, setObserved] = useState<ModelOpsSource | null>(null);

  const creating = searchParams.get('new') === '1';
  const selectedId = searchParams.get('source');

  const setParams = useCallback(
    (patch: Record<string, string | null>) => {
      const params = new URLSearchParams(searchParams);
      for (const [key, value] of Object.entries(patch)) {
        if (value === null) params.delete(key);
        else params.set(key, value);
      }
      setSearchParams(params, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const sources = sourcesQuery.data?.items || [];
  const rules = sourcesQuery.data?.rules || null;
  const windowHours = rules?.window_hours ?? WINDOW_HOURS;

  const rows = useMemo(() => {
    const needle = keyword.trim().toLowerCase();
    return sources.filter((source) => {
      if (needle) {
        const haystack =
          `${source.name} ${source.endpoint} ${source.type_name} ${(source.model_names || []).join(' ')}`.toLowerCase();
        if (!haystack.includes(needle)) return false;
      }
      if (view === 'observing') return Boolean(source.observation);
      if (view === 'bad') {
        return (
          source.status !== 2 &&
          healthTone(source.health, source.status) === 'bad'
        );
      }
      if (view.startsWith('lane:')) return source.lane === view.slice(5);
      return true;
    });
  }, [keyword, sources, view]);

  const selectedIndex = rows.findIndex(
    (source) => String(source.id) === selectedId,
  );
  const selected =
    sources.find((source) => String(source.id) === selectedId) ?? null;

  /* Opening a row lands on 凭据 rather than wherever the previous panel was
     left: the tab is a property of the panel, not of the session. */
  useEffect(() => {
    setStep('credential');
  }, [creating, selectedId]);

  const tally = useMemo(
    () => ({
      bad: sources.filter(
        (source) =>
          source.status !== 2 &&
          healthTone(source.health, source.status) === 'bad',
      ).length,
      observing: sources.filter((source) => source.observation).length,
    }),
    [sources],
  );

  const closePanel = () => setParams({ new: null, source: null });
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
            loading={ops.testAllSourcesMutation.isPending}
            size='sm'
            variant='secondary'
            onClick={() => ops.testAllSourcesMutation.mutate()}
          >
            <Zap size={14} />
            {t(`${ns}.sources.testAll`, { defaultValue: '测试全部' })}
          </Button>
          <Button
            size='sm'
            variant='primary'
            onClick={() => setParams({ new: '1', source: null })}
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
            aria-label={t(`${ns}.sources.searchLabel`, {
              defaultValue: '搜索来源',
            })}
            placeholder={t(`${ns}.sources.searchPlaceholder`, {
              defaultValue: '搜索来源或模型…',
            })}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
          />
        </span>
        <div
          aria-label={t(`${ns}.sources.view`, { defaultValue: '视图' })}
          className='mo-chips'
          role='group'
        >
          {(
            [
              ['all', t(`${ns}.sources.all`, { defaultValue: '全部' }), null],
              ...lanes.map(
                (lane) =>
                  [
                    `lane:${lane.slug}`,
                    t(`${ns}.price.laneName`, {
                      defaultValue: '{{name}}线路',
                      name: lane.name,
                    }),
                    null,
                  ] as const,
              ),
              ...(tally.observing
                ? [
                    [
                      'observing',
                      t(`${ns}.sources.statusObserving`, {
                        defaultValue: '观察中',
                      }),
                      tally.observing,
                    ] as const,
                  ]
                : []),
              ...(tally.bad
                ? [
                    [
                      'bad',
                      t(`${ns}.sources.statusBad`, { defaultValue: '异常' }),
                      tally.bad,
                    ] as const,
                  ]
                : []),
            ] as Array<readonly [string, string, number | null]>
          ).map(([key, label, count]) => (
            <Button
              aria-pressed={view === key}
              className={`mo-chip${view === key ? ' is-on' : ''}`}
              key={key}
              size='sm'
              variant='secondary'
              onClick={() => setView(key)}
            >
              {label}
              {count ? <span className='mo-chip__n'>{count}</span> : null}
            </Button>
          ))}
        </div>
      </div>

      <>
        {sourcesQuery.isLoading ? (
          <div className='mo-ledger'>
            <div className='mo-ledger__empty'>
              <span className='mo-state'>
                <span aria-hidden='true' className='mo-state__spinner' />
                {t(`${ns}.sources.loading`, { defaultValue: '正在读取来源' })}
              </span>
            </div>
          </div>
        ) : rows.length === 0 ? (
          <div className='mo-ledger'>
            <div className='mo-ledger__empty'>
              <div className='lazu-empty__slot'>
                <div className='lazu-state-title'>
                  {t(`${ns}.sources.emptyTitle`, {
                    defaultValue: '还没有来源',
                  })}
                </div>
                <div className='lazu-state-description'>
                  {t(`${ns}.sources.emptyDescription`, {
                    defaultValue: '接一个来源，模型和供货才会进来。',
                  })}
                </div>
                <Button
                  size='sm'
                  variant='primary'
                  onClick={() => setParams({ new: '1', source: null })}
                >
                  {t(`${ns}.action.connectSource`, {
                    defaultValue: '新增来源',
                  })}
                </Button>
              </div>
            </div>
          </div>
        ) : (
          <SourceLedger
            narrow={false}
            selectedId={creating ? null : selectedId}
            sources={rows}
            t={t}
            windowHours={windowHours}
            onDelete={(source) => setPendingDelete(source)}
            onOpenObservation={(source) => setObserved(source)}
            onSelect={(id) =>
              setParams({
                new: null,
                source: id === selectedId ? null : id,
              })
            }
            onTest={(source) => ops.testSourceMutation.mutate(source.id)}
            onToggle={(source, enabled) =>
              ops.sourceStatusMutation.mutate({ enabled, source })
            }
          />
        )}
      </>

      {creating || selected ? (
        <ModalFrame
          className='mo-dialog lazu-modal-content--xwide'
          hideClose
          open
          onOpenChange={(open) => {
            if (!open) closePanel();
          }}
        >
          <div className='mo-dialog__head'>
            <div className='mo-dialog__title-block'>
              <ModalTitle className='mo-dialog__title'>
                {creating ? (
                  t(`${ns}.action.connectSource`, { defaultValue: '新增来源' })
                ) : (
                  <>
                    <span className='mo-name'>{selected?.name}</span>
                    <SourceTypeBadge
                      t={t}
                      value={selected?.source_type || 'unclassified'}
                    />
                    <Badge
                      variant={selected?.status === 1 ? 'success' : 'neutral'}
                    >
                      {selected?.status === 1
                        ? t(`${ns}.sources.enabled`, {
                            defaultValue: '启用中',
                          })
                        : t(`${ns}.sources.disabled`, {
                            defaultValue: '已停用',
                          })}
                    </Badge>
                  </>
                )}
              </ModalTitle>
              {!creating ? (
                <div className='mo-sub'>
                  {[
                    selected?.type_name,
                    selected?.key_fingerprint
                      ? t(`${ns}.sources.credential`, {
                          defaultValue: '凭据 #{{fingerprint}}',
                          fingerprint: selected.key_fingerprint,
                        })
                      : null,
                    selected?.endpoint,
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </div>
              ) : null}
            </div>
            <div className='mo-dialog__tools'>
              {!creating ? (
                <>
                  <IconButton
                    aria-label={t(`${ns}.inspector.prev`, {
                      defaultValue: '上一个',
                    })}
                    disabled={selectedIndex <= 0}
                    size='sm'
                    variant='ghost'
                    onClick={() =>
                      setParams({ source: String(rows[selectedIndex - 1].id) })
                    }
                  >
                    <ChevronLeft size={16} />
                  </IconButton>
                  <IconButton
                    aria-label={t(`${ns}.inspector.next`, {
                      defaultValue: '下一个',
                    })}
                    disabled={
                      selectedIndex < 0 || selectedIndex >= rows.length - 1
                    }
                    size='sm'
                    variant='ghost'
                    onClick={() =>
                      setParams({ source: String(rows[selectedIndex + 1].id) })
                    }
                  >
                    <ChevronRight size={16} />
                  </IconButton>
                </>
              ) : null}
              <IconButton
                aria-label={t('common.close', { defaultValue: '关闭' })}
                size='sm'
                variant='ghost'
                onClick={closePanel}
              >
                <X size={16} />
              </IconButton>
            </div>
          </div>

          {creating ? (
            <ol className='mo-steps'>
              {(
                [
                  [
                    'credential',
                    t(`${ns}.step.credential`, { defaultValue: '凭据' }),
                  ],
                  ['models', t(`${ns}.step.models`, { defaultValue: '模型' })],
                  [
                    'routing',
                    t(`${ns}.step.routing`, { defaultValue: '线路' }),
                  ],
                ] as Array<[SourceStep, string]>
              ).map(([key, label], index) => (
                <li
                  aria-current={step === key ? 'step' : undefined}
                  className={`mo-steps__item${step === key ? ' is-active' : ''}`}
                  key={key}
                >
                  <span className='mo-steps__n'>{index + 1}</span>
                  {label}
                </li>
              ))}
            </ol>
          ) : (
            <Tabs
              className='mo-dialog__tabs'
              value={step}
              onValueChange={(value) => setStep(value as SourceStep)}
            >
              <TabsList variant='line'>
                {(
                  [
                    [
                      'credential',
                      t(`${ns}.step.credential`, { defaultValue: '凭据' }),
                    ],
                    [
                      'models',
                      t(`${ns}.step.models`, { defaultValue: '模型' }),
                    ],
                    [
                      'routing',
                      t(`${ns}.step.routing`, { defaultValue: '线路' }),
                    ],
                  ] as Array<[SourceStep, string]>
                ).map(([key, label]) => (
                  <TabsTrigger key={key} value={key}>
                    {label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          )}

          <SourceForm
            channels={channels}
            editingChannel={
              creating || !selected
                ? null
                : ({
                    id: selected.id,
                    name: selected.name,
                  } as unknown as ComponentProps<
                    typeof SourceForm
                  >['editingChannel'])
            }
            key={creating ? 'new' : String(selected?.id)}
            step={step}
            onCancel={closePanel}
            onSaved={closePanel}
            onStepChange={setStep}
          />
        </ModalFrame>
      ) : null}

      <Modal
        open={Boolean(pendingDelete)}
        title={t(`${ns}.sources.deleteTitle`, { defaultValue: '删除来源' })}
        description={t(`${ns}.sources.deleteDescription`, {
          defaultValue:
            '删除后该来源的凭据、模型映射和来源池关系都会移除，操作无法撤销。',
        })}
        body={<p className='mo-name'>{pendingDelete?.name || ''}</p>}
        footer={
          <>
            <Button
              disabled={channels.manageChannelMutation.isPending}
              variant='secondary'
              onClick={() => setPendingDelete(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              variant='danger'
              loading={channels.manageChannelMutation.isPending}
              onClick={async () => {
                try {
                  if (pendingDelete) {
                    await channels.manageChannelMutation.mutateAsync({
                      action: 'delete',
                      id: pendingDelete.id,
                      record: pendingDelete,
                    });
                    if (String(pendingDelete.id) === selectedId) closePanel();
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
          if (!open && !channels.manageChannelMutation.isPending)
            setPendingDelete(null);
        }}
      />

      <ObservationDialog
        busy={
          ops.goLiveSourceMutation.isPending ||
          ops.removeWatchedModelsMutation.isPending
        }
        source={observed}
        t={t}
        onGoLive={async (models) => {
          if (!observed) return;
          await ops.goLiveSourceMutation.mutateAsync({
            models,
            source: observed,
          });
          // Nothing left to watch: the dialog has nothing to show.
          if (models.length >= (observed.observation?.routes ?? 0)) {
            setObserved(null);
          }
        }}
        onOpenChange={(open) => {
          if (
            !open &&
            !ops.goLiveSourceMutation.isPending &&
            !ops.removeWatchedModelsMutation.isPending
          )
            setObserved(null);
        }}
        onRemove={async (models) => {
          if (!observed) return;
          await ops.removeWatchedModelsMutation.mutateAsync({
            models,
            source: observed,
          });
          if (models.length >= (observed.observation?.routes ?? 0)) {
            setObserved(null);
          }
        }}
      />
    </ConsolePage>
  );
}
