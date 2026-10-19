// Core Console-level Lazu UI primitives used by older settings, dashboard, and
// pricing screens while those screens move to direct primitive imports.
// Keep this file free of vendor names and style it with Lazu UI classes only.
import i18next from 'i18next';
import {
  AlertTriangle,
  Check,
  ChevronDown,
  Circle,
  Copy,
  Info,
  LoaderCircle,
  X,
  XCircle,
} from 'lucide-react';
import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { DatePicker as UiDatePicker, DateRangePicker } from './date-picker';
import { EmptyState } from './empty-state';
import { InlineNotice, type InlineNoticeTone } from './inline-notice';
import { notify as uiToast } from './toast';
import { Tooltip as UiTooltip } from './tooltip';
import { toastConstants } from '../../constants/toast.constants';
import { cn } from '../../lib/cn';
import type { ExternalToast } from 'sonner';

type CompatibilityHandler = {
  bivarianceHack(...args: unknown[]): unknown;
}['bivarianceHack'];

type SelectOptionData = {
  disabled?: boolean;
  label?: React.ReactNode;
  value: string | number;
};

export type FormRule = {
  message?: string;
  required?: boolean;
  validator?: (rule: FormRule, value: unknown) => boolean | Promise<boolean>;
};

export type FormValues = Record<string, unknown>;

export type FormApi = {
  getValue: (field: string) => unknown;
  getValues: () => FormValues;
  setValue: (field: string, value: unknown) => void;
  setValues: (values: object) => void;
  validate: () => Promise<FormValues>;
  register: (field: string, rules?: FormRule[]) => void;
  unregister: (field: string) => void;
};

export type FormContextValue = FormApi & { values: FormValues };

type ButtonProps = Omit<
  React.ButtonHTMLAttributes<HTMLButtonElement>,
  'size' | 'type'
> & {
  block?: boolean;
  htmlType?: React.ButtonHTMLAttributes<HTMLButtonElement>['type'];
  icon?: React.ReactNode;
  loading?: boolean;
  nativeType?: React.ButtonHTMLAttributes<HTMLButtonElement>['type'];
  size?: string | number;
  theme?: string;
  type?: string;
};

export type FormProps = Omit<
  React.FormHTMLAttributes<HTMLFormElement>,
  'onSubmit'
> & {
  getFormApi?: (api: FormApi) => void;
  initValues?: object;
  layout?: string;
  onSubmit?: CompatibilityHandler;
  values?: object;
};

type TableColumn<T extends object> = {
  dataIndex?: string;
  fixed?: string;
  header?: React.ReactNode;
  key?: React.Key;
  render?: (value: never, record: T, index: number) => React.ReactNode;
  title?: React.ReactNode;
  width?: React.CSSProperties['width'];
};

type TableKey = string | number;

type TableSelectionChange<T extends object> = {
  bivarianceHack(keys: TableKey[], rows: T[]): void;
}['bivarianceHack'];

type TableRowSelection<T extends object> = {
  getCheckboxProps?: (record: T) => { disabled?: boolean };
  onChange?: TableSelectionChange<T>;
  onSelect?: CompatibilityHandler;
  onSelectAll?: CompatibilityHandler;
  selectedRowKeys?: TableKey[];
};

export type PaginationProps = {
  currentPage?: number;
  onChange?: (page: number, pageSize: number) => unknown;
  onPageChange?: (page: number, pageSize: number) => unknown;
  onPageSizeChange?: (pageSize: number) => unknown;
  onShowSizeChange?: (page: number, pageSize: number) => unknown;
  pageSize?: number;
  pageSizeOptions?: Array<number | string>;
  showQuickJumper?: boolean;
  showSizeChanger?: boolean;
  size?: string;
  total?: number;
};

export type TableProps<T extends object> = Omit<
  React.TableHTMLAttributes<HTMLTableElement>,
  'data' | 'onChange' | 'onRow'
> & {
  bordered?: boolean;
  columns?: TableColumn<T>[];
  data?: T[];
  dataSource?: T[];
  empty?: React.ReactNode;
  loading?: boolean;
  onRow?: (
    record: T,
    index: number,
  ) => React.HTMLAttributes<HTMLTableRowElement>;
  pagination?: PaginationProps | false;
  rowKey?: string | ((record: T) => TableKey);
  rowSelection?: TableRowSelection<T>;
  scroll?: {
    x?: React.CSSProperties['minWidth'];
    y?: React.CSSProperties['maxHeight'];
  };
  size?: string;
};

