/* One derivation of "what is true about this model", read by both the ledger
 * row and the inspector panel.
 *
 * The two used to compute this separately — the table decided whether a model
 * was blocked, and the row expander decided again — which is how the ledger
 * could show a green switch over a tier the expander called unpriced. There is
 * one function now and both surfaces render its output.
 */
import {
  healthTone,
  indexSellPrices,
  LANE_ORDER,
  laneHasAnyPrice,
} from './formatters';
import {
  normalizeLaneSlug,
  toLane,
  type LanePrice,
  type SellPriceRow,
} from './sellPrices';
import type { ApiRecord } from './apiRecord';
import type {
  ModelOpsPoolEntry,
  ModelOpsPools,
} from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

export type ModelRecord = ApiRecord & {
  id: number | string;
  lanes?: Array<ApiRecord>;
  model_name?: string;
  status?: number | boolean;
};

export type ModelView = {
  /** The tier as published, whether or not the sell-price table was fetched. */
  cheap: LanePrice | undefined;
  cheapPool: ModelOpsPoolEntry[];
  /** Every registered lane's price and pool, keyed by slug. `direct`/`cheap`
   *  above are the two builtins, kept because the ledger's fixed columns read
   *  them directly; anything that has to handle a lane an operator added — the
   *  inspector's price cards — reads these instead. */
  lanePrices: Record<string, LanePrice | undefined>;
  lanePools: Record<string, ModelOpsPoolEntry[]>;
  description: string;
  direct: LanePrice | undefined;
  directPool: ModelOpsPoolEntry[];
  id: number | string;
  /** Why this model may not be listed, in one sentence, or null. */
  blockReason: string | null;
  listed: boolean;
  name: string;
  /** Both tiers unpriced — listing it would bill every request at $0. */
  neitherPriced: boolean;
  offender: ModelOpsPoolEntry | undefined;
  pool: ModelOpsPoolEntry[];
  record: ModelRecord;
  spend: number | undefined;
  vendorName: string;
};

export const laneOfRecord = (
  record: ModelRecord,
  lane: string,
  priceIndex: Map<string, SellPriceRow>,
): LanePrice | undefined => {
  const fromList = (record.lanes || []).find(
    (item) => normalizeLaneSlug(item.channel_group) === lane,
  );
  if (fromList) {
    return toLane({
      ...priceIndex.get(`${record.model_name}:${lane}`),
      ...fromList,
      model_name: fromList.model_name || record.model_name || '',
    } as SellPriceRow);
  }
  const row = priceIndex.get(`${record.model_name}:${lane}`);
  return row ? toLane(row) : undefined;
};

export function buildModelViews({
  poolsLoading,
  lanes,
  pools,
  records,
  sellPrices,
  spend,
  t,
  vendorMap,
}: {
  /** Registered lane slugs. Defaults to the two builtins so a caller that has
   *  not loaded the registry yet still renders. */
  lanes?: string[];
  pools: ModelOpsPools;
  poolsLoading: boolean;
  records: ModelRecord[];
  sellPrices: SellPriceRow[];
  spend: Record<string, number>;
  t: TFunction;
  vendorMap?: Record<string, { name?: string }>;
}): ModelView[] {
  const priceIndex = indexSellPrices(sellPrices);
  const laneSlugs =
    lanes && lanes.length ? lanes : (LANE_ORDER as unknown as string[]);

  return records.map((record) => {
    const name = String(record.model_name || '');
    const lanePrices: Record<string, LanePrice | undefined> = {};
    const lanePools: Record<string, ModelOpsPoolEntry[]> = {};
    laneSlugs.forEach((slug) => {
      lanePrices[slug] = laneOfRecord(record, slug, priceIndex);
      lanePools[slug] =
        (pools[name] as Record<string, ModelOpsPoolEntry[]>)?.[slug] || [];
    });
    const direct =
      lanePrices.direct ?? laneOfRecord(record, 'direct', priceIndex);
    const cheap = lanePrices.cheap ?? laneOfRecord(record, 'cheap', priceIndex);
    const directPool = lanePools.direct ?? pools[name]?.direct ?? [];
    const cheapPool = lanePools.cheap ?? pools[name]?.cheap ?? [];
    const pool = Object.values(lanePools).flat();

    /* A model needs a priced, live source on its sale lane. Agent-lane-only
       models use cheap for Keys without an explicit lane choice. */
    let blockReason: string | null = null;
    const hasDirectPrice = laneHasAnyPrice(direct);
    const hasCheapPrice = laneHasAnyPrice(cheap);
    if (!hasDirectPrice && !hasCheapPrice) {
      blockReason = t(`${ns}.models.blockedNoPrice`, {
        defaultValue: '没有任何档位售价，上架后请求会按 $0 计费',
      });
    } else if (
      !poolsLoading &&
      !(hasDirectPrice ? directPool : cheapPool).some(
        (entry) => entry.enabled && entry.channel_status === 1,
      )
    ) {
      blockReason = hasDirectPrice
        ? t(`${ns}.models.blockedNoStablePool`, {
            defaultValue: '稳定档没有已启用的来源，当前无法路由请求',
          })
        : t(`${ns}.models.blockedNoCheapPool`, {
            defaultValue: 'Agent 专线没有已启用的来源，当前无法路由请求',
          });
    }

    return {
      blockReason,
      cheap,
      cheapPool,
      description: String(
        record.description ||
          record.description_zh ||
          record.description_en ||
          '',
      ),
      direct,
      directPool,
      id: record.id,
      lanePools,
      lanePrices,
      listed: record.status === true || record.status === 1,
      name,
      neitherPriced: !laneHasAnyPrice(direct) && !laneHasAnyPrice(cheap),
      offender: pool.find(
        (entry) => healthTone(entry.health, entry.channel_status) === 'bad',
      ),
      pool,
      record,
      spend: spend[name],
      vendorName: vendorMap?.[String(record.vendor_id ?? '')]?.name || '',
    };
  });
}
