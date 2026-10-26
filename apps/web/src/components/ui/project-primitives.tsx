/* Shared Console layout and confirmation primitives, backed by Lazu UI. */
import { ChevronDown, Check } from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { Checkbox } from './checkbox';
import { Input } from './input';
import { Modal } from './modal';
import { Popover, PopoverContent, PopoverTrigger } from './popover';
import { cn } from '../../lib/cn';
import './project-primitives.css';
/* Three, and only three: a plain label, something the reader should act on,
   and something that has stopped working. A fourth tone always turns out to
   be decoration, which is how one page ended up with five chip looks. */
export type Tone = 'neutral' | 'warn' | 'bad' | 'evi';

/* ---------------------------------------------------------------- layout */

export function Card({
  children,
  className,
  pad = true,
  dashed = false,
  ...props
}: React.HTMLAttributes<HTMLElement> & { pad?: boolean; dashed?: boolean }) {
  return (
    <section
      className={cn(
        'pj-card',
        pad && 'pj-card--pad',
        dashed && 'pj-card--dashed',
        className,
      )}
      {...props}
    >
      {children}
    </section>
  );
}

export function SectionHead({
  title,
  sub,
  right,
  className,
}: {
  title: React.ReactNode;
  sub?: React.ReactNode;
  right?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('pj-sec', className)}>
      <div>
        <h2>{title}</h2>
        {sub ? <p>{sub}</p> : null}
      </div>
      {right ? <div className='pj-sec__right'>{right}</div> : null}
    </div>
  );
}

export type LedgerItem = {
  key: string;
  label: React.ReactNode;
  value: React.ReactNode;
  hint?: React.ReactNode;
  tone?: 'warn' | 'bad';
};

/** Hairline metric strip — the R-board `.metrics` / `.ledger` band. */
export function Ledger({
  items,
  size = 'metric',
}: {
  items: LedgerItem[];
  size?: 'metric' | 'ledger';
}) {
  return (
    <div
      className={cn('pj-ledger', size === 'ledger' && 'pj-ledger--compact')}
      style={{ ['--pj-cols' as string]: items.length }}
    >
      {items.map((item) => (
        <div className='pj-ledger__cell' key={item.key}>
          <span className='pj-ledger__lb'>{item.label}</span>
          <strong className='pj-ledger__v' data-tone={item.tone}>
            {item.value}
          </strong>
          {item.hint ? (
            <small className='pj-ledger__h'>{item.hint}</small>
          ) : null}
        </div>
      ))}
    </div>
  );
}

export function Kvs({
  rows,
  labelWidth,
}: {
  rows: Array<[React.ReactNode, React.ReactNode]>;
  labelWidth?: number;
}) {
  return (
    <div
      className='pj-kvs'
      style={
        labelWidth ? { gridTemplateColumns: `${labelWidth}px 1fr` } : undefined
      }
    >
      {rows.map(([label, value], index) => (
        <React.Fragment key={index}>
          <span>{label}</span>
          <b>{value}</b>
        </React.Fragment>
      ))}
    </div>
  );
}

/** Form rows: label column + value column with an optional hint. */
export function FormRows({
  children,
  labelWidth = 150,
  layout,
}: {
  children: React.ReactNode;
  labelWidth?: number;
  layout?: 'settings';
}) {
  return (
    <div
      className={layout === 'settings' ? 'pj-setting-rows' : 'pj-fs'}
      style={
        layout === 'settings'
          ? undefined
          : { gridTemplateColumns: `${labelWidth}px 1fr` }
      }
    >
      {children}
    </div>
  );
}

export function FormRow({
  label,
  children,
  hint,
  htmlFor,
  layout,
}: {
  label: React.ReactNode;
  children: React.ReactNode;
  hint?: React.ReactNode;
  htmlFor?: string;
  layout?: 'settings';
}) {
  if (layout === 'settings')
    return (
      <div className='pj-setting-row'>
        <label htmlFor={htmlFor}>{label}</label>
        <div className='pj-setting-row__value'>{children}</div>
        {hint ? <div className='pj-setting-row__hint'>{hint}</div> : null}
      </div>
    );
  return (
    <>
      <label className='pj-fs__l' htmlFor={htmlFor}>
        {label}
      </label>
      <div className='pj-fs__v'>
        {children}
        {hint ? <span className='pj-fs__h'>{hint}</span> : null}
      </div>
    </>
  );
}

