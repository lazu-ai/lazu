import { useEffect, useMemo, useState } from 'react';
import './observation-dialog.css';
import { longContextLabel, observationSuccessBar } from './observation';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import {
  ModalClose,
  ModalFrame,
  ModalTitle,
  Modal,
} from '../../components/ui/modal';
import { PendingBlock, PendingValue } from '../../components/ui/pending';
import {
  StatusBars,
  StatusBarsLegend,
  formatSliceWhen,
  type StatusBarSlice,
  type StatusBarTone,
} from '../../components/ui/status-bars';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import {
  useModelOpsSourceObservationQuery,
  type ModelOpsObservationCheckKind,
  type ModelOpsObservationReason,
  type ModelOpsObservationRoute,
  type ModelOpsSource,
} from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

/* 观察详情 — one watched source opened from the ledger: every model it brings,
   each drawn as its record of probes over the observation (two-hour slots,
   the status page's grain) with the three daily checks underneath, why it is
   held back, and the two things an operator does next: take the ready ones
   live, or drop the ones that will not pass. */

const ns = 'page.console.modelOps';
const DAY_MS = 24 * 60 * 60 * 1000;
const CHECK_KINDS: ModelOpsObservationCheckKind[] = [
  'billing',
  'long_context',
  'tools',
];

type Tone = 'ok' | 'warn' | 'bad' | 'wait';

/** "claude-haiku-4-5-20251001" → "haiku-4-5": the ledger line names models
 *  in a few characters; the dialog prints them whole. */
export const shortModelName = (name: string) =>
  name.replace(/^claude-/, '').replace(/-\d{8}$/, '');

const pad = (value: number) => String(value).padStart(2, '0');

/** "09-30 12:13" in the reader's clock. */
const formatMoment = (value: string | number) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
};

/** Upstream errors the probe records, said the way an operator would. */
const probeErrorPhrase = (detail: string | undefined, t: TFunction) => {
  const text = (detail || '').toLowerCase();
  if (/busy|overload|retry in a moment|529/.test(text)) {
    return t(`${ns}.observation.errBusy`, { defaultValue: '上游繁忙' });
  }
  if (/unauthori|invalid api key|forbidden|401|403/.test(text)) {
    return t(`${ns}.observation.errAuth`, { defaultValue: '上游拒绝了 Key' });
  }
  if (/malformed|invalid|rejected|400/.test(text)) {
    return t(`${ns}.observation.errRejected`, {
      defaultValue: '上游拒绝了请求',
    });
  }
  if (/not found|does not exist|404/.test(text)) {
    return t(`${ns}.observation.errMissing`, {
      defaultValue: '上游没有这个模型',
    });
  }
  if (/timeout|timed out|deadline/.test(text)) {
    return t(`${ns}.observation.errTimeout`, { defaultValue: '上游超时' });
  }
  return t(`${ns}.observation.errProbe`, { defaultValue: '抽测失败' });
};

/** The same upstream errors, quoted the way the upstream says them, for the
 *  sentence under a route. */
const probeErrorQuote = (detail: string | undefined, t: TFunction) => {
  const text = (detail || '').toLowerCase();
  if (/busy|overload|retry in a moment|529/.test(text)) {
    return t(`${ns}.observation.quoteBusy`, {
      defaultValue: '繁忙，请稍后重试',
    });
  }
  if (/unauthori|invalid api key|forbidden|401|403/.test(text)) {
    return t(`${ns}.observation.quoteAuth`, {
      defaultValue: 'Key 无效或没有权限',
    });
  }
  if (/malformed|invalid|rejected|400/.test(text)) {
    return t(`${ns}.observation.quoteRejected`, {
      defaultValue: '请求被拒绝',
    });
  }
  if (/not found|does not exist|404/.test(text)) {
    return t(`${ns}.observation.quoteMissing`, {
      defaultValue: '没有这个模型',
    });
  }
  if (/timeout|timed out|deadline/.test(text)) {
    return t(`${ns}.observation.quoteTimeout`, { defaultValue: '超时' });
  }
  return (
    detail || t(`${ns}.observation.errProbe`, { defaultValue: '抽测失败' })
  );
};

