import { Search } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Checkbox } from '../../components/ui/checkbox';
import { Input } from '../../components/ui/input';
import { cn } from '../../lib/cn';
import { makerLabel, makerOf } from '../../lib/model-makers';
import type { TokenModelFact } from '../../hooks/tokens/useTokensQuery';
import type { TFunction } from 'i18next';

/* A Key's model scope as model_limits stores it:
   - off: every model, including ones added later;
   - "a,b": only these;
   - "*,-a,-b": every model but these, including ones added later. */
type ScopeMode = 'all' | 'except' | 'only';

const EXCLUDE_ALL = '*';

export const scopeModeOf = (enabled: boolean, limits: string[]): ScopeMode =>
  !enabled ? 'all' : limits.includes(EXCLUDE_ALL) ? 'except' : 'only';

/** The names a scope ticks: the allowed ones, or the excluded ones. */
const scopeNames = (limits: string[]) =>
  limits.includes(EXCLUDE_ALL)
    ? limits
        .filter((name) => name.startsWith('-'))
        .map((name) => name.slice(1))
        .filter(Boolean)
    : limits.filter((name) => name && name !== EXCLUDE_ALL);

const scopeLimits = (mode: ScopeMode, names: string[]) =>
  mode === 'except'
    ? [EXCLUDE_ALL, ...names.map((name) => `-${name}`)]
    : mode === 'only'
      ? names
      : [];

type Capability = 'cheap' | 'code' | 'embed' | 'vision';

const CAPABILITIES: Array<[Capability, string]> = [
  ['code', '编程'],
  ['cheap', '便宜'],
  ['vision', '视觉'],
  ['embed', '向量'],
];

/* 便宜 is a list price at or under a dollar per million input tokens. */
const CHEAP_INPUT_PER_MTOK = 1;

const capabilitiesOf = (name: string, fact?: TokenModelFact) => {
  const caps = new Set<Capability>();
  const embed =
    Boolean(fact?.endpointTypes.includes('embeddings')) ||
    /embed|bge-|rerank/i.test(name);
  if (embed) caps.add('embed');
  if (!embed && fact?.tools) caps.add('code');
  if (fact?.vision) caps.add('vision');
  if (
    fact?.inputPerMtok !== undefined &&
    fact.inputPerMtok > 0 &&
    fact.inputPerMtok <= CHEAP_INPUT_PER_MTOK
  ) {
    caps.add('cheap');
  }
  return caps;
};

const trimPrice = (value: number) => `$${Number(value.toFixed(4)).toString()}`;

const priceOf = (fact?: TokenModelFact) => {
  if (!fact || fact.inputPerMtok === undefined) return '—';
  if (fact.outputPerMtok === undefined || fact.outputPerMtok === 0) {
    return trimPrice(fact.inputPerMtok);
  }
  return `${trimPrice(fact.inputPerMtok)} / ${trimPrice(fact.outputPerMtok)}`;
};

const contextOf = (fact?: TokenModelFact) => {
  const tokens = fact?.contextLength;
  if (!tokens) return '—';
  if (tokens >= 1_000_000) {
    return `${Number((tokens / 1_000_000).toFixed(1))}M`;
  }
  return `${Math.round(tokens / 1000)}K`;
};

