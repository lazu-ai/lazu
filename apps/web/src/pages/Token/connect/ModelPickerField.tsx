/** The model field in the connect dialog: a searchable list grouped by
 *  vendor, newest first. It replaces a flat select that held the whole
 *  catalog in catalog order, where the model a tool should open on was
 *  wherever the id happened to sort. */

import { Check, ChevronDown, Plus, Search, X } from 'lucide-react';
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import {
  buildPicker,
  findPickerModel,
  groupToRevealFor,
  releaseLabel,
  type ModelTier,
  type PickerModel,
  type PickerResult,
} from './modelPicker';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '../../../components/ui/popover';
import { resolveProviderBrand } from '../../../components/ui/provider-brands';
import { ProviderLogo } from '../../../components/ui/provider-icons';
import { cn } from '../../../lib/cn';
import type { TFunction } from 'i18next';

/** How many models a vendor shows before the rest fold away. */
const VISIBLE_PER_GROUP = 4;

/** Every option needs an id of its own for aria-activedescendant to name it. */
const optionId = (fieldId: string, index: number) =>
  `${fieldId}-option-${index}`;

const tierLabel = (tier: ModelTier, t: TFunction): string => {
  if (tier === 'flagship') {
    return t('page.console.token.connect.tier.flagship', {
      defaultValue: '旗舰',
    });
  }
  if (tier === 'light') {
    return t('page.console.token.connect.tier.light', { defaultValue: '轻量' });
  }
  return t('page.console.token.connect.tier.balanced', {
    defaultValue: '均衡',
  });
};

type Row =
  | { kind: 'expand'; groupKey: string; hidden: number; label: string }
  | { kind: 'model'; model: PickerModel; variant?: boolean }
  | { kind: 'section'; label: string }
  | { kind: 'vendor'; label: string; vendorName: string };

/** The list as it is drawn: headers, the visible models of each group, and
 *  one expander per folded remainder. */
function buildRows(
  result: PickerResult,
  expanded: Set<string>,
  searching: boolean,
  t: TFunction,
): Row[] {
  const rows: Row[] = [];
  result.sections.forEach((section) => {
    rows.push({
      kind: 'section',
      label:
        section.kind === 'recommended'
          ? t('page.console.token.connect.recommended', {
              defaultValue: '推荐 · {{vendor}}',
              vendor: section.label,
            })
          : t('page.console.token.connect.otherVendors', {
              defaultValue: '其他供应商',
            }),
    });
    section.groups.forEach((group) => {
      if (section.kind === 'other' || section.groups.length > 1) {
        rows.push({
          kind: 'vendor',
          label: group.label,
          vendorName: group.vendorName,
        });
      }
      // A search shows every hit; the fold only applies to the resting list.
      const open = searching || expanded.has(group.key);
      const shown = open
        ? group.models
        : group.models.slice(0, VISIBLE_PER_GROUP);
      shown.forEach((model) => {
        rows.push({ kind: 'model', model });
        // A dated or named snapshot sits under the model it varies, and only
        // once the group is open — folded away at rest, never discarded.
        if (!open) return;
        model.variants.forEach((variant) =>
          rows.push({ kind: 'model', model: variant, variant: true }),
        );
      });
      const foldedAway =
        group.models.length -
        shown.length +
        (open
          ? 0
          : shown.reduce((count, model) => count + model.variants.length, 0));
      if (foldedAway > 0) {
        rows.push({
          groupKey: group.key,
          hidden: foldedAway,
          kind: 'expand',
          label: group.label,
        });
      }
    });
  });
  return rows;
}

/** The searchable list itself, behind whichever trigger a field puts in
 *  front of it. The single-model field closes it on a pick; the multi-model
 *  field keeps it open and toggles. */