const checkName = (kind: ModelOpsObservationCheckKind, t: TFunction) =>
  ({
    billing: t(`${ns}.observation.shortBilling`, { defaultValue: '计费' }),
    long_context: t(`${ns}.observation.shortLong`, { defaultValue: '长文' }),
    tools: t(`${ns}.observation.shortTools`, { defaultValue: '工具' }),
  })[kind];

/** The few words the ledger line gives one problem: "上游繁忙",
 *  "没调用工具", "计费 3.07". */
export const reasonShort = (
  reason: ModelOpsObservationReason,
  t: TFunction,
) => {
  const detail = reason.detail || '';
  switch (reason.kind) {
    case 'probe':
      return probeErrorPhrase(detail, t);
    case 'billing':
      return reason.value
        ? t(`${ns}.observation.shortBillingRatio`, {
            defaultValue: '计费 {{value}}',
            value: reason.value.toFixed(2),
          })
        : t(`${ns}.observation.shortBillingOdd`, {
            defaultValue: '计费用量异常',
          });
    case 'long_context':
      return detail.startsWith('truncated')
        ? t(`${ns}.observation.shortTruncated`, {
            defaultValue: '长文被截断',
          })
        : t(`${ns}.observation.shortLongFail`, { defaultValue: '长文没过' });
    case 'tools':
      return t(`${ns}.observation.shortToolsFail`, {
        defaultValue: '没调用工具',
      });
  }
  return '';
};

/** One sentence for a check's failure, from the detail the check recorded. */
const checkSentence = (
  kind: ModelOpsObservationCheckKind,
  detail: string,
  t: TFunction,
) => {
  const billed = /^billed (\d+) input tokens for about (\d+)/.exec(detail);
  if (kind === 'billing' && billed) {
    return t(`${ns}.observation.whyBilled`, {
      billed: Number(billed[1]).toLocaleString(),
      defaultValue:
        '计费：按 {{billed}} 个输入 token 计费，实际约 {{actual}}。',
      actual: Number(billed[2]).toLocaleString(),
    });
  }
  const flat =
    /^usage did not grow with the prompt \((\d+) then (\d+) tokens\)/.exec(
      detail,
    );
  if (kind === 'billing' && flat) {
    return t(`${ns}.observation.whyFlat`, {
      defaultValue:
        '计费：用量没有随输入增长（{{first}} → {{second}} token）。',
      first: flat[1],
      second: flat[2],
    });
  }
  const cut = /^truncated: billed (\d+) of about (\d+)/.exec(detail);
  if (kind === 'long_context' && cut) {
    return t(`${ns}.observation.whyTruncated`, {
      billed: Number(cut[1]).toLocaleString(),
      defaultValue:
        '长文：只计了 {{billed}} / 约 {{sent}} 个输入 token，上下文被截断。',
      sent: Number(cut[2]).toLocaleString(),
    });
  }
  if (kind === 'long_context' && detail.includes('code word')) {
    return t(`${ns}.observation.whyCodeWord`, {
      defaultValue: '长文：回复里没有开头的暗号。',
    });
  }
  if (kind === 'tools' && detail.includes('did not call')) {
    return t(`${ns}.observation.whyNoTool`, {
      defaultValue: '工具：回复没有调用给定的函数。',
    });
  }
  if (kind === 'tools' && detail) {
    return t(`${ns}.observation.whyBadTool`, {
      defaultValue: '工具：调用了函数，但参数不对。',
    });
  }
  return `${checkName(kind, t)}：${detail || t(`${ns}.observation.fail`, { defaultValue: '未过' })}`;
};

