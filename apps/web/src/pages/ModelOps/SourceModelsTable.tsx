import { ChevronDown, Plus, Search } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import {
  buildSourceModelRows,
  parseModelMapping,
  serializeModelMapping,
  stopServing,
  type SourceModelRow,
} from './sourceModels';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import { Input } from '../../components/ui/input';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { groupByMaker, makerBrand, makerLabel } from '../../lib/model-makers';
import type { SourceCatalog } from './sourceCatalog';
import type { ChannelFormValues } from '../../hooks/channels/useChannelsQuery';
import type { UpstreamSyncView } from '../../hooks/models/useSourceUpstream';
import type { TFunction } from 'i18next';
import type { UseFormReturn } from 'react-hook-form';

const ns = 'page.console.modelOps.sourcePage.models';

export type SourceModelStatusView = 'all' | 'gone' | 'new' | 'off' | 'on';

type SourceModelsTableProps = {
  catalog: SourceCatalog;
  disabled?: boolean;
  form: UseFormReturn<ChannelFormValues>;
  initialView?: SourceModelStatusView;
  onResolve: (body: { gone?: string[]; new?: string[] }) => void;
  resolving?: boolean;
  t: TFunction;
  upstream: UpstreamSyncView | null | undefined;
};

/** 来源 › 模型: grouped by maker with collapsible headers and a 2-column
 *  checkbox grid so full model IDs, prices, context and upstream mappings stay
 *  readable inside the centered dialog. */
