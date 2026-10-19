import { ChevronLeft, ChevronRight } from 'lucide-react';
import React, { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { PendingValue } from './pending';
import { Select, SelectItem } from './select';
import { cn } from '../../lib/cn';

type PaginationProps = {
  className?: string;
  currentPage?: number;
  onPageChange?: (page: number) => void;
  onPageSizeChange?: (pageSize: number) => void;
  pageSize?: number;
  /** True while the page's query is in flight. A total of 0 is then an
   *  absence of an answer, not an answer of none, and the summary says so by
   *  holding its space instead of printing "共 0 项". */
  pending?: boolean;
  pageSizeOpts?: number[];
  showSizeChanger?: boolean;
  showTotal?:
    | boolean
    | string
    | ((total: number, range: [number, number]) => React.ReactNode);
  total?: number;
};

function getPages(currentPage: number, pageCount: number): number[] {
  const pages = new Set([1, pageCount]);
  for (let page = currentPage - 1; page <= currentPage + 1; page += 1) {
    if (page > 1 && page < pageCount) {
      pages.add(page);
    }
  }
  return [...pages].sort((a, b) => a - b);
}

export function Pagination({
  className,
  currentPage = 1,
  onPageChange,
  onPageSizeChange,
  pageSize = 10,
  pageSizeOpts = [10, 20, 50, 100],
  pending = false,
  showSizeChanger = false,
  showTotal,
  total = 0,
}: PaginationProps) {
  const { t } = useTranslation();
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const pages = useMemo(
    () => getPages(currentPage, pageCount),
    [currentPage, pageCount],
  );

  const goToPage = (page: number) => {
    const nextPage = Math.min(pageCount, Math.max(1, page));
    if (nextPage !== currentPage) {
      onPageChange?.(nextPage);
    }
  };

  const start = total ? (currentPage - 1) * pageSize + 1 : 0;
  const end = Math.min(currentPage * pageSize, total);
  const totalContent = pending ? (
    <PendingValue chars={14} />
  ) : typeof showTotal === 'function' ? (
    showTotal(total, [start, end])
  ) : typeof showTotal === 'string' ? (
    showTotal
  ) : (
    t('legacy.i18n.items_total_showing_items.e7f2f3866e', {
      total,
      start,
      end,
    })
  );

  return (
    <nav
      className={cn('lazu-pagination', className)}
      aria-label={t('legacy.i18n.pagination.88b6b4f79a')}
    >
      {showTotal === false ? null : (
        <span className='lazu-pagination__summary'>{totalContent}</span>
      )}
      {showSizeChanger ? (
        <Select
          value={String(pageSize)}
          onValueChange={(value) => onPageSizeChange?.(Number(value))}
          size='sm'
          aria-label={t('legacy.i18n.items_per_page.e4523b2473')}
        >
          {pageSizeOpts.map((option) => (
            <SelectItem key={option} value={String(option)}>
              {t('legacy.i18n.page.6ff3736147', { count: option })}
            </SelectItem>
          ))}
        </Select>
      ) : null}
      <button
        type='button'
        className='lazu-pagination-button'
        disabled={currentPage <= 1}
        onClick={() => goToPage(currentPage - 1)}
        aria-label={t('legacy.i18n.previous_page.b41561d807')}
      >
        <ChevronLeft size={15} />
      </button>
      {pages.map((page, index) => {
        const previousPage = pages[index - 1];
        const needsGap = previousPage && page - previousPage > 1;

        return (
          <React.Fragment key={page}>
            {needsGap ? (
              <span className='lazu-pagination__ellipsis'>...</span>
            ) : null}
            <button
              type='button'
              className='lazu-pagination-button'
              data-active={page === currentPage}
              onClick={() => goToPage(page)}
              aria-label={t('legacy.i18n.page.07351cd55a', { page })}
            >
              {page}
            </button>
          </React.Fragment>
        );
      })}
      <button
        type='button'
        className='lazu-pagination-button'
        disabled={currentPage >= pageCount}
        onClick={() => goToPage(currentPage + 1)}
        aria-label={t('legacy.i18n.next_page.67a246a344')}
      >
        <ChevronRight size={15} />
      </button>
    </nav>
  );
}

export default Pagination;