/** The line under a route: why it is held back, or — for a check that failed
 *  once and recovered — why it still counts as passed. */
const routeWhy = (route: ModelOpsObservationRoute, t: TFunction) => {
  const observation = route.observation;
  if (route.reason?.kind === 'probe') {
    const samples = observation.samples;
    const ok = samples - (observation.failures ?? 0);
    return t(`${ns}.observation.whyProbe`, {
      defaultValue:
        '上游一直返回「{{error}}」：{{samples}} 次抽测只成功 {{ok}} 次。',
      error: probeErrorQuote(route.reason.detail, t),
      ok,
      samples,
    });
  }
  if (route.reason) {
    return checkSentence(
      route.reason.kind as ModelOpsObservationCheckKind,
      route.reason.detail || '',
      t,
    );
  }
  for (const kind of CHECK_KINDS) {
    const check = observation.checks.find((item) => item.kind === kind);
    if (check?.state !== 'pass' || check.runs <= check.passed) continue;
    const failedDay = route.days.findIndex((day) => day[kind] === 'fail');
    const passedDay =
      route.days.length -
      1 -
      [...route.days].reverse().findIndex((day) => day[kind] === 'pass');
    return t(`${ns}.observation.whyRecovered`, {
      check: checkName(kind, t),
      defaultValue:
        '{{check}}第 {{failed}} 天没过，第 {{passed}} 天通过：最近一次通过、之前只失败一次，算过。',
      failed: failedDay + 1,
      passed: passedDay + 1,
    });
  }
  return '';
};

const daysLeft = (startedAt: string, required: number) => {
  const end = new Date(startedAt).getTime() + required * DAY_MS;
  return Math.max(1, Math.ceil((end - Date.now()) / DAY_MS));
};

const checksPassed = (route: ModelOpsObservationRoute) =>
  route.verdict !== 'problem' &&
  CHECK_KINDS.every((kind) => {
    const state = route.observation.checks.find(
      (check) => check.kind === kind,
    )?.state;
    return state === 'pass' || state === 'na';
  });

const sliceTone = (
  slot: { failures: number; samples: number } | undefined,
): StatusBarTone => {
  if (!slot) return 'future';
  if (slot.samples === 0) return 'none';
  if (slot.failures === 0) return 'ok';
  return slot.failures >= slot.samples ? 'bad' : 'warn';
};