/* ---------------------------------------------------------------- people */

export function Avatar({
  name,
  square = false,
  muted = false,
}: {
  name: string;
  square?: boolean;
  muted?: boolean;
}) {
  const letter = square ? '‹›' : (name.trim()[0] || '?').toUpperCase();
  return (
    <span
      className='pj-av'
      data-square={square || undefined}
      data-muted={muted || undefined}
      aria-hidden='true'
    >
      {letter}
    </span>
  );
}

export function Who({
  name,
  sub,
  square = false,
  muted = false,
}: {
  name: React.ReactNode;
  sub?: React.ReactNode;
  square?: boolean;
  muted?: boolean;
}) {
  return (
    <span className='pj-who' data-muted={muted || undefined}>
      <Avatar
        name={typeof name === 'string' ? name : '?'}
        square={square}
        muted={muted}
      />
      <span className='pj-who__t'>
        <strong>{name}</strong>
        {sub ? <span className='pj-sub'>{sub}</span> : null}
      </span>
    </span>
  );
}

export function PjBadge({
  tone = 'neutral',
  children,
  className,
}: {
  tone?: Tone;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn('pj-badge', className)}
      data-tone={tone === 'neutral' ? undefined : tone}
    >
      {children}
    </span>
  );
}

export function Meter({
  pct,
  tone = 'ok',
  height = 5,
  className,
}: {
  pct: number | null;
  tone?: 'ok' | 'warn' | 'bad';
  height?: number;
  className?: string;
}) {
  const width = pct === null ? 0 : Math.min(100, Math.max(0, pct));
  return (
    <div
      className={cn('pj-meter', className)}
      data-tone={tone}
      style={{ height }}
      aria-hidden='true'
    >
      <i style={{ width: `${width}%` }} />
    </div>
  );
}

export function Bars({
  values,
  height = 56,
}: {
  values: number[];
  height?: number;
}) {
  const max = Math.max(1, ...values);
  const peak = values.indexOf(max);
  return (
    <div className='pj-bars' style={{ height }} aria-hidden='true'>
      {values.map((value, index) => (
        <i
          key={index}
          data-peak={index === peak || undefined}
          style={{ height: `${Math.max(4, (value / max) * 100)}%` }}
        />
      ))}
    </div>
  );
}

/* ---------------------------------------------------------------- controls */

export function SegControl<T extends string>({
  value,
  options,
  onChange,
  disabled,
  ariaLabel,
}: {
  value: T;
  options: Array<{ value: T; label: React.ReactNode; disabled?: boolean }>;
  onChange: (value: T) => void;
  disabled?: boolean;
  ariaLabel?: string;
}) {
  return (
    <span className='pj-seg' role='group' aria-label={ariaLabel}>
      {options.map((option) => (
        <button
          aria-pressed={option.value === value}
          className='pj-seg__btn'
          data-on={option.value === value || undefined}
          data-owns-chrome=''
          disabled={disabled || option.disabled}
          key={option.value}
          onClick={() => onChange(option.value)}
          type='button'
        >
          {option.label}
        </button>
      ))}
    </span>
  );
}

/** Quick chips + "custom" + "unlimited" in one row (S2, R2, R9, S6). */

export function OkLine({ children }: { children: React.ReactNode }) {
  return (
    <span className='pj-okt' role='status'>
      <Check aria-hidden='true' size={12} /> {children}
    </span>
  );
}

export function ErrLine({
  children,
  tone = 'bad',
}: {
  children: React.ReactNode;
  tone?: 'bad' | 'warn';
}) {
  return (
    <span
      className='pj-errt'
      data-tone={tone}
      role={tone === 'bad' ? 'alert' : 'status'}
    >
      {children}
    </span>
  );
}