export type ConsolePrimitiveProps = Record<string, unknown> & {
  active?: boolean;
  activeKey?: React.Key | React.Key[];
  align?: React.CSSProperties['alignItems'];
  alt?: string;
  autosize?: boolean | { minRows?: number };
  block?: boolean;
  body?: React.ReactNode;
  bodyStyle?: React.CSSProperties;
  bordered?: boolean;
  cancelText?: React.ReactNode;
  checked?: boolean;
  children?: React.ReactNode;
  className?: string;
  closable?: boolean;
  closeIcon?: React.ReactNode;
  collapseIcon?: React.ReactNode;
  color?: string;
  columns?: TableColumn<object>[];
  confirmLoading?: boolean;
  content?: React.ReactNode;
  copyable?: boolean | { content?: string };
  count?: React.ReactNode;
  current?: number;
  currentPage?: number;
  darkModeImage?: React.ReactNode;
  data?: Record<string, unknown>[] | Array<Record<string, unknown>>;
  dataSource?: Record<string, unknown>[];
  defaultActiveKey?: React.Key | React.Key[];
  defaultOpen?: boolean;
  description?: React.ReactNode;
  disabled?: boolean;
  dot?: boolean;
  empty?: React.ReactNode;
  expandIcon?: React.ReactNode;
  extraText?: React.ReactNode;
  field?: string;
  filter?: unknown;
  footer?: React.ReactNode | null;
  fullMode?: boolean;
  getFormApi?: (api: FormApi) => void;
  gutter?: number | [number, number];
  hasCancel?: boolean;
  header?: React.ReactNode;
  headerExtraContent?: React.ReactNode;
  headerLine?: boolean;
  heading?: number;
  height?: React.CSSProperties['height'];
  htmlType?: React.ButtonHTMLAttributes<HTMLButtonElement>['type'];
  icon?: React.ReactNode;
  image?: React.ReactNode;
  initValue?: unknown;
  initValues?: FormValues;
  isOpen?: boolean;
  itemKey?: React.Key;
  label?: React.ReactNode;
  loading?: boolean;
  margin?: React.CSSProperties['marginBlock'];
  md?: number;
  multiple?: boolean;
  nativeType?: React.ButtonHTMLAttributes<HTMLButtonElement>['type'];
  okText?: React.ReactNode;
  onCancel?: CompatibilityHandler;
  onChange?: CompatibilityHandler;
  onCheckedChange?: (checked: boolean) => void;
  onClear?: CompatibilityHandler;
  onConfirm?: CompatibilityHandler;
  onOk?: CompatibilityHandler;
  onPageChange?: (page: number) => void;
  onRow?: (
    record: Record<string, unknown>,
    index: number,
  ) => React.HTMLAttributes<HTMLTableRowElement>;
  onSubmit?: (values: FormValues) => void;
  onToggle?: CompatibilityHandler;
  onValueChange?: (value: unknown) => void;
  onVisibleChange?: (visible: boolean) => void;
  open?: boolean;
  optionList?: SelectOptionData[];
  options?: SelectOptionData[];
  pageSize?: number;
  pagination?: ConsolePrimitiveProps | false;
  percent?: number;
  placeholder?: React.ReactNode;
  prefix?: React.ReactNode;
  prefixIcon?: React.ReactNode;
  position?: string;
  row?: number;
  rowKey?: string | ((record: Record<string, unknown>) => React.Key);
  rowSelection?: TableRowSelection<object>;
  rows?: number;
  rules?: FormRule[];
  scroll?: {
    x?: React.CSSProperties['minWidth'];
    y?: React.CSSProperties['maxHeight'];
  };
  searchWords?: unknown[] | unknown;
  shape?: string;
  showClear?: boolean;
  showInfo?: boolean;
  size?: string | number;
  sm?: number;
  sourceString?: string;
  spacing?: number | [number, number];
  spinning?: boolean;
  src?: string;
  strong?: boolean;
  style?: React.CSSProperties;
  suffix?: React.ReactNode;
  suffixIcon?: React.ReactNode;
  svg?: React.ReactNode;
  text?: React.ReactNode;
  tab?: React.ReactNode;
  theme?: string;
  tip?: React.ReactNode;
  title?: React.ReactNode;
  trigger?: 'click' | 'hover';
  type?: string;
  value?: unknown;
  values?: FormValues;
  vertical?: boolean;
  visible?: boolean;
  width?: React.CSSProperties['width'];
  wrap?: boolean;
  xl?: number;
  xs?: number;
};
export type ConsoleComponent = React.FC<ConsolePrimitiveProps>;

export const asDomProps = <T extends HTMLElement>(
  props: Record<string, unknown>,
) => props as React.HTMLAttributes<T>;

export const FormContext = React.createContext<FormContextValue | null>(null);
const RadioContext = React.createContext<{
  onChange?: (value: unknown) => void;
  value?: unknown;
} | null>(null);

export const valueFromEvent = (eventOrValue: unknown) => {
  if (
    !eventOrValue ||
    typeof eventOrValue !== 'object' ||
    !('target' in eventOrValue)
  ) {
    return eventOrValue;
  }
  const target = eventOrValue.target as HTMLInputElement;
  if (target.type === 'checkbox') return target.checked;
  return target.value;
};

export const normalizeCollapseKeys = (keys: unknown): string[] => {
  if (keys === undefined || keys === null || keys === '') return [];
  return Array.isArray(keys) ? keys.map(String) : [String(keys)];
};

