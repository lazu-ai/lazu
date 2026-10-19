import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table';
import { ChevronDown, MoreHorizontal } from 'lucide-react';
import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu';
import { EmptyState } from './empty-state';
import { IconButton } from './icon-button';
import { LoadingState } from './loading-state';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { useMotionPresence } from '../../hooks/common/useMotionPresence';
import { cn } from '../../lib/cn';
import './row-actions.css';
import type {
  Cell,
  Column,
  ColumnDef,
  Row,
  Table,
} from '@tanstack/react-table';
import type { Dispatch, MouseEvent, ReactNode, SetStateAction } from 'react';

// Column ids that get special placement on mobile cards.
const SELECT_COLUMN_IDS = new Set(['select', 'selection', 'checkbox']);
const ACTIONS_COLUMN_IDS = new Set(['actions', 'action', 'operations']);
const TITLE_COLUMN_IDS = new Set(['name', 'title', 'username', 'model_name']);
const STATUS_COLUMN_IDS = new Set(['status', 'state']);

type RowRecord = object;

type LegacyColumn<TData extends RowRecord> = ColumnDef<TData, unknown> & {
  dataIndex?: string;
  key?: string;
  render?: (value: unknown, record: TData) => ReactNode;
  title?: ReactNode;
};

type MobileCardConfig = {
  hideIds?: string[];
  statusIds?: string[];
  titleIds?: string[];
};

type DataTableShellProps<TData extends RowRecord> = {
  className?: string;
  columns?: Array<LegacyColumn<TData>>;
  data?: TData[];
  empty?: ReactNode;
  loading?: boolean;
  mobileColumns?: Array<LegacyColumn<TData>>;
  mobileCard?: false | MobileCardConfig;
  onRowClick?: (record: TData, event: MouseEvent<HTMLElement>) => void;
  renderExpanded?: (record: TData) => ReactNode;
  rowKey?: PropertyKey | ((record: TData) => number | string);
  selectedRowKey?: number | string | null;
};

type ExpandableDetailRowProps = {
  children: () => ReactNode;
  colSpan: number;
  expanded: boolean;
};

const ExpandableDetailRow = ({
  children,
  colSpan,
  expanded,
}: ExpandableDetailRowProps) => {
  const presence = useMotionPresence(expanded);
  if (!presence.present) return null;

  return (
    <tr
      aria-hidden={!expanded}
      className='lazu-table__expand-row'
      data-state={presence.state}
    >
      <td colSpan={colSpan}>
        <div
          className='lazu-table__expand-grid'
          onTransitionEnd={presence.onTransitionEnd}
        >
          <div className='lazu-table__expand-content'>{children()}</div>
        </div>
      </td>
    </tr>
  );
};

function normalizeColumn<TData extends RowRecord>(
  column: LegacyColumn<TData>,
): ColumnDef<TData, unknown> {
  const looseColumn = column as LegacyColumn<TData> & {
    accessorFn?: unknown;
    accessorKey?: string;
    cell?: ColumnDef<TData, unknown>['cell'];
    header?: ColumnDef<TData, unknown>['header'];
  };
  if (looseColumn.accessorKey || looseColumn.accessorFn) {
    return column;
  }
  const accessorKey = looseColumn.dataIndex || looseColumn.key || 'id';

  return {
    ...looseColumn,
    accessorKey,
    header: looseColumn.header ?? looseColumn.title ?? accessorKey,
    cell:
      looseColumn.cell ||
      ((context) => {
        const value = context.getValue();
        const record = context.row.original;
        return looseColumn.render
          ? looseColumn.render(value, record)
          : (value as ReactNode);
      }),
  } as unknown as ColumnDef<TData, unknown>;
}

