import { ChevronDown, X } from 'lucide-react';
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

/** 限制模型: grouped by maker with collapsible headers, selected chips + search
 *  bar, and a 2-column checkbox grid inside the steady 640px Key modal. */
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
  const ticked = new Set(names);
  const vendorSet = new Set(mode === 'only' ? vendors : []);
  const isTicked = (row: (typeof rows)[number]) =>
    ticked.has(row.name) ||
    (row.vendorID !== '' && vendorSet.has(row.vendorID));

  const effectiveTickedNames = useMemo(() => {
    if (vendorSet.size === 0) return names;
    const expanded = new Set(names);
    for (const row of rows) {
      if (row.vendorID && vendorSet.has(row.vendorID)) expanded.add(row.name);
    }
    return [...expanded];
  }, [names, rows, vendorSet]);

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

  const reach =
    mode === 'only'
      ? rows.filter(isTicked).length
      : rows.filter((row) => !ticked.has(row.name)).length;

  const write = (nextNames: string[], nextVendors = vendors) =>
    onChange({
      limits: scopeLimits(mode, [...new Set(nextNames)]),
      mode,
      vendors: mode === 'only' ? nextVendors : [],
    });

  const toggle = (targets: typeof rows, on: boolean) => {
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

  const removeOne = (targetName: string) => {
    const match = rows.find((row) => row.name === targetName);
    if (match) {
      toggle([match], false);
    } else {
      write(
        names.filter((name) => name !== targetName),
        vendors,
      );
    }
  };

  const pickMode = (next: Exclude<ScopeMode, 'all'>) => {
    if (next === mode) return;
    const carried =
      mode === 'only' ? rows.filter(isTicked).map((row) => row.name) : names;
    onChange({ limits: scopeLimits(next, carried), mode: next, vendors: [] });
  };

  const excluding = mode === 'except';

  return (
    <div className='tok-scope2'>
      <div className='tok-scope2__modes'>
        <div
          aria-label={t('page.console.token.scope.modeLabel', {
            defaultValue: '限制方式',
          })}
          className='tokv2-seg'
          role='radiogroup'
        >
          {(
            [
              [
                'only',
                t('page.console.token.scope.modeOnlyShort', {
                  defaultValue: '只允许',
                }),
              ],
              [
                'except',
                t('page.console.token.scope.modeExceptShort', {
                  defaultValue: '排除',
                }),
              ],
            ] as const
          ).map(([key, label]) => (
            <button
              aria-checked={mode === key}
              className={cn('tokv2-seg__btn', mode === key && 'is-on')}
              data-owns-chrome='true'
              key={key}
              role='radio'
              type='button'
              onClick={() => pickMode(key)}
            >
              {label}
            </button>
          ))}
        </div>

        <span className='tokv2-hint tok-scope2__reach'>
          {t('page.console.token.scope.reachable', { defaultValue: '可调' })}{' '}
          <b className='tok-scope2__mono tok-scope2__reach-num'>{reach}</b>
          {` / ${rows.length}`}
          {excluding ? (
            <span className='tok-scope2__future'>
              {` · ${t('page.console.token.scope.futureCallable', {
                defaultValue: '新模型默认可调',
              })}`}
            </span>
          ) : null}
        </span>

        {effectiveTickedNames.length > 0 ? (
          <button
            className='tok-scope2__clear'
            data-owns-chrome='true'
            type='button'
            onClick={() =>
              onChange({ limits: scopeLimits(mode, []), mode, vendors: [] })
            }
          >
            {t('common.clear', { defaultValue: '清空' })}
          </button>
        ) : null}
      </div>

      <div className='tok-scope2__tagbox'>
        {effectiveTickedNames.map((id) => (
          <span
            className={cn('tok-scope2__tag', excluding && 'is-out')}
            key={id}
          >
            <span>{id}</span>
            <button
              aria-label={`${t('common.remove', { defaultValue: '移除' })} ${id}`}
              className='tok-scope2__tag-rm'
              data-owns-chrome='true'
              type='button'
              onClick={() => removeOne(id)}
            >
              <X aria-hidden='true' size={12} />
            </button>
          </span>
        ))}
        <input
          aria-label={t('page.console.token.scope.searchShort', {
            defaultValue: '搜模型',
          })}
          className='tok-scope2__q'
          placeholder={t('page.console.token.scope.searchPlaceholder', {
            defaultValue: '搜模型…',
          })}
          type='text'
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>

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
                    className={excluding ? 'tok-scope2__box--out' : undefined}
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
                          className={cn(
                            'tok-scope2__item',
                            on && (excluding ? 'is-out' : 'is-in'),
                          )}
                          key={row.name}
                          title={tip || undefined}
                        >
                          <Checkbox
                            aria-label={row.name}
                            checked={on}
                            className={
                              excluding ? 'tok-scope2__box--out' : undefined
                            }
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

      {invalid && mode === 'only' && reach === 0 ? (
        <span className='tok-scope2__error' role='alert'>
          {t('page.console.token.scope.emptyOnly', {
            defaultValue: '至少选择一个模型，或关闭「限制模型」',
          })}
        </span>
      ) : null}
    </div>
  );
}