export const getCollapsePanelKey = (
  child: React.ReactElement<ConsolePrimitiveProps>,
  index: number,
) => String(child.props.itemKey ?? child.key ?? index);

type NotificationInput =
  | React.ReactNode
  | {
      content?: React.ReactNode;
      description?: React.ReactNode;
      title?: React.ReactNode;
    };

const contentOf = (input: NotificationInput): React.ReactNode =>
  typeof input === 'string' || React.isValidElement(input)
    ? input
    : input && typeof input === 'object' && !Array.isArray(input)
      ? 'content' in input || 'description' in input || 'title' in input
        ? input.content || input.description || input.title || ''
        : ''
      : String(input ?? '');

type ToastMethod = 'error' | 'info' | 'success' | 'warning';

const TOAST_DURATION: Record<ToastMethod, number> = {
  error: toastConstants.ERROR_TIMEOUT,
  info: toastConstants.INFO_TIMEOUT,
  success: toastConstants.SUCCESS_TIMEOUT,
  warning: toastConstants.WARNING_TIMEOUT,
};

const notify = (
  method: ToastMethod,
  input: NotificationInput,
  options?: ExternalToast,
) =>
  uiToast[method](contentOf(input), {
    duration: TOAST_DURATION[method],
    ...options,
  });

type ModalConfig = {
  content?: React.ReactNode;
  onCancel?: () => void;
  onOk?: () => void;
  title?: React.ReactNode;
};

type ModalTone = 'danger' | 'info' | 'warning';
type MountedModal = { host: HTMLDivElement; root: Root };

const modalRoots = new Set<MountedModal>();

function mountModal(config: ModalConfig, tone: ModalTone = 'info') {
  if (typeof document === 'undefined') return null;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root = createRoot(host);
  modalRoots.add({ host, root });
  const close = () => {
    root.unmount();
    host.remove();
    for (const item of modalRoots) {
      if (item.host === host) modalRoots.delete(item);
    }
  };

  root.render(
    <Modal
      visible
      title={config.title}
      type={tone}
      onCancel={() => {
        config.onCancel?.();
        close();
      }}
      onOk={() => {
        config.onOk?.();
        close();
      }}
    >
      {config.content}
    </Modal>,
  );
  return close;
}

function mapButtonVariant(type?: string, theme?: string) {
  if (type === 'primary') return 'primary';
  if (type === 'danger') return theme === 'solid' ? 'danger' : 'soft-danger';
  if (type === 'warning') return 'soft';
  if (type === 'tertiary') return theme === 'solid' ? 'secondary' : 'ghost';
  if (type === 'link') return 'link';
  return theme === 'borderless' || theme === 'light' ? 'ghost' : 'secondary';
}

function mapButtonSize(size?: string | number) {
  if (size === 'small') return 'sm';
  if (size === 'large') return 'lg';
  if (size === 'default') return 'md';
  return size || 'md';
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      block,
      children,
      className,
      disabled,
      htmlType,
      icon,
      loading,
      nativeType,
      size,
      theme,
      type,
      ...props
    },
    ref,
  ) => (
    <button
      {...props}
      ref={ref}
      type={htmlType || nativeType || 'button'}
      className={cn(
        'lazu-button lazu-button',
        `lazu-button--${mapButtonVariant(type, theme)}`,
        `lazu-button--${mapButtonSize(size)}`,
        block && 'lazu-button--full',
        className,
      )}
      disabled={disabled || loading}
    >
      {loading ? <LoaderCircle size={15} className='lazu-spin-icon' /> : icon}
      {children}
    </button>
  ),
);

Button.displayName = 'Button';

export function Card({
  bordered,
  bodyStyle,
  children,
  className,
  footer,
  headerExtraContent,
  headerLine,
  style,
  title,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <section
      {...asDomProps<HTMLElement>(props)}
      className={cn('lazu-data-card lazu-card', className)}
      style={style}
    >
      {title || headerExtraContent ? (
        <div
          className={cn(
            'lazu-data-card__header',
            headerLine === false && 'lazu-card__header--plain',
          )}
        >
          <div className='lazu-data-card__title'>{title}</div>
          {headerExtraContent}
        </div>
      ) : null}
      <div className='lazu-data-card__body lazu-card__body' style={bodyStyle}>
        {children}
      </div>
      {footer ? <div className='lazu-data-card__footer'>{footer}</div> : null}
    </section>
  );
}

export function Space({
  align = 'center',
  children,
  className,
  spacing = 8,
  vertical = false,
  wrap = true,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn(
        'lazu-space',
        vertical && 'lazu-space--vertical',
        className,
      )}
      style={{
        alignItems: align,
        display: 'flex',
        flexDirection: vertical ? 'column' : 'row',
        flexWrap: wrap ? 'wrap' : 'nowrap',
        gap: Array.isArray(spacing) ? spacing.join('px ') : spacing,
        ...props.style,
      }}
    >
      {children}
    </div>
  );
}

