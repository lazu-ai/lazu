import { useQuery } from '@tanstack/react-query';
import { RefreshCw, Zap } from 'lucide-react';
import { useEffect } from 'react';
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import { formatLatency, healthTone } from './formatters';
import { useSourceCatalog } from './sourceCatalog';
import SourceForm, { SOURCE_FORM_ID, type SourceTab } from './SourceForm';
import { formatCheckTime } from './sourceModels';
import { isObservingSource, sourceAdapterLabel } from './sourceStatus';
import { Button } from '../../components/ui/button';
import ConsolePage from '../../components/ui/console-page';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { API } from '../../helpers/api';
import { useChannelsQuery } from '../../hooks/channels/useChannelsQuery';
import {
  useModelOpsMutations,
  useModelOpsSourcesQuery,
  type ModelOpsSource,
} from '../../hooks/models/useModelOpsQuery';
import {
  useSourceUpstreamMutations,
  useSourceUpstreamQuery,
} from '../../hooks/models/useSourceUpstream';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { TFunction } from 'i18next';
import type { ComponentProps } from 'react';
import './model-ops.css';
import './ledger.css';
import './source-pages.css';

const ns = 'page.console.modelOps.sourcePage';
const LIST = '/console/model-ops/sources';

type PageTab = SourceTab | 'records';
const TABS: PageTab[] = ['models', 'connection', 'routing', 'sync', 'records'];

type EditingChannel = ComponentProps<typeof SourceForm>['editingChannel'];