/** Adjacent confirmation for reversible actions (S3, S5, S10). */
export function ConfirmPop({
  trigger,
  text,
  confirmLabel,
  cancelLabel,
  danger = false,
  busy = false,
  onConfirm,
  open,
  onOpenChange,
  align = 'end',
}: {
  trigger: React.ReactElement;
  text: React.ReactNode;
  confirmLabel: React.ReactNode;
  cancelLabel?: React.ReactNode;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void | Promise<void>;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  align?: 'start' | 'center' | 'end';
}) {
  const { t } = useTranslation();
  const [internal, setInternal] = useState(false);
  const isOpen = open ?? internal;
  const setOpen = (next: boolean) => {
    if (!next && busy) return;
    onOpenChange ? onOpenChange(next) : setInternal(next);
  };
  return (
    <Popover open={isOpen} onOpenChange={setOpen}>
      <PopoverTrigger>{trigger}</PopoverTrigger>
      <PopoverContent align={align} className='pj-pop' side='bottom'>
        <p>{text}</p>
        <div className='pj-pop__acts'>
          <Button
            disabled={busy}
            onClick={() => setOpen(false)}
            size='sm'
            variant='secondary'
          >
            {cancelLabel ?? t('common.cancel', { defaultValue: 'Cancel' })}
          </Button>
          <Button
            loading={busy}
            onClick={() => void onConfirm()}
            size='sm'
            variant={danger ? 'danger' : 'primary'}
          >
            {confirmLabel}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

/** Centered modal with a checkbox or typed-name gate (S5, S10, S11). Escape
 *  and outside click cancel; while busy the dialog cannot close. */
export function GateModal({
  open,
  onOpenChange,
  title,
  description,
  bullets,
  gate,
  confirmLabel,
  busyLabel,
  danger = true,
  busy = false,
  disabled = false,
  onConfirm,
  extraFooter,
  body,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  bullets?: React.ReactNode[];
  gate?:
    | { type: 'checkbox'; label: React.ReactNode }
    | {
        type: 'typed';
        expected: string;
        label: React.ReactNode;
        placeholder?: string;
      };
  confirmLabel: React.ReactNode;
  busyLabel?: React.ReactNode;
  danger?: boolean;
  busy?: boolean;
  disabled?: boolean;
  onConfirm: () => void | Promise<void>;
  extraFooter?: React.ReactNode;
  body?: React.ReactNode;
}) {
  const { t } = useTranslation();
  const [checked, setChecked] = useState(false);
  const [typed, setTyped] = useState('');
  useEffect(() => {
    if (!open) {
      setChecked(false);
      setTyped('');
    }
  }, [open]);
  const satisfied = !gate
    ? true
    : gate.type === 'checkbox'
      ? checked
      : typed.trim().toLowerCase() === gate.expected.trim().toLowerCase();
  return (
    <Modal
      busy={busy}
      className='pj-modal'
      closeLabel={t('common.close', { defaultValue: '关闭' })}
      description={description}
      onOpenChange={onOpenChange}
      open={open}
      title={title}
      body={
        <div className='pj-modal__body'>
          {bullets?.length ? (
            <ul className='pj-modal__list'>
              {bullets.map((item, index) => (
                <li key={index}>{item}</li>
              ))}
            </ul>
          ) : null}
          {body}
          {gate?.type === 'checkbox' ? (
            <Checkbox
              checked={checked}
              disabled={busy}
              onCheckedChange={(next) => setChecked(Boolean(next))}
            >
              {gate.label}
            </Checkbox>
          ) : null}
          {gate?.type === 'typed' ? (
            <FormRows labelWidth={110}>
              <FormRow label={gate.label}>
                <Input
                  autoComplete='off'
                  disabled={busy}
                  onChange={(event) => setTyped(event.target.value)}
                  placeholder={gate.placeholder ?? gate.expected}
                  style={{ width: 220 }}
                  value={typed}
                />
              </FormRow>
            </FormRows>
          ) : null}
        </div>
      }
      footer={
        <div className='pj-modal__foot'>
          <span className='pj-modal__esc'>
            {t('common.escapeHint', { defaultValue: 'Esc to cancel' })}
          </span>
          {extraFooter}
          <Button
            disabled={busy}
            onClick={() => onOpenChange(false)}
            variant='secondary'
          >
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={disabled || !satisfied}
            loading={busy}
            loadingLabel={busyLabel}
            onClick={() => void onConfirm()}
            variant={danger ? 'danger' : 'primary'}
          >
            {confirmLabel}
          </Button>
        </div>
      }
    />
  );
}

/** Plain confirmation modal without a gate (S4 role changes, S6, S8). */
export function ActionModal({
  open,
  onOpenChange,
  title,
  description,
  children,
  confirmLabel,
  busyLabel,
  danger = false,
  busy = false,
  disabled = false,
  onConfirm,
  extraFooter,
  wide = false,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  children?: React.ReactNode;
  confirmLabel: React.ReactNode;
  busyLabel?: React.ReactNode;
  danger?: boolean;
  busy?: boolean;
  disabled?: boolean;
  onConfirm: () => void | Promise<void>;
  extraFooter?: React.ReactNode;
  wide?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Modal
      busy={busy}
      className={cn('pj-modal', wide && 'pj-modal--wide')}
      closeLabel={t('common.close', { defaultValue: '关闭' })}
      description={description}
      onOpenChange={onOpenChange}
      open={open}
      title={title}
      body={<div className='pj-modal__body'>{children}</div>}
      footer={
        <div className='pj-modal__foot'>
          <span className='pj-modal__esc'>
            {t('common.escapeHint', { defaultValue: 'Esc to cancel' })}
          </span>
          {extraFooter}
          <Button
            disabled={busy}
            onClick={() => onOpenChange(false)}
            variant='secondary'
          >
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={disabled}
            loading={busy}
            loadingLabel={busyLabel}
            onClick={() => void onConfirm()}
            variant={danger ? 'danger' : 'primary'}
          >
            {confirmLabel}
          </Button>
        </div>
      }
    />
  );
}

export function SaveBar({
  text,
  dirtyCount,
  busy,
  onDiscard,
  onSave,
  saveLabel,
  sticky = false,
}: {
  text?: React.ReactNode;
  dirtyCount?: number;
  busy?: boolean;
  onDiscard: () => void;
  onSave: () => void;
  saveLabel?: React.ReactNode;
  sticky?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className='pj-savebar' data-sticky={sticky || undefined} role='status'>
      <span>
        {text ?? t('common.unsavedCount', { count: dirtyCount ?? 0 })}
      </span>
      <span className='pj-savebar__acts'>
        <Button
          disabled={busy}
          onClick={onDiscard}
          size='sm'
          variant='secondary'
        >
          {t('common.discard', { defaultValue: 'Discard' })}
        </Button>
        <Button loading={busy} onClick={onSave} size='sm' variant='primary'>
          {saveLabel ?? t('common.save', { defaultValue: '保存' })}
        </Button>
      </span>
    </div>
  );
}

export function Notice({
  tone,
  title,
  body,
  action,
}: {
  tone?: 'warn' | 'bad' | 'evi';
  title: React.ReactNode;
  body?: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div
      className='pj-notice'
      data-tone={tone}
      role={tone === 'bad' ? 'alert' : 'status'}
    >
      <div className='pj-notice__copy'>
        <strong>{title}</strong>
        {body ? <p>{body}</p> : null}
      </div>
      {action ? <div className='pj-notice__act'>{action}</div> : null}
    </div>
  );
}

export function TableFoot({
  left,
  right,
}: {
  left?: React.ReactNode;
  right?: React.ReactNode;
}) {
  return (
    <div className='pj-tfoot'>
      <span>{left}</span>
      <span>{right}</span>
    </div>
  );
}

export const SelectLike = React.forwardRef<
  HTMLButtonElement,
  {
    children: React.ReactNode;
    onClick?: () => void;
    width?: number;
    disabled?: boolean;
  } & Omit<
    React.ButtonHTMLAttributes<HTMLButtonElement>,
    'children' | 'onClick'
  >
>(({ children, onClick, width, disabled, style, ...rest }, ref) => (
  <button
    className='pj-fld'
    data-owns-chrome=''
    disabled={disabled}
    onClick={onClick}
    ref={ref}
    style={{ width, ...style }}
    type='button'
    {...rest}
  >
    <span>{children}</span>
    <ChevronDown aria-hidden='true' size={14} />
  </button>
));

SelectLike.displayName = 'SelectLike';

/** Visual toggle used inside a trigger button (a real Switch would nest a
 *  button in a button). */
export function ToggleGlyph({ on }: { on: boolean }) {
  return (
    <span aria-hidden='true' className='pj-tg' data-off={!on || undefined} />
  );
}

export function MonoText({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return <span className={cn('pj-mono', className)}>{children}</span>;
}
