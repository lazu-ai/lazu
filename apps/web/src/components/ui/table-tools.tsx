import { Columns3, Copy, Search, X } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Badge } from './badge';
import { Button } from './button';
import { Checkbox } from './checkbox';
import { DataSurface } from './data-surface';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu';
import { FilterBar } from './filter-bar';
import { Input } from './input';
import { Pagination } from './pagination';
import { RefreshButton } from './refresh-button';
import { Tag } from './tag';
import { Toolbar, ToolbarGroup } from './toolbar';
import { cn } from '../../lib/cn';
import type { TFunction } from 'i18next';
import type { ReactNode } from 'react';

export const DEFAULT_PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

type BadgeVariant = 'danger' | 'info' | 'neutral' | 'success' | 'warning';

export type ConsoleColumnMeta = {
  defaultHidden?: boolean;
  id: string;
  label: ReactNode;
};

type ItemsPayload<T> = {
  items: T[];
  meta: unknown;
  page: number;
  pageSize: number;
  total: number;
  /** The list stopped counting at `total`; more rows match. */
  totalAtLeast: boolean;
};

type PaginationConfig = {
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  page: number;
  pageSize: number;
  total: number;
};

export function coerceItemsPayload<T = unknown>(
  payload: unknown,
  fallbackPage = 1,
  fallbackSize = 10,
): ItemsPayload<T> {
  const payloadRecord =
    typeof payload === 'object' && payload !== null
      ? (payload as Record<string, unknown>)
      : {};
  const data = payloadRecord.data ?? payload;
  const dataRecord =
    typeof data === 'object' && data !== null && !Array.isArray(data)
      ? (data as Record<string, unknown>)
      : {};
  const items = Array.isArray(dataRecord.items)
    ? dataRecord.items
    : Array.isArray(data)
      ? data
      : [];

  return {
    items: items as T[],
    page: Number(dataRecord.page || fallbackPage),
    pageSize: Number(
      dataRecord.page_size || dataRecord.pageSize || fallbackSize,
    ),
    total: Number(dataRecord.total ?? items.length),
    totalAtLeast: dataRecord.total_at_least === true,
    meta: data,
  };
}

export function createSearchParams(params: Record<string, unknown>) {
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') {
      return;
    }
    query.set(key, String(value));
  });
  return query.toString();
}

export function compactNumber(value: unknown, fallback = '-') {
  const number = Number(value);
  if (!Number.isFinite(number)) {
    return fallback;
  }
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: number >= 100 ? 0 : 2,
  }).format(number);
}

export function formatUnixTime(value: unknown, fallback = '-') {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return fallback;
  }
  const millis = number > 100000000000 ? number : number * 1000;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(millis));
}

export function truncateMiddle(value: unknown, head = 18, tail = 8) {
  const text = String(value || '');
  if (text.length <= head + tail + 3) {
    return text || '-';
  }
  return `${text.slice(0, head)}...${text.slice(-tail)}`;
}

export function statusBadgeVariant(status: unknown): BadgeVariant {
  const successStatuses: readonly unknown[] = [
    1,
    '1',
    true,
    'SUCCESS',
    'success',
    'active',
  ];
  const warningStatuses: readonly unknown[] = [
    2,
    '2',
    'QUEUED',
    'SUBMITTED',
    'IN_PROGRESS',
  ];
  const dangerStatuses: readonly unknown[] = [
    0,
    '0',
    3,
    '3',
    false,
    'FAILURE',
    'failure',
    'disabled',
  ];

  if (successStatuses.includes(status)) {
    return 'success';
  }
  if (warningStatuses.includes(status)) {
    return 'warning';
  }
  if (dangerStatuses.includes(status)) {
    return 'danger';
  }
  return 'neutral';
}

export function InlineCode({
  children,
  title,
}: {
  children?: ReactNode;
  title?: string;
}) {
  return (
    <span
      className='whitespace-nowrap rounded border border-[var(--ui-border)] bg-[var(--ui-bg-muted)] px-2 py-1 font-mono text-xs text-[var(--ui-text)]'
      title={title || String(children || '')}
    >
      {children || '-'}
    </span>
  );
}

export function TextStack({
  eyebrow,
  title,
  description,
}: {
  description?: ReactNode;
  eyebrow?: ReactNode;
  title?: ReactNode;
}) {
  return (
    <div className='min-w-[12rem]'>
      {eyebrow ? (
        <div className='text-xs font-medium text-[var(--ui-text-tertiary)]'>
          {eyebrow}
        </div>
      ) : null}
      <div className='font-medium text-[var(--ui-text)]'>{title || '-'}</div>
      {description ? (
        <div className='mt-1 max-w-[24rem] truncate text-xs text-[var(--ui-text-secondary)]'>
          {description}
        </div>
      ) : null}
    </div>
  );
}