type ModelScopeTableProps = {
  facts: TokenModelFact[];
  invalid?: boolean;
  limits: string[];
  mode: ScopeMode;
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

/** 能调哪些模型: three ways to scope a Key over one table of the models it
 *  could call, with the price, context and what each is good for beside the
 *  box. */
export default function ModelScopeTable({
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
  const [capability, setCapability] = useState<Capability | null>(null);
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
          caps: capabilitiesOf(name, fact),
          fact,
          maker: makerOf(fact?.maker, { name }),
          name,
          vendorID: fact?.vendorID ? String(fact.vendorID) : '',
        };
      }),
    [factOf, models],
  );

  const names = scopeNames(limits);
  const ticked = new Set(names);
  /* A vendor taken whole ticks every model it ships. */
  const vendorSet = new Set(mode === 'only' ? vendors : []);
  const isTicked = (row: (typeof rows)[number]) =>
    ticked.has(row.name) ||
    (row.vendorID !== '' && vendorSet.has(row.vendorID));

  const needle = query.trim().toLowerCase();
  const shown = rows.filter(
    (row) =>
      (!capability || row.caps.has(capability)) &&
      (!needle ||
        row.name.toLowerCase().includes(needle) ||
        makerLabel(row.maker, t).toLowerCase().includes(needle)),
  );

  const reach =
    mode === 'all'
      ? rows.length
      : mode === 'only'
        ? rows.filter(isTicked).length
        : rows.filter((row) => !ticked.has(row.name)).length;

  /* Unticking one model of a vendor taken whole names the rest one by one:
     the vendor no longer means all of it. */
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

  const pickMode = (next: ScopeMode) => {
    if (next === mode) return;
    /* The ticked set carries over: what was allowed becomes what is left
       out, and the other way round. */
    const carried =
      mode === 'only' ? rows.filter(isTicked).map((row) => row.name) : names;
    onChange({ limits: scopeLimits(next, carried), mode: next, vendors: [] });
  };

  const locked = mode === 'all';
  const allShownOn = shown.length > 0 && shown.every(isTicked);
  const someShownOn = shown.some(isTicked);
  const excluding = mode === 'except';

  const summary =
    mode === 'all'
      ? t('page.console.token.scope.reachAll', {
          count: reach,
          defaultValue: '能调 {{count}} 个，含之后上新',
        })
      : mode === 'except'
        ? t('page.console.token.scope.reachExcept', {
            count: reach,
            defaultValue: '能调 {{count}} 个，之后上新的也能调',
          })
        : t('page.console.token.scope.reachOnly', {
            count: reach,
            defaultValue: '能调 {{count}} 个',
          });

  return (
    <div className='tok-scope2'>
      <div className='tok-scope2__modes'>
        <span className='tok-create__label' id='token-scope-label'>
          {t('page.console.token.scope.title', {
            defaultValue: '能调哪些模型',
          })}
        </span>
        <div
          aria-labelledby='token-scope-label'
          className='tok-scope2__seg'
          role='radiogroup'
        >
          {(
            [
              [
                'all',
                t('page.console.token.scope.modeAll', { defaultValue: '全部' }),
              ],
              [
                'only',
                t('page.console.token.scope.modeOnly', {
                  defaultValue: '只允许勾选的',
                }),
              ],
              [
                'except',
                t('page.console.token.scope.modeExcept', {
                  defaultValue: '全部，除了勾选的',
                }),
              ],
            ] as Array<[ScopeMode, string]>
          ).map(([key, label]) => (
            <button
              aria-checked={mode === key}
              className={cn('tok-scope2__mode', mode === key && 'is-on')}
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
        <span className='tokv2-hint tok-scope2__summary'>{summary}</span>
      </div>

      <div className='tok-scope2__table'>
        <div className='tok-scope2__tools'>
          <span className='tok-scope2__search'>
            <Search aria-hidden='true' size={14} />
            <Input
              aria-label={t('page.console.token.scope.searchShort', {
                defaultValue: '搜模型',
              })}
              placeholder={t('page.console.token.scope.searchShort', {
                defaultValue: '搜模型',
              })}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </span>
          {(
            [[null, '全部能力'], ...CAPABILITIES] as Array<
              [Capability | null, string]
            >
          ).map(([key, label]) => (
            <button
              aria-pressed={capability === key}
              className={cn('tok-scope2__chip', capability === key && 'is-on')}
              data-owns-chrome='true'
              key={key || 'all'}
              type='button'
              onClick={() => setCapability(key)}
            >
              {t(`page.console.token.scope.cap.${key || 'all'}`, {
                defaultValue: label,
              })}
            </button>
          ))}
        </div>
        <div className='tok-scope2__row tok-scope2__row--head'>
          <Checkbox
            aria-label={t('page.console.token.scope.tickShown', {
              defaultValue: '勾选筛出的全部',
            })}
            checked={locked || allShownOn}
            disabled={locked || shown.length === 0}
            indeterminate={!locked && !allShownOn && someShownOn}
            onCheckedChange={() => toggle(shown, !allShownOn)}
          />
          <span className='tok-scope2__th'>
            {t('page.console.token.scope.colModel', { defaultValue: '模型' })}
          </span>
          <span className='tok-scope2__th'>
            {t('page.console.token.scope.colMaker', { defaultValue: '厂商' })}
          </span>
          <span className='tok-scope2__th is-num'>
            {t('page.console.token.scope.colPrice', {
              defaultValue: '入/出 每百万',
            })}
          </span>
          <span className='tok-scope2__th is-num'>
            {t('page.console.token.scope.colContext', {
              defaultValue: '上下文',
            })}
          </span>
          <span className='tok-scope2__th is-num'>
            {t('page.console.token.scope.colCaps', { defaultValue: '能力' })}
          </span>
        </div>
        <div className='tok-scope2__rows'>
          {shown.length === 0 ? (
            <div className='tok-scope2__empty'>
              {t('page.console.token.scope.noMatch', {
                defaultValue: '没有符合筛选的模型',
              })}
            </div>
          ) : (
            shown.map((row) => {
              const on = isTicked(row);
              return (
                <label
                  className={cn(
                    'tok-scope2__row',
                    !locked && on && (excluding ? 'is-out' : 'is-in'),
                  )}
                  key={row.name}
                >
                  <Checkbox
                    aria-label={row.name}
                    checked={locked || on}
                    className={excluding ? 'tok-scope2__box--out' : undefined}
                    disabled={locked}
                    onCheckedChange={(checked: boolean) =>
                      toggle([row], checked)
                    }
                  />
                  <span className='tok-scope2__name'>{row.name}</span>
                  <span className='tokv2-hint'>
                    {row.maker ? makerLabel(row.maker, t) : '—'}
                  </span>
                  <span className='tok-scope2__mono is-num'>
                    {priceOf(row.fact)}
                  </span>
                  <span className='tok-scope2__mono is-num'>
                    {contextOf(row.fact)}
                  </span>
                  <span className='tokv2-hint is-num'>
                    {CAPABILITIES.filter(([key]) => row.caps.has(key))
                      .slice(0, 2)
                      .map(([key, label]) =>
                        t(`page.console.token.scope.cap.${key}`, {
                          defaultValue: label,
                        }),
                      )
                      .join(' · ') || '—'}
                  </span>
                </label>
              );
            })
          )}
        </div>
      </div>
      {invalid && mode === 'only' && reach === 0 ? (
        <span className='tok-scope2__error' role='alert'>
          {t('page.console.token.scope.emptyOnly', {
            defaultValue: '至少勾一个模型；不想限制就选「全部」。',
          })}
        </span>
      ) : null}
      <span className='tokv2-hint'>
        {t('page.console.token.scope.tableHint', {
          defaultValue:
            '价格、上下文来自模型页；Key 页面上就能比，不用来回切。',
        })}
      </span>
    </div>
  );
}
