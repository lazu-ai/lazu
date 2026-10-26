import { formatRate } from './formatters';
import { Button } from '../../components/ui/button';
import { Modal } from '../../components/ui/modal';
import { Tooltip } from '../../components/ui/tooltip';
import type {
  ModelOpsObservation,
  ModelOpsObservationCheck,
  ModelOpsObservationCheckKind,
  ModelOpsObservationState,
} from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

/* A new source's routes sit in inventory for an observation period before
   they may carry traffic: probed like every route, plus three daily checks a
   liveness probe cannot make (billing, long context, tools). This file draws
   where a watched route stands, the same way on the source ledger and on a
   model's tier card, and the one control that ends the watch. */

const ns = 'page.console.modelOps';

const DAY_MS = 24 * 60 * 60 * 1000;

/** A success-rate bar as a percentage: 0.95 reads "95%", 0.975 "97.5%". */
export const formatBarRate = (rate: number | null | undefined) =>
  rate == null || !Number.isFinite(rate)
    ? '—'
    : `${Number((rate * 100).toFixed(1))}%`;

/** The success-rate bar of an observation, as a percentage. */
export const observationSuccessBar = (observation: ModelOpsObservation) =>
  formatBarRate(observation.min_success_rate);

/** "长上下文 32k": the long-context check named with its input size. */
export const longContextLabel = (
  observation: ModelOpsObservation | null | undefined,
  t: TFunction,
) => {
  const tokens = observation ? observation.long_context_tokens : null;
  if (!tokens || !Number.isFinite(tokens)) return checkName('long_context', t);
  return t(`${ns}.observation.checkLongSized`, {
    defaultValue: '长上下文 {{size}}',
    size:
      tokens >= 1000
        ? `${Math.round(tokens / 1000)}k`
        : tokens.toLocaleString(),
  });
};

export const observationCheck = (
  observation: ModelOpsObservation,
  kind: ModelOpsObservationCheckKind,
): ModelOpsObservationCheck | undefined =>
  observation.checks.find((check) => check.kind === kind);

/** "第 4 天 / 5", or "第 5 天 · 期满" once the observation is over. */
export const observationProgress = (
  observation: ModelOpsObservation,
  t: TFunction,
) =>
  observation.complete
    ? t(`${ns}.observation.dayDone`, {
        defaultValue: '第 {{total}} 天 · 期满',
        total: observation.days_required,
      })
    : t(`${ns}.observation.day`, {
        day: observation.day,
        defaultValue: '第 {{day}} 天 / {{total}}',
        total: observation.days_required,
      });

/** Whole days left in the observation, never less than one while it runs. */
const daysLeft = (observation: ModelOpsObservation) => {
  const started = new Date(observation.started_at).getTime();
  if (!Number.isFinite(started)) {
    return Math.max(1, observation.days_required - observation.day + 1);
  }
  const end = started + observation.days_required * DAY_MS;
  return Math.max(1, Math.ceil((end - Date.now()) / DAY_MS));
};

const checkName = (kind: ModelOpsObservationCheckKind, t: TFunction) =>
  ({
    billing: t(`${ns}.observation.checkBilling`, { defaultValue: '计费比值' }),
    long_context: t(`${ns}.observation.checkLong`, {
      defaultValue: '长上下文',
    }),
    tools: t(`${ns}.observation.checkTools`, { defaultValue: '工具调用' }),
  })[kind];

/** The one-word result the ledger prints for a check. */
const checkWord = (
  check: ModelOpsObservationCheck | undefined,
  t: TFunction,
) => {
  if (!check || check.state === 'pending') {
    return t(`${ns}.observation.pending`, { defaultValue: '待测' });
  }
  if (check.state === 'na') {
    return t(`${ns}.observation.na`, { defaultValue: '不适用' });
  }
  if (check.kind === 'billing' && check.value != null) {
    return check.value.toFixed(2);
  }
  if (check.state === 'pass') {
    return t(`${ns}.observation.pass`, { defaultValue: '通过' });
  }
  if (check.kind === 'long_context' && check.detail?.startsWith('truncated')) {
    return t(`${ns}.observation.truncated`, { defaultValue: '截断' });
  }
  return t(`${ns}.observation.fail`, { defaultValue: '未过' });
};

/** "通过 · 3 / 3" style figure for the tier card and the dialog. */
const checkRuns = (
  check: ModelOpsObservationCheck | undefined,
  t: TFunction,
) => {
  if (!check || check.state === 'pending') {
    return t(`${ns}.observation.pending`, { defaultValue: '待测' });
  }
  if (check.state === 'na') {
    return t(`${ns}.observation.na`, { defaultValue: '不适用' });
  }
  if (check.kind === 'billing') return checkWord(check, t);
  return `${check.passed} / ${check.runs}`;
};