export function parseTagList(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.map((v) => String(v)).filter(Boolean);
  }
  if (value === null || value === undefined) return [];
  const text = String(value).trim();
  if (!text) return [];
  if (text.startsWith('[') || text.startsWith('{')) {
    try {
      const parsed = JSON.parse(text);
      if (Array.isArray(parsed)) {
        return parsed.map((v) => String(v)).filter(Boolean);
      }
    } catch {
      // fall through to comma split
    }
  }
  return text
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean);
}

export function TagList({
  items,
  max = 3,
  fallback = '-',
}: {
  fallback?: ReactNode;
  items: unknown;
  max?: number;
}) {
  const list = Array.isArray(items) ? items : parseTagList(items);
  if (!list.length) {
    return <span className='text-[var(--ui-text-tertiary)]'>{fallback}</span>;
  }
  const shown = list.slice(0, max);
  const hidden = list.length - shown.length;
  return (
    <div className='flex max-w-[18rem] flex-wrap gap-1' title={list.join(', ')}>
      {shown.map((item, idx) => (
        <Tag key={`${item}-${idx}`}>{item}</Tag>
      ))}
      {hidden > 0 ? <Tag>+{hidden}</Tag> : null}
    </div>
  );
}

export function MetricPill({
  label,
  value,
  variant = 'neutral',
}: {
  label: ReactNode;
  value: ReactNode;
  variant?: BadgeVariant;
}) {
  return (
    <Badge variant={variant}>
      <span className='text-[var(--ui-text-tertiary)]'>{label}</span>
      <span>{value}</span>
    </Badge>
  );
}