export function DataTableShell<TData extends RowRecord>({
  className,
  columns = [],
  data = [],
  empty,
  loading = false,
  mobileColumns,
  mobileCard,
  onRowClick,
  renderExpanded,
  rowKey = 'id',
  selectedRowKey,
}: DataTableShellProps<TData>) {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const tableColumns = isMobile ? mobileColumns || columns : columns;
  const table = useReactTable({
    columns: tableColumns.map(normalizeColumn),
    data,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row, index) =>
      String(
        typeof rowKey === 'function'
          ? rowKey(row)
          : ((row as Record<PropertyKey, unknown>)[rowKey] ?? index),
      ),
  });

  if (loading) {
    return <LoadingState title={t('legacy.i18n.loading_data.b0b12b127d')} />;
  }

  if (!data.length) {
    return empty || <EmptyState title={t('legacy.i18n.no_data.b246458f20')} />;
  }

  if (isMobile && mobileCard !== false) {
    return (
      <MobileCardList
        table={table}
        renderExpanded={renderExpanded}
        onRowClick={onRowClick}
        expandedId={expandedId}
        setExpandedId={setExpandedId}
        mobileCard={mobileCard}
        selectedRowKey={selectedRowKey}
        t={t}
        className={className}
      />
    );
  }

  const isInteractiveTarget = (target: EventTarget | null, rowEl: Element) => {
    if (!(target instanceof Element)) return false;
    const interactive = target.closest(
      'button, a, input, select, textarea, label, [role="button"], [role="link"], [role="menuitem"]',
    );
    if (!interactive) return false;
    if (rowEl && interactive === rowEl) return false;
    return true;
  };

  const interactive = Boolean(onRowClick || renderExpanded);

  return (
    <div className={cn('lazu-table-wrap', className)}>
      <table
        className={cn('lazu-table', interactive && 'lazu-table--clickable')}
      >
        <thead>
          {table.getHeaderGroups().map((headerGroup) => (
            <tr key={headerGroup.id}>
              {renderExpanded ? (
                <th
                  className='lazu-table__expand-toggle-cell'
                  aria-label={t(
                    'legacy.i18n.expand_collapse_details.25e100128a',
                  )}
                />
              ) : null}
              {headerGroup.headers.map((header) => (
                <th
                  key={header.id}
                  className={cn(
                    ACTIONS_COLUMN_IDS.has(header.column.id) &&
                      'lazu-table__actions-col',
                  )}
                >
                  {header.isPlaceholder
                    ? null
                    : flexRender(
                        header.column.columnDef.header,
                        header.getContext(),
                      )}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => {
            const isExpanded = renderExpanded && expandedId === row.id;
            const isSelected =
              selectedRowKey !== null &&
              selectedRowKey !== undefined &&
              row.id === String(selectedRowKey);
            const handleActivate = (event: MouseEvent<HTMLElement>) => {
              if (renderExpanded) {
                setExpandedId((current) =>
                  current === row.id ? null : row.id,
                );
              }
              if (onRowClick) {
                onRowClick(row.original, event);
              }
            };
            // Mouse: clicking the row (outside a real control) toggles
            // expansion / fires onRowClick. Keyboard: when the row
            // exposes detail content via renderExpanded, the dedicated
            // chevron <button> below is the focusable activator —
            // sighted keyboard users see it, AT users hear it
            // announced as "expand details" without us trampling the
            // row's native table semantics with role="button".
            const handleClick = interactive
              ? (event: MouseEvent<HTMLElement>) => {
                  if (isInteractiveTarget(event.target, event.currentTarget)) {
                    return;
                  }
                  handleActivate(event);
                }
              : undefined;
            const cells = row.getVisibleCells();
            const colSpan = cells.length + (renderExpanded ? 1 : 0);
            return (
              <Fragment key={row.id}>
                <tr
                  onClick={handleClick}
                  aria-expanded={
                    renderExpanded ? Boolean(isExpanded) : undefined
                  }
                  aria-selected={isSelected || undefined}
                  className={cn(
                    isExpanded && 'lazu-table__row--expanded',
                    isSelected && 'lazu-table__row--selected',
                  )}
                >
                  {renderExpanded ? (
                    <td className='lazu-table__expand-toggle-cell'>
                      <button
                        type='button'
                        className='lazu-table__expand-toggle'
                        aria-label={
                          isExpanded
                            ? t('legacy.i18n.collapse_details.a86af73bae')
                            : t('legacy.i18n.expand_details.1f43c288a2')
                        }
                        aria-expanded={Boolean(isExpanded)}
                        onClick={(event) => {
                          event.stopPropagation();
                          handleActivate(event);
                        }}
                      >
                        <ChevronDown
                          size={14}
                          className={cn(
                            'lazu-table__expand-toggle-icon',
                            isExpanded &&
                              'lazu-table__expand-toggle-icon--open',
                          )}
                        />
                      </button>
                    </td>
                  ) : null}
                  {cells.map((cell) => (
                    <td
                      key={cell.id}
                      className={cn(
                        ACTIONS_COLUMN_IDS.has(cell.column.id) &&
                          'lazu-table__actions-col',
                      )}
                    >
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext(),
                      )}
                    </td>
                  ))}
                </tr>
                {renderExpanded ? (
                  <ExpandableDetailRow
                    colSpan={colSpan}
                    expanded={Boolean(isExpanded)}
                  >
                    {() => renderExpanded(row.original)}
                  </ExpandableDetailRow>
                ) : null}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function classifyCells<TData extends RowRecord>(
  cells: Array<Cell<TData, unknown>>,
  mobileCard?: false | MobileCardConfig,
) {
  const select: Array<Cell<TData, unknown>> = [];
  const actions: Array<Cell<TData, unknown>> = [];
  const status: Array<Cell<TData, unknown>> = [];
  const title: Array<Cell<TData, unknown>> = [];
  const fields: Array<Cell<TData, unknown>> = [];

  const titleIds = new Set([
    ...TITLE_COLUMN_IDS,
    ...((mobileCard && mobileCard.titleIds) || []),
  ]);
  const statusIds = new Set([
    ...STATUS_COLUMN_IDS,
    ...((mobileCard && mobileCard.statusIds) || []),
  ]);
  const hideIds = new Set((mobileCard && mobileCard.hideIds) || []);

  cells.forEach((cell) => {
    const id = cell.column.id;
    if (hideIds.has(id)) return;
    if (SELECT_COLUMN_IDS.has(id)) {
      select.push(cell);
    } else if (ACTIONS_COLUMN_IDS.has(id)) {
      actions.push(cell);
    } else if (statusIds.has(id) && status.length === 0) {
      status.push(cell);
    } else if (titleIds.has(id) && title.length === 0) {
      title.push(cell);
    } else {
      fields.push(cell);
    }
  });

  // If we never matched a title column, promote the first non-select
  // field cell so the card always has a header.
  if (title.length === 0 && fields.length > 0) {
    const firstField = fields.shift();
    if (firstField) {
      title.push(firstField);
    }
  }

  return { select, actions, status, title, fields };
}

function getHeaderText<TData extends RowRecord>(
  column: Column<TData, unknown>,
) {
  const def = column.columnDef;
  const header = typeof def.header === 'function' ? null : def.header;
  return header ?? (def as LegacyColumn<TData>).title ?? column.id;
}

function MobileCardList<TData extends RowRecord>({
  table,
  renderExpanded,
  onRowClick,
  expandedId,
  setExpandedId,
  mobileCard,
  selectedRowKey,
  t,
  className,
}: {
  className?: string;
  expandedId: string | null;
  mobileCard?: false | MobileCardConfig;
  onRowClick?: (record: TData, event: MouseEvent<HTMLElement>) => void;
  renderExpanded?: (record: TData) => ReactNode;
  selectedRowKey?: number | string | null;
  setExpandedId: Dispatch<SetStateAction<string | null>>;
  t: (key: string) => string;
  table: Table<TData>;
}) {
  const interactive = Boolean(onRowClick || renderExpanded);

  const isInteractiveTarget = (target: EventTarget | null, rootEl: Element) => {
    if (!(target instanceof Element)) return false;
    const interactiveEl = target.closest(
      'button, a, input, select, textarea, label, [role="button"], [role="link"], [role="menuitem"]',
    );
    if (!interactiveEl) return false;
    if (rootEl && interactiveEl === rootEl) return false;
    return true;
  };

  return (
    <div className={cn('lazu-card-list', className)}>
      {table.getRowModel().rows.map((row) => {
        const cells = row.getVisibleCells();
        const { select, actions, status, title, fields } = classifyCells(
          cells,
          mobileCard,
        );
        const isExpanded = renderExpanded && expandedId === row.id;
        const isSelected =
          selectedRowKey !== null &&
          selectedRowKey !== undefined &&
          row.id === String(selectedRowKey);

        const handleActivate = (event: MouseEvent<HTMLElement>) => {
          if (renderExpanded) {
            setExpandedId((current) => (current === row.id ? null : row.id));
          }
          if (onRowClick) {
            onRowClick(row.original, event);
          }
        };
        const handleClick = interactive
          ? (event: MouseEvent<HTMLElement>) => {
              if (isInteractiveTarget(event.target, event.currentTarget))
                return;
              handleActivate(event);
            }
          : undefined;

        return (
          <article
            key={row.id}
            className={cn(
              'lazu-card-list__item',
              interactive && 'lazu-card-list__item--interactive',
              isExpanded && 'lazu-card-list__item--expanded',
              isSelected && 'lazu-card-list__item--selected',
            )}
            onClick={handleClick}
          >
            <header className='lazu-card-list__header'>
              {select.length > 0 ? (
                <div className='lazu-card-list__select'>
                  {select.map((cell) => (
                    <Fragment key={cell.id}>
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext(),
                      )}
                    </Fragment>
                  ))}
                </div>
              ) : null}
              <div className='lazu-card-list__title'>
                {title.map((cell) => (
                  <Fragment key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </Fragment>
                ))}
              </div>
              {status.length > 0 ? (
                <div className='lazu-card-list__status'>
                  {status.map((cell) => (
                    <Fragment key={cell.id}>
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext(),
                      )}
                    </Fragment>
                  ))}
                </div>
              ) : null}
              {renderExpanded ? (
                <button
                  type='button'
                  className='lazu-card-list__chevron-button'
                  aria-label={
                    isExpanded
                      ? t('legacy.i18n.collapse_details.a86af73bae')
                      : t('legacy.i18n.expand_details.1f43c288a2')
                  }
                  aria-expanded={Boolean(isExpanded)}
                  onClick={(event) => {
                    event.stopPropagation();
                    handleActivate(event);
                  }}
                >
                  <ChevronDown
                    size={16}
                    className={cn(
                      'lazu-card-list__chevron',
                      isExpanded && 'lazu-card-list__chevron--open',
                    )}
                    aria-hidden='true'
                  />
                </button>
              ) : null}
            </header>

            {fields.length > 0 ? (
              <dl className='lazu-card-list__fields'>
                {fields.map((cell) => (
                  <div className='lazu-card-list__field' key={cell.id}>
                    <dt>{getHeaderText(cell.column)}</dt>
                    <dd>
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext(),
                      )}
                    </dd>
                  </div>
                ))}
              </dl>
            ) : null}

            {actions.length > 0 ? (
              <footer className='lazu-card-list__actions'>
                {actions.map((cell) => (
                  <Fragment key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </Fragment>
                ))}
              </footer>
            ) : null}

            {isExpanded ? (
              <div className='lazu-card-list__expanded'>
                <div className='lazu-table__expand-content'>
                  {renderExpanded(row.original)}
                </div>
              </div>
            ) : null}
          </article>
        );
      })}
    </div>
  );
}

/**
 * A single row action. `primary` items render inline as compact soft
 * buttons; everything passed to `menu` collapses into the trailing
 * "⋯ More" dropdown. Mark destructive actions (disable / delete) with
 * `danger` so they read in --ui-danger and sit below a hairline divider.
 */
export type RowAction = {
  danger?: boolean;
  disabled?: boolean;
  hidden?: boolean;
  icon?: ReactNode;
  key: string;
  label: ReactNode;
  loading?: boolean;
  onSelect: () => void;
};

type RowActionsProps = {
  className?: string;
  menu?: RowAction[];
  menuLabel?: string;
  primary?: RowAction[];
};

/**
 * Shared row-actions cluster for the console tables. Keeps the primary
 * one or two operations (edit / test) inline and tucks the long tail —
 * balance, keys, copy, enable/disable, delete — into a single dropdown so
 * every row reads as one tidy control instead of seven buttons.
 */
export function RowActions({
  className,
  menu = [],
  menuLabel,
  primary = [],
}: RowActionsProps) {
  const { t } = useTranslation();
  const visiblePrimary = primary.filter((action) => !action.hidden);
  const visibleMenu = menu.filter((action) => !action.hidden);
  const moreLabel =
    menuLabel || t('page.console.tables.more', { defaultValue: '更多' });

  if (!visiblePrimary.length && !visibleMenu.length) {
    return null;
  }

  // Order menu items so all non-destructive actions come first, then a
  // single divider, then the destructive group — regardless of the order
  // the caller passed them in.
  const safeMenu = visibleMenu.filter((action) => !action.danger);
  const dangerMenu = visibleMenu.filter((action) => action.danger);

  const renderMenuItem = (action: RowAction) => (
    <DropdownMenuItem
      key={action.key}
      className={cn(action.danger && 'lazu-dropdown-item--danger')}
      disabled={action.disabled || action.loading}
      onSelect={() => {
        if (action.disabled || action.loading) {
          return;
        }
        action.onSelect();
      }}
    >
      {action.icon ? (
        <span className='lazu-dropdown-item__icon'>{action.icon}</span>
      ) : null}
      {action.label}
    </DropdownMenuItem>
  );

  return (
    <div className={cn('lazu-row-actions', className)}>
      {visiblePrimary.map((action) => (
        <Button
          key={action.key}
          size='xs'
          variant={action.danger ? 'soft-danger' : 'soft'}
          disabled={action.disabled}
          loading={action.loading}
          onClick={action.onSelect}
        >
          {action.icon}
          {action.label}
        </Button>
      ))}
      {visibleMenu.length ? (
        <DropdownMenu>
          <DropdownMenuTrigger>
            <IconButton
              size='sm'
              variant='ghost'
              aria-label={moreLabel}
              title={moreLabel}
              className='lazu-row-actions__more'
            >
              <MoreHorizontal size={16} />
            </IconButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end' className='min-w-[176px]'>
            {safeMenu.map(renderMenuItem)}
            {safeMenu.length && dangerMenu.length ? (
              <div className='lazu-dropdown-separator' role='separator' />
            ) : null}
            {dangerMenu.map(renderMenuItem)}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  );
}
