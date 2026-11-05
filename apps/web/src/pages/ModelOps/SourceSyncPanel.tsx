import { useMemo } from 'react';
import { Controller } from 'react-hook-form';
import {
  compileIgnore,
  describeLogChange,
  formatCheckTime,
} from './sourceModels';
import { Checkbox } from '../../components/ui/checkbox';
import { FieldMessage } from '../../components/ui/field';
import { Textarea } from '../../components/ui/textarea';
import type { ChannelFormValues } from './SourceForm';
import type {
  UpstreamSyncLogEntry,
  UpstreamSyncView,
} from '../../hooks/models/useSourceUpstream';
import type { TFunction } from 'i18next';
import type { UseFormReturn } from 'react-hook-form';

const ns = 'page.console.modelOps.sourcePage.sync';

type SourceSyncPanelProps = {
  disabled?: boolean;
  form: UseFormReturn<ChannelFormValues>;
  t: TFunction;
  upstream: UpstreamSyncView | null | undefined;
};

const INTERVALS: Array<[number, string, string]> = [
  [1, 'hourly', '每小时'],
  [6, 'six', '每 6 小时'],
  [24, 'daily', '每天'],
  [0, 'off', '不检查'],
];

/** What became of one check's findings, for the log's 处理 column. */
const handled = (
  entry: UpstreamSyncLogEntry,
  pending: { gone: Set<string>; new: Set<string> },
  t: TFunction,
) => {
  const parts: string[] = [];
  if (entry.added?.length) {
    parts.push(
      t(`${ns}.logAdded`, {
        count: entry.added.length,
        defaultValue: '自动开始供货 {{count}} 个',
      }),
    );
  }
  if (entry.stopped?.length) {
    parts.push(
      t(`${ns}.logStopped`, {
        count: entry.stopped.length,
        defaultValue: '自动停止供货 {{count}} 个',
      }),
    );
  }
  if (entry.note) parts.push(entry.note);
  const waiting =
    (entry.new ?? []).some((id) => pending.new.has(id)) ||
    (entry.gone ?? []).some((name) => pending.gone.has(name));
  if (waiting) parts.push(t(`${ns}.logPending`, { defaultValue: '待核对' }));
  return parts.join('；');
};

/** 来源 › 同步: how often the upstream list is read and what a change does,
 *  beside the record of the checks so far. The settings are part of the
 *  page's form and are written with 保存. */