export function Row({
  children,
  className,
  gutter,
  ...props
}: ConsolePrimitiveProps) {
  const gap = Array.isArray(gutter) ? gutter[0] : gutter;
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn('lazu-row', className)}
      style={{ gap, ...props.style }}
    >
      {children}
    </div>
  );
}

export function Col({
  children,
  className,
  span,
  xs,
  sm,
  md,
  lg,
  xl,
  ...props
}: ConsolePrimitiveProps) {
  const activeSpan = span || xl || lg || md || sm || xs || 24;
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn('lazu-col', className)}
      style={{
        flex: `0 0 ${(Number(activeSpan) / 24) * 100}%`,
        maxWidth: `${(Number(activeSpan) / 24) * 100}%`,
        ...props.style,
      }}
    >
      {children}
    </div>
  );
}

export const Divider: ConsoleComponent = ({
  className,
  margin = 16,
  ...props
}: ConsolePrimitiveProps) => (
  <hr
    {...asDomProps<HTMLHRElement>(props)}
    className={cn('lazu-divider', className)}
    style={{ marginBlock: margin, ...props.style }}
  />
);

export function Spin({
  children,
  spinning = false,
  tip,
}: ConsolePrimitiveProps) {
  return (
    <div className='lazu-spin-wrap'>
      {children}
      {spinning ? (
        <div className='lazu-spin-overlay'>
          <LoaderCircle className='lazu-spin-icon' size={22} />
          {tip ? <span>{tip}</span> : null}
        </div>
      ) : null}
    </div>
  );
}

export function Tag({
  bordered,
  children,
  className,
  color,
  prefixIcon,
  shape,
  size,
  style,
  suffixIcon,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <span
      {...asDomProps<HTMLSpanElement>(props)}
      className={cn(
        'lazu-tag lazu-tag',
        size === 'small' && 'lazu-tag--small',
        className,
      )}
      data-color={color}
      style={style}
    >
      {prefixIcon}
      {children}
      {suffixIcon}
    </span>
  );
}

export function Badge({
  children,
  count,
  dot,
  type = 'primary',
  ...props
}: ConsolePrimitiveProps) {
  return (
    <span {...asDomProps<HTMLSpanElement>(props)} className='lazu-badge-wrap'>
      {children}
      {dot ? (
        <span className={`lazu-badge-dot lazu-badge-dot--${type}`} />
      ) : null}
      {count ? <span className='lazu-badge-count'>{count}</span> : null}
    </span>
  );
}

export function Avatar({
  alt,
  children,
  className,
  color,
  size = 36,
  src,
  style,
  ...props
}: ConsolePrimitiveProps) {
  const dimension =
    typeof size === 'number'
      ? size
      : size === 'small'
        ? 28
        : size === 'large'
          ? 44
          : 36;
  return (
    <span
      {...asDomProps<HTMLSpanElement>(props)}
      className={cn('lazu-avatar', className)}
      style={{
        height: dimension,
        width: dimension,
        background: color,
        ...style,
      }}
    >
      {src ? <img src={src} alt={alt || ''} /> : children}
    </span>
  );
}

/**
 * Legacy alias for {@link EmptyState}. The product has one empty state — the
 * dashed slot — so this shim only drops the illustration props the old call
 * sites still pass. New code should import EmptyState directly.
 */
export function Empty({ description, title }: ConsolePrimitiveProps) {
  return <EmptyState description={description} title={title || 'No data'} />;
}

/**
 * Legacy alias for {@link InlineNotice}. The feedback system has one in-page
 * notice; this shim only maps the old `type` vocabulary onto it so existing
 * call sites keep working. New code should import InlineNotice directly.
 */
export function Banner({
  closeIcon,
  description,
  fullMode,
  icon,
  title,
  type = 'info',
  ...props
}: ConsolePrimitiveProps) {
  const tone: InlineNoticeTone =
    type === 'error' || type === 'danger'
      ? 'danger'
      : type === 'warning' || type === 'success'
        ? type
        : 'info';

  return (
    <InlineNotice
      {...asDomProps<HTMLDivElement>(props)}
      actions={closeIcon}
      className={cn(fullMode && 'lazu-banner--full')}
      description={description}
      icon={icon}
      title={title}
      tone={tone}
    />
  );
}

export function Progress({
  percent = 0,
  showInfo = true,
  type = 'line',
  ...props
}: ConsolePrimitiveProps) {
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn(
        'lazu-progress',
        type === 'circle' && 'lazu-progress--circle',
      )}
    >
      <div className='lazu-progress__track'>
        <div
          className='lazu-progress__bar'
          style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
        />
      </div>
      {showInfo ? (
        <span className='lazu-progress__label'>{Math.round(percent)}%</span>
      ) : null}
    </div>
  );
}

export const Tooltip: ConsoleComponent = ({
  children,
  content,
  position,
  ...props
}: ConsolePrimitiveProps) => (
  <UiTooltip
    content={content}
    side={
      String(position || 'top')
        .replace(/Left|Right/g, '')
        .toLowerCase() as React.ComponentProps<typeof UiTooltip>['side']
    }
    {...props}
  >
    {children as React.ReactElement}
  </UiTooltip>
);

