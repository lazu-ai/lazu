import {
  Empty,
  Spin,
  type PaginationProps,
  type TableProps,
} from './console-primitives-core';
import { cn } from '../../lib/cn';

export function Table<T extends object>({
  bordered,
  className,
  columns = [],
  data,
  dataSource,
  empty,
  loading,
  onRow,
  pagination,
  rowKey = 'id',
  rowSelection,
  scroll,
  size,
  style,
  ...props
}: TableProps<T>) {
  const rows = dataSource || data || [];
  const selected = new Set(rowSelection?.selectedRowKeys || []);
  const getKey = (record: T, index: number) => {
    if (typeof rowKey === 'function') return rowKey(record);
    const value = (record as Record<string, unknown>)[rowKey];
    return typeof value === 'string' || typeof value === 'number'
      ? value
      : index;
  };

  if (loading) return <Spin spinning>{null}</Spin>;
  if (!rows.length) return <>{empty || <Empty />}</>;

  return (
    <div
      className='lazu-table-wrap'
      style={{
        maxHeight: scroll?.y,
        overflowX: scroll?.x ? 'auto' : undefined,
        overflowY: scroll?.y ? 'auto' : undefined,
      }}
    >
      <table
        {...props}
        className={cn('lazu-table', className)}
        style={{
          minWidth: scroll?.x,
          ...style,
        }}
      >
        <thead>
          <tr>
            {rowSelection ? <th aria-label='Select row' /> : null}
            {columns.map((column, columnIndex) => (
              <th
                key={column.key ?? column.dataIndex ?? columnIndex}
                style={{ width: column.width }}
              >
                {column.title || column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((record, index) => {
            const key = getKey(record, index);
            const rowProps = onRow?.(record, index) ?? {};
            return (
              <tr key={key} {...rowProps}>
                {rowSelection ? (
                  <td>
                    <input
                      type='checkbox'
                      checked={selected.has(key)}
                      disabled={
                        rowSelection.getCheckboxProps?.(record)?.disabled
                      }
                      onChange={(event) => {
                        const nextKeys = event.target.checked
                          ? [...selected, key]
                          : [...selected].filter((item) => item !== key);
                        rowSelection.onChange?.(
                          nextKeys,
                          rows.filter((row, rowIndex) =>
                            nextKeys.includes(getKey(row, rowIndex)),
                          ),
                        );
                      }}
                      onClick={(event) => event.stopPropagation()}
                    />
                  </td>
                ) : null}
                {columns.map((column, columnIndex) => {
                  const value = column.dataIndex
                    ? (record as Record<string, unknown>)[column.dataIndex]
                    : undefined;
                  return (
                    <td key={column.key ?? column.dataIndex ?? columnIndex}>
                      {column.render
                        ? column.render(value as never, record, index)
                        : typeof value === 'string' || typeof value === 'number'
                          ? value
                          : null}
                    </td>
                  );
                })}
              </tr>
            );
          })}
        </tbody>
      </table>
      {pagination ? <Pagination {...pagination} /> : null}
    </div>
  );
}

export function Pagination({
  currentPage = 1,
  onChange,
  onPageChange,
  pageSize = 10,
  total = 0,
}: PaginationProps) {
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  return (
    <div className='lazu-pagination lazu-pagination'>
      <button
        className='lazu-pagination-button'
        type='button'
        disabled={currentPage <= 1}
        onClick={() => (onChange || onPageChange)?.(currentPage - 1, pageSize)}
      >
        ‹
      </button>
      <span className='lazu-pagination__summary'>
        {currentPage} / {pageCount}
      </span>
      <button
        className='lazu-pagination-button'
        type='button'
        disabled={currentPage >= pageCount}
        onClick={() => (onChange || onPageChange)?.(currentPage + 1, pageSize)}
      >
        ›
      </button>
    </div>
  );
}