export default function SourceModelsTable({
  catalog,
  disabled,
  form,
  initialView = 'all',
  onResolve,
  resolving,
  t,
  upstream,
}: SourceModelsTableProps) {
  const models = form.watch('models') || [];
  const mappingText = form.watch('model_mapping') || '';
  const mapping = useMemo(() => parseModelMapping(mappingText), [mappingText]);
  const state = upstream?.state ?? null;
  const rows = useMemo(
    () => buildSourceModelRows(models, mapping, state, catalog.names),
    [catalog.names, mapping, models, state],
  );

  const [query, setQuery] = useState('');
  const [view, setView] = useState<SourceModelStatusView>(initialView);
  const [folded, setFolded] = useState<Record<string, boolean>>({});
  const [adding, setAdding] = useState(false);
  const [custom, setCustom] = useState('');

  useEffect(() => {
    setView(initialView);
  }, [initialView]);

  const write = (next: {
    mapping: Record<string, string>;
    models: string[];
  }) => {
    form.setValue('models', next.models, {
      shouldDirty: true,
      shouldValidate: true,
    });
    form.setValue('model_mapping', serializeModelMapping(next.mapping), {
      shouldDirty: true,
    });
  };

  const serve = (targets: SourceModelRow[]) => {
    const nextModels = [...models];
    const nextMapping = { ...mapping };
    for (const row of targets) {
      if (row.served || nextModels.includes(row.name)) continue;
      nextModels.push(row.name);
      if (row.upstreamId !== row.name) nextMapping[row.name] = row.upstreamId;
    }
    write({ mapping: nextMapping, models: nextModels });
  };

  const stop = (targets: SourceModelRow[]) =>
    write(
      stopServing(
        models,
        mapping,
        targets.filter((row) => row.served).map((row) => row.name),
      ),
    );

  const needle = query.trim().toLowerCase();
  const matchesText = (row: SourceModelRow) =>
    !needle ||
    row.name.toLowerCase().includes(needle) ||
    row.upstreamId.toLowerCase().includes(needle) ||
    makerLabel(catalog.maker(row.name), t).toLowerCase().includes(needle);

  const inView: Record<
    SourceModelStatusView,
    (row: SourceModelRow) => boolean
  > = {
    all: () => true,
    gone: (row) => row.status === 'gone',
    new: (row) => row.status === 'new',
    off: (row) => !row.served,
    on: (row) => row.served,
  };

  const base = rows.filter(matchesText);
  const shown = base.filter(inView[view]);

  const allByMaker = useMemo(() => {
    const map = new Map<string, SourceModelRow[]>();
    for (const row of rows) {
      const mk = catalog.maker(row.name);
      const list = map.get(mk) ?? [];
      list.push(row);
      map.set(mk, list);
    }
    return map;
  }, [catalog, rows]);

  const groups = useMemo(
    () =>
      groupByMaker(
        shown,
        (row) => catalog.maker(row.name),
        (row) => catalog.makerRank(catalog.maker(row.name)),
      ),
    [catalog, shown],
  );

  const newCount = state?.new?.length ?? 0;
  const goneServed = rows.filter(
    (row) => row.served && row.status === 'gone',
  ).length;
  const stoppedCount = state?.stopped?.length ?? 0;
  const showBanner = newCount + goneServed + stoppedCount > 0;
  const filtered = Boolean(needle) || view !== 'all';

  const addCustom = () => {
    const names = custom
      .split(/[\s,，]+/)
      .map((name) => name.trim())
      .filter(Boolean);
    if (!names.length) return;
    serve(
      names.map((name) => ({
        name,
        served: false,
        status: 'unknown' as const,
        upstreamId: name,
      })),
    );
    setCustom('');
    setAdding(false);
  };

  return (
    <div className='src-models'>
      {showBanner ? (
        <div className='src-banner' role='status'>
          <strong>
            {t(`${ns}.bannerTitle`, { defaultValue: '上游变化' })}
          </strong>
          <span>
            {[
              newCount
                ? t(`${ns}.bannerNew`, {
                    count: newCount,
                    defaultValue: '新增 {{count}} 个',
                  })
                : null,
              goneServed
                ? t(`${ns}.bannerGone`, {
                    count: goneServed,
                    defaultValue: '下架 {{count}} 个',
                  })
                : null,
              stoppedCount
                ? t(`${ns}.bannerStopped`, {
                    count: stoppedCount,
                    defaultValue: '已自动停供 {{count}} 个',
                  })
                : null,
            ]
              .filter(Boolean)
              .join('，')}
          </span>
          {newCount ? (
            <Button
              className='src-banner__first'
              size='xs'
              type='button'
              variant='secondary'
              onClick={() => setView('new')}
            >
              {t(`${ns}.showNew`, {
                count: newCount,
                defaultValue: '看新增 ({{count}})',
              })}
            </Button>
          ) : null}
          {goneServed + stoppedCount ? (
            <Button
              className={newCount ? undefined : 'src-banner__first'}
              size='xs'
              type='button'
              variant='secondary'
              onClick={() => setView('gone')}
            >
              {t(`${ns}.showGone`, {
                count: goneServed + stoppedCount,
                defaultValue: '看下架 ({{count}})',
              })}
            </Button>
          ) : null}
        </div>
      ) : null}

      <section className='src-panel'>
        <div className='src-toolbar'>
          <span className='lazu-input-affix src-search'>
            <span className='lazu-input-affix__prefix'>
              <Search aria-hidden='true' size={14} />
            </span>
            <Input
              aria-label={t(`${ns}.search`, { defaultValue: '搜模型' })}
              placeholder={t(`${ns}.searchPlaceholder`, {
                defaultValue: '搜模型…',
              })}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </span>
          <div
            aria-label={t(`${ns}.statusFilter`, { defaultValue: '供货状态' })}
            className='src-seg'
            role='group'
          >
            {(
              [
                ['all', t(`${ns}.viewAll`, { defaultValue: '全部' })],
                ['on', t(`${ns}.viewOn`, { defaultValue: '供货中' })],
                ['off', t(`${ns}.viewOff`, { defaultValue: '未供货' })],
                ['new', t(`${ns}.viewNew`, { defaultValue: '新增' })],
                ['gone', t(`${ns}.viewGone`, { defaultValue: '下架' })],
              ] as Array<[SourceModelStatusView, string]>
            ).map(([key, label]) => (
              <button
                aria-pressed={view === key}
                className={`src-seg__item${view === key ? ' is-on' : ''}`}
                data-owns-chrome=''
                key={key}
                type='button'
                onClick={() => setView(key)}
              >
                {label}
                <span className='src-num'>
                  {base.filter(inView[key]).length}
                </span>
              </button>
            ))}
          </div>
          <span className='src-toolbar__end'>
            <Button
              disabled={disabled}
              size='sm'
              type='button'
              variant='secondary'
              onClick={() => setAdding((open) => !open)}
            >
              <Plus size={14} />
              {t(`${ns}.addCustom`, { defaultValue: '手动添加' })}
            </Button>
          </span>
        </div>

        {adding ? (
          <div className='src-addrow'>
            <Input
              aria-label={t(`${ns}.addCustomLabel`, {
                defaultValue: '模型名',
              })}
              placeholder={t(`${ns}.addCustomPlaceholder`, {
                defaultValue: '输入模型名，多个用空格或逗号分隔',
              })}
              value={custom}
              onChange={(event) => setCustom(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  addCustom();
                }
              }}
            />
            <Button
              disabled={!custom.trim()}
              size='sm'
              type='button'
              variant='secondary'
              onClick={addCustom}
            >
              {t(`${ns}.addCustomConfirm`, { defaultValue: '添加' })}
            </Button>
          </div>
        ) : null}

        {filtered && shown.length > 0 ? (
          <div className='src-bulk'>
            <span>
              {t(`${ns}.bulkShown`, { defaultValue: '筛出' })}{' '}
              <b className='src-num'>{shown.length}</b>{' '}
              {t(`${ns}.bulkUnit`, { defaultValue: '个' })}
            </span>
            <Button
              disabled={disabled || !shown.some((row) => !row.served)}
              size='xs'
              type='button'
              variant='secondary'
              onClick={() => serve(shown)}
            >
              {t(`${ns}.bulkOn`, { defaultValue: '全部供货' })}
            </Button>
            <Button
              disabled={disabled || !shown.some((row) => row.served)}
              size='xs'
              type='button'
              variant='secondary'
              onClick={() => stop(shown)}
            >
              {t(`${ns}.bulkOff`, { defaultValue: '全部停止' })}
            </Button>
            {view === 'new' ? (
              <Button
                className='src-bulk__end'
                loading={resolving}
                size='xs'
                type='button'
                variant='ghost'
                onClick={() =>
                  onResolve({ new: shown.map((row) => row.upstreamId) })
                }
              >
                {t(`${ns}.dismissNew`, { defaultValue: '忽略这些' })}
              </Button>
            ) : null}
            {view === 'gone' ? (
              <Button
                className='src-bulk__end'
                loading={resolving}
                size='xs'
                type='button'
                variant='ghost'
                onClick={() =>
                  onResolve({ gone: shown.map((row) => row.name) })
                }
              >
                {t(`${ns}.dismissGone`, { defaultValue: '忽略这些' })}
              </Button>
            ) : null}
          </div>
        ) : null}

        <div
          aria-label={t(`${ns}.label`, { defaultValue: '模型' })}
          className='src-mgroups'
          role='table'
        >
          {groups.length === 0 ? (
            <div className='src-empty'>
              {rows.length === 0
                ? t(`${ns}.emptyAll`, {
                    defaultValue: '暂无模型，点右上角「查上游」或手动添加',
                  })
                : t(`${ns}.emptyFiltered`, {
                    defaultValue: '无匹配模型',
                  })}
            </div>
          ) : (
            groups.map((group) => {
              const key = group.maker || '__other__';
              const label = makerLabel(group.maker, t);
              const totalInMaker = allByMaker.get(group.maker) ?? group.rows;
              const servedTotal = totalInMaker.filter((r) => r.served).length;
              const servedShown = group.rows.filter((r) => r.served).length;
              const allOn =
                group.rows.length > 0 && servedShown === group.rows.length;
              const someOn = servedShown > 0 && !allOn;
              const isOpen = filtered || !folded[key];

              return (
                <div className='src-mgroup' key={key} role='rowgroup'>
                  <div className='src-mgroup__head'>
                    <Checkbox
                      aria-label={t(`${ns}.toggleGroup`, {
                        defaultValue: '全选 {{maker}}',
                        maker: label,
                      })}
                      checked={allOn}
                      disabled={disabled}
                      indeterminate={someOn}
                      onCheckedChange={(checked: boolean) =>
                        checked ? serve(group.rows) : stop(group.rows)
                      }
                    />
                    <button
                      aria-expanded={isOpen}
                      className='src-mgroup__toggle'
                      data-owns-chrome=''
                      type='button'
                      onClick={() =>
                        setFolded((prev) => ({ ...prev, [key]: !prev[key] }))
                      }
                    >
                      <ProviderLogo
                        brand={makerBrand(
                          group.maker,
                          group.rows[0] ? { name: group.rows[0].name } : {},
                        )}
                        decorative
                        size={18}
                      />
                      <span className='src-mgroup__name'>{label}</span>
                      <span className='src-num src-mgroup__count'>
                        {`${servedTotal} / ${totalInMaker.length}`}
                      </span>
                      <ChevronDown
                        aria-hidden='true'
                        className={`src-mgroup__chev${isOpen ? ' is-open' : ''}`}
                        size={14}
                      />
                    </button>
                  </div>

                  {isOpen ? (
                    <div className='src-mgroup__grid'>
                      {group.rows.map((row) => {
                        const calls = upstream?.requests_7d?.[row.name];
                        const price = catalog.price(row.name);
                        const ctx = catalog.context(row.name);
                        const goneOn = row.served && row.status === 'gone';
                        const priceCtx = [
                          price !== '—'
                            ? price
                            : t(`${ns}.unpriced`, { defaultValue: '待补定价' }),
                          ctx,
                        ]
                          .filter(Boolean)
                          .join(' · ');

                        return (
                          <div
                            className={`src-mcard${row.served ? ' is-served' : ''}${goneOn ? ' is-gone' : ''}`}
                            key={`${row.name}\u0000${row.upstreamId}`}
                            role='row'
                            tabIndex={disabled ? -1 : 0}
                            onClick={(event) => {
                              if (
                                disabled ||
                                (event.target as HTMLElement | null)?.closest(
                                  'button, input, label',
                                )
                              ) {
                                return;
                              }
                              if (row.served) stop([row]);
                              else serve([row]);
                            }}
                            onKeyDown={(event) => {
                              if (
                                disabled ||
                                (event.target as HTMLElement | null)?.closest(
                                  'button, input, label',
                                )
                              ) {
                                return;
                              }
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault();
                                if (row.served) stop([row]);
                                else serve([row]);
                              }
                            }}
                          >
                            <span className='src-mcard__box' role='cell'>
                              <Checkbox
                                aria-label={t(`${ns}.toggle`, {
                                  defaultValue: '供货 {{name}}',
                                  name: row.name,
                                })}
                                checked={row.served}
                                disabled={disabled}
                                onCheckedChange={(checked: boolean) =>
                                  checked ? serve([row]) : stop([row])
                                }
                              />
                            </span>
                            <span className='src-mcard__body' role='cell'>
                              <span className='src-mcard__top'>
                                <span className='src-mcard__id'>
                                  {row.name}
                                </span>
                                {row.status === 'new' ? (
                                  <span className='src-status is-new'>
                                    {t(`${ns}.badgeNew`, {
                                      defaultValue: '新增',
                                    })}
                                  </span>
                                ) : null}
                                {row.status === 'gone' ? (
                                  <span className='src-status is-gone'>
                                    {t(`${ns}.badgeGone`, {
                                      defaultValue: '下架',
                                    })}
                                  </span>
                                ) : null}
                              </span>
                              <span className='src-mcard__sub'>
                                <span>{priceCtx}</span>
                                {row.upstreamId !== row.name ? (
                                  <span className='src-mcard__map'>
                                    {` · ← ${row.upstreamId}`}
                                  </span>
                                ) : null}
                              </span>
                            </span>
                            {row.served && calls !== undefined ? (
                              <span
                                className='src-mono src-mcard__calls'
                                role='cell'
                              >
                                {calls.toLocaleString()}
                              </span>
                            ) : null}
                          </div>
                        );
                      })}
                    </div>
                  ) : null}
                </div>
              );
            })
          )}
        </div>
      </section>
    </div>
  );
}