export function Popover({
  children,
  content,
  trigger = 'click',
}: ConsolePrimitiveProps) {
  const [open, setOpen] = React.useState(false);
  const eventProps =
    trigger === 'hover'
      ? {
          onMouseEnter: () => setOpen(true),
          onMouseLeave: () => setOpen(false),
        }
      : { onClick: () => setOpen((value) => !value) };
  return (
    <span className='lazu-popover' {...eventProps}>
      {children}
      {open ? <span className='lazu-popover__content'>{content}</span> : null}
    </span>
  );
}

export function Popconfirm({
  children,
  content,
  onConfirm,
  title,
}: ConsolePrimitiveProps) {
  const onlyChild = React.Children.only(children) as React.ReactElement<
    React.HTMLAttributes<HTMLElement>
  >;
  return React.cloneElement(onlyChild, {
    onClick: (event: React.MouseEvent<HTMLElement>) => {
      onlyChild.props.onClick?.(event);
      if (!window.confirm(String(content || title || ''))) return;
      onConfirm?.(event);
    },
  });
}

function InputBase({
  className,
  disabled,
  onChange,
  onClear,
  prefix,
  showClear,
  suffix,
  value,
  ...props
}: ConsolePrimitiveProps) {
  const hasValue =
    value !== undefined && value !== null && String(value) !== '';

  return (
    <span className='lazu-input-affix'>
      {prefix ? (
        <span className='lazu-input-affix__prefix'>{prefix}</span>
      ) : null}
      <input
        {...(props as React.InputHTMLAttributes<HTMLInputElement>)}
        className={cn('lazu-input lazu-input', className)}
        disabled={disabled}
        onChange={(event) => onChange?.(event.target.value, event)}
        value={value as React.InputHTMLAttributes<HTMLInputElement>['value']}
      />
      {showClear && hasValue && !disabled ? (
        <button
          type='button'
          className='lazu-input-affix__clear'
          aria-label='Clear'
          onMouseDown={(event) => event.preventDefault()}
          onClick={(event) => {
            onChange?.('', event);
            onClear?.(event);
          }}
        >
          <X size={13} />
        </button>
      ) : suffix ? (
        <span className='lazu-input-affix__suffix'>{suffix}</span>
      ) : null}
    </span>
  );
}

export const Input = InputBase as typeof InputBase & {
  Password: ConsoleComponent;
};

Input.Password = function PasswordInput(props: ConsolePrimitiveProps) {
  return <Input type='password' {...props} />;
};

export function TextArea({
  autosize,
  className,
  onChange,
  rows = 4,
  ...props
}: ConsolePrimitiveProps) {
  const nextRows =
    (typeof autosize === 'object' ? autosize.minRows : undefined) || rows;
  return (
    <textarea
      {...(props as React.TextareaHTMLAttributes<HTMLTextAreaElement>)}
      className={cn('lazu-textarea lazu-textarea', className)}
      rows={nextRows}
      onChange={(event) => onChange?.(event.target.value, event)}
    />
  );
}

export function InputNumber({
  className,
  onChange,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <input
      {...(props as React.InputHTMLAttributes<HTMLInputElement>)}
      className={cn('lazu-input lazu-input-number', className)}
      type='number'
      onChange={(event) => {
        const value =
          event.target.value === '' ? undefined : Number(event.target.value);
        onChange?.(Number.isNaN(value) ? undefined : value, event);
      }}
    />
  );
}

export function TagInput({
  className,
  onChange,
  value = [],
  ...props
}: ConsolePrimitiveProps) {
  return (
    <Input
      className={className}
      value={Array.isArray(value) ? value.join(', ') : value}
      onChange={(nextValue: unknown, event: unknown) => {
        onChange?.(
          String(nextValue)
            .split(',')
            .map((item) => item.trim())
            .filter(Boolean),
          event,
        );
      }}
      {...props}
    />
  );
}

export const InputGroup: ConsoleComponent = ({
  children,
  className,
}: ConsolePrimitiveProps) => (
  <div className={cn('lazu-input-group', className)}>{children}</div>
);

const optionLabelToText = (label: React.ReactNode): string => {
  if (label === undefined || label === null) return '';
  if (typeof label === 'string' || typeof label === 'number') {
    return String(label);
  }
  if (Array.isArray(label)) {
    return label.map(optionLabelToText).join('');
  }
  if (React.isValidElement(label)) {
    return optionLabelToText(
      (label.props as { children?: React.ReactNode }).children,
    );
  }
  return String(label);
};

