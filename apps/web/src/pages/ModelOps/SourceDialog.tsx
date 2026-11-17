import { useQuery } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, RefreshCw, X, Zap } from 'lucide-react';
import { useEffect, useState, type ComponentProps } from 'react';
import { Link } from 'react-router-dom';
import { formatLatency, formatRate, healthTone } from './formatters';
import { useSourceCatalog } from './sourceCatalog';
import SourceForm, { SOURCE_FORM_ID, type SourceTab } from './SourceForm';
import { formatCheckTime } from './sourceModels';
import { isObservingSource, sourceAdapterLabel } from './sourceStatus';
import { sourceTypeLabel } from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { IconButton } from '../../components/ui/icon-button';
import { ModalFrame, ModalTitle } from '../../components/ui/modal';
import { resolveProviderBrand } from '../../components/ui/provider-brands';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { API } from '../../helpers/api';
import { useChannelsQuery } from '../../hooks/channels/useChannelsQuery';
import {
  useModelOpsMutations,
  type ModelOpsSource,
} from '../../hooks/models/useModelOpsQuery';
import {
  useSourceUpstreamMutations,
  useSourceUpstreamQuery,
} from '../../hooks/models/useSourceUpstream';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { SourceModelStatusView } from './SourceModelsTable';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.sourcePage';

export type SourceDialogTab = SourceTab | 'records';
export const SOURCE_DIALOG_TABS: SourceDialogTab[] = [
  'models',
  'connection',
  'routing',
  'sync',
  'records',
];

type EditingChannel = ComponentProps<typeof SourceForm>['editingChannel'];

