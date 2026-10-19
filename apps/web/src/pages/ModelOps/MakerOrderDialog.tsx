import { GripVertical } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button, Modal } from '../../components/ui';
import { ProviderLogo } from '../../components/ui/provider-icons';
import {
  useModelInfoMutations,
  useModelMakersQuery,
  type ModelMaker,
} from '../../hooks/models/useModelInfoQuery';
import { makerBrand, makerFullLabel } from '../../lib/model-makers';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.makers';

type Props = {
  onOpenChange: (open: boolean) => void;
  open: boolean;
  t: TFunction;
};

/**
 * 厂商顺序 — the one order the plaza, model status, the connect dialog and the
 * ledger all read. Ranked makers are dragged into place (or moved with ↑/↓
 * on a focused handle); makers left unranked follow, by name.
 */
export default function MakerOrderDialog({ onOpenChange, open, t }: Props) {
  const makersQuery = useModelMakersQuery(open);
  const { makerOrderMutation } = useModelInfoMutations(t);
  const [ranked, setRanked] = useState<string[]>([]);
  const [dragging, setDragging] = useState<string | null>(null);
  // A callback ref: the modal body mounts after this component renders.
  const [list, setList] = useState<HTMLDivElement | null>(null);

  const makers = makersQuery.data?.makers || [];
  const byKey = new Map(makers.map((maker) => [maker.key, maker]));
  const unranked = makers
    .filter((maker) => !ranked.includes(maker.key))
    .sort((a, b) => a.label.localeCompare(b.label));

  useEffect(() => {
    if (open && makersQuery.data) setRanked(makersQuery.data.order);
  }, [open, makersQuery.data]);

  const moveTo = (key: string, index: number | null) => {
    setRanked((current) => {
      const without = current.filter((item) => item !== key);
      if (index === null) return without;
      const at = Math.max(0, Math.min(index, without.length));
      return [...without.slice(0, at), key, ...without.slice(at)];
    });
  };

  // base-ui popups swallow React keydown handlers inside them, so the
  // handles' arrow keys are wired as a native listener on the list.
  useEffect(() => {
    if (!list || !open) return;
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const key = target?.dataset?.makerKey;
      if (!key || (event.key !== 'ArrowUp' && event.key !== 'ArrowDown')) {
        return;
      }
      event.preventDefault();
      setRanked((current) => {
        const index = current.indexOf(key);
        if (index < 0) {
          // An unranked maker moves up into the ranked list's end.
          return event.key === 'ArrowUp' ? [...current, key] : current;
        }
        const next = event.key === 'ArrowUp' ? index - 1 : index + 1;
        if (next >= current.length) {
          return current.filter((item) => item !== key);
        }
        if (next < 0) return current;
        const copy = [...current];
        copy.splice(index, 1);
        copy.splice(next, 0, key);
        return copy;
      });
      window.requestAnimationFrame(() => {
        list.querySelector<HTMLElement>(`[data-maker-key="${key}"]`)?.focus();
      });
    };
    list.addEventListener('keydown', onKey);
    return () => list.removeEventListener('keydown', onKey);
  }, [list, open]);

  const countLabel = (maker: ModelMaker | undefined) =>
    maker && maker.models > 0
      ? t(`${ns}.modelCount`, {
          count: maker.models,
          defaultValue: '{{count}} 个模型',
        })
      : t(`${ns}.notListed`, { defaultValue: '未上架' });

  const item = (key: string, index: number | null) => {
    const maker = byKey.get(key);
    return (
      <li
        className={`mo-maker${index === null ? ' is-unranked' : ''}${dragging === key ? ' is-dragging' : ''}`}
        draggable
        key={key}
        onDragEnd={() => setDragging(null)}
        onDragOver={(event) => event.preventDefault()}
        onDragStart={(event) => {
          setDragging(key);
          event.dataTransfer.effectAllowed = 'move';
          event.dataTransfer.setData('text/plain', key);
        }}
        onDrop={(event) => {
          event.preventDefault();
          event.stopPropagation();
          const dropped = event.dataTransfer.getData('text/plain') || dragging;
          if (!dropped || dropped === key) return;
          moveTo(dropped, index === null ? null : ranked.indexOf(key));
          setDragging(null);
        }}
      >
        <button
          aria-label={t(`${ns}.drag`, {
            defaultValue: '拖动 {{name}}，或用上下方向键移动',
            name: makerFullLabel(key, t),
          })}
          className='mo-maker__handle'
          data-maker-key={key}
          type='button'
        >
          <GripVertical aria-hidden='true' size={15} />
        </button>
        <span className='mo-maker__rank'>
          {index === null ? '—' : index + 1}
        </span>
        <span className='mo-maker__name'>
          <ProviderLogo brand={makerBrand(key)} decorative size={18} />
          {makerFullLabel(key, t)}
        </span>
        <span className='mo-maker__count'>{countLabel(maker)}</span>
      </li>
    );
  };

  return (
    <Modal
      busy={makerOrderMutation.isPending}
      description={t(`${ns}.description`, {
        defaultValue:
          '模型广场、模型状态、接入弹窗和这里的台账都按这个顺序排；每家厂商内按发布日期从新到旧。',
      })}
      footer={
        <>
          <Button variant='secondary' onClick={() => onOpenChange(false)}>
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={!makersQuery.data}
            loading={makerOrderMutation.isPending}
            variant='primary'
            onClick={() =>
              makerOrderMutation.mutate(ranked, {
                onSuccess: () => onOpenChange(false),
              })
            }
          >
            {t(`${ns}.save`, { defaultValue: '保存顺序' })}
          </Button>
        </>
      }
      open={open}
      title={t(`${ns}.title`, { defaultValue: '厂商顺序' })}
      onOpenChange={onOpenChange}
      body={
        <div className='mo-makers' ref={setList}>
          {makersQuery.isPending ? (
            <span className='mo-state'>
              <span aria-hidden='true' className='mo-state__spinner' />
              {t(`${ns}.loading`, { defaultValue: '正在读取厂商' })}
            </span>
          ) : (
            <>
              <ol
                className='mo-makers__list'
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  const dropped = event.dataTransfer.getData('text/plain');
                  if (dropped) moveTo(dropped, ranked.length);
                  setDragging(null);
                }}
              >
                {ranked.map((key, index) => item(key, index))}
              </ol>
              <div
                className='mo-makers__rest'
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  const dropped = event.dataTransfer.getData('text/plain');
                  if (dropped) moveTo(dropped, null);
                  setDragging(null);
                }}
              >
                <div className='mo-makers__rest-label'>
                  {t(`${ns}.unranked`, {
                    defaultValue: '未排序 · 排在最后，按名字',
                  })}
                </div>
                <ol className='mo-makers__list'>
                  {unranked.map((maker) => item(maker.key, null))}
                </ol>
              </div>
            </>
          )}
        </div>
      }
    />
  );
}
