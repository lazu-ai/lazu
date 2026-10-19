import { Plus } from 'lucide-react';
import { useState } from 'react';
import LaneForm from './LaneForm';
import { Button, Input, Modal, PendingBlock } from '../../components/ui';
import { isRoot } from '../../helpers/utils';
import {
  useChannelLanesQuery,
  useModelOpsDimensionMutations,
} from '../../hooks/models/useModelOpsDimensions';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

type View = { kind: 'list' } | { kind: 'lane'; slug: string } | { kind: 'new' };

/**
 * 线路: each lane's default price as a percent of the official price, and
 * the lanes themselves. Models follow their lane's ratio unless they set
 * their own, so changing one number here reprices every model that follows.
 */
export default function LanesDialog({ t }: { t: TFunction }) {
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<View>({ kind: 'list' });
  const [ratios, setRatios] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const lanesQuery = useChannelLanesQuery();
  const mutations = useModelOpsDimensionMutations(t);
  const lanes = lanesQuery.data ?? [];
  const selected =
    view.kind === 'lane'
      ? (lanes.find((lane) => lane.slug === view.slug) ?? null)
      : null;
  const back = () => setView({ kind: 'list' });
  const ratioOf = (slug: string) =>
    Number(lanes.find((lane) => lane.slug === slug)?.price_ratio) || 100;
  const changed = lanes.filter((lane) => {
    const raw = ratios[lane.slug];
    return (
      raw !== undefined &&
      raw.trim() !== '' &&
      Number(raw) !== ratioOf(lane.slug)
    );
  });
  const invalid = changed.some((lane) => {
    const value = Number(ratios[lane.slug]);
    return !Number.isFinite(value) || value <= 0 || value > 1000;
  });

  const close = () => {
    setOpen(false);
    setRatios({});
    back();
  };

  const saveRatios = async () => {
    setSaving(true);
    try {
      for (const lane of changed) {
        await mutations.saveLane.mutateAsync({
          ...lane,
          price_ratio: Number(ratios[lane.slug]),
        });
      }
      setRatios({});
    } catch {
      /* Reported by the mutation; the edits stay for a retry. */
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      busy={saving}
      className='mo-lanes-dialog'
      open={open}
      onOpenChange={(next) => {
        if (next) setOpen(true);
        else close();
      }}
      title={t(`${ns}.lanes.title`, { defaultValue: '线路' })}
      description={
        view.kind === 'list'
          ? t(`${ns}.lanes.ratioHint`, {
              defaultValue: '模型默认按线路比例定价。',
            })
          : undefined
      }
      body={
        view.kind === 'list' ? (
          lanesQuery.isPending ? (
            <PendingBlock height={132} />
          ) : (
            <div className='mo-lanedlg'>
              <div className='mo-lanedlg__row mo-lanedlg__row--head'>
                <span>
                  {t(`${ns}.lanes.colLane`, { defaultValue: '线路' })}
                </span>
                <span>
                  {t(`${ns}.price.colRatio`, { defaultValue: '官方价的' })}
                </span>
                <span>
                  {t(`${ns}.lanes.colModels`, { defaultValue: '模型' })}
                </span>
              </div>
              {lanes.map((lane) => (
                <div className='mo-lanedlg__row' key={lane.slug}>
                  <span className='mo-lanedlg__lane'>
                    <Button
                      size='sm'
                      variant='link'
                      onClick={() => setView({ kind: 'lane', slug: lane.slug })}
                    >
                      {lane.name}
                    </Button>
                    <span className='mo-sub'>
                      {t(`${ns}.lanes.sources`, {
                        count: lane.sources,
                        defaultValue: '{{count}} 个来源',
                      })}
                      {!lane.enabled
                        ? ` · ${t(`${ns}.lanes.stopped`, { defaultValue: '已停用' })}`
                        : ''}
                    </span>
                  </span>
                  <span className='mo-lanedlg__ratio'>
                    <Input
                      aria-label={t(`${ns}.lanes.ratioFor`, {
                        defaultValue: '{{name}}默认比例',
                        name: lane.name,
                      })}
                      disabled={!isRoot()}
                      inputMode='decimal'
                      value={ratios[lane.slug] ?? String(ratioOf(lane.slug))}
                      onChange={(event) =>
                        setRatios((current) => ({
                          ...current,
                          [lane.slug]: event.target.value,
                        }))
                      }
                    />
                    <span className='mo-lanedlg__unit'>%</span>
                  </span>
                  <span className='mo-sub'>
                    {t(`${ns}.lanes.modes`, {
                      defaultValue: '{{following}} 跟随 · {{fixed}} 固定',
                      fixed: lane.fixed ?? 0,
                      following: lane.following ?? 0,
                    })}
                  </span>
                </div>
              ))}
              {changed.map((lane) => (
                <p className='mo-notice mo-notice--warn' key={lane.slug}>
                  {t(`${ns}.lanes.repriceNote`, {
                    count: lane.following ?? 0,
                    defaultValue:
                      '{{name}} {{from}}% → {{to}}%，影响 {{count}} 个模型。',
                    from: ratioOf(lane.slug),
                    name: lane.name,
                    to: ratios[lane.slug],
                  })}
                </p>
              ))}
              {isRoot() ? (
                <div>
                  <Button
                    size='sm'
                    variant='secondary'
                    onClick={() => setView({ kind: 'new' })}
                  >
                    <Plus size={14} />
                    {t(`${ns}.lanes.add`, { defaultValue: '新线路' })}
                  </Button>
                </div>
              ) : null}
            </div>
          )
        ) : (
          <LaneForm
            busy={mutations.saveLane.isPending}
            canEdit={isRoot()}
            isNew={view.kind === 'new'}
            key={view.kind === 'lane' ? view.slug : 'new'}
            lane={selected}
            onCancel={back}
            onDelete={() => {
              if (!selected) return;
              mutations.deleteLane.mutate(selected.slug, { onSuccess: back });
            }}
            onSave={(next) => {
              mutations.saveLane.mutate(
                {
                  ...next,
                  builtin: selected?.builtin,
                  ever_enabled: selected?.ever_enabled,
                  sort: selected?.sort,
                },
                { onSuccess: back },
              );
            }}
            onToggleEnabled={(enabled) => {
              if (!selected) return;
              mutations.setLaneEnabled.mutate({ enabled, slug: selected.slug });
            }}
            t={t}
          />
        )
      }
      footer={
        view.kind === 'list' && isRoot() ? (
          <>
            <Button disabled={saving} variant='secondary' onClick={close}>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              disabled={!changed.length || invalid}
              loading={saving}
              variant='primary'
              onClick={() => void saveRatios()}
            >
              {t('common.save', { defaultValue: '保存' })}
            </Button>
          </>
        ) : undefined
      }
    >
      <Button size='sm' variant='secondary' onClick={() => setOpen(true)}>
        {t(`${ns}.lanes.title`, { defaultValue: '线路' })}
      </Button>
    </Modal>
  );
}