function RouteRow({
  checked,
  onCheckedChange,
  route,
  slotCount,
  slotSeconds,
  t,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  route: ModelOpsObservationRoute;
  slotCount: number;
  slotSeconds: number;
  t: TFunction;
}) {
  const observation = route.observation;
  const selectable = checksPassed(route);
  const origin = new Date(route.slot_origin).getTime();
  const slices: StatusBarSlice[] = Array.from(
    { length: slotCount },
    (_, index) => {
      const slot = route.slots[index];
      const tone = sliceTone(slot);
      const startAt = new Date(
        origin + index * slotSeconds * 1000,
      ).toISOString();
      const verdict =
        tone === 'future'
          ? t(`${ns}.observation.sliceFuture`, { defaultValue: '还没到' })
          : tone === 'none'
            ? t(`${ns}.observation.sliceNone`, { defaultValue: '没测到' })
            : t(`${ns}.observation.sliceProbes`, {
                defaultValue: '抽测 {{ok}} / {{samples}}',
                ok: (slot?.samples ?? 0) - (slot?.failures ?? 0),
                samples: slot?.samples ?? 0,
              });
      return { tone, verdict, when: formatSliceWhen(startAt, slotSeconds) };
    },
  );
  const failures = observation.failures ?? 0;
  const probeTone: Tone =
    observation.samples === 0
      ? 'wait'
      : route.reason?.kind === 'probe'
        ? 'bad'
        : failures > 0
          ? 'warn'
          : 'ok';
  const cell = (kind: ModelOpsObservationCheckKind) => {
    const check = observation.checks.find((item) => item.kind === kind);
    if (!check || check.state === 'pending') {
      return {
        tone: 'wait' as Tone,
        text: t(`${ns}.observation.pending`, { defaultValue: '待测' }),
      };
    }
    if (check.state === 'na') {
      return {
        tone: 'ok' as Tone,
        text: t(`${ns}.observation.na`, { defaultValue: '不适用' }),
      };
    }
    const failed = check.state === 'fail';
    if (kind === 'billing') {
      const ratio = check.latest ?? (failed ? null : check.value);
      return {
        tone: (failed ? 'bad' : 'ok') as Tone,
        text:
          ratio != null && ratio > 0
            ? ratio.toFixed(2)
            : t(`${ns}.observation.billingOdd`, { defaultValue: '异常' }),
      };
    }
    return {
      tone: (failed ? 'bad' : 'ok') as Tone,
      text: failed
        ? t(`${ns}.observation.failed`, { defaultValue: '失败' })
        : t(`${ns}.observation.pass`, { defaultValue: '通过' }),
    };
  };
  const verdict =
    route.verdict === 'ready'
      ? {
          cls: 'ok',
          text: t(`${ns}.observation.ready`, { defaultValue: '可上线' }),
        }
      : route.verdict === 'problem'
        ? {
            cls: 'bad',
            text: t(`${ns}.observation.problem`, { defaultValue: '有问题' }),
          }
        : selectable && !observation.complete
          ? {
              cls: 'wait',
              text: t(`${ns}.observation.blockDays`, {
                count: daysLeft(
                  observation.started_at,
                  observation.days_required,
                ),
                defaultValue: '还差 {{count}} 天',
              }),
            }
          : {
              cls: 'wait',
              text: t(`${ns}.observation.watching`, { defaultValue: '观察中' }),
            };
  const why = routeWhy(route, t);
  const today = route.days.length - 1;

  return (
    <div className='mo-obsd__row'>
      <div className='mo-obsd__grid mo-obsd__top'>
        <Checkbox
          aria-label={t(`${ns}.observation.select`, {
            defaultValue: '选择 {{model}}',
            model: route.model,
          })}
          checked={checked}
          disabled={!selectable}
          onCheckedChange={(next: boolean) => onCheckedChange(next)}
        />
        <span className='mo-obsd__model'>{route.model}</span>
        <span className={`mo-obsd__cell is-${probeTone}`}>
          <i aria-hidden='true' className='mo-obsd__dot' />
          {observation.samples === 0
            ? t(`${ns}.observation.pending`, { defaultValue: '待测' })
            : `${observation.samples - failures}/${observation.samples}`}
        </span>
        {CHECK_KINDS.map((kind) => {
          const value = cell(kind);
          return (
            <span className={`mo-obsd__cell is-${value.tone}`} key={kind}>
              <i aria-hidden='true' className='mo-obsd__dot' />
              {value.text}
            </span>
          );
        })}
        <span>
          <span className={`mo-obsd__verdict is-${verdict.cls}`}>
            {verdict.text}
          </span>
        </span>
      </div>
      <div className='mo-obsd__chart'>
        <StatusBars
          height={16}
          label={t(`${ns}.observation.barsLabel`, {
            defaultValue: '{{model}} 观察期内每 2 小时的抽测',
            model: route.model,
          })}
          slices={slices}
        />
        <div className='mo-obsd__days'>
          {Array.from({ length: observation.days_required }, (_, index) => {
            const day = route.days[index];
            return (
              <div
                className={`mo-obsd__day${index === today ? ' is-today' : ''}`}
                key={index}
              >
                {CHECK_KINDS.map((kind) => {
                  const state = day?.[kind];
                  return (
                    <i
                      aria-label={`${checkName(kind, t)} ${
                        state === 'pass'
                          ? t(`${ns}.observation.pass`, {
                              defaultValue: '通过',
                            })
                          : state === 'fail'
                            ? t(`${ns}.observation.failed`, {
                                defaultValue: '失败',
                              })
                            : state === 'na'
                              ? t(`${ns}.observation.na`, {
                                  defaultValue: '不适用',
                                })
                              : t(`${ns}.observation.pending`, {
                                  defaultValue: '待测',
                                })
                      }`}
                      className={`mo-obsd__sq is-${state === 'pass' || state === 'na' ? 'ok' : state === 'fail' ? 'bad' : 'none'}`}
                      key={kind}
                      role='img'
                    />
                  );
                })}
                <span className='mo-obsd__daylabel'>
                  {t(`${ns}.observation.dayLabel`, {
                    day: index + 1,
                    defaultValue: '第{{day}}天',
                  })}
                </span>
              </div>
            );
          })}
        </div>
      </div>
      {why ? <div className='mo-obsd__why'>{why}</div> : null}
    </div>
  );
}

