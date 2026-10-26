import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { ErrorState } from '../../components/ui/error-state';
import { LoadingState } from '../../components/ui/loading-state';
import { Modal } from '../../components/ui/modal';
import {
  useSyncPreviewQuery,
  type SyncPreviewKind,
} from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.sync';

type SyncPreviewModalProps = {
  busy: boolean;
  kind: SyncPreviewKind | null;
  onConfirm: (kind: SyncPreviewKind) => void;
  onOpenChange: (open: boolean) => void;
  t: TFunction;
};

const TITLES: Record<SyncPreviewKind, string> = {
  litellm: '同步 LiteLLM 定价与能力',
  openrouter: '同步 OpenRouter 描述与标签',
  upstream: '同步上游目录',
};

/** Dry run in front of a catalog-wide write.
 *
 *  All three sync endpoints already ship a `/preview` sibling that nothing in
 *  the console called, so the buttons fired bulk writes blind. This shows what
 *  the write would touch and makes the operator confirm it. */
export default function SyncPreviewModal({
  busy,
  kind,
  onConfirm,
  onOpenChange,
  t,
}: SyncPreviewModalProps) {
  const preview = useSyncPreviewQuery(kind);
  const data = preview.data;

  const stats = () => {
    if (!data) return null;
    const chips: Array<{ label: string; value: number }> = [];
    if (kind === 'upstream') {
      chips.push({
        label: t(`${ns}.missing`, { defaultValue: '本地缺失' }),
        value: data.missing?.length || 0,
      });
      chips.push({
        label: t(`${ns}.conflicts`, { defaultValue: '字段冲突' }),
        value: data.conflicts?.length || 0,
      });
    } else {
      chips.push({
        label: t(`${ns}.matched`, { defaultValue: '可匹配' }),
        value: data.matched_count || 0,
      });
      chips.push({
        label: t(`${ns}.unmatched`, { defaultValue: '匹配不到' }),
        value: data.unmatched_count || 0,
      });
      chips.push({
        label: t(`${ns}.localTotal`, { defaultValue: '本地模型' }),
        value: data.local_total || 0,
      });
    }
    return (
      <div className='model-ops__preview-stats'>
        {chips.map((chip) => (
          <Badge key={chip.label}>{`${chip.label} ${chip.value}`}</Badge>
        ))}
      </div>
    );
  };

  const willWrite =
    kind === 'upstream'
      ? (data?.missing?.length || 0) + (data?.conflicts?.length || 0)
      : data?.matched_count || 0;

  return (
    <Modal
      className='lazu-modal-content--wide'
      description={t(`${ns}.description`, {
        defaultValue: '先看清这次同步会改什么，再决定是否写入。',
      })}
      open={Boolean(kind)}
      title={
        kind
          ? t(`${ns}.title.${kind}`, { defaultValue: TITLES[kind] })
          : undefined
      }
      body={
        <div className='model-ops__preview'>
          {preview.isLoading ? <LoadingState /> : null}
          {preview.isError ? (
            <ErrorState
              error={preview.error}
              title={t(`${ns}.failed`, { defaultValue: '预览失败' })}
            />
          ) : null}
          {data ? stats() : null}
          {kind === 'upstream' && data?.missing?.length ? (
            <div className='model-ops__preview-group'>
              <h4>{t(`${ns}.willCreate`, { defaultValue: '将新建的模型' })}</h4>
              {data.missing.map((name) => (
                <div className='model-ops__preview-row' key={name}>
                  {name}
                </div>
              ))}
            </div>
          ) : null}
          {kind === 'upstream' && data?.conflicts?.length ? (
            <div className='model-ops__preview-group'>
              <h4>
                {t(`${ns}.willOverwrite`, {
                  defaultValue: '与上游不一致的字段（同步会以上游为准）',
                })}
              </h4>
              {data.conflicts.map((conflict) => (
                <div
                  className='model-ops__preview-row'
                  key={conflict.model_name}
                >
                  <b>{conflict.model_name}</b>{' '}
                  <span className='model-ops__preview-field'>
                    {(conflict.fields || [])
                      .map((field) => field.field)
                      .join(', ')}
                  </span>
                </div>
              ))}
            </div>
          ) : null}
          {kind !== 'upstream' && data?.unmatched?.length ? (
            <div className='model-ops__preview-group'>
              <h4>
                {t(`${ns}.unmatchedList`, {
                  defaultValue: '匹配不到上游数据、本次不会变更的模型',
                })}
              </h4>
              {data.unmatched.map((name) => (
                <div className='model-ops__preview-row' key={name}>
                  {name}
                </div>
              ))}
            </div>
          ) : null}
        </div>
      }
      footer={
        <>
          <Button variant='secondary' onClick={() => onOpenChange(false)}>
            {t('legacy.i18n.cancel.625fb26b4b', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={!data || willWrite === 0}
            loading={busy}
            variant='primary'
            onClick={() => kind && onConfirm(kind)}
          >
            {t(`${ns}.confirm`, {
              count: willWrite,
              defaultValue: '确认同步（影响 {{count}} 项）',
            })}
          </Button>
        </>
      }
      onOpenChange={onOpenChange}
    />
  );
}
