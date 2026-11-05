import { Columns3, Plus, Search } from 'lucide-react';
import { useMemo, useState } from 'react';
import { makerChips, type SourceCatalog } from './sourceCatalog';
import {
  buildSourceModelRows,
  parseModelMapping,
  serializeModelMapping,
  stopServing,
  type SourceModelRow,
  type UpstreamStatus,
} from './sourceModels';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu';
import { Input } from '../../components/ui/input';
import { Switch } from '../../components/ui/switch';
import { makerLabel } from '../../lib/model-makers';
import type { ChannelFormValues } from './SourceForm';
import type { UpstreamSyncView } from '../../hooks/models/useSourceUpstream';
import type { TFunction } from 'i18next';
import type { UseFormReturn } from 'react-hook-form';

const ns = 'page.console.modelOps.sourcePage.models';
const PAGE_SIZE = 50;

type StatusView = 'all' | 'gone' | 'new' | 'off' | 'on';
type OptionalColumn = 'calls' | 'maker' | 'price' | 'upstream';

const STATUS_LABEL: Record<UpstreamStatus, string> = {
  gone: '已不列',
  in: '在列',
  new: '新出现',
  unknown: '未检查',
};

type SourceModelsTableProps = {
  catalog: SourceCatalog;
  disabled?: boolean;
  form: UseFormReturn<ChannelFormValues>;
  onResolve: (body: { gone?: string[]; new?: string[] }) => void;
  resolving?: boolean;
  t: TFunction;
  upstream: UpstreamSyncView | null | undefined;
};

/** 来源 › 模型: every model the source serves or the upstream lists, one row
 *  each, with the switch that decides whether it is served. Edits go into
 *  the page's form and are written with 保存. */