function SelectBase({
  children,
  className,
  filter,
  multiple,
  onChange,
  onValueChange,
  optionList,
  options,
  placeholder,
  prefix,
  size,
  value,
  ...props
}: ConsolePrimitiveProps) {
  const mergedOptions = options || optionList;
  return (
    <select
      {...(props as React.SelectHTMLAttributes<HTMLSelectElement>)}
      className={cn('lazu-select-trigger lazu-select', className)}
      multiple={multiple}
      value={
        (value ??
          (multiple
            ? []
            : '')) as React.SelectHTMLAttributes<HTMLSelectElement>['value']
      }
      onChange={(event) => {
        const nextValue = multiple
          ? Array.from(event.target.selectedOptions).map(
              (option) => option.value,
            )
          : event.target.value;
        onChange?.(nextValue, event);
        onValueChange?.(nextValue);
      }}
    >
      {placeholder ? <option value=''>{placeholder}</option> : null}
      {mergedOptions
        ? mergedOptions.map((option) => (
            <option
              key={option.value}
              value={option.value}
              disabled={option.disabled}
            >
              {optionLabelToText(option.label)}
            </option>
          ))
        : children}
    </select>
  );
}

export const Select = SelectBase as typeof SelectBase & {
  Option: ConsoleComponent;
};

Select.Option = function SelectOption({
  children,
  disabled,
  value,
}: ConsolePrimitiveProps) {
  return (
    <option
      value={value as React.OptionHTMLAttributes<HTMLOptionElement>['value']}
      disabled={disabled}
    >
      {children}
    </option>
  );
};

export function Checkbox({
  checked,
  children,
  className,
  onChange,
  onCheckedChange,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <label className={cn('lazu-checkbox-label lazu-checkbox-label', className)}>
      <input
        {...(props as React.InputHTMLAttributes<HTMLInputElement>)}
        type='checkbox'
        className='lazu-checkbox-input'
        checked={checked}
        onChange={(event) => {
          onChange?.(event, event.target.checked);
          onCheckedChange?.(event.target.checked);
        }}
      />
      <span className='lazu-checkbox lazu-checkbox' aria-hidden='true'>
        {checked ? <Check size={13} strokeWidth={3} /> : null}
      </span>
      {children ? <span>{children}</span> : null}
    </label>
  );
}

export function Switch({
  checked,
  children,
  className,
  onChange,
  onCheckedChange,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <label className={cn('lazu-switch-label lazu-switch-label', className)}>
      <button
        {...(props as React.ButtonHTMLAttributes<HTMLButtonElement>)}
        type='button'
        className='lazu-switch lazu-switch'
        role='switch'
        aria-checked={Boolean(checked)}
        data-state={checked ? 'checked' : 'unchecked'}
        onClick={(event) => {
          const nextValue = !checked;
          onChange?.(nextValue, event);
          onCheckedChange?.(nextValue);
        }}
      >
        <span className='lazu-switch__thumb' />
      </button>
      {children ? <span>{children}</span> : null}
    </label>
  );
}

export function Radio({ children, value, ...props }: ConsolePrimitiveProps) {
  const context = React.useContext(RadioContext);
  const checked = context?.value === value;
  return (
    <label className='lazu-radio'>
      <input
        {...(props as React.InputHTMLAttributes<HTMLInputElement>)}
        type='radio'
        checked={checked}
        value={value as React.InputHTMLAttributes<HTMLInputElement>['value']}
        onChange={() => context?.onChange?.(value)}
      />
      <Circle size={12} fill={checked ? 'currentColor' : 'none'} />
      <span>{children}</span>
    </label>
  );
}

export function RadioGroup({
  children,
  onChange,
  value,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <RadioContext.Provider value={{ value, onChange }}>
      <div {...asDomProps<HTMLDivElement>(props)} className='lazu-radio-group'>
        {children}
      </div>
    </RadioContext.Provider>
  );
}

Radio.Group = RadioGroup;
export namespace Radio {
  export declare const Group: typeof RadioGroup;
}

export function DatePicker({
  onChange,
  type,
  ...props
}: ConsolePrimitiveProps) {
  if (type === 'dateRange' || type === 'dateTimeRange') {
    return (
      <DateRangePicker
        {...(props as React.ComponentProps<typeof DateRangePicker>)}
        onChange={
          onChange as React.ComponentProps<typeof DateRangePicker>['onChange']
        }
      />
    );
  }
  return (
    <UiDatePicker
      {...(props as React.ComponentProps<typeof UiDatePicker>)}
      onChange={
        onChange as React.ComponentProps<typeof UiDatePicker>['onChange']
      }
    />
  );
}

const MODAL_TONE_ICON = {
  danger: XCircle,
  info: Info,
  warning: AlertTriangle,
} as const;