type Pending = { kind: 'golive' | 'remove'; models: string[] } | null;

/** The dialog opened by 「查看 N 个」 on a watched source. */
export function ObservationDialog({
  busy,
  onGoLive,
  onOpenChange,
  onRemove,
  source,
  t,
}: {
  busy: boolean;
  onGoLive: (models: string[]) => Promise<unknown>;
  onOpenChange: (open: boolean) => void;
  onRemove: (models: string[]) => Promise<unknown>;
  source: ModelOpsSource | null;
  t: TFunction;
}) {
  const query = useModelOpsSourceObservationQuery(source ? source.id : null);
  const lanes = useChannelLanesQuery().data ?? [];
  const routes = useMemo(() => query.data?.routes ?? [], [query.data]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [pending, setPending] = useState<Pending>(null);
  const [removeChoice, setRemoveChoice] = useState<Set<string>>(new Set());

  // Every route that has passed its checks starts ticked; a refetch keeps
  // what the operator untoggled.
  const selectableKey = routes
    .filter(checksPassed)
    .map((route) => route.model)
    .join(',');
  useEffect(() => {
    setSelected(new Set(selectableKey ? selectableKey.split(',') : []));
  }, [selectableKey, source?.id]);

  const first = routes[0]?.observation;
  const problems = routes.filter((route) => route.verdict === 'problem');
  const allPassed = routes.filter(checksPassed).length;
  const waiting = routes.length - allPassed - problems.length;
  const chosen = routes.filter((route) => selected.has(route.model));
  const chosenReady =
    chosen.length > 0 && chosen.every((route) => route.verdict === 'ready');
  const longestWait = chosen.reduce(
    (most, route) =>
      route.observation.complete
        ? most
        : Math.max(
            most,
            daysLeft(
              route.observation.started_at,
              route.observation.days_required,
            ),
          ),
    0,
  );
  const laneName =
    lanes.find(
      (lane) => lane.slug === (query.data?.source.lane ?? source?.lane),
    )?.name ||
    query.data?.source.lane ||
    source?.lane ||
    '';
  const startedAt = routes.reduce(
    (earliest, route) =>
      !earliest || route.observation.started_at < earliest
        ? route.observation.started_at
        : earliest,
    '',
  );
  const endsAt =
    startedAt && first
      ? new Date(
          new Date(startedAt).getTime() + first.days_required * DAY_MS,
        ).toISOString()
      : '';
  const day = routes.reduce(
    (most, route) => Math.max(most, route.observation.day),
    0,
  );

  const goLiveLabel = t(`${ns}.observation.goLiveSelected`, {
    count: chosen.length,
    defaultValue: '上线选中的 {{count}} 个',
  });

  return (
    <>
      <ModalFrame
        busy={busy}
        className='mo-obsd lazu-modal-content--xwide'
        hideClose
        open={Boolean(source)}
        onOpenChange={onOpenChange}
      >
        <div className='mo-obsd__head'>
          <div className='mo-obsd__title'>
            <ModalTitle className='mo-obsd__name'>{source?.name}</ModalTitle>
            <span className='mo-obsd__tag is-lane'>{laneName}</span>
            <span className='mo-obsd__tag'>
              {t(`${ns}.sources.observing`, { defaultValue: '观察中' })}
            </span>
            <span className='mo-obsd__spacer' />
            <ModalClose
              render={
                <Button disabled={busy} size='sm' variant='secondary'>
                  {t('common.close', { defaultValue: '关闭' })}
                </Button>
              }
            />
          </div>
          <div className='mo-obsd__sub'>
            {first && startedAt
              ? t(`${ns}.observation.dialogSub`, {
                  day,
                  defaultValue:
                    '第 {{day}} 天 / {{total}} · {{start}} 开始 · {{end}} 期满 · 只被抽测和检查，不接流量',
                  end: formatMoment(endsAt),
                  start: formatMoment(startedAt),
                  total: first.days_required,
                })
              : ' '}
          </div>
          <div className='mo-obsd__counts'>
            <span className='mo-obsd__pill'>
              <i className='mo-obsd__dot is-ok' />
              {t(`${ns}.observation.countPassed`, {
                count: allPassed,
                defaultValue: '全过 {{count}}',
              })}
            </span>
            <span className='mo-obsd__pill'>
              <i className='mo-obsd__dot is-bad' />
              {t(`${ns}.observation.countProblem`, {
                count: problems.length,
                defaultValue: '有问题 {{count}}',
              })}
            </span>
            <span className='mo-obsd__pill'>
              <i className='mo-obsd__dot is-wait' />
              {t(`${ns}.observation.countWaiting`, {
                count: waiting,
                defaultValue: '待测 {{count}}',
              })}
            </span>
            <span className='mo-obsd__spacer' />
            <StatusBarsLegend
              className='mo-obsd__legend'
              items={[
                {
                  label: t(`${ns}.observation.legendOk`, {
                    defaultValue: '2 小时全成功',
                  }),
                  tone: 'ok',
                },
                {
                  label: t(`${ns}.observation.legendWarn`, {
                    defaultValue: '部分失败',
                  }),
                  tone: 'warn',
                },
                {
                  label: t(`${ns}.observation.legendBad`, {
                    defaultValue: '全失败',
                  }),
                  tone: 'bad',
                },
                {
                  label: t(`${ns}.observation.sliceNone`, {
                    defaultValue: '没测到',
                  }),
                  tone: 'none',
                },
                {
                  label: t(`${ns}.observation.legendFuture`, {
                    defaultValue: '未到',
                  }),
                  tone: 'future',
                },
              ]}
            />
            <span className='mo-obsd__legend-days'>
              <i className='mo-obsd__sq is-ok' />
              <i className='mo-obsd__sq is-ok' />
              <i className='mo-obsd__sq is-bad' />
              {t(`${ns}.observation.legendDays`, {
                defaultValue: '当天 计费 · 长文 · 工具',
              })}
            </span>
          </div>
        </div>
        <div className='mo-obsd__body'>
          <div className='mo-obsd__grid mo-obsd__colhead'>
            <span />
            <span>
              {first ? (
                t(`${ns}.observation.colModel`, {
                  days: first.days_required,
                  defaultValue:
                    '模型 · {{days}} 天抽测（每格 2 小时）· 每日检查',
                })
              ) : (
                <PendingValue chars={20} />
              )}
            </span>
            <span>
              {t(`${ns}.observation.colProbes`, { defaultValue: '抽测累计' })}
            </span>
            <span>
              {t(`${ns}.observation.shortBilling`, { defaultValue: '计费' })}
            </span>
            <span>
              {first ? longContextLabel(first, t) : <PendingValue chars={6} />}
            </span>
            <span>
              {t(`${ns}.observation.shortTools`, { defaultValue: '工具' })}
            </span>
            <span>
              {t(`${ns}.observation.colVerdict`, { defaultValue: '结论' })}
            </span>
          </div>
          {query.isPending ? (
            <PendingBlock className='mo-obsd__pending' height={480} />
          ) : (
            routes.map((route) => (
              <RouteRow
                checked={selected.has(route.model)}
                key={`${route.model}|${route.lane}`}
                route={route}
                slotCount={query.data?.slots ?? 0}
                slotSeconds={query.data?.slot_seconds ?? 7200}
                t={t}
                onCheckedChange={(next) =>
                  setSelected((current) => {
                    const copy = new Set(current);
                    if (next) copy.add(route.model);
                    else copy.delete(route.model);
                    return copy;
                  })
                }
              />
            ))
          )}
        </div>
        <div className='mo-obsd__foot'>
          <span className='mo-obsd__note'>
            {t(`${ns}.observation.footNote`, {
              defaultValue: '上线后成为{{lane}}线路的备用，不动现有权重。',
              lane: laneName,
            })}
          </span>
          <span className='mo-obsd__spacer' />
          {problems.length > 0 ? (
            <Button
              disabled={busy}
              variant='secondary'
              onClick={() => {
                setRemoveChoice(new Set(problems.map((route) => route.model)));
                setPending({
                  kind: 'remove',
                  models: problems.map((route) => route.model),
                });
              }}
            >
              {t(`${ns}.observation.removeProblems`, {
                count: problems.length,
                defaultValue: '移除有问题的 {{count}} 个…',
              })}
            </Button>
          ) : null}
          <Button
            disabled={busy || !chosenReady}
            variant='primary'
            onClick={() =>
              setPending({
                kind: 'golive',
                models: chosen.map((route) => route.model),
              })
            }
          >
            {chosen.length > 0 && longestWait > 0
              ? `${goLiveLabel} · ${t(`${ns}.observation.blockDays`, {
                  count: longestWait,
                  defaultValue: '还差 {{count}} 天',
                })}`
              : goLiveLabel}
          </Button>
        </div>
        <Modal
          busy={busy}
          className='mo-obsd-confirm'
          hideClose
          open={pending?.kind === 'golive'}
          title={t(`${ns}.observation.confirmSelectedTitle`, {
            count: pending?.models.length ?? 0,
            defaultValue: '把 {{count}} 个模型上线为{{lane}}线路的备用？',
            lane: laneName,
          })}
          body={
            <div className='mo-obsd-confirm__body'>
              <ul className='mo-obsd-confirm__list'>
                {(pending?.models ?? []).map((name) => (
                  <li key={name}>
                    <span className='mo-obsd-confirm__model'>{name}</span>
                    <em>
                      {t(`${ns}.observation.confirmPassed`, {
                        days: first?.days_required,
                        defaultValue:
                          '满 {{days}} 天 · 抽测 ≥ {{rate}} · 三项全过',
                        rate: first ? observationSuccessBar(first) : '—',
                      })}
                    </em>
                  </li>
                ))}
              </ul>
              <p className='mo-obsd-confirm__desc'>
                {t(`${ns}.observation.confirmSelectedDesc`, {
                  count: routes.length - (pending?.models.length ?? 0),
                  defaultValue:
                    '它们开始接流量，只在主用来源失败时顶上。其余 {{count}} 个模型继续观察，不受影响。',
                })}
              </p>
            </div>
          }
          footer={
            <>
              <Button
                disabled={busy}
                variant='secondary'
                onClick={() => setPending(null)}
              >
                {t('common.cancel', { defaultValue: '取消' })}
              </Button>
              <Button
                loading={busy}
                variant='primary'
                onClick={async () => {
                  if (!pending) return;
                  try {
                    await onGoLive(pending.models);
                    setPending(null);
                  } catch {
                    // The mutation reports its own error.
                  }
                }}
              >
                {t(`${ns}.observation.goLive`, { defaultValue: '上线为备用' })}
              </Button>
            </>
          }
          onOpenChange={(open) => {
            if (!open && !busy) setPending(null);
          }}
        />

        <Modal
          busy={busy}
          className='mo-obsd-confirm'
          hideClose
          open={pending?.kind === 'remove'}
          title={t(`${ns}.observation.confirmRemoveTitle`, {
            count: removeChoice.size,
            defaultValue: '从这个来源移除 {{count}} 个模型？',
          })}
          body={
            <div className='mo-obsd-confirm__body'>
              <ul className='mo-obsd-confirm__list'>
                {problems.map((route) => (
                  <li key={route.model}>
                    <Checkbox
                      checked={removeChoice.has(route.model)}
                      onCheckedChange={(next: boolean) =>
                        setRemoveChoice((current) => {
                          const copy = new Set(current);
                          if (next) copy.add(route.model);
                          else copy.delete(route.model);
                          return copy;
                        })
                      }
                    >
                      <span className='mo-obsd-confirm__model'>
                        {route.model}
                      </span>
                      <em>
                        {route.reason ? reasonShort(route.reason, t) : ''}
                      </em>
                    </Checkbox>
                  </li>
                ))}
              </ul>
              <p className='mo-obsd-confirm__desc'>
                {t(`${ns}.observation.confirmRemoveDesc`, {
                  days: first?.days_required,
                  defaultValue:
                    '只是把它们从这个来源的模型列表里去掉，模型本身和其它来源不受影响。以后想再试，加回来会重新观察 {{days}} 天。',
                })}
              </p>
            </div>
          }
          footer={
            <>
              <Button
                disabled={busy}
                variant='secondary'
                onClick={() => setPending(null)}
              >
                {t('common.cancel', { defaultValue: '取消' })}
              </Button>
              <Button
                disabled={removeChoice.size === 0}
                loading={busy}
                variant='danger'
                onClick={async () => {
                  try {
                    await onRemove([...removeChoice]);
                    setPending(null);
                  } catch {
                    // The mutation reports its own error.
                  }
                }}
              >
                {t(`${ns}.observation.removeCount`, {
                  count: removeChoice.size,
                  defaultValue: '移除 {{count}} 个',
                })}
              </Button>
            </>
          }
          onOpenChange={(open) => {
            if (!open && !busy) setPending(null);
          }}
        />
      </ModalFrame>
    </>
  );
}

/** The ledger's third line for a watched source: how many models passed every
 *  check, then the problem models by name with a few words each. */
export function observationSummaryLine(
  summary: NonNullable<ModelOpsSource['observation']>['summary'],
  t: TFunction,
) {
  if (!summary) return null;
  const parts: Array<{ text: string; tone: 'ok' | 'bad' | 'plain' }> = [];
  if (summary.all_passed > 0) {
    parts.push({
      text: t(`${ns}.observation.lineAllPassed`, {
        count: summary.all_passed,
        defaultValue: '{{count}} 个全过',
      }),
      tone: 'ok',
    });
  }
  summary.problems.slice(0, 3).forEach((problem) => {
    parts.push({
      text: `${shortModelName(problem.model)} ${reasonShort(problem, t)}`,
      tone: 'bad',
    });
  });
  if (summary.problems.length > 3) {
    parts.push({
      text: t(`${ns}.observation.lineMoreProblems`, {
        count: summary.problems.length - 3,
        defaultValue: '另 {{count}} 个有问题',
      }),
      tone: 'bad',
    });
  }
  if (summary.pending > 0) {
    parts.push({
      text: t(`${ns}.observation.linePending`, {
        count: summary.pending,
        defaultValue: '{{count}} 个待测',
      }),
      tone: 'plain',
    });
  }
  return parts;
}
