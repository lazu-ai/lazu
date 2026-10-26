import { ChevronDown, ChevronRight, TriangleAlert } from 'lucide-react';
import { Fragment, useState } from 'react';
import {
  aggregatePoolHealth,
  formatPerMtok,
  formatRate,
  healthTone,
  LANE_ORDER,
} from './formatters';
import { laneHasAnyPrice } from './formatters';
import {
  emptyBuckets,
  foldBuckets,
  ModelOpsBars,
  poolBuckets,
} from './healthBars';
import { infoSourceLabel } from './modelInfo';
import { officialOf, pinnedSet } from './ratioPricing';
import { Badge } from '../../components/ui/badge';
import { Checkbox } from '../../components/ui/checkbox';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { Switch } from '../../components/ui/switch';
import {
  groupByMaker,
  makerBrand,
  makerLabel,
  makerOf,
} from '../../lib/model-makers';
import type { ModelView } from './modelView';
import type { LanePrice } from './sellPrices';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

/* The maker is the group header above the rows, so it takes no column; a
   row opens its dialog, which is also where a model is edited or deleted. */
/* The last column holds a 40px switch inside the cells' 16px insets; at 64px
   it clipped the switch against the ledger's edge. */
const COLS = '48px minmax(0, 1fr) 104px 128px 156px 156px 196px 80px';

/* A click that landed on a control inside the row belongs to that control.
   Guarding here keeps the cells free of stopPropagation wrappers, which are
   click handlers on non-interactive elements as far as a11y is concerned. */
const fromControl = (event: React.MouseEvent | React.KeyboardEvent) =>
  Boolean(
    (event.target as HTMLElement | null)?.closest(
      'a, button, input, label, [role="switch"], [role="checkbox"], [role="menu"]',
    ),
  );

type ModelLedgerProps = {
  /** Each lane's default ratio, percent of the official price. */
  laneRatios: Record<string, number>;
  onSelect: (id: string | null) => void;
  onToggleListed: (view: ModelView, listed: boolean) => void;
  onToggleSelection: (view: ModelView, checked: boolean) => void;
  /** Models whose release date is waiting on a 资料更新 decision. */
  pendingRelease?: Set<string>;
  poolsLoading: boolean;
  selectedId: string | null;
  selectedIds: Set<number | string>;
  t: TFunction;
  views: ModelView[];
};

/** The published price of one tier, as tabular figures rather than prose. */
export function lanePriceText(price: LanePrice | undefined, t: TFunction) {
  if (price?.per_call != null) {
    return `$${formatPerMtok(price.per_call)}${t(`${ns}.tier.perCallUnit`, { defaultValue: '/ 次' })}`;
  }
  if (price?.per_second != null) {
    return `$${formatPerMtok(price.per_second)}${t(`${ns}.tier.perSecondUnit`, { defaultValue: '/ 秒' })}`;
  }
  return null;
}

/** One lane of one model: the percent of the official price it follows, or
 *  the fixed price it charges. */
function LaneCell({
  laneRatio,
  price,
  t,
}: {
  laneRatio: number;
  price: LanePrice | undefined;
  t: TFunction;
}) {
  if (!laneHasAnyPrice(price)) {
    return (
      <span style={{ color: 'var(--lazu-text-faint)' }}>
        {t(`${ns}.price.laneOff`, { defaultValue: '未开' })}
      </span>
    );
  }
  if (price?.follow_official) {
    const pins = [...pinnedSet(price.pinned_fields)].filter(
      (field) => field !== 'context_tier',
    ).length;
    return (
      <span className='mo-lanecell'>
        <b className='mo-num'>{price.price_ratio ?? laneRatio}%</b>
        {pins > 0 ? (
          <Badge variant='warning'>
            {t(`${ns}.price.pinnedCount`, {
              count: pins,
              defaultValue: '{{count}} 项固定',
            })}
          </Badge>
        ) : price.price_ratio != null ? (
          <small className='mo-sub'>
            {t(`${ns}.price.ownRatio`, { defaultValue: '单独' })}
          </small>
        ) : null}
      </span>
    );
  }
  const flat = lanePriceText(price, t);
  return (
    <span className='mo-lanecell'>
      <span className='mo-mono'>
        {flat ??
          `${formatPerMtok(price?.input_per_million)} / ${formatPerMtok(price?.output_per_million)}`}
      </span>
      <small className='mo-sub'>
        {t(`${ns}.price.fixedShort`, { defaultValue: '固定' })}
      </small>
    </span>
  );
}