function ModalBase({
  body,
  cancelText,
  children,
  closable = true,
  confirmLoading,
  footer,
  hasCancel = true,
  okText,
  onCancel,
  onOk,
  open,
  title,
  type,
  visible,
  ...props
}: ConsolePrimitiveProps) {
  const titleId = React.useId();
  const isOpen = open ?? visible;
  if (!isOpen) return null;
  const {
    'aria-describedby': ariaDescribedBy,
    'aria-label': ariaLabel,
    'aria-labelledby': ariaLabelledBy,
    ...overlayProps
  } = asDomProps<HTMLDivElement>(props);
  // Without this the tone passed by Modal.error / Modal.warning was swallowed
  // by asDomProps and every dialog looked like a neutral confirm.
  const tone = type === 'danger' || type === 'warning' ? type : null;
  const ToneIcon = tone ? MODAL_TONE_ICON[tone] : null;
  return (
    <div {...overlayProps} className='lazu-modal-overlay lazu-modal-overlay'>
      <div
        className={cn(
          'lazu-modal-content',
          tone && `lazu-modal-content--${tone}`,
        )}
        aria-describedby={ariaDescribedBy}
        aria-label={ariaLabel}
        aria-labelledby={title ? ariaLabelledBy || titleId : ariaLabelledBy}
        aria-modal='true'
        role={tone === 'danger' ? 'alertdialog' : 'dialog'}
      >
        {closable ? (
          <button
            type='button'
            aria-label={i18next.t('page.confirmDialog.cancel')}
            className='lazu-modal-close'
            onClick={() => onCancel?.()}
          >
            <X size={17} />
          </button>
        ) : null}
        {title ? (
          <div id={titleId} className='lazu-modal-title lazu-modal-title'>
            {ToneIcon ? (
              <ToneIcon
                aria-hidden='true'
                className='lazu-modal-title__icon'
                size={16}
              />
            ) : null}
            {title}
          </div>
        ) : null}
        <div className='lazu-modal-body'>{body || children}</div>
        {footer === null ? null : (
          <div className='lazu-modal-footer'>
            {footer || (
              <>
                {hasCancel ? (
                  <Button onClick={() => onCancel?.()}>
                    {cancelText || i18next.t('page.confirmDialog.cancel')}
                  </Button>
                ) : null}
                <Button
                  type='primary'
                  loading={confirmLoading}
                  onClick={() => onOk?.()}
                >
                  {okText || i18next.t('page.confirmDialog.confirm')}
                </Button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

export const Modal = ModalBase as typeof ModalBase & {
  confirm: (config: ModalConfig) => (() => void) | null;
  destroyAll: () => void;
  error: (config: ModalConfig) => (() => void) | null;
  info: (config: ModalConfig) => (() => void) | null;
  warning: (config: ModalConfig) => (() => void) | null;
};

Modal.confirm = (config) => mountModal(config, 'info');
Modal.error = (config) => mountModal(config, 'danger');
Modal.info = (config) => mountModal(config, 'info');
Modal.warning = (config) => mountModal(config, 'warning');
Modal.destroyAll = () => {
  for (const { host, root } of modalRoots) {
    root.unmount();
    host.remove();
  }
  modalRoots.clear();
};

export function SideSheet({
  children,
  onCancel,
  title,
  visible,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <Modal visible={visible} onCancel={onCancel} title={title} {...props}>
      {children}
    </Modal>
  );
}

export function Image({ alt = '', src, ...props }: ConsolePrimitiveProps) {
  return (
    <img
      {...(props as React.ImgHTMLAttributes<HTMLImageElement>)}
      alt={alt}
      src={src}
    />
  );
}

export function ImagePreview({
  src,
  visible,
  onVisibleChange,
}: ConsolePrimitiveProps) {
  return (
    <Modal
      visible={visible}
      aria-label={i18next.t('legacy.i18n.preview.de61aa8e1c')}
      onCancel={() => onVisibleChange?.(false)}
      footer={null}
    >
      <img src={src} alt='' className='lazu-image-preview' />
    </Modal>
  );
}

export const LocaleProvider: ConsoleComponent = ({
  children,
}: ConsolePrimitiveProps) => children;

type ToastApi = Record<
  ToastMethod,
  (input: NotificationInput, options?: ExternalToast) => string | number
>;

export const Toast: ToastApi = {
  error: (input, options) => notify('error', input, options),
  info: (input, options) => notify('info', input, options),
  success: (input, options) => notify('success', input, options),
  warning: (input, options) => notify('warning', input, options),
};

export const Notification = Toast;

export const Typography: Record<string, ConsoleComponent> = {
  Text: ({
    children,
    className,
    strong,
    type,
    ...props
  }: ConsolePrimitiveProps) => (
    <span
      {...asDomProps<HTMLSpanElement>(props)}
      className={cn(
        'lazu-typography-text',
        strong && 'font-semibold',
        type && `lazu-text--${type}`,
        className,
      )}
    >
      {children}
    </span>
  ),
  Paragraph: ({
    children,
    className,
    copyable,
    ...props
  }: ConsolePrimitiveProps) => (
    <p
      {...asDomProps<HTMLParagraphElement>(props)}
      className={cn('lazu-typography-paragraph', className)}
    >
      {children}
      {copyable ? (
        <Button
          size='small'
          theme='light'
          aria-label='Copy'
          onClick={() =>
            navigator.clipboard?.writeText(
              (typeof copyable === 'object' ? copyable.content : undefined) ||
                String(children),
            )
          }
        >
          <Copy size={13} />
        </Button>
      ) : null}
    </p>
  ),
  Title: ({
    children,
    className,
    heading = 4,
    ...props
  }: ConsolePrimitiveProps) => {
    const TagName = `h${Math.min(6, Math.max(1, Number(heading)))}` as
      | 'h1'
      | 'h2'
      | 'h3'
      | 'h4'
      | 'h5'
      | 'h6';
    return (
      <TagName
        {...asDomProps<HTMLHeadingElement>(props)}
        className={cn('lazu-typography-title', className)}
      >
        {children}
      </TagName>
    );
  },
};

type DescriptionItem = {
  children?: React.ReactNode;
  itemKey?: React.Key;
  key?: React.Key;
  label?: React.ReactNode;
  value?: React.ReactNode;
};

function DescriptionsBase({ children, data, row }: ConsolePrimitiveProps) {
  const items =
    (data as DescriptionItem[] | undefined) ||
    React.Children.toArray(children)
      .filter((child): child is React.ReactElement<DescriptionItem> =>
        React.isValidElement<DescriptionItem>(child),
      )
      .map((child) => child.props);
  return (
    <dl
      className='lazu-descriptions'
      style={{
        gridTemplateColumns: row ? `repeat(${row}, minmax(0, 1fr))` : undefined,
      }}
    >
      {items.map((item, index) => (
        <div
          key={item.key || item.itemKey || index}
          className='lazu-descriptions__item'
        >
          <dt>{item.label ?? String(item.key ?? '')}</dt>
          <dd>{item.value || item.children}</dd>
        </div>
      ))}
    </dl>
  );
}

export const Descriptions = DescriptionsBase as typeof DescriptionsBase & {
  Item: ConsoleComponent;
};

Descriptions.Item = function DescriptionItem() {
  return null;
};

function StepsBase({ children, current = 0 }: ConsolePrimitiveProps) {
  return (
    <ol className='lazu-steps'>
      {React.Children.map(children, (child, index) => {
        if (!React.isValidElement<ConsolePrimitiveProps>(child)) return child;
        return (
          <li
            className={cn(
              'lazu-steps__item',
              index <= current && 'lazu-steps__item--active',
            )}
          >
            <span>{index + 1}</span>
            <div>{child.props.title || child.props.description}</div>
          </li>
        );
      })}
    </ol>
  );
}

export const Steps = StepsBase as typeof StepsBase & {
  Step: ConsoleComponent;
};

Steps.Step = function Step() {
  return null;
};

function SkeletonBox({
  active,
  className,
  height,
  style,
  title,
  width,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      aria-hidden='true'
      className={cn('lazu-skeleton lazu-skeleton', className)}
      style={{ height, width, ...style }}
    />
  );
}

function SkeletonBase({
  active,
  children,
  className,
  height,
  loading = false,
  placeholder,
  style,
  width,
  ...props
}: ConsolePrimitiveProps) {
  if (!loading) return <>{children}</>;
  if (placeholder !== undefined && placeholder !== null)
    return <>{placeholder}</>;

  return (
    <SkeletonBox
      className={className}
      height={height}
      style={style}
      width={width}
      {...props}
    />
  );
}

export const Skeleton = SkeletonBase as typeof SkeletonBase & {
  Avatar: ConsoleComponent;
  Button: ConsoleComponent;
  Paragraph: ConsoleComponent;
  Title: ConsoleComponent;
};

Skeleton.Avatar = function SkeletonAvatar({
  className,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <SkeletonBox
      className={cn('lazu-skeleton--avatar', className)}
      {...props}
    />
  );
};

Skeleton.Button = function SkeletonButton({
  className,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <SkeletonBox
      className={cn('lazu-skeleton--button', className)}
      {...props}
    />
  );
};

Skeleton.Paragraph = function SkeletonParagraph({
  active,
  className,
  rows = 3,
  style,
  title,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn('lazu-skeleton-paragraph', className)}
      style={style}
    >
      {Array.from({ length: rows }).map((_, index) => (
        <SkeletonBox key={index} />
      ))}
    </div>
  );
};

Skeleton.Title = function SkeletonTitle({
  className,
  ...props
}: ConsolePrimitiveProps) {
  return (
    <SkeletonBox className={cn('lazu-skeleton--title', className)} {...props} />
  );
};

export function Icon({ svg }: ConsolePrimitiveProps) {
  return <span className='lazu-icon'>{svg}</span>;
}

export function Highlight({
  sourceString = '',
  searchWords = [],
}: ConsolePrimitiveProps) {
  const words = Array.isArray(searchWords)
    ? searchWords.filter(Boolean)
    : [searchWords].filter(Boolean);
  if (!words.length) return sourceString;
  const pattern = new RegExp(
    `(${words.map((word) => String(word).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`,
    'gi',
  );
  return String(sourceString)
    .split(pattern)
    .map((part, index) =>
      words.some(
        (word) => String(word).toLowerCase() === part.toLowerCase(),
      ) ? (
        <mark key={`${part}-${index}`}>{part}</mark>
      ) : (
        part
      ),
    );
}