export default function SourceSyncPanel({
  disabled,
  form,
  t,
  upstream,
}: SourceSyncPanelProps) {
  const interval = Number(form.watch('upstream_sync_interval') || 0);
  const ignoreText = form.watch('upstream_sync_ignore') || '';
  const state = upstream?.state ?? null;
  const ignored = useMemo(() => {
    const matcher = compileIgnore(ignoreText);
    return (state?.upstream ?? []).filter((id) => matcher.match(id));
  }, [ignoreText, state?.upstream]);
  const pending = useMemo(
    () => ({
      gone: new Set(state?.gone ?? []),
      new: new Set(state?.new ?? []),
    }),
    [state?.gone, state?.new],
  );
  const intervals = INTERVALS.some(([hours]) => hours === interval)
    ? INTERVALS
    : [
        [
          interval,
          'custom',
          t(`${ns}.everyHours`, {
            defaultValue: '每 {{hours}} 小时',
            hours: interval,
          }),
        ] as [number, string, string],
        ...INTERVALS,
      ];
  const log = state?.log ?? [];

  const radio = <K extends 'upstream_sync_on_gone' | 'upstream_sync_on_new'>(
    name: K,
    options: Array<[ChannelFormValues[K], string, string]>,
  ) => (
    <Controller
      control={form.control}
      name={name}
      render={({ field }) => (
        <span className='src-radios' role='radiogroup'>
          {options.map(([value, label, sub]) => (
            <span className='src-radio' key={String(value)}>
              <input
                checked={field.value === value}
                disabled={disabled}
                id={`${name}-${value}`}
                name={name}
                type='radio'
                onChange={() => field.onChange(value)}
              />
              <label className='src-radio__text' htmlFor={`${name}-${value}`}>
                <span>{label}</span>
                <span className='src-hint'>{sub}</span>
              </label>
            </span>
          ))}
        </span>
      )}
    />
  );

  return (
    <div className='src-sync'>
      <section className='src-panel src-sync__rules'>
        <div className='src-setrow'>
          <span className='src-setrow__label'>
            <span className='src-setrow__title'>
              {t(`${ns}.interval`, { defaultValue: '检查上游' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.intervalHint`, {
                defaultValue: '定时拉一次上游的模型列表',
              })}
            </span>
          </span>
          <span className='src-setrow__body src-setrow__body--line'>
            {intervals.map(([hours, key, label]) => (
              <button
                data-owns-chrome=''
                aria-pressed={interval === hours}
                className={`src-toggle${interval === hours ? ' is-on' : ''}`}
                disabled={disabled}
                key={key}
                type='button'
                onClick={() =>
                  form.setValue('upstream_sync_interval', hours, {
                    shouldDirty: true,
                  })
                }
              >
                {key === 'custom'
                  ? label
                  : t(`${ns}.intervalOption.${key}`, { defaultValue: label })}
              </button>
            ))}
            <span className='src-hint src-setrow__aside'>
              {t(`${ns}.lastNext`, {
                defaultValue: '上次 {{last}} · 下次 {{next}}',
                last: formatCheckTime(state?.checked_at),
                next: interval
                  ? formatCheckTime(upstream?.next_check_at)
                  : t(`${ns}.never`, { defaultValue: '不检查' }),
              })}
            </span>
          </span>
        </div>

        <div className='src-setrow'>
          <span className='src-setrow__label'>
            <span className='src-setrow__title'>
              {t(`${ns}.onNew`, { defaultValue: '上游新出现的模型' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.onNewHint`, {
                defaultValue: '不在你供货单里、上次检查之后才出现的',
              })}
            </span>
          </span>
          <span className='src-setrow__body'>
            {radio('upstream_sync_on_new', [
              [
                'review',
                t(`${ns}.onNewReview`, {
                  defaultValue: '先不供货，进「上游新增」等我核对',
                }),
                t(`${ns}.onNewReviewHint`, {
                  defaultValue: '推荐。来源列表和详情页会提示有几个待核对',
                }),
              ],
              [
                'auto',
                t(`${ns}.onNewAuto`, { defaultValue: '自动开始供货' }),
                t(`${ns}.onNewAutoHint`, {
                  defaultValue: '适合只放一家模型的官方来源；聚合商不建议',
                }),
              ],
            ])}
          </span>
        </div>

        <div className='src-setrow'>
          <span className='src-setrow__label'>
            <span className='src-setrow__title'>
              {t(`${ns}.onGone`, { defaultValue: '上游不再列出的模型' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.onGoneHint`, {
                defaultValue: '你在供货、但上游列表里没有了',
              })}
            </span>
          </span>
          <span className='src-setrow__body'>
            {radio('upstream_sync_on_gone', [
              [
                'stop',
                t(`${ns}.onGoneStop`, { defaultValue: '停止供货，保留记录' }),
                t(`${ns}.onGoneStopHint`, {
                  defaultValue: '路由改走其他来源；上游恢复列出时会再提示你',
                }),
              ],
              [
                'keep',
                t(`${ns}.onGoneKeep`, { defaultValue: '继续供货，只提醒' }),
                t(`${ns}.onGoneKeepHint`, {
                  defaultValue: '有些上游列表不全，但实际还能调',
                }),
              ],
            ])}
          </span>
        </div>

        <div className='src-setrow'>
          <span className='src-setrow__label'>
            <span className='src-setrow__title'>
              {t(`${ns}.ignore`, { defaultValue: '忽略' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.ignoreHintBefore`, {
                defaultValue: '这些名字不进「新出现」，也不提醒。每行一条，',
              })}
              <span className='src-mono'>*</span>
              {t(`${ns}.ignoreHintMiddle`, { defaultValue: ' 通配，' })}
              <span className='src-mono'>re:</span>
              {t(`${ns}.ignoreHintAfter`, { defaultValue: ' 开头写正则' })}
            </span>
          </span>
          <span className='src-setrow__body'>
            <Textarea
              aria-label={t(`${ns}.ignore`, { defaultValue: '忽略' })}
              className='src-ignore'
              disabled={disabled}
              placeholder={'*-preview\nre:^text-embedding-'}
              rows={4}
              {...form.register('upstream_sync_ignore')}
            />
            <FieldMessage error>
              {form.formState.errors.upstream_sync_ignore?.message}
            </FieldMessage>
            <span className='src-hint'>
              {state?.listed_at ? (
                <>
                  {t(`${ns}.ignoredNow`, {
                    defaultValue: '现在忽略上游的',
                  })}{' '}
                  <b className='src-mono src-ink'>{ignored.length}</b>{' '}
                  {t(`${ns}.ignoredUnit`, { defaultValue: '个' })}
                  {ignored.length ? (
                    <>
                      {'：'}
                      <span className='src-mono'>
                        {ignored.slice(0, 8).join('、')}
                        {ignored.length > 8 ? ' …' : ''}
                      </span>
                    </>
                  ) : null}
                </>
              ) : (
                t(`${ns}.ignoredUnknown`, {
                  defaultValue: '检查过一次上游后，这里会列出被忽略的模型。',
                })
              )}
            </span>
          </span>
        </div>

        <div className='src-setrow src-setrow--last'>
          <span className='src-setrow__label'>
            <span className='src-setrow__title'>
              {t(`${ns}.notify`, { defaultValue: '通知' })}
            </span>
            <span className='src-hint'>
              {t(`${ns}.notifyHint`, { defaultValue: '有变化时告诉谁' })}
            </span>
          </span>
          <span className='src-setrow__body src-setrow__body--line src-notify'>
            <Checkbox checked disabled>
              {t(`${ns}.notifyList`, {
                defaultValue: '来源列表「上游变化」列',
              })}
            </Checkbox>
            <Checkbox checked disabled>
              {t(`${ns}.notifyBanner`, { defaultValue: '详情页提示条' })}
            </Checkbox>
            <Controller
              control={form.control}
              name='upstream_sync_notify_ops'
              render={({ field }) => (
                <Checkbox
                  checked={field.value}
                  disabled={disabled}
                  onCheckedChange={(checked: boolean) =>
                    field.onChange(checked)
                  }
                >
                  {t(`${ns}.notifyOps`, { defaultValue: '运维通知邮件' })}
                </Checkbox>
              )}
            />
          </span>
        </div>
      </section>

      <section className='src-panel src-sync__log'>
        <div className='src-panel__title'>
          {t(`${ns}.log`, { defaultValue: '检查记录' })}
        </div>
        <div className='src-logrow src-logrow--head'>
          <span className='src-th'>
            {t(`${ns}.logTime`, { defaultValue: '时间' })}
          </span>
          <span className='src-th'>
            {t(`${ns}.logChange`, { defaultValue: '变化' })}
          </span>
          <span className='src-th'>
            {t(`${ns}.logHandled`, { defaultValue: '处理' })}
          </span>
        </div>
        <div className='src-log__body'>
          {log.length === 0 ? (
            <div className='src-empty'>
              {t(`${ns}.logEmpty`, {
                defaultValue: '还没检查过。点「立即检查上游」读一次。',
              })}
            </div>
          ) : (
            [...log]
              .sort((a, b) => b.at - a.at)
              .map((entry, index) => {
                const change = describeLogChange(entry);
                return (
                  <div className='src-logrow' key={`${entry.at}-${index}`}>
                    <span className='src-mono src-logrow__time'>
                      {formatCheckTime(entry.at)}
                    </span>
                    <span className={`src-logrow__change is-${change.tone}`}>
                      {change.tone === 'bad'
                        ? t(`${ns}.logFailed`, {
                            defaultValue: '检查失败：{{error}}',
                            error: change.text,
                          })
                        : change.text ||
                          t(`${ns}.logNone`, {
                            count: entry.listed ?? 0,
                            defaultValue: '无变化',
                          })}
                      {entry.manual ? (
                        <span className='src-hint'>
                          {' · '}
                          {t(`${ns}.logManual`, { defaultValue: '手动' })}
                        </span>
                      ) : null}
                    </span>
                    <span className='src-hint'>
                      {handled(entry, pending, t)}
                    </span>
                  </div>
                );
              })
          )}
        </div>
      </section>
    </div>
  );
}
