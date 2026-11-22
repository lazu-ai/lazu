import { ChevronDown } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Checkbox } from '../../components/ui/checkbox';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { cn } from '../../lib/cn';
import {
  groupByMaker,
  makerBrand,
  makerLabel,
  makerOf,
} from '../../lib/model-makers';
import type { TokenModelFact } from '../../hooks/tokens/useTokensQuery';
import type { TFunction } from 'i18next';

/* A Key's model scope as model_limits stores it:
   - off: every model, including ones added later;
   - "a,b": only these;
   - "*,-a,-b": every model but these, including ones added later. */
export type ScopeMode = 'all' | 'except' | 'only';

const EXCLUDE_ALL = '*';

export const scopeModeOf = (enabled: boolean, limits: string[]): ScopeMode =>
  !enabled ? 'all' : limits.includes(EXCLUDE_ALL) ? 'except' : 'only';

/** The names a scope ticks: the allowed ones, or the excluded ones. */
export const scopeNames = (limits: string[]) =>
  limits.includes(EXCLUDE_ALL)
    ? limits
        .filter((name) => name.startsWith('-'))
        .map((name) => name.slice(1))
        .filter(Boolean)
    : limits.filter((name) => name && name !== EXCLUDE_ALL);

export const scopeLimits = (mode: ScopeMode, names: string[]) =>
  mode === 'except'
    ? [EXCLUDE_ALL, ...names.map((name) => `-${name}`)]
    : mode === 'only'
      ? names
      : [];

/** How many models a scope leaves usable. Exclusions keep every model that is
 *  not named; an allowlist keeps the named ones and whole vendors. */
export function scopeReach(
  mode: Exclude<ScopeMode, 'all'>,
  limits: string[],
  vendors: string[],
  models: Array<{ value: string }>,
  facts: TokenModelFact[],
) {
  const named = new Set(scopeNames(limits));
  if (mode === 'except') {
    return models.filter((option) => !named.has(String(option.value))).length;
  }
  const vendorSet = new Set(vendors);
  const vendorOf = new Map(
    facts.map((fact) => [
      fact.model,
      fact.vendorID ? String(fact.vendorID) : '',
    ]),
  );
  return models.filter((option) => {
    const name = String(option.value);
    const vendor = vendorOf.get(name) ?? '';
    return named.has(name) || (vendor !== '' && vendorSet.has(vendor));
  }).length;
}

const trimPrice = (value: number) => `$${Number(value.toFixed(4)).toString()}`;

const priceOf = (fact: TokenModelFact | undefined, useAgent: boolean) => {
  if (!fact) return '';
  const lane = useAgent
    ? fact.lanes?.find((item) => item.key === 'cheap' && item.available)
    : undefined;
  const inp = lane?.inputPerMtok ?? fact.inputPerMtok;
  const out = lane?.outputPerMtok ?? fact.outputPerMtok;
  if (inp === undefined) return '';
  if (out === undefined || out === 0) return trimPrice(inp);
  return `${trimPrice(inp)}/${trimPrice(out)}`;
};

const contextOf = (fact?: TokenModelFact) => {
  const tokens = fact?.contextLength;
  if (!tokens) return '';
  if (tokens >= 1_000_000) {
    return `${Number((tokens / 1_000_000).toFixed(1))}M`;
  }
  return `${Math.round(tokens / 1000)}K`;
};

type ModelScopeTableProps = {
  agentMakers?: ReadonlySet<string>;
  facts: TokenModelFact[];
  invalid?: boolean;
  limits: string[];
  mode: Exclude<ScopeMode, 'all'>;
  models: Array<{ label: string; value: string }>;
  onChange: (next: {
    limits: string[];
    mode: ScopeMode;
    vendors: string[];
  }) => void;
  t: TFunction;
  /** Vendors taken whole, an older shape of the scope that still works. */
  vendors: string[];
};

/** 可用模型: a search box and the models grouped by maker, each ticked while it
 *  is usable. A new scope starts as "every model but these" with nothing
 *  excluded, so every box starts ticked and unticking takes a model away;
 *  models added later stay usable. An older allowlist scope keeps its mode:
 *  only its models are ticked, and ticking adds to it. */