const hostOf = (endpoint: string) => {
  try {
    return new URL(endpoint).host;
  } catch {
    return endpoint.replace(/^https?:\/\//, '').split('/')[0];
  }
};

/** The pill beside a source's name: whether it is serving right now. */
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
  return (
    <span className={`src-pill is-${tone}`}>
      <span aria-hidden='true'>● </span>
      {tone === 'idle'
        ? t(`${ns}.state.idle`, { defaultValue: '暂无流量' })
        : tone === 'bad'
          ? t(`${ns}.state.bad`, { defaultValue: '异常' })
          : tone === 'warn'
            ? t(`${ns}.state.warn`, { defaultValue: '不稳' })
            : t(`${ns}.state.ok`, { defaultValue: '正常' })}
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

/** 记录: the source's latest requests, newest first, from the usage log. */
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
          {t(`${ns}.records.tokens`, { defaultValue: 'tokens 入/出' })}
        </span>
        <span className='src-th'>
          {t(`${ns}.records.note`, { defaultValue: '说明' })}
        </span>
      </div>
      {query.isLoading ? (
        <div className='src-empty'>
          {t(`${ns}.records.loading`, { defaultValue: '正在读取记录' })}
        </div>
      ) : query.isError ? (
        <div className='src-empty'>
          {t(`${ns}.records.error`, { defaultValue: '没读到记录' })}
        </div>
      ) : rows.length === 0 ? (
        <div className='src-empty'>
          {t(`${ns}.records.empty`, {
            defaultValue: '最近 7 天没有经过这个来源的请求',
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
            defaultValue: '最近 7 天 · 最新 {{count}} 条',
          })}
        </span>
        <Link className='src-foot__link' to='/console/usage-logs'>
          {t(`${ns}.records.all`, { defaultValue: '去用量与日志看全部' })}
        </Link>
      </div>
    </section>
  );
}

/**
 * 来源 › 新建 and 来源 › one source. A source is edited on a page of its
 * own: the model table needs the width, and the long settings sit in tabs
 * rather than a dialog.
 */
export default function SourcePage() {
  const { id = '' } = useParams();
  const creating = id === 'new';
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const channels = useChannelsQuery({ listEnabled: false });
  const { t } = channels;
  const catalog = useSourceCatalog(channels.channelModelOptions);
  const ops = useModelOpsMutations(t);
  const sourcesQuery = useModelOpsSourcesQuery('', 48);
  const source = creating
    ? null
    : ((sourcesQuery.data?.items ?? []).find(
        (item) => String(item.id) === id,
      ) ?? null);
  const upstreamQuery = useSourceUpstreamQuery(creating ? null : id);
  const upstream = upstreamQuery.data ?? null;
  const { check, resolve } = useSourceUpstreamMutations(creating ? null : id);

  const rawTab = searchParams.get('tab') as PageTab | null;
  const tab: PageTab = rawTab && TABS.includes(rawTab) ? rawTab : 'models';
  const setTab = (next: PageTab) => {
    const params = new URLSearchParams(searchParams);
    if (next === 'models') params.delete('tab');
    else params.set('tab', next);
    setSearchParams(params, { replace: true });
  };

  useEffect(() => {
    if (!creating) window.scrollTo({ top: 0 });
  }, [creating, id]);

  const crumb = (
    <nav
      aria-label={t(`${ns}.crumb`, { defaultValue: '位置' })}
      className='src-crumb'
    >
      <Link to={LIST}>{t(`${ns}.list`, { defaultValue: '来源' })}</Link>
      <span aria-hidden='true'> / </span>
      <span className='src-crumb__here'>
        {creating
          ? t(`${ns}.newTitle`, { defaultValue: '新建来源' })
          : source?.name || id}
      </span>
    </nav>
  );

  if (creating) {
    return (
      <ConsolePage
        appearance='review'
        badge={
          <span className='src-hint src-title-hint'>
            {t(`${ns}.newHint`, {
              defaultValue:
                '一页填完：连上、测通、挑模型。三段从上往下，前一段没完成，后一段是灰的。',
            })}
          </span>
        }
        className='src-page src-page--create'
        description={crumb}
        title={t(`${ns}.newTitle`, { defaultValue: '新建来源' })}
      >
        <SourceForm
          catalog={catalog}
          channels={channels}
          editingChannel={null}
          layout='create'
          onCancel={() => navigate(LIST)}
          onSaved={async (ids) => {
            const created = ids[0];
            if (!created) {
              navigate(LIST);
              return;
            }
            /* Read the upstream list once now, so the new source's page
               opens on what the upstream offers rather than an empty
               check. A failure here is shown on that page. */
            try {
              await API.post(
                `/api/model_ops/sources/${created}/upstream/check`,
                undefined,
                {
                  skipErrorHandler: true,
                },
              );
            } catch {
              /* The page shows the check's error. */
            }
            navigate(`${LIST}/${created}`, { replace: true });
          }}
        />
      </ConsolePage>
    );
  }

  const settings = upstream?.settings;
  const interval = settings?.interval_hours ?? 0;
  const checkedAt = upstream?.state?.checked_at;
  const syncLine = interval
    ? t(`${ns}.syncLine`, {
        defaultValue: '每 {{hours}} 小时检查上游 · 上次 {{last}}',
        hours: interval,
        last: formatCheckTime(checkedAt),
      })
    : checkedAt
      ? t(`${ns}.syncLineOff`, {
          defaultValue: '不定时检查上游 · 上次 {{last}}',
          last: formatCheckTime(checkedAt),
        })
      : t(`${ns}.syncLineNever`, { defaultValue: '不定时检查上游' });

  const tabLabel: Record<PageTab, string> = {
    connection: t(`${ns}.tab.connection`, { defaultValue: '连接' }),
    models: t(`${ns}.tab.models`, { defaultValue: '模型' }),
    records: t(`${ns}.tab.records`, { defaultValue: '记录' }),
    routing: t(`${ns}.tab.routing`, { defaultValue: '线路' }),
    sync: t(`${ns}.tab.sync`, { defaultValue: '同步' }),
  };

  const meta = source
    ? [
        `#${source.internal_id}`,
        sourceAdapterLabel(source, t),
        hostOf(source.endpoint || source.base_url || ''),
      ]
        .filter(Boolean)
        .join(' · ')
    : '';

  return (
    <ConsolePage
      actions={
        <>
          <Button
            disabled={!source}
            loading={ops.testSourceMutation.isPending}
            size='sm'
            variant='secondary'
            onClick={() => source && ops.testSourceMutation.mutate(source.id)}
          >
            <Zap size={14} />
            {t(`${ns}.test`, { defaultValue: '测试连接' })}
          </Button>
          <Button
            loading={check.isPending}
            size='sm'
            variant='secondary'
            onClick={() => check.mutate()}
          >
            <RefreshCw size={14} />
            {t(`${ns}.checkNow`, { defaultValue: '立即检查上游' })}
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
        </>
      }
      appearance='review'
      badge={
        <>
          {meta ? (
            <span className='src-mono src-hint src-title-meta'>{meta}</span>
          ) : null}
          {source ? <SourceStatePill source={source} t={t} /> : null}
        </>
      }
      className='src-page'
      description={crumb}
      tabs={
        <div className='src-tabs'>
          <Tabs value={tab} onValueChange={(value) => setTab(value as PageTab)}>
            <TabsList variant='line'>
              {TABS.map((key) => (
                <TabsTrigger key={key} value={key}>
                  {tabLabel[key]}
                  {key === 'models' && source ? (
                    <span className='src-num src-tabs__count'>
                      {source.model_count}
                    </span>
                  ) : null}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <span className='src-hint src-tabs__aside'>{syncLine}</span>
        </div>
      }
      title={source?.name || (sourcesQuery.isLoading ? '…' : id)}
    >
      {tab === 'records' ? (
        <SourceRecords internalId={source?.internal_id} t={t} />
      ) : null}
      {/* The form stays mounted under 记录, so edits made on the other tabs
          survive a look at the log. */}
      <div className='src-formhost' hidden={tab === 'records'}>
        <SourceForm
          catalog={catalog}
          channels={channels}
          editingChannel={{ id } as unknown as EditingChannel}
          key={id}
          layout='page'
          resolving={resolve.isPending}
          tab={tab === 'records' ? 'models' : tab}
          upstream={upstream}
          onCancel={() => navigate(LIST)}
          onResolve={(body) => resolve.mutate(body)}
          onSaved={() => undefined}
          onTabChange={setTab}
        />
      </div>
    </ConsolePage>
  );
}