const hostOf = (endpoint: string) => {
  try {
    return new URL(endpoint).host;
  } catch {
    return endpoint.replace(/^https?:\/\//, '').split('/')[0];
  }
};

function SourceStatePill({
  source,
  t,
}: {
  source: ModelOpsSource;
  t: TFunction;
}) {
  if (source.status !== 1) {
    return (
      <span className='src-pill is-off'>
        {t(`${ns}.state.off`, { defaultValue: '已停用' })}
      </span>
    );
  }
  if (isObservingSource(source)) {
    return (
      <span className='src-pill is-watch'>
        {t(`${ns}.state.watch`, { defaultValue: '观察中' })}
      </span>
    );
  }
  const tone = healthTone(source.health, source.status);
  const rateText =
    source.health.request_count > 0 && source.health.success_rate !== null
      ? formatRate(source.health.success_rate)
      : '';
  return (
    <span className={`src-pill is-${tone}`}>
      <span aria-hidden='true'>● </span>
      {rateText ||
        (tone === 'idle'
          ? t(`${ns}.state.idle`, { defaultValue: '无流量' })
          : tone === 'bad'
            ? t(`${ns}.state.bad`, { defaultValue: '异常' })
            : tone === 'warn'
              ? t(`${ns}.state.warn`, { defaultValue: '波动' })
              : t(`${ns}.state.ok`, { defaultValue: '正常' }))}
    </span>
  );
}

type LogRecord = {
  completion_tokens?: number;
  content?: string;
  created_at: number;
  id: number;
  model_name?: string;
  prompt_tokens?: number;
  type: number;
  use_time?: number;
};

function SourceRecords({
  internalId,
  t,
}: {
  internalId?: number;
  t: TFunction;
}) {
  const query = useQuery({
    enabled: Boolean(internalId),
    queryKey: queryKeys.console.table('source-records', { internalId }),
    queryFn: async () => {
      const end = Math.floor(Date.now() / 1000);
      const params = new URLSearchParams({
        channel: String(internalId),
        end_timestamp: String(end),
        p: '1',
        page_size: '50',
        start_timestamp: String(end - 7 * 86400),
        type: '0',
      });
      const data = unwrap<{ items?: LogRecord[] } | LogRecord[]>(
        await API.get(`/api/log/?${params}`, { skipErrorHandler: true }),
      );
      return (Array.isArray(data) ? data : data?.items) ?? [];
    },
  });
  const rows = (query.data ?? []).filter(
    (row) => row.type === 2 || row.type === 5,
  );
  return (
    <section className='src-panel'>
      <div className='src-recrow src-recrow--head'>
        <span className='src-th'>
          {t(`${ns}.records.time`, { defaultValue: '时间' })}
        </span>
        <span className='src-th'>
          {t(`${ns}.records.model`, { defaultValue: '模型' })}
        </span>
        <span className='src-th'>
          {t(`${ns}.records.result`, { defaultValue: '结果' })}
        </span>
        <span className='src-th is-num'>
          {t(`${ns}.records.latency`, { defaultValue: '用时' })}
        </span>
        <span className='src-th is-num'>
          {t(`${ns}.records.tokens`, { defaultValue: '入 / 出' })}
        </span>
        <span className='src-th'>
          {t(`${ns}.records.note`, { defaultValue: '说明' })}
        </span>
      </div>
      {query.isLoading ? (
        <div className='src-empty'>
          {t(`${ns}.records.loading`, { defaultValue: '读取中…' })}
        </div>
      ) : query.isError ? (
        <div className='src-empty'>
          {t(`${ns}.records.error`, { defaultValue: '读取失败' })}
        </div>
      ) : rows.length === 0 ? (
        <div className='src-empty'>
          {t(`${ns}.records.empty`, {
            defaultValue: '近 7 天暂无请求记录',
          })}
        </div>
      ) : (
        rows.map((row) => {
          const failed = row.type === 5;
          return (
            <div className='src-recrow' key={row.id}>
              <span className='src-mono'>
                {formatCheckTime(row.created_at)}
              </span>
              <span className='src-mono src-recrow__model'>
                {row.model_name || '—'}
              </span>
              <span>
                <span className={`src-status ${failed ? 'is-gone' : 'is-in'}`}>
                  {failed
                    ? t(`${ns}.records.failed`, { defaultValue: '失败' })
                    : t(`${ns}.records.ok`, { defaultValue: '成功' })}
                </span>
              </span>
              <span className='src-mono is-num'>
                {row.use_time ? formatLatency(row.use_time * 1000) : '—'}
              </span>
              <span className='src-mono is-num'>
                {failed
                  ? '—'
                  : `${(row.prompt_tokens ?? 0).toLocaleString()} / ${(row.completion_tokens ?? 0).toLocaleString()}`}
              </span>
              <span
                className='src-hint src-recrow__note'
                title={failed ? row.content : undefined}
              >
                {failed ? row.content || '' : ''}
              </span>
            </div>
          );
        })
      )}
      <div className='src-foot'>
        <span>
          {t(`${ns}.records.foot`, {
            count: rows.length,
            defaultValue: '近 7 天 · 最新 {{count}} 条',
          })}
        </span>
        <Link className='src-foot__link' to='/console/usage-logs'>
          {t(`${ns}.records.all`, { defaultValue: '全部日志' })}
        </Link>
      </div>
    </section>
  );
}

type SourceDialogProps = {
  channels: ReturnType<typeof useChannelsQuery>;
  hasNext: boolean;
  hasPrev: boolean;
  initialModelView?: SourceModelStatusView;
  isSourcesLoading?: boolean;
  onClose: () => void;
  onCreated: (createdId?: string) => void;
  onNext: () => void;
  onPrev: () => void;
  onSaved: () => void;
  onTabChange: (tab: SourceDialogTab) => void;
  source: ModelOpsSource | null;
  sourceId: string;
  tab: SourceDialogTab;
};

/** Centered wide dialog over the Sources ledger for both editing an existing
 *  source and creating a new one without leaving the list. */
export default function SourceDialog({
  channels,
  hasNext,
  hasPrev,
  initialModelView,
  isSourcesLoading,
  onClose,
  onCreated,
  onNext,
  onPrev,
  onSaved,
  onTabChange,
  source,
  sourceId,
  tab,
}: SourceDialogProps) {
  const creating = sourceId === 'new';
  const { t } = channels;
  const catalog = useSourceCatalog(channels.channelModelOptions);
  const ops = useModelOpsMutations(t);
  const upstreamQuery = useSourceUpstreamQuery(creating ? null : sourceId);
  const upstream = upstreamQuery.data ?? null;
  const { check, resolve } = useSourceUpstreamMutations(
    creating ? null : sourceId,
  );
  const [servedCount, setServedCount] = useState<number | null>(null);

  useEffect(() => {
    setServedCount(null);
  }, [sourceId]);

  if (creating) {
    return (
      <ModalFrame
        busy={channels.saveChannelMutation.isPending}
        className='mo-dialog src-dialog src-dialog--create lazu-modal-content--xwide'
        hideClose
        open
        sheet='full'
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
      >
        <div className='mo-dialog__head src-dialog__head--create'>
          <div className='mo-dialog__title-block'>
            <ModalTitle className='mo-dialog__title'>
              {t(`${ns}.newTitle`, { defaultValue: '新建来源' })}
            </ModalTitle>
          </div>
          <div className='mo-dialog__tools'>
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
        <div className='mo-dialog__body src-dialog__body'>
          <SourceForm
            catalog={catalog}
            channels={channels}
            editingChannel={null}
            layout='create'
            onCancel={onClose}
            onSaved={async (ids) => {
              const created = ids[0];
              if (!created) {
                onCreated();
                return;
              }
              try {
                await API.post(
                  `/api/model_ops/sources/${created}/upstream/check`,
                  undefined,
                  { skipErrorHandler: true },
                );
              } catch {
                /* Any check failure surfaces inside the opened source dialog. */
              }
              onCreated(created);
            }}
          />
        </div>
      </ModalFrame>
    );
  }

  const settings = upstream?.settings;
  const interval = settings?.interval_hours ?? 0;
  const checkedAt = upstream?.state?.checked_at;
  const syncLine = interval
    ? t(`${ns}.syncLineShort`, {
        defaultValue: '每 {{hours}}h 同步 · {{last}}',
        hours: interval,
        last: formatCheckTime(checkedAt),
      })
    : checkedAt
      ? formatCheckTime(checkedAt)
      : '';

  const displayCount = servedCount ?? source?.model_count ?? 0;
  const tabLabel: Record<SourceDialogTab, string> = {
    connection: t(`${ns}.tab.connection`, { defaultValue: '连接' }),
    models: t(`${ns}.tab.models`, { defaultValue: '模型' }),
    records: t(`${ns}.tab.records`, { defaultValue: '记录' }),
    routing: t(`${ns}.tab.routing`, { defaultValue: '线路' }),
    sync: t(`${ns}.tab.sync`, { defaultValue: '同步' }),
  };

  const meta = source
    ? [
        `#${source.internal_id}`,
        sourceTypeLabel(source.source_type, t),
        sourceAdapterLabel(source, t),
        hostOf(source.endpoint || source.base_url || ''),
      ]
        .filter(Boolean)
        .join(' · ')
    : '';

  return (
    <ModalFrame
      busy={channels.saveChannelMutation.isPending}
      className='mo-dialog src-dialog lazu-modal-content--xwide'
      hideClose
      open
      sheet='full'
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <div className='mo-dialog__head src-dialog__head'>
        <div className='src-dialog__identity'>
          <ProviderLogo
            brand={resolveProviderBrand({
              id: source?.endpoint,
              name: `${source?.type_name || ''} ${source?.name || ''}`,
            })}
            decorative
            size={30}
          />
          <div className='mo-dialog__title-block'>
            <ModalTitle className='mo-dialog__title'>
              <span>{source?.name || (isSourcesLoading ? '…' : sourceId)}</span>
              {source ? <SourceStatePill source={source} t={t} /> : null}
            </ModalTitle>
            {meta ? <div className='mo-sub src-mono'>{meta}</div> : null}
          </div>
        </div>
        <div className='mo-dialog__tools'>
          <Button
            disabled={!source}
            loading={ops.testSourceMutation.isPending}
            size='sm'
            type='button'
            variant='secondary'
            onClick={() => source && ops.testSourceMutation.mutate(source.id)}
          >
            <Zap size={14} />
            {t(`${ns}.testShort`, { defaultValue: '测试' })}
          </Button>
          <Button
            loading={check.isPending}
            size='sm'
            type='button'
            variant='secondary'
            onClick={() => check.mutate()}
          >
            <RefreshCw size={14} />
            {t(`${ns}.checkShort`, { defaultValue: '查上游' })}
          </Button>
          <IconButton
            aria-label={t('page.console.modelOps.inspector.prev', {
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
            aria-label={t('page.console.modelOps.inspector.next', {
              defaultValue: '下一个',
            })}
            disabled={!hasNext}
            size='sm'
            variant='ghost'
            onClick={onNext}
          >
            <ChevronRight size={16} />
          </IconButton>
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

      <div className='src-dialog__tabs-bar'>
        <Tabs
          className='src-dialog__tabs'
          value={tab}
          onValueChange={(value) => onTabChange(value as SourceDialogTab)}
        >
          <TabsList variant='line'>
            {SOURCE_DIALOG_TABS.map((key) => (
              <TabsTrigger key={key} value={key}>
                {tabLabel[key]}
                {key === 'models' ? (
                  <span className='src-num src-tabs__count'>
                    {displayCount}
                  </span>
                ) : null}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        {syncLine ? (
          <span className='src-hint src-tabs__aside'>{syncLine}</span>
        ) : null}
      </div>

      <div className='mo-dialog__body src-dialog__body'>
        {tab === 'records' ? (
          <SourceRecords internalId={source?.internal_id} t={t} />
        ) : null}
        <div className='src-formhost' hidden={tab === 'records'}>
          <SourceForm
            catalog={catalog}
            channels={channels}
            editingChannel={{ id: sourceId } as unknown as EditingChannel}
            initialModelView={initialModelView}
            key={sourceId}
            layout='page'
            resolving={resolve.isPending}
            tab={tab === 'records' ? 'models' : tab}
            upstream={upstream}
            onCancel={onClose}
            onResolve={(body) => resolve.mutate(body)}
            onSaved={() => {
              onSaved();
            }}
            onServedCountChange={setServedCount}
            onTabChange={onTabChange}
          />
        </div>
      </div>

      <div className='mo-dialog__foot src-dialog__foot'>
        <span className='src-hint'>
          {t(`${ns}.footServing`, { defaultValue: '供货' })}{' '}
          <strong className='src-mono src-ink'>{displayCount}</strong>{' '}
          {t(`${ns}.footServingUnit`, { defaultValue: '个' })}
        </span>
        <div className='src-dialog__foot-actions'>
          <Button size='sm' type='button' variant='secondary' onClick={onClose}>
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          {tab !== 'records' ? (
            <Button
              form={SOURCE_FORM_ID}
              loading={channels.saveChannelMutation.isPending}
              size='sm'
              type='submit'
              variant='primary'
            >
              {t(`${ns}.save`, { defaultValue: '保存' })}
            </Button>
          ) : null}
        </div>
      </div>
    </ModalFrame>
  );
}