/** 模型账簿 — one row per model, 40px tall, scanned top to bottom. */
export default function ModelLedger({
  laneRatios,
  onSelect,
  onToggleListed,
  onToggleSelection,
  pendingRelease,
  poolsLoading,
  selectedId,
  selectedIds,
  t,
  views,
}: ModelLedgerProps) {
  const optional = 'mo-col-optional';
  // Rows arrive in catalog order (maker order, newest release first); the
  // ledger folds them under one header per maker without reordering.
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const serverRanks = new Map(views.map((view, index) => [view, index]));
  const groups = groupByMaker(
    views,
    (view) =>
      makerOf(String(view.record.maker_resolved || ''), { id: view.name }),
    (view) => serverRanks.get(view),
  );
  const toggleGroup = (maker: string) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(maker)) next.delete(maker);
      else next.add(maker);
      return next;
    });

  return (
    <div
      className='mo-ledger'
      role='table'
      style={
        {
          '--cols': COLS,
          '--row-h': '52px',
        } as React.CSSProperties
      }
    >
      <div className='mo-ledger__head' role='row'>
        <span role='columnheader' />
        <span role='columnheader'>
          {t(`${ns}.models.colModel`, { defaultValue: '模型' })}
        </span>
        <span className={optional} role='columnheader'>
          {t(`${ns}.models.colReleased`, { defaultValue: '发布' })}
        </span>
        <span className={optional} role='columnheader'>
          {t(`${ns}.price.official`, { defaultValue: '官方价 $/M' })}
        </span>
        <span role='columnheader'>
          {t(`${ns}.price.stableLane`, { defaultValue: '稳定线路' })}
        </span>
        <span className={optional} role='columnheader'>
          {t(`${ns}.price.cheapLane`, { defaultValue: 'Agent 专线' })}
        </span>
        <span className={`${optional} mo-col-compact`} role='columnheader'>
          {t(`${ns}.models.colLaneHealth`, { defaultValue: '线路健康' })}
        </span>
        <span role='columnheader'>
          {t(`${ns}.models.colListed`, { defaultValue: '上架' })}
        </span>
      </div>

      {groups.map(({ maker, rows }) => {
        const open = !collapsed.has(maker);
        return (
          <Fragment key={maker || 'other'}>
            <div className='mo-ledger__group' role='row'>
              <button
                aria-expanded={open}
                className='mo-ledger__group-toggle'
                type='button'
                onClick={() => toggleGroup(maker)}
              >
                {open ? (
                  <ChevronDown aria-hidden='true' size={14} />
                ) : (
                  <ChevronRight aria-hidden='true' size={14} />
                )}
                <ProviderLogo
                  brand={makerBrand(maker, { name: rows[0]?.vendorName })}
                  decorative
                  size={18}
                />
                <b>{makerLabel(maker, t)}</b>
                <span className='mo-ledger__group-count'>{rows.length}</span>
              </button>
            </div>
            {open ? rows.map((view) => renderRow(view)) : null}
          </Fragment>
        );
      })}
    </div>
  );

  function renderRow(view: ModelView) {
    const id = String(view.id);
    const releaseDate = String(view.record.release_date || '');
    const releaseSource = (
      view.record.info_sources as Record<string, string> | undefined
    )?.release_date;
    const releasePending =
      releaseSource === 'legacy' || Boolean(pendingRelease?.has(view.name));
    const isSelected = selectedId === id;
    const blocked = Boolean(view.blockReason) && !view.listed;
    const official = officialOf(view.record as Record<string, unknown>);
    return (
      <div
        className={`mo-ledger__row${isSelected ? ' is-selected' : ''}`}
        key={id}
        aria-selected={isSelected}
        role='row'
        tabIndex={0}
        onClick={(event) => {
          if (fromControl(event)) return;
          onSelect(id);
        }}
        onKeyDown={(event) => {
          if (fromControl(event)) return;
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            onSelect(id);
          }
        }}
      >
        <span role='cell'>
          <Checkbox
            aria-label={t(`${ns}.models.selectModel`, {
              defaultValue: '选择 {{name}}',
              name: view.name,
            })}
            checked={selectedIds.has(view.id)}
            onCheckedChange={(checked: unknown) =>
              onToggleSelection(view, checked === true)
            }
          />
        </span>
        <span className='mo-ledger__cell--name' role='cell'>
          <span className='mo-name'>{view.name}</span>
          {blocked ? (
            <span
              className='mo-flag'
              style={{
                color: view.neitherPriced
                  ? 'var(--lazu-danger)'
                  : 'var(--lazu-warning)',
              }}
              title={view.blockReason || undefined}
            >
              <TriangleAlert aria-hidden='true' size={13} />
            </span>
          ) : null}
        </span>
        <span className={`mo-num ${optional}`} role='cell'>
          {releaseDate ? (
            <span className='mo-release'>
              {releaseDate}
              <small
                className={`mo-release__src${releasePending ? ' is-pending' : ''}`}
              >
                {releasePending
                  ? infoSourceLabel('legacy', t)
                  : infoSourceLabel(releaseSource, t)}
              </small>
            </span>
          ) : (
            <span style={{ color: 'var(--lazu-text-faint)' }}>—</span>
          )}
        </span>
        <span className={`mo-num mo-mono ${optional}`} role='cell'>
          {official
            ? `${formatPerMtok(official.input)} / ${formatPerMtok(official.output)}`
            : '—'}
        </span>
        <span role='cell'>
          {view.neitherPriced ? (
            /* A risk, said in words: warning-coloured text and its icon,
               never a filled red chip (brand/foundations/feedback-system.md). */
            <span className='mo-risk-line'>
              <TriangleAlert aria-hidden='true' size={13} />
              {t(`${ns}.models.zeroRiskLine`, {
                defaultValue: '未定价，上架后按 $0 计费',
              })}
            </span>
          ) : (
            <LaneCell
              laneRatio={laneRatios.direct ?? 100}
              price={view.direct}
              t={t}
            />
          )}
        </span>
        <span className={optional} role='cell'>
          <LaneCell
            laneRatio={laneRatios.cheap ?? 100}
            price={view.cheap}
            t={t}
          />
        </span>
        <span
          className={`${optional} mo-col-compact mo-ledger__cell--lanes`}
          role='cell'
        >
          {poolsLoading && view.pool.length === 0 ? (
            <span className='mo-sub'>
              {t(`${ns}.models.poolLoading`, { defaultValue: '读取中' })}
            </span>
          ) : view.pool.length === 0 ? (
            <span className='mo-sub'>
              {t(`${ns}.models.noSources`, { defaultValue: '无来源' })}
            </span>
          ) : (
            <span
              aria-label={t(`${ns}.models.poolSummary`, {
                count: view.pool.length,
                defaultValue: '{{count}} 个来源',
              })}
              className='mo-lanes'
              role='group'
            >
              {LANE_ORDER.map((lane) => {
                const entries = view.pool.filter(
                  (entry) => entry.lane === lane,
                );
                if (!entries.length) return null;
                const pooled = aggregatePoolHealth(entries);
                const tone = healthTone(pooled);
                const pooledSlices = foldBuckets(poolBuckets(entries), 2);
                const laneLabel =
                  lane === 'cheap'
                    ? t(`${ns}.tier.cheap`, { defaultValue: 'Agent' })
                    : t(`${ns}.tier.stable`, { defaultValue: '稳定' });
                return (
                  <span className='mo-lanes__row' key={lane}>
                    <span className='mo-lanes__label'>{laneLabel}</span>
                    <ModelOpsBars
                      buckets={
                        pooledSlices.length ? pooledSlices : emptyBuckets(24)
                      }
                      height={8}
                      label={t(`${ns}.models.laneBarsLabel`, {
                        defaultValue: '{{name}} {{lane}}线路的来源合并记录',
                        lane: laneLabel,
                        name: view.name,
                      })}
                    />
                    <span
                      className={`mo-lanes__uptime mo-lanes__uptime--${tone}`}
                    >
                      {pooled.request_count > 0
                        ? formatRate(pooled.success_rate)
                        : '—'}
                    </span>
                    <span className='mo-lanes__count'>
                      {t(`${ns}.models.laneSources`, {
                        defaultValue: '{{count}} 源',
                        count: entries.length,
                      })}
                    </span>
                  </span>
                );
              })}
            </span>
          )}
        </span>
        <span role='cell'>
          <Switch
            aria-label={
              view.blockReason ||
              t(`${ns}.models.toggleListing`, {
                defaultValue: '上架 {{name}}',
                name: view.name,
              })
            }
            checked={view.listed}
            disabled={!view.listed && (poolsLoading || blocked)}
            title={view.blockReason || undefined}
            onCheckedChange={(checked: boolean) =>
              onToggleListed(view, checked)
            }
          />
        </span>
      </div>
    );
  }
}