function ModelPickerPopover({
  current,
  id,
  isOn,
  onOpenChange,
  onPick,
  open,
  priceForModel,
  result,
  t,
  trigger,
}: {
  /** The model the keyboard opens on and the list scrolls to. */
  current: string;
  id: string;
  isOn: (model: string) => boolean;
  onOpenChange: (open: boolean) => void;
  /** Returns true to keep the list open after the pick. */
  onPick: (model: string) => boolean | void;
  open: boolean;
  priceForModel: (model: PickerModel) => string;
  result: PickerResult;
  t: TFunction;
  trigger: ReactElement;
}) {
  const [query, setQuery] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);

  // The search rebuilds the list from the same catalog facts, so a hit in a
  // folded group is reachable without expanding it first.
  const shown = useMemo(
    () => (query.trim() ? buildPicker({ ...result.args, query }) : result),
    [query, result],
  );
  const searching = Boolean(query.trim());
  const rows = useMemo(
    () => buildRows(shown, expanded, searching, t),
    [expanded, searching, shown, t],
  );
  const modelRows = useMemo(
    () => rows.filter((row) => row.kind === 'model'),
    [rows],
  );

  // Opening the field expands whichever group holds the current model, so a
  // choice sitting past the fold is on screen and is what Enter would keep.
  useEffect(() => {
    if (!open) {
      setQuery('');
      return;
    }
    const key = groupToRevealFor(result, current, VISIBLE_PER_GROUP);
    if (key) setExpanded((existing) => new Set(existing).add(key));
    // Typing should narrow the list straight away, the same as a select that
    // opens focused.
    const focus = window.setTimeout(() => searchRef.current?.focus(), 0);
    return () => window.clearTimeout(focus);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // The active option follows the current model once the rows settle.
  useEffect(() => {
    if (!open) return;
    const index = modelRows.findIndex(
      (row) => row.kind === 'model' && row.model.id === current,
    );
    setActive(index < 0 ? 0 : index);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, rows.length]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  useEffect(() => {
    const node = listRef.current?.querySelector<HTMLElement>(
      '[data-active="true"]',
    );
    node?.scrollIntoView({ block: 'nearest' });
  }, [active, rows.length]);

  const commit = (model: string) => {
    if (!onPick(model)) onOpenChange(false);
  };

  // Arrow keys and Enter are bound to the input element itself rather than
  // through React's onKeyDown. The popup stops keydown from propagating out
  // of itself, which is above React's delegated listener at the app root, so
  // a JSX handler here would never run — and the popup's own focus
  // management would move focus off the search field on the first arrow.
  useEffect(() => {
    const input = searchRef.current;
    if (!open || !input) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        event.stopPropagation();
        if (!modelRows.length) return;
        const step = event.key === 'ArrowDown' ? 1 : -1;
        setActive((existing) => {
          const next = existing + step;
          if (next < 0) return modelRows.length - 1;
          if (next >= modelRows.length) return 0;
          return next;
        });
        return;
      }
      if (event.key === 'Enter') {
        event.preventDefault();
        event.stopPropagation();
        const row = modelRows[active];
        if (row?.kind === 'model') commit(row.model.id);
      }
    };
    input.addEventListener('keydown', onKeyDown);
    return () => input.removeEventListener('keydown', onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, modelRows, open]);

  const empty = !modelRows.length;
  const activeId =
    open && modelRows.length && active < modelRows.length
      ? optionId(id, active)
      : undefined;

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger>{trigger}</PopoverTrigger>
      <PopoverContent align='start' className='tok-model__pop'>
        <div className='tok-model__search'>
          <Search aria-hidden='true' size={14} />
          <input
            aria-activedescendant={activeId}
            aria-autocomplete='list'
            aria-controls={`${id}-list`}
            aria-expanded
            aria-label={t('page.console.token.connect.searchModels', {
              defaultValue: '搜模型名，或供应商',
            })}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('page.console.token.connect.searchModels', {
              defaultValue: '搜模型名，或供应商',
            })}
            ref={searchRef}
            role='combobox'
            value={query}
          />
          <kbd>esc</kbd>
        </div>
        <div
          className='tok-model__list'
          id={`${id}-list`}
          ref={listRef}
          role='listbox'
        >
          {empty ? (
            <p className='tok-model__none'>
              {t('page.console.token.connect.noMatch', {
                defaultValue: '没有匹配的模型',
              })}
            </p>
          ) : null}
          {rows.map((row, index) => {
            if (row.kind === 'section') {
              return (
                <div
                  className='tok-model__section'
                  key={`s-${row.label}-${index}`}
                >
                  {row.label}
                </div>
              );
            }
            if (row.kind === 'vendor') {
              const brand = resolveProviderBrand({
                name: row.vendorName || row.label,
              });
              return (
                <div
                  className='tok-model__vendor'
                  key={`v-${row.label}-${index}`}
                >
                  <ProviderLogo brand={brand} decorative size={16} />
                  {row.label}
                </div>
              );
            }
            if (row.kind === 'expand') {
              return (
                <button
                  className='tok-model__more'
                  key={`e-${row.groupKey}`}
                  onClick={() =>
                    setExpanded((existing) =>
                      new Set(existing).add(row.groupKey),
                    )
                  }
                  type='button'
                >
                  {t('page.console.token.connect.showOlder', {
                    defaultValue: '更早的 {{total}} 个 {{vendor}} 模型',
                    total: row.hidden,
                    vendor: row.label,
                  })}
                </button>
              );
            }
            const position = modelRows.indexOf(row);
            const on = isOn(row.model.id);
            return (
              <button
                aria-selected={on}
                className={cn(
                  'tok-model__row',
                  row.variant && 'is-variant',
                  on && 'is-on',
                )}
                data-active={position === active}
                id={optionId(id, position)}
                key={row.model.id}
                onClick={() => commit(row.model.id)}
                onMouseEnter={() => setActive(position)}
                role='option'
                type='button'
              >
                <span className='tok-model__row-id'>
                  <Check
                    aria-hidden='true'
                    className='tok-model__check'
                    size={14}
                  />
                  <span>{row.model.id}</span>
                </span>
                <span className={`tok-model__tier is-${row.model.tier}`}>
                  {tierLabel(row.model.tier, t)}
                </span>
                <span className='tok-model__date'>
                  {releaseLabel(row.model)}
                </span>
                <span className='tok-model__price'>
                  {priceForModel(row.model)}
                </span>
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}

export function ModelPickerField({
  disabled,
  id,
  label,
  lane,
  onChange,
  priceForModel,
  result,
  t,
  value,
}: {
  disabled?: boolean;
  id: string;
  label: string;
  /** This model's own lane control, rendered under the field. Absent for a
   *  field whose model has no lane to choose between. */
  lane?: ReactNode;
  onChange: (model: string) => void;
  priceForModel: (model: PickerModel) => string;
  result: PickerResult;
  t: TFunction;
  value: string;
}) {
  const [open, setOpen] = useState(false);
  const selected = findPickerModel(result, value);

  return (
    <div className='tok-connect__field'>
      <span className='tok-connect__label' id={`${id}-label`}>
        {label}
      </span>
      <ModelPickerPopover
        current={value}
        id={id}
        isOn={(model) => model === value}
        onOpenChange={setOpen}
        onPick={onChange}
        open={open}
        priceForModel={priceForModel}
        result={result}
        t={t}
        trigger={
          <button
            aria-expanded={open}
            aria-haspopup='listbox'
            aria-label={`${label}${value ? `: ${value}` : ''}`}
            className='tok-model__trigger'
            data-owns-chrome='true'
            disabled={disabled}
            type='button'
          >
            <span className='tok-model__id'>
              {value ||
                t('page.console.token.connect.modelEmpty', {
                  defaultValue: '没有可用模型',
                })}
            </span>
            {selected ? (
              <span className={`tok-model__tier is-${selected.tier}`}>
                {tierLabel(selected.tier, t)}
              </span>
            ) : null}
            <ChevronDown aria-hidden='true' size={14} />
          </button>
        }
      />
      {lane}
    </div>
  );
}

/** A list of models beside the default and light ones, for the tools that
 *  keep a model list of their own. One row per model: a tick that puts it in
 *  the tool's list, its own lane control, and what that lane costs. The same
 *  picker adds more, staying open so several can be ticked in one go. */
export function ModelMultiField({
  candidates,
  disabled,
  fixed,
  hint,
  id,
  label,
  laneFor,
  note,
  priceForModel,
  onChange,
  result,
  t,
  values,
}: {
  /** Rows to show besides the chosen ones: the models this tool was seeded
   *  with. Unticking one leaves its row in place rather than making it
   *  vanish, so the list reads the same before and after. */
  candidates?: string[];
  disabled?: boolean;
  /** Models already on the list through their own fields (the default and
   *  light ones): ticked in the picker, not rows, not toggled. */
  fixed: string[];
  hint?: string;
  id: string;
  label: string;
  /** One row's lane control. */
  laneFor?: (model: string) => ReactNode;
  /** The line above the rows, to the right of the label. */
  note?: string;
  onChange: (models: string[]) => void;
  priceForModel: (model: PickerModel) => string;
  result: PickerResult;
  t: TFunction;
  values: string[];
}) {
  const [open, setOpen] = useState(false);
  const chosen = new Set(values);
  const pinned = new Set(fixed.filter(Boolean));
  const toggle = (model: string) => {
    if (pinned.has(model)) return true;
    onChange(
      chosen.has(model)
        ? values.filter((item) => item !== model)
        : [...values, model],
    );
    return true;
  };
  const rows: string[] = [];
  [...values, ...(candidates || [])].forEach((model) => {
    if (model && !pinned.has(model) && !rows.includes(model)) rows.push(model);
  });

  return (
    <div className='tok-connect__field tok-connect__extras'>
      <div className='tok-connect__label-row'>
        <span className='tok-connect__label' id={`${id}-label`}>
          {label}
        </span>
        {note ? <span className='tok-connect__note'>{note}</span> : null}
      </div>
      <div aria-labelledby={`${id}-label`} className='tok-model__rows'>
        {rows.map((model) => {
          const fact = findPickerModel(result, model);
          const on = chosen.has(model);
          return (
            <div className={cn('tok-model__row', !on && 'is-off')} key={model}>
              <input
                aria-label={t('page.console.token.connect.extrasInclude', {
                  defaultValue: '导入 {{model}}',
                  model,
                })}
                checked={on}
                className='tok-model__row-check'
                disabled={disabled}
                onChange={() => toggle(model)}
                type='checkbox'
              />
              <span className='tok-model__row-id'>{model}</span>
              {fact ? (
                <span className={`tok-model__tier is-${fact.tier}`}>
                  {tierLabel(fact.tier, t)}
                </span>
              ) : null}
              <span className='tok-model__row-lane'>{laneFor?.(model)}</span>
              <span className='tok-model__row-price'>
                {fact ? priceForModel(fact) : ''}
              </span>
            </div>
          );
        })}
        <ModelPickerPopover
          current={values[values.length - 1] || ''}
          id={id}
          isOn={(model) => pinned.has(model) || chosen.has(model)}
          onOpenChange={setOpen}
          onPick={toggle}
          open={open}
          priceForModel={priceForModel}
          result={result}
          t={t}
          trigger={
            <button
              aria-expanded={open}
              aria-haspopup='listbox'
              className='tok-model__add'
              data-owns-chrome='true'
              disabled={disabled}
              type='button'
            >
              <Plus aria-hidden='true' size={12} />
              {t('page.console.token.connect.extrasAdd', {
                defaultValue: '添加',
              })}
            </button>
          }
        />
      </div>
      {hint ? <span className='tok-connect__muted'>{hint}</span> : null}
    </div>
  );
}
