import { useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import {
  diffNoteText,
  formatInfoValue,
  formatThreshold,
  infoFieldLabel,
} from './modelInfo';
import { Button, ErrorState, PendingValue } from '../../components/ui';
import {
  useModelInfoMutations,
  useModelInfoReviewsQuery,
  type ModelInfoPrefill,
  type ModelInfoReview,
} from '../../hooks/models/useModelInfoQuery';
import { makerLabel } from '../../lib/model-makers';

const ns = 'page.console.modelOps.updates';

const FIRST_RUN_PREVIEW = 5;
const FILLED_PREVIEW = 4;
const TIER_PREVIEW = 5;
const DIFF_PREVIEW = 8;

const parsePrefill = (row: ModelInfoReview): ModelInfoPrefill | null => {
  if (!row.payload) return null;
  try {
    return JSON.parse(row.payload) as ModelInfoPrefill;
  } catch {
    return null;
  }
};

const perMtok = (value?: number) =>
  value == null ? null : String(Number(value.toFixed(4)));

/** "1M", "500K" — a context window at a glance. */
const shortTokens = (value?: number) => {
  if (!value) return '—';
  if (value >= 1_000_000) {
    const millions = Math.round((value / 1_000_000) * 100) / 100;
    return `${millions}M`;
  }
  return `${Math.round(value / 1000)}K`;
};

function Section({
  children,
  count,
  hint,
  id,
  title,
}: {
  children: ReactNode;
  count: number;
  hint: string;
  id: string;
  title: string;
}) {
  return (
    <section aria-labelledby={id} className='mo-updates__panel'>
      <div className='mo-updates__head'>
        <h2 id={id}>{title}</h2>
        <span className='mo-updates__count'>{count}</span>
        {hint ? <p>{hint}</p> : null}
      </div>
      {children}
    </section>
  );
}

/**
 * 资料更新: what the daily models.dev sync found that needs a person. New lab
 * releases wait to be registered; disagreements on facts that change a bill
 * or a request are proposed, never applied; models the lab prices by input
 * length wait for a tier; the first sync's release-date corrections come as
 * one batch; and fills of empty fields stay undoable for a day.
 */
export default function ModelUpdates() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const reviews = useModelInfoReviewsQuery();
  const mutations = useModelInfoMutations(t);
  const [showAllFirstRun, setShowAllFirstRun] = useState(false);
  const [showAllFilled, setShowAllFilled] = useState(false);
  const [showAllTiers, setShowAllTiers] = useState(false);
  const [showAllDiffs, setShowAllDiffs] = useState(false);

  const groups = useMemo(() => {
    const items = reviews.data?.items || [];
    const pick = (kind: ModelInfoReview['kind']) =>
      items.filter(
        (row) =>
          row.kind === kind &&
          (kind === 'filled'
            ? row.status === 'applied'
            : row.status === 'pending'),
      );
    return {
      diffs: pick('diff'),
      filled: pick('filled'),
      firstRun: pick('first_run'),
      followed: pick('price_followed'),
      fresh: pick('new_release'),
      tiers: pick('tier_gap'),
    };
  }, [reviews.data]);

  const lastRun = reviews.data?.last_run_at
    ? new Intl.DateTimeFormat(i18n.language || undefined, {
        day: '2-digit',
        hour: '2-digit',
        hour12: false,
        minute: '2-digit',
        month: '2-digit',
        year: 'numeric',
      }).format(new Date(reviews.data.last_run_at * 1000))
    : null;

  const busyId =
    mutations.resolveMutation.isPending && mutations.resolveMutation.variables
      ? mutations.resolveMutation.variables.id
      : null;
  const resolve = (id: number, action: 'accept' | 'keep' | 'undo') =>
    mutations.resolveMutation.mutate({ action, id });

  const channelFresh = groups.fresh.filter((row) => row.note === 'channel');
  const missingDates = reviews.data?.missing_release_dates ?? [];
  const nothing =
    !missingDates.length &&
    !groups.fresh.length &&
    !groups.diffs.length &&
    !groups.tiers.length &&
    !groups.followed.length &&
    !groups.firstRun.length &&
    !groups.filled.length;

  const dismissButton = (row: ModelInfoReview) => (
    <Button
      disabled={busyId === row.id}
      size='xs'
      variant='secondary'
      onClick={() => resolve(row.id, 'keep')}
    >
      {t(`${ns}.dismiss`, { defaultValue: '忽略此建议' })}
    </Button>
  );

  const decideButtons = (row: ModelInfoReview) => (
    <span className='mo-updates__actions'>
      <Button
        disabled={busyId === row.id}
        size='xs'
        variant='secondary'
        onClick={() => resolve(row.id, 'accept')}
      >
        {t(`${ns}.accept`, { defaultValue: '采用上游' })}
      </Button>
      <Button
        disabled={busyId === row.id}
        size='xs'
        variant='secondary'
        onClick={() => resolve(row.id, 'keep')}
      >
        {t(`${ns}.keep`, { defaultValue: '保留' })}
      </Button>
    </span>
  );

  return (
    <>
      <div className='mo-updates__bar'>
        <span className='mo-sub'>
          {reviews.isPending ? (
            <PendingValue chars={24} />
          ) : reviews.isError ? null : lastRun ? (
            t(`${ns}.lastSync`, {
              defaultValue: '上次同步 {{time}}',
              time: lastRun,
            })
          ) : (
            t(`${ns}.neverSynced`, { defaultValue: '还没有同步过' })
          )}
        </span>
        <Button
          loading={mutations.syncMutation.isPending}
          size='sm'
          variant='secondary'
          onClick={() => mutations.syncMutation.mutate()}
        >
          {t(`${ns}.syncNow`, { defaultValue: '现在同步' })}
        </Button>
      </div>
      {reviews.isError ? (
        <ErrorState
          title={t(`${ns}.loadFailed`, { defaultValue: '资料更新读取失败' })}
          action={
            <Button variant='ghost' onClick={() => reviews.refetch()}>
              {t('legacy.i18n.retry.e2d53a6d3a')}
            </Button>
          }
        />
      ) : reviews.isPending ? (
        <div className='mo-updates'>
          <div className='mo-updates__panel mo-updates__panel--pending'>
            <span className='mo-state'>
              <span aria-hidden='true' className='mo-state__spinner' />
              {t(`${ns}.loading`, { defaultValue: '正在读取资料更新' })}
            </span>
          </div>
        </div>
      ) : (
        <div className='mo-updates'>
          {missingDates.length ? (
            <Section
              count={missingDates.length}
              hint={t(`${ns}.missingDates.hint`, {
                defaultValue:
                  '按模型首次公开发布日期填写；未确认的日期会使模型排在厂商末尾。',
              })}
              id='mo-updates-missing-dates'
              title={t(`${ns}.missingDates.title`, {
                defaultValue: '在售模型缺少发布日期',
              })}
            >
              {missingDates.map((row) => (
                <div
                  className='mo-updates__row mo-updates__row--missing-date'
                  key={row.model_name}
                >
                  <span>{row.model_name}</span>
                  <Button
                    disabled={!row.model_id}
                    size='xs'
                    variant='secondary'
                    onClick={() =>
                      navigate(
                        `/console/model-ops?view=pending&model=${row.model_id}&panel=info`,
                      )
                    }
                  >
                    {t(`${ns}.missingDates.edit`, {
                      defaultValue: '核对发布日期',
                    })}
                  </Button>
                </div>
              ))}
            </Section>
          ) : null}
          {nothing ? (
            <p className='mo-updates__quiet'>
              {t(`${ns}.nothing`, {
                defaultValue: '资料都是最新的，没有要处理的事。',
              })}
            </p>
          ) : null}

          {groups.fresh.length || !nothing ? (
            <Section
              count={groups.fresh.length}
              hint={t(`${ns}.fresh.hint`, {
                defaultValue: '所有已知厂商，包含未排序的厂商',
              })}
              id='mo-updates-fresh'
              title={t(`${ns}.fresh.title`, {
                defaultValue: '新发布，还没上架',
              })}
            >
              {groups.fresh.length ? (
                <div className='mo-updates__grid mo-updates__grid--fresh'>
                  <div className='mo-updates__row mo-updates__row--head'>
                    <span>
                      {t(`${ns}.col.model`, { defaultValue: '模型' })}
                    </span>
                    <span>
                      {t(`${ns}.col.maker`, { defaultValue: '厂商' })}
                    </span>
                    <span>
                      {t(`${ns}.col.released`, { defaultValue: '发布' })}
                    </span>
                    <span>
                      {t(`${ns}.col.context`, { defaultValue: '上下文' })}
                    </span>
                    <span>
                      {t(`${ns}.col.officialPrice`, {
                        defaultValue: '官方价 $/M 入 / 出',
                      })}
                    </span>
                    <span />
                  </div>
                  {groups.fresh.map((row) => {
                    const prefill = parsePrefill(row);
                    const prices = [
                      perMtok(prefill?.official_input_per_mtok),
                      perMtok(prefill?.official_output_per_mtok),
                    ];
                    return (
                      <div className='mo-updates__row' key={row.id}>
                        <span className='mo-name'>
                          {row.model_name}
                          {row.note === 'channel' ? (
                            <small className='mo-updates__from'>
                              {t(`${ns}.fresh.fromChannel`, {
                                defaultValue: '渠道里出现',
                              })}
                            </small>
                          ) : null}
                        </span>
                        <span>{makerLabel(row.maker, t)}</span>
                        <span className='mo-num'>
                          {prefill?.release_date || row.upstream_value || '—'}
                        </span>
                        <span className='mo-num'>
                          {shortTokens(prefill?.context_length)}
                        </span>
                        <span className='mo-num'>
                          {prices.every(Boolean) ? prices.join(' / ') : '—'}
                        </span>
                        <span className='mo-updates__actions'>
                          <Button
                            size='xs'
                            variant='primary'
                            onClick={() =>
                              navigate(
                                `/console/model-ops?view=pending&model=new&panel=meta&prefill=${encodeURIComponent(row.model_name)}`,
                              )
                            }
                          >
                            {t(`${ns}.fresh.register`, {
                              defaultValue: '登记',
                            })}
                          </Button>
                          {dismissButton(row)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              ) : null}
              <p className='mo-updates__footnote'>
                {channelFresh.length
                  ? t(`${ns}.fresh.channelNote`, {
                      defaultValue:
                        '渠道里出现、目录里还没有的模型也列在这里，标着「渠道里出现」。',
                    })
                  : t(`${ns}.fresh.channelNone`, {
                      defaultValue:
                        '渠道里出现、目录里还没有的模型也会列在这里。今天没有。',
                    })}
              </p>
            </Section>
          ) : null}

          {groups.diffs.length ? (
            <Section
              count={groups.diffs.length}
              hint={t(`${ns}.diff.hint`, {
                defaultValue: '价格、上下文会影响计费和接入，只提示，不自动改',
              })}
              id='mo-updates-diff'
              title={t(`${ns}.diff.title`, { defaultValue: '和上游不一致' })}
            >
              <div className='mo-updates__grid mo-updates__grid--diff'>
                <div className='mo-updates__row mo-updates__row--head'>
                  <span>{t(`${ns}.col.model`, { defaultValue: '模型' })}</span>
                  <span>{t(`${ns}.col.field`, { defaultValue: '字段' })}</span>
                  <span>{t(`${ns}.col.now`, { defaultValue: '现在' })}</span>
                  <span>models.dev</span>
                  <span>{t(`${ns}.col.note`, { defaultValue: '说明' })}</span>
                  <span />
                </div>
                {(showAllDiffs
                  ? groups.diffs
                  : groups.diffs.slice(0, DIFF_PREVIEW)
                ).map((row) => (
                  <div className='mo-updates__row' key={row.id}>
                    <span className='mo-name' title={row.model_name}>
                      {row.model_name}
                    </span>
                    <span>{infoFieldLabel(row.field, t)}</span>
                    <span className='mo-num mo-updates__was'>
                      {formatInfoValue(row.field, row.local_value, t)}
                    </span>
                    <span className='mo-num mo-updates__up'>
                      {formatInfoValue(row.field, row.upstream_value, t)}
                    </span>
                    <span className='mo-updates__note'>
                      {diffNoteText(row.note, t)}
                    </span>
                    {decideButtons(row)}
                  </div>
                ))}
              </div>
              {groups.diffs.length > DIFF_PREVIEW ? (
                <div className='mo-updates__foot'>
                  <Button
                    size='xs'
                    variant='link'
                    onClick={() => setShowAllDiffs((value) => !value)}
                  >
                    {showAllDiffs
                      ? t(`${ns}.collapse`, { defaultValue: '收起' })
                      : t(`${ns}.expandRest`, {
                          count: groups.diffs.length - DIFF_PREVIEW,
                          defaultValue: '展开其余 {{count}} 项',
                        })}
                  </Button>
                </div>
              ) : null}
            </Section>
          ) : null}

          {groups.tiers.length ? (
            <Section
              count={groups.tiers.length}
              hint={t(`${ns}.tier.hint`, {
                defaultValue:
                  '长输入的请求可能按低价卖出去。照抄官方分档前先看渠道实际账单：中转不一定转嫁长上下文加价',
              })}
              id='mo-updates-tier'
              title={t(`${ns}.tier.title`, {
                defaultValue: '官方分段收费，我们还是单一价',
              })}
            >
              <div className='mo-updates__grid mo-updates__grid--tier'>
                <div className='mo-updates__row mo-updates__row--head'>
                  <span>{t(`${ns}.col.model`, { defaultValue: '模型' })}</span>
                  <span>
                    {t(`${ns}.col.threshold`, { defaultValue: '分段点' })}
                  </span>
                  <span>
                    {t(`${ns}.col.tierPrice`, {
                      defaultValue: '官方高段价 $/M',
                    })}
                  </span>
                  <span>
                    {t(`${ns}.col.ourPrice`, {
                      defaultValue: '我们现在（稳定）',
                    })}
                  </span>
                  <span />
                </div>
                {(showAllTiers
                  ? groups.tiers
                  : groups.tiers.slice(0, TIER_PREVIEW)
                ).map((row) => (
                  <div className='mo-updates__row' key={row.id}>
                    <span className='mo-name' title={row.model_name}>
                      {row.model_name}
                    </span>
                    <span className='mo-num'>
                      {t(`${ns}.tier.thresholdValue`, {
                        defaultValue: '{{threshold}} 输入',
                        threshold: formatThreshold(Number(row.note) || 0),
                      })}
                    </span>
                    <span className='mo-num mo-updates__up'>
                      {row.upstream_value || '—'}
                    </span>
                    <span className='mo-num mo-updates__was'>
                      {row.local_value
                        ? t(`${ns}.tier.single`, {
                            defaultValue: '{{price}} · 单一价',
                            price: row.local_value,
                          })
                        : '—'}
                    </span>
                    <span className='mo-updates__actions'>
                      <Button
                        disabled={!row.model_id}
                        size='xs'
                        variant='primary'
                        onClick={() =>
                          navigate(
                            `/console/model-ops?view=pending&model=${row.model_id}`,
                          )
                        }
                      >
                        {t(`${ns}.tier.set`, { defaultValue: '去设置' })}
                      </Button>
                      {dismissButton(row)}
                    </span>
                  </div>
                ))}
              </div>
              {groups.tiers.length > TIER_PREVIEW ? (
                <div className='mo-updates__foot'>
                  <Button
                    size='xs'
                    variant='link'
                    onClick={() => setShowAllTiers((value) => !value)}
                  >
                    {showAllTiers
                      ? t(`${ns}.collapse`, { defaultValue: '收起' })
                      : t(`${ns}.expandRest`, {
                          count: groups.tiers.length - TIER_PREVIEW,
                          defaultValue: '展开其余 {{count}} 项',
                        })}
                  </Button>
                </div>
              ) : null}
            </Section>
          ) : null}

          {groups.firstRun.length ? (
            <Section
              count={groups.firstRun.length}
              hint={t(`${ns}.firstRun.hint`, {
                defaultValue:
                  '库里的日期大多是上架那天，建议整批换成真实发布日',
              })}
              id='mo-updates-first'
              title={t(`${ns}.firstRun.title`, {
                defaultValue: '首次校正：发布日期',
              })}
            >
              <div className='mo-updates__grid mo-updates__grid--diff'>
                <div className='mo-updates__row mo-updates__row--head'>
                  <span>{t(`${ns}.col.model`, { defaultValue: '模型' })}</span>
                  <span>{t(`${ns}.col.field`, { defaultValue: '字段' })}</span>
                  <span>{t(`${ns}.col.now`, { defaultValue: '现在' })}</span>
                  <span>models.dev</span>
                  <span>{t(`${ns}.col.gap`, { defaultValue: '相差' })}</span>
                  <span />
                </div>
                {(showAllFirstRun
                  ? groups.firstRun
                  : groups.firstRun.slice(0, FIRST_RUN_PREVIEW)
                ).map((row) => (
                  <div className='mo-updates__row' key={row.id}>
                    <span className='mo-name' title={row.model_name}>
                      {row.model_name}
                    </span>
                    <span>{infoFieldLabel(row.field, t)}</span>
                    <span className='mo-num mo-updates__was'>
                      {row.local_value || '—'}
                    </span>
                    <span className='mo-num mo-updates__up'>
                      {row.upstream_value}
                    </span>
                    <span className='mo-updates__note'>
                      {t(`${ns}.firstRun.days`, {
                        count: Number(row.note) || 0,
                        defaultValue: '{{count}} 天',
                      })}
                    </span>
                    {decideButtons(row)}
                  </div>
                ))}
              </div>
              <div className='mo-updates__foot'>
                {groups.firstRun.length > FIRST_RUN_PREVIEW ? (
                  <Button
                    size='xs'
                    variant='link'
                    onClick={() => setShowAllFirstRun((value) => !value)}
                  >
                    {showAllFirstRun
                      ? t(`${ns}.collapse`, { defaultValue: '收起' })
                      : t(`${ns}.expandRest`, {
                          count: groups.firstRun.length - FIRST_RUN_PREVIEW,
                          defaultValue: '展开其余 {{count}} 项',
                        })}
                  </Button>
                ) : (
                  <span />
                )}
                <Button
                  loading={mutations.acceptFirstRunMutation.isPending}
                  size='sm'
                  variant='primary'
                  onClick={() =>
                    mutations.acceptFirstRunMutation.mutate(
                      groups.firstRun.map((row) => row.id),
                    )
                  }
                >
                  {t(`${ns}.firstRun.acceptAll`, {
                    count: groups.firstRun.length,
                    defaultValue: '全部采用上游（{{count}} 项）',
                  })}
                </Button>
              </div>
            </Section>
          ) : null}

          {groups.followed.length ? (
            <Section
              count={groups.followed.length}
              hint=''
              id='mo-updates-followed'
              title={t(`${ns}.followed.title`, {
                defaultValue: '官方价变动 · 已跟随',
              })}
            >
              <div className='mo-updates__grid mo-updates__grid--followed'>
                {groups.followed.map((row) => (
                  <div className='mo-updates__row' key={row.id}>
                    <span className='mo-name' title={row.model_name}>
                      {row.model_name}
                    </span>
                    <span>{infoFieldLabel(row.field, t)}</span>
                    <span className='mo-num'>
                      {formatInfoValue(row.field, row.local_value, t)} →{' '}
                      {formatInfoValue(row.field, row.upstream_value, t)}
                    </span>
                    <span className='mo-updates__actions'>
                      <Button
                        disabled={busyId === row.id}
                        size='xs'
                        variant='ghost'
                        onClick={() => resolve(row.id, 'keep')}
                      >
                        {t(`${ns}.followed.ack`, { defaultValue: '知道了' })}
                      </Button>
                    </span>
                  </div>
                ))}
              </div>
            </Section>
          ) : null}

          {groups.filled.length ? (
            <Section
              count={groups.filled.length}
              hint={t(`${ns}.filled.hint`, {
                defaultValue: '原来是空的字段，直接写入，可以撤销',
              })}
              id='mo-updates-filled'
              title={t(`${ns}.filled.title`, { defaultValue: '已自动补齐' })}
            >
              <div className='mo-updates__grid mo-updates__grid--filled'>
                {(showAllFilled
                  ? groups.filled
                  : groups.filled.slice(0, FILLED_PREVIEW)
                ).map((row) => (
                  <div className='mo-updates__row' key={row.id}>
                    <span className='mo-name' title={row.model_name}>
                      {row.model_name}
                    </span>
                    <span>{infoFieldLabel(row.field, t)}</span>
                    <span className='mo-num'>
                      {formatInfoValue(row.field, row.upstream_value, t)}
                    </span>
                    <span className='mo-updates__actions'>
                      <Button
                        disabled={busyId === row.id}
                        size='xs'
                        variant='link'
                        onClick={() => resolve(row.id, 'undo')}
                      >
                        {t(`${ns}.filled.undo`, { defaultValue: '撤销' })}
                      </Button>
                    </span>
                  </div>
                ))}
              </div>
              {groups.filled.length > FILLED_PREVIEW ? (
                <div className='mo-updates__foot'>
                  <Button
                    size='xs'
                    variant='link'
                    onClick={() => setShowAllFilled((value) => !value)}
                  >
                    {showAllFilled
                      ? t(`${ns}.collapse`, { defaultValue: '收起' })
                      : t(`${ns}.expandRest`, {
                          count: groups.filled.length - FILLED_PREVIEW,
                          defaultValue: '展开其余 {{count}} 项',
                        })}
                  </Button>
                </div>
              ) : null}
            </Section>
          ) : null}

          <p className='mo-updates__source'>
            {t(`${ns}.attribution`, {
              defaultValue:
                '资料来自 models.dev（github.com/anomalyco/models.dev，MIT 许可）',
            })}
          </p>
        </div>
      )}
    </>
  );
}