export default function ModelScopeTable({
  agentMakers,
  facts,
  invalid,
  limits,
  mode,
  models,
  onChange,
  t,
  vendors,
}: ModelScopeTableProps) {
  const [query, setQuery] = useState('');
  const [folded, setFolded] = useState<Record<string, boolean>>({});

  const factOf = useMemo(
    () => new Map(facts.map((fact) => [fact.model, fact])),
    [facts],
  );
  const rows = useMemo(
    () =>
      models.map((option) => {
        const name = String(option.value);
        const fact = factOf.get(name);
        return {
          fact,
          maker: makerOf(fact?.maker, { name }),
          makerRank: fact?.makerRank,
          name,
          vendorID: fact?.vendorID ? String(fact.vendorID) : '',
        };
      }),
    [factOf, models],
  );

  const names = scopeNames(limits);
  const named = new Set(names);
  const excluding = mode === 'except';
  const vendorSet = new Set(excluding ? [] : vendors);
  /** A box is ticked while its model is usable. */
  const isTicked = (row: (typeof rows)[number]) =>
    excluding
      ? !named.has(row.name)
      : named.has(row.name) ||
        (row.vendorID !== '' && vendorSet.has(row.vendorID));

  const needle = query.trim().toLowerCase();
  const shown = rows.filter(
    (row) =>
      !needle ||
      row.name.toLowerCase().includes(needle) ||
      makerLabel(row.maker, t).toLowerCase().includes(needle),
  );

  const allByMaker = useMemo(() => {
    const map = new Map<string, typeof rows>();
    for (const row of rows) {
      const list = map.get(row.maker) ?? [];
      list.push(row);
      map.set(row.maker, list);
    }
    return map;
  }, [rows]);

  const groups = useMemo(
    () =>
      groupByMaker(
        shown,
        (row) => row.maker,
        (row) => row.makerRank,
      ),
    [shown],
  );

  const reach = rows.filter(isTicked).length;

  const write = (nextNames: string[], nextVendors = vendors) =>
    onChange({
      limits: scopeLimits(mode, [...new Set(nextNames)]),
      mode,
      vendors: excluding ? [] : nextVendors,
    });

  const toggle = (targets: typeof rows, on: boolean) => {
    if (excluding) {
      const out = new Set(names);
      for (const row of targets) {
        if (on) out.delete(row.name);
        else out.add(row.name);
      }
      write([...out]);
      return;
    }
    let nextNames = [...names];
    let nextVendors = [...vendors];
    for (const row of targets) {
      if (on) {
        if (!isTicked(row)) nextNames.push(row.name);
        continue;
      }
      if (row.vendorID && vendorSet.has(row.vendorID)) {
        nextVendors = nextVendors.filter((id) => id !== row.vendorID);
        nextNames.push(
          ...rows
            .filter((other) => other.vendorID === row.vendorID)
            .map((other) => other.name),
        );
      }
      nextNames = nextNames.filter((name) => name !== row.name);
    }
    write(nextNames, nextVendors);
  };

  return (
    <div className='tok-scope2'>
      <input
        aria-label={t('page.console.token.scope.searchShort', {
          defaultValue: '搜索模型',
        })}
        className='tok-scope2__search'
        placeholder={t('page.console.token.scope.searchPlaceholder', {
          defaultValue: '搜索模型',
        })}
        type='search'
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />

      <div className='tok-scope2__groups'>
        {groups.length === 0 ? (
          <div className='tok-scope2__empty'>
            {t('page.console.token.scope.noMatch', {
              defaultValue: '无匹配模型',
            })}
          </div>
        ) : (
          groups.map((group) => {
            const key = group.maker || '__other__';
            const label = makerLabel(group.maker, t);
            const allInMaker = allByMaker.get(group.maker) ?? group.rows;
            const pickedAll = allInMaker.filter(isTicked).length;
            const pickedShown = group.rows.filter(isTicked).length;
            const allOn =
              group.rows.length > 0 && pickedShown === group.rows.length;
            const someOn = pickedShown > 0 && !allOn;
            const isOpen = Boolean(needle) || !folded[key];
            const isAgent = Boolean(agentMakers?.has(group.maker));

            return (
              <div className='tok-scope2__group' key={key}>
                <div className='tok-scope2__ghead'>
                  <Checkbox
                    aria-label={label}
                    checked={allOn}
                    indeterminate={someOn}
                    onCheckedChange={(checked: boolean) =>
                      toggle(group.rows, checked)
                    }
                  />
                  <button
                    aria-expanded={isOpen}
                    className='tok-scope2__gtoggle'
                    data-owns-chrome='true'
                    type='button'
                    onClick={() =>
                      setFolded((prev) => ({ ...prev, [key]: !prev[key] }))
                    }
                  >
                    <ProviderLogo
                      brand={makerBrand(
                        group.maker,
                        group.rows[0] ? { name: group.rows[0].name } : {},
                      )}
                      decorative
                      size={18}
                    />
                    <span className='tok-scope2__gname'>{label}</span>
                    <span className='tok-scope2__mono tok-scope2__gcount'>
                      {`${pickedAll}/${allInMaker.length}`}
                    </span>
                    {isAgent ? (
                      <span className='tok-scope2__agent'>Agent</span>
                    ) : null}
                    <ChevronDown
                      aria-hidden='true'
                      className={cn('tok-scope2__gchev', isOpen && 'is-open')}
                      size={14}
                    />
                  </button>
                </div>

                {isOpen ? (
                  <div className='tok-scope2__grid'>
                    {group.rows.map((row) => {
                      const on = isTicked(row);
                      const price = priceOf(row.fact, isAgent);
                      const ctx = contextOf(row.fact);
                      const tip = [ctx, price].filter(Boolean).join(' · ');

                      return (
                        <label
                          className={cn('tok-scope2__item', on && 'is-in')}
                          key={row.name}
                          title={tip || undefined}
                        >
                          <Checkbox
                            aria-label={row.name}
                            checked={on}
                            onCheckedChange={(checked: boolean) =>
                              toggle([row], checked)
                            }
                          />
                          <span className='tok-scope2__item-id'>
                            {row.name}
                          </span>
                          {price ? (
                            <span className='tok-scope2__mono tok-scope2__item-price'>
                              {price}
                            </span>
                          ) : null}
                        </label>
                      );
                    })}
                  </div>
                ) : null}
              </div>
            );
          })
        )}
      </div>

      {invalid && reach === 0 ? (
        <span className='tok-scope2__error' role='alert'>
          {t('page.console.token.scope.emptyOnly', {
            defaultValue: '至少保留一个模型',
          })}
        </span>
      ) : null}
    </div>
  );
}