export default function SourceModelsTable({
  catalog,
  disabled,
  form,
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
  const [view, setView] = useState<StatusView>('all');
  const [maker, setMaker] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const [hidden, setHidden] = useState<Set<OptionalColumn>>(new Set());
  const [adding, setAdding] = useState(false);
  const [custom, setCustom] = useState('');

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
    row.upstreamId.toLowerCase().includes(needle);
  const inView: Record<StatusView, (row: SourceModelRow) => boolean> = {
    all: () => true,
    gone: (row) => row.status === 'gone',
    new: (row) => row.status === 'new',
    off: (row) => !row.served,
    on: (row) => row.served,
  };
  const base = rows.filter(
    (row) =>
      matchesText(row) && (maker === null || catalog.maker(row.name) === maker),
  );
  const shown = base.filter(inView[view]);
  const pages = Math.max(1, Math.ceil(shown.length / PAGE_SIZE));
  const current = Math.min(page, pages - 1);
  const pageRows = shown.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE);
  const chips = makerChips(
    rows.filter(matchesText).map((row) => row.name),
    catalog,
  );

  const newCount = state?.new?.length ?? 0;
  const goneServed = rows.filter(
    (row) => row.served && row.status === 'gone',
  ).length;
  const stoppedCount = state?.stopped?.length ?? 0;
  const showBanner = newCount + goneServed + stoppedCount > 0;
  const filtered = Boolean(needle) || view !== 'all';

  const pick = (next: StatusView) => {
    setView(next);
    setPage(0);
  };

  const columns: Array<[OptionalColumn, string]> = [
    ['maker', t(`${ns}.colMaker`, { defaultValue: '厂商' })],
    ['upstream', t(`${ns}.colUpstream`, { defaultValue: '上游' })],
    ['price', t(`${ns}.colPrice`, { defaultValue: '价格 入/出 每百万' })],
    ['calls', t(`${ns}.colCalls`, { defaultValue: '7 天请求' })],
  ];
  const visible = (key: OptionalColumn) => !hidden.has(key);
  const template = [
    '56px',
    'minmax(0, 1.6fr)',
    visible('maker') ? '90px' : null,
    visible('upstream') ? '96px' : null,
    visible('price') ? '120px' : null,
    visible('calls') ? '90px' : null,
  ]
    .filter(Boolean)
    .join(' ');

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
            {t(`${ns}.bannerTitle`, { defaultValue: '上游有变化' })}
          </strong>
          <span>
            {[
              newCount
                ? t(`${ns}.bannerNew`, {
                    count: newCount,
                    defaultValue: '新出现 {{count}} 个',
                  })
                : null,
              goneServed
                ? t(`${ns}.bannerGone`, {
                    count: goneServed,
                    defaultValue: '已不列 {{count}} 个（你在供货的）',
                  })
                : null,
              stoppedCount
                ? t(`${ns}.bannerStopped`, {
                    count: stoppedCount,
                    defaultValue: '已自动停止 {{count}} 个',
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
              variant='secondary'
              onClick={() => {
                setMaker(null);
                pick('new');
              }}
            >
              {t(`${ns}.showNew`, { defaultValue: '看新出现的' })}
            </Button>
          ) : null}
          {goneServed + stoppedCount ? (
            <Button
              className={newCount ? undefined : 'src-banner__first'}
              size='xs'
              variant='secondary'
              onClick={() => {
                setMaker(null);
                pick('gone');
              }}
            >
              {t(`${ns}.showGone`, { defaultValue: '看已不列的' })}
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
              aria-label={t(`${ns}.search`, { defaultValue: '搜模型名' })}
              placeholder={t(`${ns}.search`, { defaultValue: '搜模型名' })}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setPage(0);
              }}
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
                ['new', t(`${ns}.viewNew`, { defaultValue: '上游新增' })],
                ['gone', t(`${ns}.viewGone`, { defaultValue: '上游已无' })],
              ] as Array<[StatusView, string]>
            ).map(([key, label]) => (
              <button
                data-owns-chrome=''
                aria-pressed={view === key}
                className={`src-seg__item${view === key ? ' is-on' : ''}`}
                key={key}
                type='button'
                onClick={() => pick(key)}
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
              variant='secondary'
              onClick={() => setAdding((open) => !open)}
            >
              <Plus size={14} />
              {t(`${ns}.addCustom`, { defaultValue: '手动添加' })}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button size='sm' variant='secondary'>
                  <Columns3 size={14} />
                  {t(`${ns}.columns`, { defaultValue: '列' })}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align='end' className='src-colmenu'>
                {columns.map(([key, label]) => (
                  <Checkbox
                    checked={visible(key)}
                    key={key}
                    labelClassName='src-colmenu__item'
                    onCheckedChange={(checked: boolean) =>
                      setHidden((previous) => {
                        const next = new Set(previous);
                        if (checked) next.delete(key);
                        else next.add(key);
                        return next;
                      })
                    }
                  >
                    {label}
                  </Checkbox>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>

        {adding ? (
          <div className='src-addrow'>
            <Input
              aria-label={t(`${ns}.addCustomLabel`, {
                defaultValue: '要添加的模型名',
              })}
              placeholder={t(`${ns}.addCustomPlaceholder`, {
                defaultValue: '模型名，多个用逗号或空格分开',
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
              variant='secondary'
              onClick={addCustom}
            >
              {t(`${ns}.addCustomConfirm`, { defaultValue: '添加' })}
            </Button>
            <span className='src-hint'>
              {t(`${ns}.addCustomHint`, {
                defaultValue:
                  '上游列表里没有、但调得通的模型，按名字加进供货单。',
              })}
            </span>
          </div>
        ) : null}

        <div
          className='src-makers'
          role='group'
          aria-label={t(`${ns}.makerFilter`, { defaultValue: '厂商' })}
        >
          <button
            data-owns-chrome=''
            aria-pressed={maker === null}
            className={`src-chip${maker === null ? ' is-on' : ''}`}
            type='button'
            onClick={() => {
              setMaker(null);
              setPage(0);
            }}
          >
            {t(`${ns}.allMakers`, { defaultValue: '全部厂商' })}
            <span className='src-num'>{rows.filter(matchesText).length}</span>
          </button>
          {chips.map((chip) => (
            <button
              data-owns-chrome=''
              aria-pressed={maker === chip.key}
              className={`src-chip${maker === chip.key ? ' is-on' : ''}`}
              key={chip.key || 'other'}
              type='button'
              onClick={() => {
                setMaker(maker === chip.key ? null : chip.key);
                setPage(0);
              }}
            >
              {makerLabel(chip.key, t)}
              <span className='src-num'>{chip.count}</span>
            </button>
          ))}
        </div>

        {filtered ? (
          <div className='src-bulk'>
            <span>
              {t(`${ns}.bulkShown`, { defaultValue: '筛出' })}{' '}
              <b className='src-num'>{shown.length}</b>{' '}
              {t(`${ns}.bulkUnit`, { defaultValue: '个' })}
            </span>
            <Button
              disabled={disabled || !shown.some((row) => !row.served)}
              size='xs'
              variant='secondary'
              onClick={() => serve(shown)}
            >
              {t(`${ns}.bulkOn`, { defaultValue: '全部开始供货' })}
            </Button>
            <Button
              disabled={disabled || !shown.some((row) => row.served)}
              size='xs'
              variant='secondary'
              onClick={() => stop(shown)}
            >
              {t(`${ns}.bulkOff`, { defaultValue: '全部停止' })}
            </Button>
            {view === 'new' && shown.length ? (
              <Button
                className='src-bulk__end'
                loading={resolving}
                size='xs'
                variant='ghost'
                onClick={() =>
                  onResolve({ new: shown.map((row) => row.upstreamId) })
                }
              >
                {t(`${ns}.dismissNew`, { defaultValue: '不再提示这些' })}
              </Button>
            ) : null}
            {view === 'gone' && shown.length ? (
              <Button
                className='src-bulk__end'
                loading={resolving}
                size='xs'
                variant='ghost'
                onClick={() =>
                  onResolve({ gone: shown.map((row) => row.name) })
                }
              >
                {t(`${ns}.dismissGone`, { defaultValue: '不再提示这些' })}
              </Button>
            ) : null}
          </div>
        ) : null}

        <div
          className='src-table'
          role='table'
          aria-label={t(`${ns}.label`, { defaultValue: '模型' })}
        >
          <div
            className='src-row src-row--head'
            role='row'
            style={{ gridTemplateColumns: template }}
          >
            <span className='src-th' role='columnheader'>
              {t(`${ns}.colServe`, { defaultValue: '供货' })}
            </span>
            <span className='src-th' role='columnheader'>
              {t(`${ns}.colName`, { defaultValue: '模型 · 上游 ID' })}
            </span>
            {columns
              .filter(([key]) => visible(key))
              .map(([key, label]) => (
                <span
                  className={`src-th${key === 'price' || key === 'calls' ? ' is-num' : ''}`}
                  key={key}
                  role='columnheader'
                >
                  {label}
                </span>
              ))}
          </div>
          {pageRows.length === 0 ? (
            <div className='src-empty'>
              {rows.length === 0
                ? t(`${ns}.emptyAll`, {
                    defaultValue:
                      '还没有模型。点「立即检查上游」读一次上游列表，或手动添加。',
                  })
                : t(`${ns}.emptyFiltered`, {
                    defaultValue: '没有符合筛选的模型',
                  })}
            </div>
          ) : (
            pageRows.map((row) => {
              const calls = upstream?.requests_7d?.[row.name];
              return (
                <div
                  className={`src-row${row.served && row.status === 'gone' ? ' is-gone' : ''}`}
                  key={`${row.name}\u0000${row.upstreamId}`}
                  role='row'
                  style={{ gridTemplateColumns: template }}
                >
                  <span role='cell'>
                    <Switch
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
                  <span className='src-name' role='cell'>
                    <span className='src-name__main'>{row.name}</span>
                    {row.upstreamId !== row.name ? (
                      <span className='src-name__sub'>
                        {t(`${ns}.upstreamId`, {
                          defaultValue: '上游 {{id}}',
                          id: row.upstreamId,
                        })}
                      </span>
                    ) : null}
                  </span>
                  {visible('maker') ? (
                    <span className='src-hint' role='cell'>
                      {catalog.maker(row.name)
                        ? makerLabel(catalog.maker(row.name), t)
                        : '—'}
                    </span>
                  ) : null}
                  {visible('upstream') ? (
                    <span role='cell'>
                      <span className={`src-status is-${row.status}`}>
                        {t(`${ns}.status.${row.status}`, {
                          defaultValue: STATUS_LABEL[row.status],
                        })}
                      </span>
                    </span>
                  ) : null}
                  {visible('price') ? (
                    <span className='src-mono is-num' role='cell'>
                      {catalog.price(row.name)}
                    </span>
                  ) : null}
                  {visible('calls') ? (
                    <span className='src-mono is-num is-soft' role='cell'>
                      {row.served && calls !== undefined
                        ? calls.toLocaleString()
                        : '—'}
                    </span>
                  ) : null}
                </div>
              );
            })
          )}
        </div>
        <div className='src-foot'>
          <span>
            {t(`${ns}.total`, {
              count: shown.length,
              defaultValue: '共 {{count}} 行',
            })}
          </span>
          {pages > 1 ? (
            <span className='src-pager'>
              <Button
                disabled={current === 0}
                size='xs'
                variant='ghost'
                onClick={() => setPage(current - 1)}
              >
                {t(`${ns}.prev`, { defaultValue: '上一页' })}
              </Button>
              <span className='src-num'>
                {current + 1} / {pages}
              </span>
              <Button
                disabled={current >= pages - 1}
                size='xs'
                variant='ghost'
                onClick={() => setPage(current + 1)}
              >
                {t(`${ns}.next`, { defaultValue: '下一页' })}
              </Button>
            </span>
          ) : (
            <span>{t(`${ns}.perPage`, { defaultValue: '每页 50' })}</span>
          )}
        </div>
      </section>
    </div>
  );
}
