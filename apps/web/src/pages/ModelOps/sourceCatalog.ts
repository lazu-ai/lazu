import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { formatPerMtok } from './formatters';
import {
  fetchTokenVendorOptions,
  type TokenModelFact,
} from '../../hooks/tokens/useTokensQuery';
import { makerOf } from '../../lib/model-makers';
import { queryKeys } from '../../lib/query-keys';

/** "$15", "$0.3", "$1.25": list prices without the trailing zeros a ledger
 *  column keeps. */
const shortPrice = (value: number | undefined) =>
  `$${formatPerMtok(value).replace(/\.00$/, '')}`;

/** What the catalog knows about the names a source serves: which ones it
 *  has registered, and for those on sale their maker and list price. */
export type SourceCatalog = {
  fact: (name: string) => TokenModelFact | undefined;
  /** The maker key of a name, from the catalog or, failing that, the name. */
  maker: (name: string) => string;
  /** Catalog rank of a maker key, for ordering maker chips. */
  makerRank: (maker: string) => number;
  names: ReadonlySet<string>;
  price: (name: string) => string;
};

export function useSourceCatalog(registered: Array<{ value: string }>) {
  const pricing = useQuery({
    queryKey: queryKeys.console.detail('tokens', 'vendor-options'),
    queryFn: fetchTokenVendorOptions,
    staleTime: 5 * 60 * 1000,
  });
  return useMemo<SourceCatalog>(() => {
    const facts = new Map(
      (pricing.data?.models ?? []).map((fact) => [fact.model, fact]),
    );
    const names = new Set<string>([
      ...registered.map((option) => String(option.value)),
      ...facts.keys(),
    ]);
    const ranks = new Map<string, number>();
    for (const fact of facts.values()) {
      if (
        fact.maker &&
        fact.makerRank !== undefined &&
        !ranks.has(fact.maker)
      ) {
        ranks.set(fact.maker, fact.makerRank);
      }
    }
    const maker = (name: string) =>
      makerOf(facts.get(name)?.maker, { name }) || '';
    return {
      fact: (name) => facts.get(name),
      maker,
      makerRank: (key) => ranks.get(key) ?? 1000,
      names,
      price: (name) => {
        const fact = facts.get(name);
        if (
          fact?.inputPerMtok === undefined &&
          fact?.outputPerMtok === undefined
        ) {
          return '—';
        }
        if (fact.outputPerMtok === undefined || fact.outputPerMtok === 0) {
          return shortPrice(fact.inputPerMtok);
        }
        return `${shortPrice(fact.inputPerMtok)} / ${shortPrice(fact.outputPerMtok)}`;
      },
    };
  }, [pricing.data, registered]);
}

/** Maker chips for a set of names: present makers, in catalog order. */
export const makerChips = (names: string[], catalog: SourceCatalog) => {
  const counts = new Map<string, number>();
  for (const name of names) {
    const key = catalog.maker(name);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts.entries()]
    .sort(
      ([a], [b]) =>
        (a ? 0 : 1) - (b ? 0 : 1) ||
        catalog.makerRank(a) - catalog.makerRank(b) ||
        a.localeCompare(b),
    )
    .map(([key, count]) => ({ count, key }));
};
