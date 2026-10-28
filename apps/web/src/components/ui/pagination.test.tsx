import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Pagination } from './pagination';

const buttons = (html: string) =>
  [...html.matchAll(/<button([^>]*)>(.*?)<\/button>/g)].map((m) => ({
    attrs: m[1],
    text: m[2].replace(/<[^>]+>/g, ''),
  }));

const nextButton = (html: string) => {
  const all = buttons(html);
  return all[all.length - 1];
};
const pageNumbers = (html: string) =>
  buttons(html)
    .map((b) => b.text)
    .filter((text) => /^\d+$/.test(text))
    .map(Number);

describe('Pagination', () => {
  it('stops at the last page an exact total covers', () => {
    const html = renderToStaticMarkup(
      <Pagination currentPage={5} pageSize={10} total={50} showTotal={false} />,
    );
    expect(nextButton(html).attrs).toContain('disabled');
    expect(pageNumbers(html)).toEqual([1, 4, 5]);
  });

  it('keeps going past a lower-bound total while pages come back full', () => {
    const html = renderToStaticMarkup(
      <Pagination
        currentPage={1000}
        pageSize={10}
        total={10000}
        totalAtLeast
        hasNextPage
        showTotal={false}
      />,
    );
    expect(nextButton(html).attrs).not.toContain('disabled');
    expect(pageNumbers(html)).toEqual([1, 999, 1000, 1001]);
    expect(html).toContain('lazu-pagination__ellipsis');
  });

  it('stops past the counted pages once a page comes back short', () => {
    const html = renderToStaticMarkup(
      <Pagination
        currentPage={1500}
        pageSize={10}
        total={10000}
        totalAtLeast
        hasNextPage={false}
        showTotal={false}
      />,
    );
    expect(nextButton(html).attrs).toContain('disabled');
    expect(pageNumbers(html)).toEqual([1, 1499, 1500]);
  });

  it('offers the counted pages ahead even from an early page', () => {
    const html = renderToStaticMarkup(
      <Pagination
        currentPage={1}
        pageSize={10}
        total={10000}
        totalAtLeast
        hasNextPage
        showTotal={false}
      />,
    );
    expect(nextButton(html).attrs).not.toContain('disabled');
    expect(pageNumbers(html)).toEqual([1, 2, 1000]);
    // The highest counted page is offered, marked as not the end.
    expect(html).toContain('lazu-pagination__ellipsis');
  });
});
