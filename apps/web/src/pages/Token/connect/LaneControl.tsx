/** The lane control that sits under one model.
 *
 *  A Key used to carry one lane per vendor, and the connect dialog showed a
 *  single switch for whichever vendor the default model happened to belong
 *  to. That hid the case this control exists for: one tool's models come from
 *  several vendors, and a lane that is real for one of them can be missing,
 *  or reserved for another client, on the next. So each model slot asks the
 *  question for itself, and asks it only when there is something to answer.
 */

import { cn } from '../../../lib/cn';
import type { Lane, LaneOffer, PickerModel } from './modelPicker';
import type { TFunction } from 'i18next';

const CLIENT_NAMES: Record<string, string> = {
  claude_code: 'Claude Code',
  codex: 'Codex',
};

export const laneClientName = (client?: string): string =>
  (client && CLIENT_NAMES[client]) || client || '';

export function LaneControl({
  compact,
  disabled,
  labelledBy,
  offer,
  onChange,
  t,
  value,
}: {
  /** Row variant: the price lives in the row's own column, so the buttons
   *  carry their two words alone. */
  compact?: boolean;
  disabled?: boolean;
  labelledBy?: string;
  offer: LaneOffer;
  onChange: (lane: Lane) => void;
  t: TFunction;
  value: Lane;
}) {
  const laneName = (lane: Lane) =>
    lane === 'direct'
      ? t('page.console.token.form.laneDirect', { defaultValue: '稳定' })
      : t('page.console.token.form.laneCheap', { defaultValue: 'Agent' });

  if (offer.kind === 'direct-only') {
    return (
      <span className='tok-lane__only'>
        <span className='tok-lane__dot' aria-hidden='true' />
        {t('page.console.token.connect.laneDirectOnly', {
          defaultValue: '仅稳定线路',
        })}
        {offer.directPrice ? (
          <span className='tok-lane__price'>{offer.directPrice}</span>
        ) : null}
      </span>
    );
  }

  if (offer.kind === 'agent-lane-only') {
    return (
      <span className='tok-lane__only'>
        <span className='tok-lane__dot' aria-hidden='true' />
        {t('page.console.token.connect.laneCheapOnly', {
          defaultValue: '仅 Agent 专线',
        })}
        {offer.cheapPrice ? (
          <span className='tok-lane__price'>{offer.cheapPrice}</span>
        ) : null}
      </span>
    );
  }

  const locked = offer.kind === 'client-locked';
  // The Key still says cheap for a model this tool cannot reach it on. The
  // switch stays live so the user can move it, and the row turns to warn.
  const conflict = locked && value === 'cheap';
  const priceOf = (lane: Lane) =>
    lane === 'direct' ? offer.directPrice : offer.cheapPrice;

  return (
    <div className='tok-lane'>
      <div
        aria-labelledby={labelledBy}
        className={cn('tok-lane__seg', conflict && 'is-conflict')}
        role='radiogroup'
      >
        {(['direct', 'cheap'] as const).map((lane) => {
          const unreachable = locked && lane === 'cheap' && !conflict;
          return (
            <button
              aria-checked={value === lane}
              aria-disabled={unreachable ? 'true' : undefined}
              className={cn('tok-lane__btn', value === lane && 'is-on')}
              data-owns-chrome='true'
              disabled={disabled || unreachable}
              key={lane}
              onClick={() => onChange(lane)}
              role='radio'
              type='button'
            >
              {laneName(lane)}
              {!compact && priceOf(lane) ? (
                <span className='tok-lane__price'>{priceOf(lane)}</span>
              ) : null}
            </button>
          );
        })}
      </div>
      {locked ? (
        <span className={cn('tok-lane__note', conflict && 'is-warn')}>
          {conflict
            ? t('page.console.token.connect.laneConflict', {
                defaultValue: '这个工具的请求会被拒绝，请改为稳定',
              })
            : t('page.console.token.connect.laneClientOnly', {
                client: laneClientName(offer.lockedTo),
                defaultValue: 'Agent 专线只接受 {{client}} 客户端',
              })}
        </span>
      ) : null}
    </div>
  );
}

/** The lane list for a tool with no model field of its own: a chat app picks
 *  its model inside the app, so the Key is where its lanes are decided. Only
 *  models with a lane worth choosing are listed — the rest would be a page of
 *  rows that all say the same thing. */
export function LaneTable({
  canUpdate,
  laneOf,
  models,
  offerFor,
  onChange,
  priceForModel,
  t,
  vendorLabelOf,
  vendorLaneOf,
}: {
  canUpdate: boolean;
  laneOf: (model: string) => Lane;
  models: PickerModel[];
  offerFor: (model: string) => LaneOffer;
  onChange: (model: string, lane: Lane) => void;
  priceForModel: (model: PickerModel) => string;
  t: TFunction;
  vendorLabelOf: (model: string) => string;
  vendorLaneOf: (model: string) => Lane;
}) {
  const groups: Array<{ label: string; models: PickerModel[] }> = [];
  models.forEach((model) => {
    const label = vendorLabelOf(model.id);
    const group = groups.find((entry) => entry.label === label);
    if (group) group.models.push(model);
    else groups.push({ label, models: [model] });
  });

  if (!groups.length) return null;

  const laneName = (lane: Lane) =>
    lane === 'direct'
      ? t('page.console.token.form.laneDirect', { defaultValue: '稳定' })
      : t('page.console.token.form.laneCheap', { defaultValue: 'Agent' });

  return (
    <div className='tok-connect__field tok-connect__extras'>
      <div className='tok-connect__label-row'>
        <span className='tok-connect__label'>
          {t('page.console.token.connect.laneTable', {
            defaultValue: '线路 · 这把 Key 上有 Agent 专线来源的模型',
          })}
        </span>
        <span className='tok-connect__note'>
          {t('page.console.token.connect.laneTableNote', {
            defaultValue: '只列出有得选的；其余模型只有稳定线路',
          })}
        </span>
      </div>
      <div className='tok-model__rows'>
        {groups.map((group) => (
          <div className='tok-model__group' key={group.label}>
            <div className='tok-model__group-head'>
              {group.label}
              <span className='tok-model__group-default'>
                {t('page.console.token.connect.laneVendorDefault', {
                  defaultValue: '厂商默认 {{lane}}',
                  lane: laneName(vendorLaneOf(group.models[0].id)),
                })}
              </span>
            </div>
            {group.models.map((model) => (
              <div className='tok-model__row' key={model.id}>
                <span className='tok-model__row-id'>{model.id}</span>
                <span className='tok-model__row-lane'>
                  <LaneControl
                    compact
                    disabled={!canUpdate}
                    offer={offerFor(model.id)}
                    onChange={(lane) => onChange(model.id, lane)}
                    t={t}
                    value={laneOf(model.id)}
                  />
                </span>
                <span className='tok-model__row-price'>
                  {priceForModel(model)}
                </span>
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
