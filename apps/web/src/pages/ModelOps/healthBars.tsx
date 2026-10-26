import { memo, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { bucketTone } from './formatters';
import {
  StatusBars,
  formatSliceRate,
  formatSliceWhen,
  type StatusBarSlice,
  type StatusBarTone,
} from '../../components/ui/status-bars';
import type {
  ModelOpsPoolEntry,
  ModelOpsProbeBucket,
} from '../../hooks/models/useModelOpsQuery';

/* Model operations draws a source's (or a tier's) record with the health
   page's thin bars. The window's slices come from the server; this file only
   pools them across sources and turns them into bars. */

const TONE: Record<ReturnType<typeof bucketTone>, StatusBarTone> = {
  bad: 'bad',
  idle: 'none',
  ok: 'ok',
  warn: 'warn',
};

/** Several sources' slices summed index by index: a tier's record is every
 *  request its pool served, not an average of averages. */
export const poolBuckets = (
  entries: ModelOpsPoolEntry[] | undefined,
): ModelOpsProbeBucket[] => {
  const series = (entries || [])
    .map((entry) => entry.health?.buckets || [])
    .filter((buckets) => buckets.length > 0);
  if (!series.length) return [];
  const length = Math.max(...series.map((buckets) => buckets.length));
  return Array.from({ length }, (_, index) => {
    const slices = series.map((buckets) => buckets[index]).filter(Boolean);
    const first = slices[0];
    const requests = slices.reduce((sum, b) => sum + b.request_count, 0);
    const errors = slices.reduce((sum, b) => sum + b.error_count, 0);
    return {
      start_at: first?.start_at ?? '',
      end_at: first?.end_at ?? '',
      request_count: requests,
      error_count: errors,
      success_rate: requests > 0 ? Math.max(0, 1 - errors / requests) : null,
    };
  });
};

/** A window with nothing in it, drawn as grey "no data" bars the way the
 *  health page draws a quiet lane, so every lane row keeps its bars. */
export const emptyBuckets = (count: number): ModelOpsProbeBucket[] =>
  Array.from({ length: count }, () => ({
    start_at: '',
    end_at: '',
    request_count: 0,
    error_count: 0,
    success_rate: null,
  }));

/** Pairs of slices folded into one, for a column too narrow for 48 bars. */
export const foldBuckets = (
  buckets: ModelOpsProbeBucket[],
  factor: number,
): ModelOpsProbeBucket[] => {
  if (factor <= 1) return buckets;
  const folded: ModelOpsProbeBucket[] = [];
  for (let index = 0; index < buckets.length; index += factor) {
    const group = buckets.slice(index, index + factor);
    const requests = group.reduce((sum, b) => sum + b.request_count, 0);
    const errors = group.reduce((sum, b) => sum + b.error_count, 0);
    folded.push({
      start_at: group[0].start_at,
      end_at: group[group.length - 1].end_at,
      request_count: requests,
      error_count: errors,
      success_rate: requests > 0 ? Math.max(0, 1 - errors / requests) : null,
    });
  }
  return folded;
};

type ModelOpsBarsProps = {
  buckets: ModelOpsProbeBucket[];
  label: string;
  height?: number;
  /** The record is probes, not traffic: "observing" for a route being
   *  watched before going live, "idle" for any other route without traffic. */
  probed?: 'idle' | 'observing';
};

/** Two records hold the same slices. Callers pool and fold buckets during
 *  render, so the array is new every time even when nothing changed. */
const sameBuckets = (a: ModelOpsProbeBucket[], b: ModelOpsProbeBucket[]) =>
  a === b ||
  (a.length === b.length &&
    a.every((bucket, index) => {
      const other = b[index];
      return (
        bucket.start_at === other.start_at &&
        bucket.end_at === other.end_at &&
        bucket.request_count === other.request_count &&
        bucket.error_count === other.error_count &&
        bucket.success_rate === other.success_rate
      );
    }));

/* A ledger re-renders every row when a dialog opens or a filter moves; the
   bars only redraw when their own slices, label or mode change. */
export const ModelOpsBars = memo(
  ModelOpsBarsRecord,
  (prev, next) =>
    prev.label === next.label &&
    prev.height === next.height &&
    prev.probed === next.probed &&
    sameBuckets(prev.buckets, next.buckets),
);

function ModelOpsBarsRecord({
  buckets,
  label,
  height,
  probed,
}: ModelOpsBarsProps) {
  const { t, i18n } = useTranslation();
  const language = i18n.language;
  const slices = useMemo(
    () => buildSlices(buckets, probed, t, language),
    [buckets, probed, t, language],
  );
  return <StatusBars height={height} label={label} slices={slices} />;
}

function buildSlices(
  buckets: ModelOpsProbeBucket[],
  probed: ModelOpsBarsProps['probed'],
  t: ReturnType<typeof useTranslation>['t'],
  language: string,
): StatusBarSlice[] {
  const toneWord = (tone: StatusBarTone) =>
    t(`page.console.channelHealth.tip.${tone}`, {
      defaultValue: {
        ok: '正常',
        warn: '部分失败',
        bad: '中断',
        none: '暂无数据',
        future: '还没到',
        blocked: '无法探测',
      }[tone],
    });
  return buckets.map((bucket) => {
    const tone = TONE[bucketTone(bucket.success_rate)];
    const seconds =
      (new Date(bucket.end_at).getTime() -
        new Date(bucket.start_at).getTime()) /
      1000;
    return {
      tone,
      when: formatSliceWhen(bucket.start_at, seconds || 0, language),
      verdict:
        bucket.success_rate != null
          ? `${toneWord(tone)} · ${t('page.console.channelHealth.tip.rate', {
              defaultValue: '可用 {{value}}',
              value: formatSliceRate(bucket.success_rate),
            })}`
          : toneWord(tone),
      note:
        bucket.request_count > 0
          ? probed === 'observing'
            ? t('page.console.modelOps.bars.probesObserving', {
                count: bucket.request_count,
                defaultValue: '探测 {{count}} 次 · 无流量（观察中）',
              })
            : probed === 'idle'
              ? t('page.console.modelOps.bars.probes', {
                  count: bucket.request_count,
                  defaultValue: '探测 {{count}} 次 · 无流量',
                })
              : t('page.console.modelOps.bars.requests', {
                  defaultValue: '{{requests}} 次请求',
                  requests: bucket.request_count.toLocaleString(),
                })
          : undefined,
    };
  });
}
