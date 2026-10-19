import { useState } from 'react';
import { Button, Input } from '../../components/ui';
import type { ChannelLane } from '../../hooks/models/useModelOpsDimensions';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

const COLORS = ['evidence', 'warning', 'info', 'success', 'danger'];

const slugify = (value: string) =>
  value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32);

/**
 * One lane's definition, reached from the 线路 dialog. Only the footer
 * differs between lanes, because what you can do to a lane depends on whether
 * it has ever run.
 *
 * The asymmetry is the point: a lane that has carried traffic can be stopped
 * but never deleted (its prices and billing history must keep resolving),
 * while one created by mistake and never turned on is just noise to remove.
 */
export default function LaneForm({
  busy,
  canEdit,
  lane,
  isNew,
  onCancel,
  onDelete,
  onSave,
  onToggleEnabled,
  t,
}: {
  busy?: boolean;
  canEdit: boolean;
  isNew: boolean;
  lane: ChannelLane | null;
  onCancel: () => void;
  onDelete: () => void;
  onSave: (next: {
    slug: string;
    name: string;
    note: string;
    color: string;
  }) => void;
  onToggleEnabled: (enabled: boolean) => void;
  t: TFunction;
}) {
  const [name, setName] = useState(lane?.name ?? '');
  const [slug, setSlug] = useState(lane?.slug ?? '');
  const [slugTouched, setSlugTouched] = useState(!isNew);
  const [note, setNote] = useState(lane?.note ?? '');
  const [color, setColor] = useState(lane?.color ?? 'info');

  const effectiveSlug = slugTouched ? slug : slugify(name);
  const stopped = Boolean(lane && !lane.enabled);

  return (
    <>
      <div className='mo-dialog__body'>
        <label className='mo-dim-field'>
          <span className='mo-dim-field__label'>
            {t(`${ns}.lane.name`, { defaultValue: '名称' })}
            <span className='mo-sub'>
              {' · '}
              {t(`${ns}.lane.nameHint`, {
                defaultValue: '运营和用户看到的词',
              })}
            </span>
          </span>
          <Input
            disabled={!canEdit}
            onChange={(event) => setName(event.target.value)}
            value={name}
          />
        </label>

        <label className='mo-dim-field'>
          <span className='mo-dim-field__label'>
            {t(`${ns}.lane.slug`, { defaultValue: '标识' })}
            <span className='mo-sub'>
              {' · '}
              {t(`${ns}.lane.slugHint`, {
                defaultValue: '小写英文，建后不能改；Key 指定档位、账单里用它',
              })}
            </span>
          </span>
          <Input
            className='mo-mono'
            disabled={!canEdit || !isNew}
            onChange={(event) => {
              setSlugTouched(true);
              setSlug(slugify(event.target.value));
            }}
            value={effectiveSlug}
          />
        </label>

        <label className='mo-dim-field'>
          <span className='mo-dim-field__label'>
            {t(`${ns}.lane.note`, { defaultValue: '一句话说明' })}
            <span className='mo-sub'>
              {' · '}
              {t(`${ns}.lane.noteHint`, { defaultValue: '用户在选档时看到' })}
            </span>
          </span>
          <Input
            disabled={!canEdit}
            onChange={(event) => setNote(event.target.value)}
            value={note}
          />
        </label>

        <div className='mo-dim-field'>
          <span className='mo-dim-field__label'>
            {t(`${ns}.lane.color`, { defaultValue: '颜色' })}
          </span>
          <div className='mo-swatches'>
            {COLORS.map((option) => (
              <button
                aria-label={option}
                aria-pressed={color === option}
                className={`mo-swatch mo-swatch--${option}${color === option ? ' is-active' : ''}`}
                data-owns-chrome=''
                disabled={!canEdit}
                key={option}
                onClick={() => setColor(option)}
                type='button'
              />
            ))}
          </div>
        </div>

        {isNew ? (
          <div className='mo-effect'>
            <span className='mo-effect__title'>
              {t(`${ns}.lane.afterTitle`, { defaultValue: '建完之后' })}
            </span>
            <p>
              {t(`${ns}.lane.afterBody`, {
                defaultValue:
                  '折扣页多一列、模型页多一档售价、来源那一行的档位多一个选项。',
              })}
            </p>
            <p className='mo-sub'>
              {t(`${ns}.lane.afterHint`, {
                defaultValue:
                  '建好先是「未启用」：把来源挪进来、给模型定了这档的售价，才能开。',
              })}
            </p>
          </div>
        ) : (
          <div className='mo-effect'>
            <span className='mo-effect__title'>
              {t(`${ns}.lane.stateTitle`, { defaultValue: '这档现在的样子' })}
            </span>
            <p>
              {t(`${ns}.lane.stateBody`, {
                defaultValue:
                  '{{sources}} 个来源 · {{priced}} 个模型定了这档售价。',
                priced: lane?.priced ?? 0,
                sources: lane?.sources ?? 0,
              })}
            </p>
            <p className='mo-sub'>
              {stopped
                ? t(`${ns}.lane.stoppedHint`, {
                    defaultValue:
                      '已停用：新请求不再落到这档，已定的售价和折扣保留。',
                  })
                : t(`${ns}.lane.stopHint`, {
                    defaultValue:
                      '停用后：新请求不再落到这档，已定的售价和折扣保留。',
                  })}
            </p>
          </div>
        )}
      </div>

      <div className='mo-dialog__foot'>
        {isNew ? (
          <span />
        ) : lane?.builtin ? (
          <span className='mo-sub'>
            {t(`${ns}.lane.builtin`, { defaultValue: '内置档位，不能停用' })}
          </span>
        ) : lane?.ever_enabled ? (
          <Button
            disabled={!canEdit || busy}
            onClick={() => onToggleEnabled(!lane.enabled)}
            size='sm'
            variant={stopped ? 'secondary' : 'danger'}
          >
            {stopped
              ? t(`${ns}.lane.enable`, { defaultValue: '启用这档' })
              : t(`${ns}.lane.disable`, { defaultValue: '停用这档' })}
          </Button>
        ) : (
          <Button
            disabled={!canEdit || busy}
            onClick={onDelete}
            size='sm'
            variant='danger'
          >
            {t('common.delete', { defaultValue: '删除' })}
          </Button>
        )}
        <div className='mo-dialog__foot-actions'>
          <Button onClick={onCancel} size='sm' variant='secondary'>
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={!canEdit || busy || !name.trim() || !effectiveSlug}
            loading={busy}
            onClick={() =>
              onSave({
                color,
                name: name.trim(),
                note: note.trim(),
                slug: effectiveSlug,
              })
            }
            size='sm'
            variant='primary'
          >
            {t('common.save', { defaultValue: '保存' })}
          </Button>
        </div>
      </div>
    </>
  );
}