export function SearchField({
  label,
  onChange,
  placeholder,
  value,
}: {
  label: ReactNode;
  onChange: (value: string) => void;
  placeholder?: string;
  value: string;
}) {
  return (
    <label className='min-w-[12rem] flex-1 text-xs font-medium text-[var(--ui-text-secondary)]'>
      <span className='mb-1 block'>{label}</span>
      <Input
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}

export function ColumnVisibilityMenu({
  columns,
  iconOnly = false,
  onChange,
  t,
  value,
}: {
  columns: ConsoleColumnMeta[];
  /** A bare icon, for a menu that lives in the table's own header row. */
  iconOnly?: boolean;
  onChange: (columnId: string, checked: unknown) => void;
  t: TFunction;
  value: Record<string, boolean>;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger>
        {/* Column visibility is a table-only control; cards show every field,
            so hide it on the mobile card view. */}
        {iconOnly ? (
          <Button
            size='xs'
            variant='ghost'
            aria-label={t('page.console.tables.columns')}
            className='lazu-column-menu-icon hidden md:inline-flex'
          >
            <Columns3 aria-hidden='true' size={15} />
          </Button>
        ) : (
          <Button
            size='sm'
            variant='secondary'
            className='hidden md:inline-flex'
          >
            <Columns3 size={15} />
            {t('page.console.tables.columns')}
          </Button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='min-w-[220px] p-2'>
        {columns.map((column) => (
          <DropdownMenuItem
            key={column.id}
            onSelect={(event) => event.preventDefault()}
          >
            <Checkbox
              checked={value[column.id] !== false}
              onCheckedChange={(checked) => onChange(column.id, checked)}
            >
              {column.label}
            </Checkbox>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function useColumnVisibility(
  storageKey: string,
  columns: ConsoleColumnMeta[],
  legacyStorageKeys: string[] = [],
  legacyDefaultHiddenColumnIds: string[] = [],
) {
  const [value, setValue] = useState<Record<string, boolean>>(() => {
    const readStoredValue = (key: string) => {
      try {
        const raw = localStorage.getItem(key);
        if (!raw) return null;
        const parsed = JSON.parse(raw);
        return parsed && typeof parsed === 'object'
          ? (parsed as Record<string, boolean>)
          : null;
      } catch {
        return null;
      }
    };

    const storedValue = readStoredValue(storageKey);
    if (storedValue) return storedValue;

    for (const legacyKey of legacyStorageKeys) {
      const legacyValue = readStoredValue(legacyKey);
      if (!legacyValue) continue;
      const legacyHidden = new Set(legacyDefaultHiddenColumnIds);
      const migratedValue = columns.reduce<Record<string, boolean>>(
        (next, column) => {
          next[column.id] =
            legacyValue[column.id] ?? !legacyHidden.has(column.id);
          return next;
        },
        {},
      );
      try {
        localStorage.setItem(storageKey, JSON.stringify(migratedValue));
      } catch {
        // Keep the migrated preference in memory when storage is unavailable.
      }
      return migratedValue;
    }

    return {};
  });

  const setColumnVisible = (columnId: string, checked: unknown) => {
    setValue((current) => {
      const next = { ...current, [columnId]: checked !== false };
      localStorage.setItem(storageKey, JSON.stringify(next));
      return next;
    });
  };

  const isVisible = (column: ConsoleColumnMeta) => {
    const stored = value[column.id];
    if (stored === undefined) {
      return column.defaultHidden ? false : true;
    }
    return stored !== false;
  };

  const visibleColumns = useMemo(
    () => columns.filter(isVisible),
    [columns, value],
  );

  const effectiveValue = useMemo(() => {
    const next: Record<string, boolean> = {};
    columns.forEach((column) => {
      next[column.id] = isVisible(column);
    });
    return next;
  }, [columns, value]);

  return { setColumnVisible, value: effectiveValue, visibleColumns };
}

export function ConsoleTableFrame({
  actions,
  children,
  className,
  columns,
  compact = false,
  showRefresh = true,
  description,
  filters,
  filtersAsForm = true,
  isFetching,
  onRefresh,
  pagination,
  stats,
  summary,
  t,
  title,
  toolbar,
}: {
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  columns?: ReactNode;
  compact?: boolean;
  showRefresh?: boolean;
  description?: ReactNode;
  filters?: ReactNode;
  /* false when `filters` renders its own <form>. */
  filtersAsForm?: boolean;
  isFetching?: boolean;
  onRefresh?: () => void;
  pagination?: PaginationConfig;
  stats?: ReactNode;
  /** Page-wide figures that sit between the head and the table card. */
  summary?: ReactNode;
  t: TFunction;
  title: ReactNode;
  /** Controls that sit on the page above the table card, not inside it. */
  toolbar?: ReactNode;
}) {
  return (
    <section
      className={cn('console-page-shell', className)}
      data-refreshing={isFetching || undefined}
    >
      {title || description || actions || showRefresh ? (
        <header className='console-page-topbar'>
          {(title || description) && (
            <div className='console-page-topbar__copy'>
              <div className='console-page-topbar__title-row'>
                <h1 className='console-page-topbar__title'>{title}</h1>
              </div>
              {description ? (
                <p className='console-page-topbar__description'>
                  {description}
                </p>
              ) : null}
            </div>
          )}
          <div className='console-page-topbar__actions'>
            <Toolbar>
              <ToolbarGroup>
                {actions}
                {showRefresh && (
                  <RefreshButton
                    size='sm'
                    variant='secondary'
                    refreshing={isFetching}
                    onClick={onRefresh}
                  >
                    {t('page.console.tables.refresh')}
                  </RefreshButton>
                )}
              </ToolbarGroup>
            </Toolbar>
          </div>
        </header>
      ) : null}

      <div className='console-page-body'>
        {summary}
        {toolbar ? <div className='console-page-toolbar'>{toolbar}</div> : null}
        <DataSurface
          className='lazu-table-page-surface'
          description={compact ? undefined : stats}
          actions={compact ? undefined : columns}
          title={compact ? undefined : t('page.console.tables.tableTitle')}
        >
          {filters || (compact && columns) ? (
            <FilterBar searchForm={filtersAsForm}>
              {filters}
              {compact ? columns : null}
            </FilterBar>
          ) : null}
          {compact && stats ? (
            <div className='console-compact-stats'>{stats}</div>
          ) : null}
          {children}
          {pagination ? (
            <Pagination
              className='mt-4'
              currentPage={pagination.page}
              pageSize={pagination.pageSize}
              total={pagination.total}
              showSizeChanger
              pageSizeOpts={DEFAULT_PAGE_SIZE_OPTIONS}
              onPageChange={pagination.onPageChange}
              onPageSizeChange={pagination.onPageSizeChange}
            />
          ) : null}
        </DataSurface>
      </div>
    </section>
  );
}

export function FilterActions({
  disabled,
  onReset,
  onSubmit,
  t,
}: {
  disabled?: boolean;
  onReset: () => void;
  onSubmit: () => void;
  t: TFunction;
}) {
  return (
    <div className='flex flex-wrap items-end gap-2'>
      {/* type=submit: inside a FilterBar, Enter in any field clicks this. */}
      <Button type='submit' size='md' onClick={onSubmit} disabled={disabled}>
        <Search size={15} />
        {t('page.console.tables.search')}
      </Button>
      <Button size='md' variant='ghost' onClick={onReset} disabled={disabled}>
        <X size={15} />
        {t('page.console.tables.reset')}
      </Button>
    </div>
  );
}

export function CopyButton({
  label,
  onClick,
  t,
}: {
  label?: ReactNode;
  onClick: () => void;
  t: TFunction;
}) {
  return (
    <Button size='xs' variant='soft' onClick={onClick}>
      <Copy size={12} />
      {label || t('page.console.tables.copy')}
    </Button>
  );
}