const toneOf = (state: ModelOpsObservationState | undefined) =>
  state === 'pass' ? 'ok' : state === 'fail' ? 'bad' : 'wait';

/** Why a watched source or route cannot go live yet, in one or two sentences. */
export const observationBlockers = (
  observation: ModelOpsObservation,
  t: TFunction,
) => {
  const parts: string[] = [];
  if (!observation.complete) {
    parts.push(
      t(`${ns}.observation.blockDays`, {
        count: daysLeft(observation),
        defaultValue: '还差 {{count}} 天',
      }),
    );
  }
  if (observation.success_state === 'fail') {
    parts.push(
      t(`${ns}.observation.blockSuccess`, {
        defaultValue: '成功率低于 {{rate}}',
        rate: observationSuccessBar(observation),
      }),
    );
  } else if (observation.success_state === 'pending') {
    parts.push(
      t(`${ns}.observation.blockSamples`, {
        defaultValue: '探测样本还不够 {{samples}} 次',
        samples: observation.min_samples ?? '—',
      }),
    );
  }
  const failed = observation.checks
    .filter((check) => check.state === 'fail')
    .map((check) => checkName(check.kind, t));
  const pending = observation.checks
    .filter((check) => check.state === 'pending')
    .map((check) => checkName(check.kind, t));
  const joiner = t(`${ns}.observation.joiner`, { defaultValue: '、' });
  if (failed.length) {
    parts.push(
      t(`${ns}.observation.blockFailed`, {
        checks: failed.join(joiner),
        defaultValue: '{{checks}}没有通过',
      }),
    );
  }
  if (pending.length) {
    parts.push(
      t(`${ns}.observation.blockPending`, {
        checks: pending.join(joiner),
        defaultValue: '{{checks}}探测尚未运行',
      }),
    );
  }
  const sentence = parts.join(
    t(`${ns}.observation.separator`, { defaultValue: '；' }),
  );
  return `${sentence}${t(`${ns}.observation.stop`, { defaultValue: '。' })}${t(
    `${ns}.observation.readyRule`,
    {
      days: observation.days_required,
      defaultValue: '满 {{days}} 天且四项都过后才能上线。',
    },
  )}`;
};

/** 上线为备用 — enabled only once the watch has been passed; otherwise it says
 *  on hover what is still missing. */
export function GoLiveButton({
  disabledReason,
  observation,
  onClick,
  t,
}: {
  /** Set when something other than the observation holds the route back,
   *  such as a disabled source. */
  disabledReason?: string;
  observation: ModelOpsObservation;
  onClick: () => void;
  t: TFunction;
}) {
  const label = t(`${ns}.observation.goLive`, { defaultValue: '上线为备用' });
  if (observation.ready && !disabledReason) {
    return (
      <Button size='xs' variant='primary' onClick={onClick}>
        {label}
      </Button>
    );
  }
  return (
    <Tooltip
      className='mo-ink-tip'
      content={
        <span className='mo-ink-tip__body'>
          <b>{t(`${ns}.observation.notYet`, { defaultValue: '还不能上线' })}</b>
          <span>{disabledReason || observationBlockers(observation, t)}</span>
        </span>
      }
    >
      {/* aria-disabled rather than disabled: a disabled button takes no
          pointer or keyboard focus, and the reason would be unreachable. */}
      <Button
        aria-disabled='true'
        className='mo-golive-inert'
        size='xs'
        variant='secondary'
        onClick={(event) => event.preventDefault()}
      >
        {label}
      </Button>
    </Tooltip>
  );
}

