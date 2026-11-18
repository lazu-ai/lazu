import {
  ArrowUpRight,
  ChevronLeft,
  ChevronRight,
  RefreshCw,
  X,
  Zap,
} from 'lucide-react';
import { useEffect, useState, type ComponentProps } from 'react';
import { Link } from 'react-router-dom';
import { formatRate, healthTone } from './formatters';
import { useSourceCatalog } from './sourceCatalog';
import SourceForm, { SOURCE_FORM_ID } from './SourceForm';
import { isObservingSource, sourceAdapterLabel } from './sourceStatus';
import { sourceTypeLabel } from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { IconButton } from '../../components/ui/icon-button';
import { Modal, ModalFrame, ModalTitle } from '../../components/ui/modal';
import { resolveProviderBrand } from '../../components/ui/provider-brands';
import { ProviderLogo } from '../../components/ui/provider-icons';
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
import type { SourceModelStatusView } from './SourceModelsTable';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.sourcePage';

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

type SourceDialogProps = {
  channels: ReturnType<typeof useChannelsQuery>;
  hasNext: boolean;
  hasPrev: boolean;
  initialAdvancedOpen?: boolean;
  initialModelView?: SourceModelStatusView;
  isSourcesLoading?: boolean;
  onClose: () => void;
  onCreated: (createdId?: string) => void;
  onNext: () => void;
  onPrev: () => void;
  onSaved: () => void;
  source: ModelOpsSource | null;
  sourceId: string;
};

/**
 * Tabless vertical scroll sheet (07-A) over the Sources ledger:
 * auto-hugs height up to min(86vh, 860px) at max-width 700px with one unified
 * body scroll, pinned header with request-log link, and pinned footer.
 */
export default function SourceDialog({
  channels,
  hasNext,
  hasPrev,
  initialAdvancedOpen = false,
  initialModelView,
  isSourcesLoading,
  onClose,
  onCreated,
  onNext,
  onPrev,
  onSaved,
  source,
  sourceId,
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
  const [isDirty, setIsDirty] = useState(false);
  const [pendingNav, setPendingNav] = useState<
    'close' | 'next' | 'prev' | null
  >(null);

  useEffect(() => {
    setServedCount(null);
    setIsDirty(false);
    setPendingNav(null);
  }, [sourceId]);

  const guardAction = (action: 'close' | 'next' | 'prev', run: () => void) => {
    if (isDirty) {
      setPendingNav(action);
      return;
    }
    run();
  };

  if (creating) {
    return (
      <ModalFrame
        busy={channels.saveChannelMutation.isPending}
        className='mo-dialog src-dialog src-dialog--create'
        hideClose
        open
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
      >
        <div className='mo-dialog__head src-dialog__head src-dialog__head--create'>
          <div className='mo-dialog__title-block'>
            <ModalTitle className='mo-dialog__title'>
              {t(`${ns}.newTitle`, { defaultValue: '新建来源' })}
            </ModalTitle>
            <div className='mo-sub'>
              {t(`${ns}.newSubtitle`, {
                defaultValue: '先验证连接并拉取模型，勾选后再供货',
              })}
            </div>
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

  const displayCount = servedCount ?? source?.model_count ?? 0;
  const metaParts = source
    ? [
        `#${source.internal_id}`,
        sourceTypeLabel(source.source_type, t),
        sourceAdapterLabel(source, t),
        hostOf(source.endpoint || source.base_url || ''),
      ].filter(Boolean)
    : [];
  const logsHref = source?.internal_id
    ? `/console/usage?channel=${encodeURIComponent(String(source.internal_id))}&scope=platform`
    : '/console/usage?scope=platform';

  return (
    <>
      <ModalFrame
        busy={channels.saveChannelMutation.isPending}
        className='mo-dialog src-dialog'
        hideClose
        open
        onOpenChange={(open) => {
          if (!open) guardAction('close', onClose);
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
                <span>
                  {source?.name || (isSourcesLoading ? '…' : sourceId)}
                </span>
                {source ? <SourceStatePill source={source} t={t} /> : null}
                {isDirty ? (
                  <span className='src-dirty-pill'>
                    {t(`${ns}.unsavedBadge`, { defaultValue: '有未保存修改' })}
                  </span>
                ) : null}
              </ModalTitle>
              <div className='src-dialog__sub'>
                {metaParts.length ? (
                  <span className='src-mono'>{metaParts.join(' · ')}</span>
                ) : null}
                {metaParts.length ? <span aria-hidden='true'>·</span> : null}
                <Link className='src-dialog__loglink' to={logsHref}>
                  {source && source.health.request_count > 0
                    ? t(`${ns}.viewLogsCount`, {
                        count: source.health.request_count,
                        defaultValue: '查看请求日志 ({{count}})',
                      })
                    : t(`${ns}.viewLogs`, { defaultValue: '查看请求日志' })}
                  <ArrowUpRight aria-hidden='true' size={12} />
                </Link>
              </div>
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
              onClick={() => guardAction('prev', onPrev)}
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
              onClick={() => guardAction('next', onNext)}
            >
              <ChevronRight size={16} />
            </IconButton>
            <IconButton
              aria-label={t('common.close', { defaultValue: '关闭' })}
              size='sm'
              variant='ghost'
              onClick={() => guardAction('close', onClose)}
            >
              <X size={16} />
            </IconButton>
          </div>
        </div>

        <div className='mo-dialog__body src-dialog__body'>
          <SourceForm
            catalog={catalog}
            channels={channels}
            editingChannel={{ id: sourceId } as unknown as EditingChannel}
            initialAdvancedOpen={initialAdvancedOpen}
            initialModelView={initialModelView}
            key={sourceId}
            keyFingerprint={source?.key_fingerprint}
            layout='page'
            resolving={resolve.isPending}
            upstream={upstream}
            onCancel={() => guardAction('close', onClose)}
            onDirtyChange={setIsDirty}
            onResolve={(body) => resolve.mutate(body)}
            onSaved={() => {
              setIsDirty(false);
              onSaved();
            }}
            onServedCountChange={setServedCount}
          />
        </div>

        <div className='mo-dialog__foot src-dialog__foot'>
          <div className='src-dialog__foot-left'>
            {source ? (
              <Button
                className={
                  source.status === 1 ? 'src-btn--danger-outline' : undefined
                }
                loading={ops.sourceStatusMutation.isPending}
                size='sm'
                type='button'
                variant='secondary'
                onClick={() =>
                  ops.sourceStatusMutation.mutate({
                    enabled: source.status !== 1,
                    source,
                  })
                }
              >
                {source.status === 1
                  ? t(`${ns}.disableSource`, { defaultValue: '停用该来源' })
                  : t(`${ns}.enableSource`, { defaultValue: '启用该来源' })}
              </Button>
            ) : null}
            <span className='src-hint'>
              {t(`${ns}.footServing`, { defaultValue: '供货' })}{' '}
              <strong className='src-mono src-ink'>{displayCount}</strong>{' '}
              {t(`${ns}.footServingUnit`, { defaultValue: '个' })}
            </span>
          </div>
          <div className='src-dialog__foot-actions'>
            <Button
              size='sm'
              type='button'
              variant='secondary'
              onClick={() => guardAction('close', onClose)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              form={SOURCE_FORM_ID}
              loading={channels.saveChannelMutation.isPending}
              size='sm'
              type='submit'
              variant='primary'
            >
              {t(`${ns}.save`, { defaultValue: '保存' })}
            </Button>
          </div>
        </div>
      </ModalFrame>

      <Modal
        open={pendingNav !== null}
        title={t(`${ns}.discardTitle`, { defaultValue: '放弃未保存的修改？' })}
        description={t(`${ns}.discardDescription`, {
          defaultValue: '当前来源有未保存的配置改动，离开后改动将丢失。',
        })}
        footer={
          <>
            <Button
              size='sm'
              variant='secondary'
              onClick={() => setPendingNav(null)}
            >
              {t(`${ns}.keepEditing`, { defaultValue: '继续编辑' })}
            </Button>
            <Button
              size='sm'
              variant='primary'
              onClick={() => {
                const action = pendingNav;
                setPendingNav(null);
                setIsDirty(false);
                if (action === 'prev') onPrev();
                else if (action === 'next') onNext();
                else onClose();
              }}
            >
              {t(`${ns}.discardConfirm`, { defaultValue: '放弃修改' })}
            </Button>
          </>
        }
        onOpenChange={(open) => {
          if (!open) setPendingNav(null);
        }}
      />
    </>
  );
}