/** The tier card's strip for one watched source on this model and lane. */
export function ObservationStrip({
  disabledReason,
  name,
  observation,
  onGoLive,
  t,
}: {
  disabledReason?: string;
  name: string;
  observation: ModelOpsObservation;
  onGoLive: () => void;
  t: TFunction;
}) {
  const cells: Array<{
    key: string;
    label: string;
    state: ModelOpsObservationState | undefined;
    value: string;
  }> = [
    {
      key: 'success',
      label: t(`${ns}.observation.checkSuccess`, { defaultValue: '成功率' }),
      state: observation.success_state,
      value:
        observation.success_rate == null
          ? t(`${ns}.observation.pending`, { defaultValue: '待测' })
          : formatRate(observation.success_rate),
    },
    ...(['billing', 'long_context', 'tools'] as const).map((kind) => {
      const check = observationCheck(observation, kind);
      return {
        key: kind,
        label:
          kind === 'long_context'
            ? longContextLabel(observation, t)
            : checkName(kind, t),
        state: check?.state,
        value: checkRuns(check, t),
      };
    }),
  ];
  return (
    <div className='model-ops__observe'>
      <span className='model-ops__observe-title'>
        {observation.complete
          ? t(`${ns}.observation.stripDone`, {
              defaultValue: '{{name}} · 观察',
              name,
            })
          : t(`${ns}.observation.strip`, {
              defaultValue: '{{name}} · 观察中',
              name,
            })}{' '}
        <b>
          {observation.complete
            ? t(`${ns}.observation.doneDays`, {
                defaultValue: '期满 {{total}} 天',
                total: observation.days_required,
              })
            : observationProgress(observation, t)}
        </b>
      </span>
      {cells.map((cell) => (
        <span className='model-ops__observe-check' key={cell.key}>
          <i
            aria-hidden='true'
            className={`model-ops__observe-dot model-ops__observe-dot--${toneOf(cell.state)}`}
          />
          {cell.label}{' '}
          <span
            className={`model-ops__observe-value model-ops__observe-value--${toneOf(cell.state)}`}
          >
            {cell.value}
          </span>
        </span>
      ))}
      <span className='model-ops__observe-action'>
        <GoLiveButton
          disabledReason={disabledReason}
          observation={observation}
          t={t}
          onClick={onGoLive}
        />
      </span>
    </div>
  );
}

/** The confirmation before a watched source or route goes live: the facts it
 *  passed on, and what going live as a backup means. */
export function GoLiveDialog({
  busy,
  modelName,
  laneName,
  name,
  observation,
  onConfirm,
  onOpenChange,
  open,
  t,
}: {
  busy: boolean;
  /** Set for one route; left out for a whole source. */
  modelName?: string;
  laneName?: string;
  name: string;
  observation: ModelOpsObservation | null;
  onConfirm: () => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  t: TFunction;
}) {
  const rows = observation
    ? [
        {
          label: t(`${ns}.observation.rowPeriod`, { defaultValue: '观察期' }),
          value: t(`${ns}.observation.periodDone`, {
            defaultValue: '{{total}} 天 · 已满',
            total: observation.days_required,
          }),
        },
        {
          label: t(`${ns}.observation.rowSuccess`, {
            count: observation.samples,
            defaultValue: '成功率 · 探测 {{count}} 次',
          }),
          value: formatRate(observation.success_rate),
        },
        {
          label: t(`${ns}.observation.rowBilling`, {
            defaultValue: '计费比值 · 上游计费 / 实际',
          }),
          value: checkRuns(observationCheck(observation, 'billing'), t),
        },
        ...(['long_context', 'tools'] as const).map((kind) => {
          const check = observationCheck(observation, kind);
          return {
            label:
              kind === 'long_context'
                ? longContextLabel(observation, t)
                : checkName(kind, t),
            value:
              check?.state === 'pass'
                ? t(`${ns}.observation.passRuns`, {
                    defaultValue: '通过 · {{passed}} / {{runs}}',
                    passed: check.passed,
                    runs: check.runs,
                  })
                : checkRuns(check, t),
          };
        }),
      ]
    : [];
  return (
    <Modal
      busy={busy}
      className='mo-golive-dialog'
      closeLabel={t('common.close', { defaultValue: '关闭' })}
      description={
        modelName
          ? t(`${ns}.observation.confirmRoute`, {
              defaultValue:
                '在 {{model}} 的{{lane}}上，它将排在所有主用之后，只在主用全部不可用时接流量。一周内未触发断路器，可再升为主用。',
              lane: laneName || '',
              model: modelName,
            })
          : t(`${ns}.observation.confirmSource`, {
              count: observation?.models ?? 0,
              defaultValue:
                '它观察中的 {{count}} 个模型将以备用身份进入各自的线路，排在所有主用之后，只在主用全部不可用时接流量。一周内未触发断路器，可再升为主用。',
            })
      }
      footer={
        <>
          <Button
            disabled={busy}
            variant='secondary'
            onClick={() => onOpenChange(false)}
          >
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button loading={busy} variant='primary' onClick={onConfirm}>
            {t(`${ns}.observation.confirm`, { defaultValue: '确认上线' })}
          </Button>
        </>
      }
      open={open}
      title={t(`${ns}.observation.confirmTitle`, {
        defaultValue: '让 {{name}} 上线为备用',
        name,
      })}
      body={
        <dl className='mo-golive-rows'>
          {rows.map((row) => (
            <div className='mo-golive-rows__row' key={row.label}>
              <dt>{row.label}</dt>
              <dd>{row.value}</dd>
            </div>
          ))}
        </dl>
      }
      onOpenChange={onOpenChange}
    />
  );
}
